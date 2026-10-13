package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MigrationIdentityFields struct {
	ID        int    `gorm:"primaryKey"`
	Name      string `gorm:"size:64;unique"`
	Reference string `gorm:"size:64;uniqueIndex"`
	Provider  string `gorm:"size:32;uniqueIndex:,composite:provider_subject"`
	Subject   string `gorm:"size:64;uniqueIndex:,composite:provider_subject"`
}

type migrationIdentityV1 struct {
	MigrationIdentityFields
	Digest string `gorm:"type:char(32)"`
}

type migrationIdentityV2 struct {
	MigrationIdentityFields
	Digest string `gorm:"type:char(64)"`
	Note   string `gorm:"size:128"`
}

type migrationConstraintV1 struct {
	ID   int    `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

type migrationConstraintV2 struct {
	ID   int    `gorm:"primaryKey"`
	Name string `gorm:"size:64;unique"`
}

type migrationDecimalV1 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(10,6);default:0"`
}

type migrationDecimalV2 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(12,6);not null;default:0"`
}

type migrationDecimalV3 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(12,6);not null;default:1.25"`
}

func TestMigrationSchemaStability(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dsn string
			dsn = testutil.PostgresDSN(t)
			if dsn == "" {
				t.Skip("test database DSN is not configured")
			}
			t.Setenv("MIGRATION_TEST_DSN", dsn)
			db, _, err := chooseDB("MIGRATION_TEST_DSN")
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			recorder := &migrationSQLRecorder{}
			db = db.Session(&gorm.Session{Logger: recorder})

			t.Run("identity_and_indexes", func(t *testing.T) {
				const table = "migration_identity_test"
				t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV1{}))
				row := migrationIdentityV1{
					MigrationIdentityFields: MigrationIdentityFields{ID: 1, Name: "root", Reference: "token-reference", Provider: "oidc", Subject: "subject"},
					Digest:                  "old-digest",
				}
				require.NoError(t, db.Table(table).Create(&row).Error)
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV1{}))
				assert.Empty(t, recorder.schemaMutations())

				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV2{}))
				columns, err := db.Table(table).Migrator().ColumnTypes(&migrationIdentityV2{})
				require.NoError(t, err)
				for _, column := range columns {
					if column.Name() == "digest" {
						length, ok := column.Length()
						require.True(t, ok)
						assert.EqualValues(t, 64, length)
					}
				}
				assert.True(t, db.Table(table).Migrator().HasColumn(&migrationIdentityV2{}, "note"))
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV2{}))
				assert.Empty(t, recorder.schemaMutations())
				var saved migrationIdentityV2
				require.NoError(t, db.Table(table).First(&saved, 1).Error)
				assert.Equal(t, row.MigrationIdentityFields, saved.MigrationIdentityFields)
				expectedDigest := row.Digest
				if dialect == "postgres" {
					expectedDigest += strings.Repeat(" ", 64-len(row.Digest))
				}
				assert.Equal(t, expectedDigest, saved.Digest)
				for _, duplicate := range []migrationIdentityV2{
					{MigrationIdentityFields: MigrationIdentityFields{Name: "root", Reference: "other-1", Provider: "other", Subject: "1"}},
					{MigrationIdentityFields: MigrationIdentityFields{Name: "other-2", Reference: "token-reference", Provider: "other", Subject: "2"}},
					{MigrationIdentityFields: MigrationIdentityFields{Name: "other-3", Reference: "other-3", Provider: "oidc", Subject: "subject"}},
				} {
					assert.Error(t, db.Table(table).Create(&duplicate).Error)
				}
			})

			t.Run("unique_constraint_changes", func(t *testing.T) {
				const table = "migration_constraint_test"
				t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV1{}))
				require.NoError(t, db.Table(table).Create(&migrationConstraintV1{Name: "existing"}).Error)
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV2{}))
				assert.Error(t, db.Table(table).Create(&migrationConstraintV2{Name: "existing"}).Error)
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV2{}))
				assert.Empty(t, recorder.schemaMutations())
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV1{}))
				require.NoError(t, db.Table(table).Create(&migrationConstraintV1{Name: "existing"}).Error)
			})

			if dialect == "postgres" {
				t.Run("renamed_unique_constraints", func(t *testing.T) {
					const table = "migration_renamed_unique"
					t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
					tableDB := db.Table(table).Session(&gorm.Session{})
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					original := migrationConstraintV1{Name: "existing"}
					require.NoError(t, tableDB.Create(&original).Error)
					for _, name := range []string{"models_model_name_key", `imported "model" name`} {
						require.NoError(t, db.Exec("ALTER TABLE ? ADD CONSTRAINT ? UNIQUE (name)", clause.Table{Name: table}, clause.Column{Name: name}).Error)
					}
					require.NoError(t, db.Exec("ALTER TABLE ? ADD CONSTRAINT keep_composite UNIQUE (id, name)", clause.Table{Name: table}).Error)
					require.NoError(t, db.Exec("CREATE INDEX keep_name_lookup ON ? (name)", clause.Table{Name: table}).Error)
					// A unique field must keep the old names and still reject duplicates.
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV2{}))
					require.Error(t, tableDB.Create(&migrationConstraintV2{Name: "existing"}).Error)
					require.NoError(t, db.Exec("CREATE TABLE migration_unique_reference (name varchar(64) REFERENCES migration_renamed_unique(name))").Error)
					t.Cleanup(func() { _ = db.Migrator().DropTable("migration_unique_reference") })
					require.NoError(t, db.Exec("INSERT INTO migration_unique_reference (name) VALUES (?)", "existing").Error)
					require.Error(t, tableDB.AutoMigrate(&migrationConstraintV1{}), "dependent foreign keys must not be cascaded away")
					for _, name := range []string{"models_model_name_key", `imported "model" name`} {
						assert.True(t, tableDB.Migrator().HasConstraint(&migrationConstraintV1{}, name), "all old constraints must survive rollback")
					}
					var references int64
					require.NoError(t, db.Table("migration_unique_reference").Count(&references).Error)
					assert.EqualValues(t, 1, references)
					require.NoError(t, db.Migrator().DropTable("migration_unique_reference"))
					// Removing column uniqueness must resolve both actual constraint names.
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					recorder.reset()
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					assert.Empty(t, recorder.schemaMutations())
					require.NoError(t, tableDB.Create(&migrationConstraintV1{Name: "existing"}).Error)
					assert.True(t, tableDB.Migrator().HasConstraint(&migrationConstraintV1{}, "keep_composite"))
					assert.True(t, tableDB.Migrator().HasIndex(&migrationConstraintV1{}, "keep_name_lookup"))
					var rows []migrationConstraintV1
					require.NoError(t, tableDB.Order("id").Find(&rows).Error)
					require.Len(t, rows, 2)
					assert.Equal(t, original, rows[0])
				})
			}

		})
	}
}

// Startup must reject obsolete engines before connecting or creating files.
func TestChooseDBRejectsUnsupportedConnections(t *testing.T) {
	for _, tc := range []struct {
		name, dsn string
		log       bool
	}{
		{name: "missing primary"},
		{name: "blank primary", dsn: "  "},
		{name: "sqlite fallback", dsn: "local"},
		{name: "sqlite file", dsn: "file:old.db"},
		{name: "mysql primary", dsn: "user:private-password@tcp(localhost:3306)/new-api"},
		{name: "mysql log", dsn: "user:private-password@tcp(localhost:3306)/logs", log: true},
		{name: "clickhouse primary", dsn: "clickhouse://localhost:9000/logs"},
		{name: "clickhouse log", dsn: "clickhouse://localhost:9000/logs", log: true},
		{name: "invalid postgres", dsn: "postgres://user:private-password@localhost:bad/new-api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := "SQL_DSN"
			if tc.log {
				env = "LOG_SQL_DSN"
			}
			t.Setenv(env, tc.dsn)
			db, _, err := chooseDB(env)
			require.Error(t, err)
			assert.Nil(t, db)
			assert.Contains(t, err.Error(), env)
			assert.NotContains(t, err.Error(), "private-password")
		})
	}
}

func TestChooseDBAcceptsPostgreSQLConnectionForms(t *testing.T) {
	for _, form := range []string{"postgres", "postgresql", "keywords"} {
		t.Run(form, func(t *testing.T) {
			dsn := testutil.PostgresDSN(t)
			if form == "keywords" {
				config, err := pgx.ParseConfig(dsn)
				require.NoError(t, err)
				dsn = fmt.Sprintf("host=%s port=%d user=%s password='%s' dbname=%s sslmode=disable search_path=%s", config.Host, config.Port, config.User, strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(config.Password), config.Database, config.RuntimeParams["search_path"])
			} else {
				dsn = strings.Replace(dsn, "postgres://", form+"://", 1)
				dsn = strings.Replace(dsn, "postgresql://", form+"://", 1)
			}
			t.Setenv("DB_CONFIG_TEST_DSN", dsn)
			db, kind, err := chooseDB("DB_CONFIG_TEST_DSN")
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, sqlDB.Ping())
			assert.Equal(t, common.DatabaseTypePostgreSQL, kind)
			assert.False(t, db.Config.PrepareStmt)
		})
	}
}

func TestChooseDBRejectsPostgreSQL17(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_OLD_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_OLD_DSN is not configured")
	}
	for _, env := range []string{"SQL_DSN", "LOG_SQL_DSN"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, dsn)
			db, _, err := chooseDB(env)
			require.Error(t, err)
			assert.Nil(t, db)
			assert.Contains(t, err.Error(), env+" requires PostgreSQL 18 or later")
		})
	}
}

func TestPostgreSQL18FreshStartupIsIdempotent(t *testing.T) {
	t.Setenv("MIGRATION_TEST_DSN", testutil.PostgresDSN(t))
	db, _, err := chooseDB("MIGRATION_TEST_DSN")
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	previousDB, previousLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog })
	require.NoError(t, migrateDB())
	require.NoError(t, migrateLOGDB())
	recorder := &migrationSQLRecorder{}
	DB, LOG_DB = db.Session(&gorm.Session{Logger: recorder}), db.Session(&gorm.Session{Logger: recorder})
	require.NoError(t, migrateDB())
	require.NoError(t, migrateLOGDB())
	assert.Empty(t, recorder.schemaMutations(), "second startup must preserve the schema without repeated DDL")
	for _, name := range []string{"idx_tasks_unfinished_id", "idx_tasks_unfinished_submit_id"} {
		assert.True(t, db.Migrator().HasIndex(&Task{}, name))
	}
	assert.True(t, db.Migrator().HasIndex(&SystemTask{}, "idx_system_tasks_pending_type_id"))
	row := QuotaData{UserID: 1, Username: "alice", ModelName: "gpt", CreatedAt: 3600, Count: 1, Quota: 10}
	require.NoError(t, db.Create(&row).Error)
	row.Id = 0
	assert.Error(t, db.Create(&row).Error, "statistics dimensions must remain unique after restart")
}
