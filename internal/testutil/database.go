package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// PostgresDSN isolates each fixture in a new schema on a local test server.
// Only that schema is removed; existing schemas and their data are untouched.
func PostgresDSN(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN")) == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	dsn, cleanup, err := postgresSchema()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	return dsn
}

func postgresSchema() (string, func() error, error) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		return "", nil, fmt.Errorf("TEST_POSTGRES_DSN is required for database tests")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", nil, fmt.Errorf("invalid TEST_POSTGRES_DSN")
	}
	if config.Host != "localhost" && !strings.HasPrefix(config.Host, "/") && !net.ParseIP(config.Host).IsLoopback() {
		return "", nil, fmt.Errorf("database tests require a local PostgreSQL instance")
	}
	admin, err := pgx.ConnectConfig(context.Background(), config)
	if err != nil {
		return "", nil, err
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		_ = admin.Close(context.Background())
		return "", nil, err
	}
	name := "newapi_test_" + hex.EncodeToString(suffix[:])
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		_ = admin.Close(context.Background())
		return "", nil, err
	}
	cleanup := func() error {
		_, err := admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		return errors.Join(err, admin.Close(context.Background()))
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, _ := url.Parse(dsn)
		query := parsed.Query()
		query.Set("search_path", name)
		parsed.RawQuery = query.Encode()
		return parsed.String(), cleanup, nil
	}
	return dsn + " search_path=" + name, cleanup, nil
}

// OpenDB uses the same protocol mode as the production PostgreSQL connection.
func OpenDB(t *testing.T, config *gorm.Config) (*gorm.DB, error) {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: PostgresDSN(t), PreferSimpleProtocol: true}), config)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, nil
}

// OpenMainDB gives TestMain fixtures the same isolation with explicit cleanup.
func OpenMainDB(config *gorm.Config) (*gorm.DB, func() error, error) {
	dsn, cleanup, err := postgresSchema()
	if err != nil {
		return nil, nil, err
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), config)
	if err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	return db, func() error { return errors.Join(sqlDB.Close(), cleanup()) }, nil
}
