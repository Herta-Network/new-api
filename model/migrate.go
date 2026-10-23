package model

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// migrationFiles holds the versioned schema migrations. Each file is sent as
// one multi-statement query, which PostgreSQL runs as a single transaction.
//
//go:embed migrations
var migrationFiles embed.FS

// migrationSet is one directory of migrations and the table that records the
// version a database has reached. markerTable is a table of the baseline: a
// database that has it but no recorded version predates versioned migrations.
type migrationSet struct {
	name        string
	dir         string
	table       string
	markerTable string
}

var (
	mainMigrations = migrationSet{name: "main", dir: "migrations/main", table: "schema_migrations", markerTable: "users"}
	logMigrations  = migrationSet{name: "log", dir: "migrations/log", table: "log_schema_migrations", markerTable: "logs"}
)

// legacySchemaRelease is the last release that built the schema with GORM
// AutoMigrate; the baseline migrations reproduce the schema it creates.
const legacySchemaRelease = "732e212f3"

// runMigrations brings the database behind db up to the newest migration in
// set. It opens its own connection from db's DSN, because the migrate driver
// closes the database it is given.
func runMigrations(db *gorm.DB, fsys fs.FS, set migrationSet) error {
	dialector, ok := db.Dialector.(*postgres.Dialector)
	if !ok || dialector.DSN == "" {
		return errors.New("versioned migrations need a PostgreSQL connection opened from a DSN")
	}
	config, err := pgx.ParseConfig(dialector.DSN)
	if err != nil {
		return fmt.Errorf("parse the database DSN: %w", err)
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()
	files, err := iofs.New(fsys, set.dir)
	if err != nil {
		return err
	}
	baseline, err := files.First()
	if err != nil {
		return fmt.Errorf("read the %s migrations: %w", set.name, err)
	}
	latest := baseline
	for {
		next, err := files.Next(latest)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		latest = next
	}

	// Decide before the driver creates the version table, so that a refused
	// database is left untouched.
	var hasVersionTable, hasMarker bool
	if err := sqlDB.QueryRow("SELECT to_regclass($1) IS NOT NULL, to_regclass($2) IS NOT NULL", set.table, set.markerTable).Scan(&hasVersionTable, &hasMarker); err != nil {
		return fmt.Errorf("inspect the %s schema: %w", set.name, err)
	}
	recorded := false
	if hasVersionTable {
		if err := sqlDB.QueryRow("SELECT EXISTS (SELECT 1 FROM " + pgx.Identifier{set.table}.Sanitize() + ")").Scan(&recorded); err != nil {
			return fmt.Errorf("read the %s schema version: %w", set.name, err)
		}
	}
	adopt := !recorded && hasMarker
	if adopt {
		if err := checkBaselineSchema(sqlDB, files, baseline); err != nil {
			return fmt.Errorf("adopt the existing %s schema: %w", set.name, err)
		}
	}

	driver, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{MigrationsTable: set.table})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", files, "pgx5", driver)
	if err != nil {
		return err
	}
	defer m.Close()
	if adopt {
		if err := m.Force(int(baseline)); err != nil {
			return err
		}
		common.SysLog(common.LogText("adopted the existing %s schema at migration %d", set.name, baseline))
	}
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		err = nil
	}
	if err != nil {
		return err
	}
	if version > latest {
		common.SysLog(common.LogText("the %s schema is at migration %d, newer than the %d migrations of this build; skipping its migrations", set.name, version, latest))
		return nil
	}
	if dirty {
		// The failed migration ran in one transaction, so the database is still
		// at the version before it.
		previous := -1
		if prev, err := files.Prev(version); err == nil {
			previous = int(prev)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		common.SysError(common.LogText("migration %d of the %s schema did not finish; running it again", version, set.name))
		if err := m.Force(previous); err != nil {
			return err
		}
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate the %s schema: %w (the next start runs the failed migration again; a CREATE INDEX CONCURRENTLY migration can leave an INVALID index that must be dropped first)", set.name, err)
	}
	if current, _, err := m.Version(); err == nil && current != version {
		common.SysLog(common.LogText("migrated the %s schema from migration %d to %d", set.name, version, current))
	}
	return nil
}

// checkBaselineSchema compares a database created before versioned migrations
// with the baseline it is adopted at. It builds the baseline in a scratch
// schema inside a transaction it rolls back, so the database is left as it
// was. Objects the database has beyond the baseline are kept and logged.
func checkBaselineSchema(db *sql.DB, files source.Driver, baseline uint) error {
	reader, _, err := files.ReadUp(baseline)
	if err != nil {
		return err
	}
	baselineSQL, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow("SELECT current_schema()").Scan(&current); err != nil {
		return err
	}
	const scratch = "newapi_baseline_check"
	for _, statement := range []string{"CREATE SCHEMA " + scratch, "SET LOCAL search_path TO " + scratch, string(baselineSQL)} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("build the baseline schema: %w", err)
		}
	}
	expected, err := describeSchema(tx, scratch)
	if err != nil {
		return err
	}
	actual, err := describeSchema(tx, current)
	if err != nil {
		return err
	}
	var problems, extras []string
	for key, definition := range expected {
		found, ok := actual[key]
		switch {
		case !ok:
			problems = append(problems, strings.TrimSpace("missing "+key+" "+definition))
		case found != definition:
			problems = append(problems, fmt.Sprintf("%s is %q, expected %q", key, found, definition))
		}
	}
	for key := range actual {
		_, object, _ := strings.Cut(key, " ")
		table, _, _ := strings.Cut(object, ".")
		if _, inBaseline := expected["table "+table]; !inBaseline {
			continue
		}
		if _, ok := expected[key]; !ok {
			extras = append(extras, key)
		}
	}
	slices.Sort(problems)
	slices.Sort(extras)
	if len(extras) > 0 {
		common.SysLog(common.LogText("keeping objects that the baseline schema does not have: %s", strings.Join(extras, ", ")))
	}
	if len(problems) > 0 {
		return fmt.Errorf("the database does not match the schema of release %s; start that release against it once, then retry: %s", legacySchemaRelease, strings.Join(problems, "; "))
	}
	return nil
}

// describeSchema lists the tables, columns, indexes, and constraints of a
// schema as "kind table[.name]" keys and their definitions, with the schema
// name left out so that two schemas can be compared. Version tables and NOT
// NULL constraints, which the column definitions already show, are left out.
func describeSchema(tx *sql.Tx, schema string) (map[string]string, error) {
	var quoted string
	if err := tx.QueryRow("SELECT quote_ident($1)", schema).Scan(&quoted); err != nil {
		return nil, err
	}
	versionTables := []any{schema, mainMigrations.table, logMigrations.table}
	described := map[string]string{}
	queries := []struct{ kind, query string }{
		{"table", `
SELECT c.relname, '', ''
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND c.relname NOT IN ($2, $3)`},
		{"column", `
SELECT c.relname, a.attname,
       format_type(a.atttypid, a.atttypmod)
       || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
       || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND c.relname NOT IN ($2, $3)
  AND a.attnum > 0 AND NOT a.attisdropped`},
		{"index", `
SELECT c.relname, i.relname, pg_get_indexdef(i.oid)
FROM pg_index x
JOIN pg_class c ON c.oid = x.indrelid
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname NOT IN ($2, $3)`},
		{"constraint", `
SELECT c.relname, k.conname, pg_get_constraintdef(k.oid)
FROM pg_constraint k
JOIN pg_class c ON c.oid = k.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname NOT IN ($2, $3) AND k.contype <> 'n'`},
	}
	for _, q := range queries {
		rows, err := tx.Query(q.query, versionTables...)
		if err != nil {
			return nil, fmt.Errorf("describe %ss of schema %s: %w", q.kind, schema, err)
		}
		for rows.Next() {
			var table, name, definition string
			if err := rows.Scan(&table, &name, &definition); err != nil {
				_ = rows.Close()
				return nil, err
			}
			key := q.kind + " " + table
			if name != "" {
				key += "." + name
			}
			described[key] = strings.ReplaceAll(definition, quoted+".", "")
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return described, nil
}
