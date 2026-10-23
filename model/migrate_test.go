package model

import (
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// schemaModels are the models stored in the main and log databases. Their
// GORM tags must describe the schema the migrations create.
var schemaModels = []any{
	&Channel{}, &Token{}, &User{}, &UserSession{}, &AuthFlow{}, &ExternalIdentityClaim{},
	&PasskeyCredential{}, &Option{}, &LoginEncryptionKey{}, &Redemption{}, &Ability{},
	&Midjourney{}, &QuotaData{}, &Task{}, &TaskPlugin{}, &Model{}, &Vendor{}, &PrefillGroup{},
	&Setup{}, &TwoFA{}, &TwoFABackupCode{}, &Checkin{}, &UserSubscription{},
	&SubscriptionPreConsumeRecord{}, &CustomOAuthProvider{}, &UserOAuthBinding{}, &PerfMetric{},
	&SystemInstance{}, &SystemTask{}, &SystemTaskLock{}, &CasbinRule{}, &AuthzRole{},
	&UserAccessToken{}, &SubscriptionPlan{}, &entity.PaymentMethod{}, &entity.PaymentOrder{},
	&entity.PaymentUnmatched{}, &entity.PaymentExchangeRate{},
	&Log{}, &AuditLog{},
}

// sqlOnlySchemaObjects lists objects a migration creates that no GORM tag can
// declare, keyed like describeSchema.
var sqlOnlySchemaObjects = map[string]bool{}

func describeTestSchema(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	tx, err := sqlDB.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	var schema string
	require.NoError(t, tx.QueryRow("SELECT current_schema()").Scan(&schema))
	described, err := describeSchema(tx, schema)
	require.NoError(t, err)
	return described
}

func migrateTestSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, runMigrations(db, migrationFiles, mainMigrations))
	require.NoError(t, runMigrations(db, migrationFiles, logMigrations))
}

// The migrations create exactly the schema the models declare. A model change
// without a migration, or a migration without the model change, fails here.
func TestMigrationsMatchModels(t *testing.T) {
	migrated := pgtest.NewSchemaDB(t)
	migrateTestSchema(t, migrated)
	declared := pgtest.NewSchemaDB(t)
	require.NoError(t, declared.AutoMigrate(schemaModels...))

	created := describeTestSchema(t, migrated)
	expected := describeTestSchema(t, declared)
	var differences []string
	for key, definition := range expected {
		found, ok := created[key]
		switch {
		case !ok:
			differences = append(differences, "no migration creates "+key+" "+definition)
		case found != definition:
			differences = append(differences, key+": the models declare "+definition+", the migrations create "+found)
		}
	}
	for key, definition := range created {
		if _, ok := expected[key]; !ok && !sqlOnlySchemaObjects[key] {
			differences = append(differences, "no model declares "+key+" "+definition)
		}
	}
	slices.Sort(differences)
	assert.Empty(t, differences, "add a migration under model/migrations that matches the GORM tags, or list an object no tag can declare in sqlOnlySchemaObjects")
}

// A database the last release before versioned migrations created is adopted
// at the baseline, keeps its rows, and ends up with the schema of a new
// database. One that differs from the baseline is refused and left untouched.
func TestStartupAdoptsExistingDatabase(t *testing.T) {
	createLegacy := func(t *testing.T) *gorm.DB {
		db := pgtest.NewSchemaDB(t)
		for _, set := range []migrationSet{mainMigrations, logMigrations} {
			baseline, err := fs.ReadFile(migrationFiles, set.dir+"/000001_baseline.up.sql")
			require.NoError(t, err)
			require.NoError(t, db.Exec(string(baseline)).Error)
		}
		return db
	}

	db := createLegacy(t)
	require.NoError(t, db.Create(&User{Username: "legacy", Password: "placeholder", AffCode: "legacy-aff", Quota: 42}).Error)
	require.NoError(t, db.Create(&Log{UserId: 1, Content: "historical", CreatedAt: 100}).Error)
	for range 2 {
		migrateTestSchema(t, db)
	}
	fresh := pgtest.NewSchemaDB(t)
	migrateTestSchema(t, fresh)
	assert.Equal(t, describeTestSchema(t, fresh), describeTestSchema(t, db))
	for _, table := range []string{mainMigrations.table, logMigrations.table} {
		var version, freshVersion int
		var dirty bool
		require.NoError(t, db.Raw("SELECT version, dirty FROM "+table).Row().Scan(&version, &dirty))
		require.NoError(t, fresh.Raw("SELECT version FROM "+table).Row().Scan(&freshVersion))
		assert.Equal(t, freshVersion, version, table)
		assert.False(t, dirty, table)
	}
	var user User
	require.NoError(t, db.Where("username = ?", "legacy").First(&user).Error)
	assert.Equal(t, 42, user.Quota)
	var log Log
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "historical", log.Content)

	drifted := createLegacy(t)
	require.NoError(t, drifted.Exec("ALTER TABLE users DROP COLUMN quota").Error)
	err := runMigrations(drifted, migrationFiles, mainMigrations)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing column users.quota")
	assert.Contains(t, err.Error(), legacySchemaRelease)
	var versionTable *string
	require.NoError(t, drifted.Raw("SELECT to_regclass(?)::text", mainMigrations.table).Row().Scan(&versionTable))
	assert.Nil(t, versionTable, "a refused database is left untouched")
}

// A failed migration is rolled back and runs again on the next start, and an
// older build leaves a database a newer one migrated alone.
func TestMigrationRetriesAfterFailure(t *testing.T) {
	db := pgtest.NewSchemaDB(t)
	set := migrationSet{name: "retry", dir: "m", table: "retry_schema_migrations", markerTable: "retry_marker"}
	files := fstest.MapFS{
		"m/000001_first.up.sql":  {Data: []byte("CREATE TABLE retry_first (id bigint);")},
		"m/000002_second.up.sql": {Data: []byte("CREATE TABLE retry_second (id bigint);\nINSERT INTO retry_missing VALUES (1);")},
	}
	state := func() (version int, dirty bool) {
		require.NoError(t, db.Raw("SELECT version, dirty FROM "+set.table).Row().Scan(&version, &dirty))
		return version, dirty
	}
	hasTable := func(name string) bool {
		var exists bool
		require.NoError(t, db.Raw("SELECT to_regclass(?) IS NOT NULL", name).Row().Scan(&exists))
		return exists
	}

	require.Error(t, runMigrations(db, files, set))
	version, dirty := state()
	assert.Equal(t, 2, version)
	assert.True(t, dirty)
	assert.True(t, hasTable("retry_first"))
	assert.False(t, hasTable("retry_second"), "a failed migration leaves nothing behind")

	files["m/000002_second.up.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE retry_second (id bigint);")}
	require.NoError(t, runMigrations(db, files, set))
	version, dirty = state()
	assert.Equal(t, 2, version)
	assert.False(t, dirty)
	assert.True(t, hasTable("retry_second"))

	delete(files, "m/000002_second.up.sql")
	require.NoError(t, runMigrations(db, files, set))
	version, _ = state()
	assert.Equal(t, 2, version)
}
