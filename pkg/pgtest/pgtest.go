// Package pgtest runs database tests against a real PostgreSQL server.
//
// TEST_POSTGRES_DSN names a database the tests may create schemas in. Each
// test package gets its own schema, so packages that go test runs in parallel
// processes never see each other's rows; Reset empties the schema between
// tests. Tests fail when TEST_POSTGRES_DSN is not set: there is no fallback
// database.
package pgtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DSNEnv names the environment variable that holds the test server DSN.
const DSNEnv = "TEST_POSTGRES_DSN"

const (
	schemaPrefix = "newapi_t_"
	// staleSchemaAge is how old a schema left behind by a killed run must be
	// before another run drops it.
	staleSchemaAge = 6 * time.Hour
	// sequenceStart is where Reset restarts every sequence, far above the
	// explicit ids tests insert, so generated ids never collide with them.
	sequenceStart = 1_000_000
)

var nonIdentifier = regexp.MustCompile(`[^a-z0-9_]+`)

// Main creates a schema for the test package, points SQL_DSN at it, runs
// setup and the tests, and drops the schema. It returns the exit code for
// os.Exit. setup receives the package DSN; it usually initializes the
// application database from SQL_DSN.
func Main(m *testing.M, setup func(dsn string) error) int {
	baseDSN := os.Getenv(DSNEnv)
	if baseDSN == "" {
		fmt.Fprintf(os.Stderr, "%s is not set: database tests need a PostgreSQL server (for example postgres://postgres:postgres@localhost:5432/newapi_test?sslmode=disable; see `make test-db`)\n", DSNEnv)
		return 1
	}
	admin, err := open(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: connect to %s: %v\n", DSNEnv, err)
		return 1
	}
	defer closeDB(admin)

	// pgx's simple protocol, which the application uses, refuses any other
	// encoding.
	var encoding string
	if err := admin.Raw("SHOW server_encoding").Scan(&encoding).Error; err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: read server encoding: %v\n", err)
		return 1
	}
	if !strings.EqualFold(encoding, "UTF8") {
		fmt.Fprintf(os.Stderr, "pgtest: the test database uses encoding %s; create it with ENCODING 'UTF8'\n", encoding)
		return 1
	}
	dropStaleSchemas(admin, time.Now())
	schema := newSchemaName(packageName(), time.Now())
	if err := admin.Exec("CREATE SCHEMA " + quoteIdent(schema)).Error; err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: create schema %s: %v\n", schema, err)
		return 1
	}
	defer func() {
		if err := admin.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schema) + " CASCADE").Error; err != nil {
			fmt.Fprintf(os.Stderr, "pgtest: drop schema %s: %v\n", schema, err)
		}
	}()

	dsn, err := withSearchPath(baseDSN, schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		return 1
	}
	// go test runs packages as parallel processes; keep each one's pool small
	// so the server's connection limit holds for the whole run.
	setEnvDefault("SQL_MAX_OPEN_CONNS", "8")
	setEnvDefault("SQL_MAX_IDLE_CONNS", "2")
	if err := os.Setenv("SQL_DSN", dsn); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		return 1
	}
	if err := os.Unsetenv("LOG_SQL_DSN"); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		return 1
	}
	if setup != nil {
		if err := setup(dsn); err != nil {
			fmt.Fprintf(os.Stderr, "pgtest: setup: %v\n", err)
			return 1
		}
	}
	return m.Run()
}

// Reset empties every table in db's schema and restarts its sequences at a
// high value, giving the next test an empty database with migrations intact.
func Reset(tb testing.TB, db *gorm.DB) {
	tb.Helper()
	var tables []string
	if err := db.Raw("SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename").Scan(&tables).Error; err != nil {
		tb.Fatalf("pgtest: list tables: %v", err)
	}
	if len(tables) > 0 {
		quoted := make([]string, len(tables))
		for i, table := range tables {
			quoted[i] = quoteIdent(table)
		}
		if err := db.Exec("TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE").Error; err != nil {
			tb.Fatalf("pgtest: truncate: %v", err)
		}
	}
	restart := fmt.Sprintf("SELECT setval(format('%%I', sequence_name)::regclass, %d, false) FROM information_schema.sequences WHERE sequence_schema = current_schema()", sequenceStart)
	if err := db.Exec(restart).Error; err != nil {
		tb.Fatalf("pgtest: restart sequences: %v", err)
	}
}

// NewSchema creates an empty schema for one test, such as a migration test or
// a separate log database, and returns its DSN. The schema is dropped when the
// test ends.
func NewSchema(tb testing.TB) string {
	tb.Helper()
	baseDSN := os.Getenv(DSNEnv)
	if baseDSN == "" {
		tb.Fatalf("pgtest: %s is not set", DSNEnv)
	}
	admin, err := open(baseDSN)
	if err != nil {
		tb.Fatalf("pgtest: connect: %v", err)
	}
	schema := newSchemaName(packageName()+"_x", time.Now())
	if err := admin.Exec("CREATE SCHEMA " + quoteIdent(schema)).Error; err != nil {
		closeDB(admin)
		tb.Fatalf("pgtest: create schema %s: %v", schema, err)
	}
	tb.Cleanup(func() {
		defer closeDB(admin)
		if err := admin.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schema) + " CASCADE").Error; err != nil {
			tb.Errorf("pgtest: drop schema %s: %v", schema, err)
		}
	})
	dsn, err := withSearchPath(baseDSN, schema)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}
	return dsn
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// withSearchPath adds search_path to a URL DSN; pgx sends it as a runtime
// parameter, so every pooled connection starts in the schema.
func withSearchPath(dsn string, schema string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", fmt.Errorf("%s must be a postgres:// URL", DSNEnv)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// newSchemaName embeds the creation time so that a later run can tell when a
// schema left behind by a killed run is stale.
func newSchemaName(pkg string, now time.Time) string {
	random := make([]byte, 4)
	_, _ = rand.Read(random)
	name := fmt.Sprintf("%s%s_%d_%s", schemaPrefix, pkg, now.Unix(), hex.EncodeToString(random))
	// PostgreSQL truncates identifiers at 63 bytes; keep the suffix that
	// carries the time and the random part.
	if len(name) > 63 {
		name = schemaPrefix + name[len(name)-(63-len(schemaPrefix)):]
	}
	return name
}

var schemaTime = regexp.MustCompile(`_(\d{10})_[0-9a-f]{8}$`)

func dropStaleSchemas(admin *gorm.DB, now time.Time) {
	var schemas []string
	if err := admin.Raw("SELECT nspname FROM pg_namespace WHERE nspname LIKE ?", schemaPrefix+"%").Scan(&schemas).Error; err != nil {
		return
	}
	for _, schema := range schemas {
		match := schemaTime.FindStringSubmatch(schema)
		if match == nil {
			continue
		}
		created, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || now.Sub(time.Unix(created, 0)) < staleSchemaAge {
			continue
		}
		_ = admin.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schema) + " CASCADE").Error
	}
}

// packageName derives a schema-safe name from the test binary, which go test
// names after the package (authz.test).
func packageName() string {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")
	name = nonIdentifier.ReplaceAllString(strings.ToLower(name), "_")
	if name == "" {
		return "pkg"
	}
	return name
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func setEnvDefault(key string, value string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}
