package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(string) error {
		common.IsMasterNode = true
		common.RedisEnabled = false
		if err := model.InitDB(); err != nil {
			return err
		}
		return model.InitLogDB()
	}))
}

// keepPackageDatabases restores model.DB, model.LOG_DB and their database
// types when the test ends, closing the connections the test put in their
// place.
func keepPackageDatabases(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		for _, opened := range []*gorm.DB{model.DB, model.LOG_DB} {
			if opened != previousDB && opened != previousLogDB {
				closeTestDatabase(opened)
			}
		}
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
	})
}

// openTestSchema returns a connection to a new empty schema and its DSN.
func openTestSchema(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := pgtest.NewSchema(t)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { closeTestDatabase(db) })
	return db, dsn
}

func closeTestDatabase(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// newIsolatedDatabase gives a test its own empty schema, for tests that run
// the production migrations on a fresh or upgraded database. It points SQL_DSN
// at the schema and returns a connection to it and its DSN; the package
// database comes back when the test ends.
func newIsolatedDatabase(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	db, dsn := openTestSchema(t)
	keepPackageDatabases(t)
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", "")
	return db, dsn
}

// useSeparateLogDatabase moves LOG_DB to its own migrated schema, as when
// LOG_SQL_DSN names a separate log database, until the test ends.
func useSeparateLogDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := pgtest.NewSchema(t)
	keepPackageDatabases(t)
	t.Setenv("LOG_SQL_DSN", dsn)
	require.NoError(t, model.InitLogDB())
	return model.LOG_DB
}
