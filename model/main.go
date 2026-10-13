package model

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const commonGroupCol = `"group"`
const commonKeyCol = `"key"`
const commonTrueVal = "true"
const commonFalseVal = "false"

var logKeyCol string
var logGroupCol string

// jsonScanBytes 归一化 json 列的驱动返回值:不同驱动/协议模式下同一列可能
// 以 []byte 或 string 返回,静默丢弃 string 会导致字段被清零而不报错。
func jsonScanBytes(value any) []byte {
	switch v := value.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		return nil
	}
}

func initCol() {
	logGroupCol = `"group"`
	logKeyCol = `"key"`
}

var DB *gorm.DB

var LOG_DB *gorm.DB

func createRootAccountIfNeed() error {
	var user User
	//if user.Status != common.UserStatusEnabled {
	if err := DB.First(&user).Error; err != nil {
		common.SysLog(common.LogText("no user exists, create a root user for you: username is root, password is 123456"))
		hashedPassword, err := common.Password2Hash("123456")
		if err != nil {
			return err
		}
		rootUser := User{
			Username:    "root",
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		}
		DB.Create(&rootUser)
	}
	return nil
}

func CheckSetup() {
	setup := GetSetup()
	if setup == nil {
		// No setup record exists, check if we have a root user
		if RootUserExists() {
			common.SysLog(common.LogText("system is not initialized, but root user exists"))
			// Create setup record
			newSetup := Setup{
				Version:       common.Version,
				InitializedAt: time.Now().Unix(),
			}
			err := DB.Create(&newSetup).Error
			if err != nil {
				common.SysLog(common.LogText("failed to create setup record: %s", err.Error()))
			}
			constant.Setup = true
		} else {
			common.SysLog(common.LogText("system is not initialized and no root user exists"))
			constant.Setup = false
		}
	} else {
		// Setup record exists, system is initialized
		common.SysLog(common.LogText("system is already initialized at: %s", time.Unix(setup.InitializedAt, 0).String()))
		constant.Setup = true
	}
}

func chooseDB(envName string) (*gorm.DB, common.DatabaseType, error) {
	dsn := strings.TrimSpace(os.Getenv(envName))
	if dsn == "" {
		return nil, "", fmt.Errorf("%s is required; configure a PostgreSQL connection", envName)
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		// PostgreSQL also accepts libpq-style keyword/value connection strings.
		if strings.Contains(dsn, "://") || !strings.Contains(dsn, "=") {
			return nil, "", fmt.Errorf("%s only supports PostgreSQL connections", envName)
		}
	}
	if _, err := pgx.ParseConfig(dsn); err != nil {
		// Parser errors can include credentials from the connection string.
		return nil, "", fmt.Errorf("%s contains an invalid PostgreSQL connection", envName)
	}
	common.SysLog(common.LogText("using PostgreSQL as database"))
	// Disable both pgx and GORM prepared statements for transaction poolers.
	db, err := gorm.Open(postgresMigrationDialector{postgres.Dialector{Config: &postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}}}, newGormConfig(false))
	if err != nil {
		return nil, "", err
	}
	var version int
	if err = db.Raw("SELECT current_setting('server_version_num')::integer").Scan(&version).Error; err == nil && version < 180000 {
		err = fmt.Errorf("%s requires PostgreSQL 18 or later (server_version_num=%d)", envName, version)
	}
	if err != nil {
		_ = closeDB(db)
		return nil, "", err
	}
	return db, common.DatabaseTypePostgreSQL, nil
}

func InitDB() (err error) {
	db, dbType, err := chooseDB("SQL_DSN")
	if err == nil {
		common.SetMainDatabaseType(dbType)
		if os.Getenv("LOG_SQL_DSN") == "" {
			common.SetLogDatabaseType(dbType)
		}
		initCol()
		if common.DebugEnabled {
			db = db.Debug()
		}
		DB = db
		if err := ensureUserQuotaColumns(DB, common.MainDatabaseType()); err != nil {
			return err
		}
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		if !common.IsMasterNode {
			// Only the master node migrates. A node that cannot read the deadline
			// keeps rejecting legacy access tokens instead of refusing to start.
			if err := EnsureLegacyAccessTokenRetireAt(common.GetTimestamp()); err != nil {
				common.SysError(common.LogText("initialize legacy access token deadline: %s", err.Error()))
			}
			return nil
		}
		common.SysLog(common.LogText("database migration started"))
		err = migrateDB()
		return err
	}
	return err
}

func InitLogDB() (err error) {
	if os.Getenv("LOG_SQL_DSN") == "" {
		LOG_DB = DB
		common.SetLogDatabaseType(common.MainDatabaseType())
		initCol()
		if common.IsMasterNode {
			return MigrateAuditLogs()
		}
		return
	}
	db, dbType, err := chooseDB("LOG_SQL_DSN")
	if err == nil {
		common.SetLogDatabaseType(dbType)
		initCol()
		if common.DebugEnabled {
			db = db.Debug()
		}
		LOG_DB = db
		sqlDB, err := LOG_DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		if !common.IsMasterNode {
			return nil
		}
		common.SysLog(common.LogText("database migration started"))
		err = migrateLOGDB()
		return err
	}
	return err
}

var userQuotaColumns = []string{"quota", "used_quota", "aff_quota", "aff_history"}

// ensureUserQuotaColumns rejects a legacy 32-bit wallet schema before any
// migrations run. The 64-bit-only build intentionally does not auto-upgrade
// an existing wallet; operators must migrate it explicitly before starting.
func ensureUserQuotaColumns(db *gorm.DB, dbType common.DatabaseType) error {
	if common.GetEnvOrDefaultBool("SKIP_64BIT_QUOTA_SCHEMA_CHECK", false) {
		common.SysLog(common.LogText("SKIP_64BIT_QUOTA_SCHEMA_CHECK=true; skipping user quota schema check"))
		return nil
	}
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable(&User{}) {
		return nil
	}
	columnTypes, err := db.Migrator().ColumnTypes(&User{})
	if err != nil {
		return fmt.Errorf("failed to inspect users schema: %w", err)
	}
	for _, expected := range userQuotaColumns {
		for _, actual := range columnTypes {
			if !strings.EqualFold(actual.Name(), expected) {
				continue
			}
			dataType := actual.DatabaseTypeName()
			if !is64BitIntegerType(dbType, dataType) {
				return fmt.Errorf("users.%s uses %s; 32-bit is not supported", expected, dataType)
			}
		}
	}
	return nil
}

func is64BitIntegerType(dbType common.DatabaseType, dataType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dataType))
	switch dbType {
	case common.DatabaseTypePostgreSQL:
		return normalized == "bigint" || normalized == "int8"
	default:
		return false
	}
}

func migrateDB() error {
	if err := migrateTokenKeyUniqueness(DB); err != nil {
		return err
	}
	if err := migratePrefillGroupUniqueness(DB); err != nil {
		return err
	}
	// Migrate price_amount column from float/double to decimal for existing tables
	migrateSubscriptionPlanPriceAmount()
	// Migrate model_limits column from varchar to text for existing tables
	if err := migrateTokenModelLimitsToText(); err != nil {
		return err
	}
	if err := migrateOptionPrimaryKey(DB); err != nil {
		common.SysError(common.LogText("failed to migrate options primary key: %s", err.Error()))
	}

	err := DB.AutoMigrate(
		&Channel{},
		&Token{},
		&User{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
		&PasskeyCredential{},
		&Option{},
		&LoginEncryptionKey{},
		&Redemption{},
		&Ability{},
		&Log{},
		&Midjourney{},
		&TopUp{},
		&QuotaData{},
		&Task{},
		&TaskPlugin{},
		&Model{},
		&Vendor{},
		&PrefillGroup{},
		&Setup{},
		&TwoFA{},
		&TwoFABackupCode{},
		&Checkin{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
		&PerfMetric{},
		&StatisticsWriteCheckpoint{},
		&SystemInstance{},
		&SystemTask{},
		&SystemTaskLock{},
		&CasbinRule{},
		&AuthzRole{},
		&UserAccessToken{},
	)
	if err != nil {
		return err
	}
	if err := InitializeUserAuthVersions(); err != nil {
		return err
	}
	if err := EnsureLegacyAccessTokenRetireAt(common.GetTimestamp()); err != nil {
		return fmt.Errorf("initialize legacy access token deadline: %w", err)
	}
	if err := InitializeExternalIdentityClaims(); err != nil {
		return err
	}
	if err := DB.AutoMigrate(&SubscriptionPlan{}); err != nil {
		return err
	}
	return nil
}

func migrateLOGDB() error {
	if err := MigrateAuditLogs(); err != nil {
		return err
	}
	return LOG_DB.AutoMigrate(&Log{})
}

// migrateTokenModelLimitsToText migrates model_limits column from varchar(1024) to text
// This is safe to run multiple times - it checks the column type first
func migrateTokenModelLimitsToText() error {
	tableName := "tokens"
	columnName := "model_limits"

	if !DB.Migrator().HasTable(tableName) {
		return nil
	}

	if !DB.Migrator().HasColumn(&Token{}, columnName) {
		return nil
	}

	var alterSQL string
	{
		var dataType string
		if err := DB.Raw(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&dataType).Error; err != nil {
			common.SysLog(common.LogText("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if dataType == "text" {
			return nil
		}
		alterSQL = fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE text`, tableName, columnName)
	}

	if alterSQL != "" {
		if err := DB.Exec(alterSQL).Error; err != nil {
			return fmt.Errorf("failed to migrate %s.%s to text: %w", tableName, columnName, err)
		}
		common.SysLog(common.LogText("Successfully migrated %s.%s to text", tableName, columnName))
	}
	return nil
}

// migrateSubscriptionPlanPriceAmount migrates price_amount column from float/double to decimal(10,6)
// This is safe to run multiple times - it checks the column type first
func migrateSubscriptionPlanPriceAmount() {
	tableName := "subscription_plans"
	columnName := "price_amount"

	// Check if table exists first
	if !DB.Migrator().HasTable(tableName) {
		return
	}

	// Check if column exists
	if !DB.Migrator().HasColumn(&SubscriptionPlan{}, columnName) {
		return
	}

	var alterSQL string
	{
		// PostgreSQL: Check if already decimal/numeric
		var dataType string
		if err := DB.Raw(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&dataType).Error; err != nil {
			common.SysLog(common.LogText("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if dataType == "numeric" {
			return // Already decimal/numeric
		}
		alterSQL = fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE decimal(10,6) USING %s::decimal(10,6)`,
			tableName, columnName, columnName)
	}

	if alterSQL != "" {
		if err := DB.Exec(alterSQL).Error; err != nil {
			common.SysLog(common.LogText("Warning: failed to migrate %s.%s to decimal: %v", tableName, columnName, err))
		} else {
			common.SysLog(common.LogText("Successfully migrated %s.%s to decimal(10,6)", tableName, columnName))
		}
	}
}

func closeDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	return err
}

func CloseDB() error {
	if LOG_DB != DB {
		err := closeDB(LOG_DB)
		if err != nil {
			return err
		}
	}
	return closeDB(DB)
}

var (
	lastPingTime time.Time
	pingMutex    sync.Mutex
)

func PingDB() error {
	pingMutex.Lock()
	defer pingMutex.Unlock()

	if time.Since(lastPingTime) < time.Second*10 {
		return nil
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Print(common.LogText("Error getting sql.DB from GORM: %v", err))
		return err
	}

	err = sqlDB.Ping()
	if err != nil {
		log.Print(common.LogText("Error pinging DB: %v", err))
		return err
	}

	lastPingTime = time.Now()
	common.SysLog(common.LogText("Database pinged successfully"))
	return nil
}
