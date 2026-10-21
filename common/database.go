package common

type DatabaseType string

const (
	DatabaseTypePostgreSQL DatabaseType = "postgres"
	DatabaseTypeClickHouse DatabaseType = "clickhouse"
)

// The primary database is always PostgreSQL. The log database is the primary
// database unless LOG_SQL_DSN names a separate PostgreSQL or ClickHouse server.
var logDatabaseType = DatabaseTypePostgreSQL

func LogDatabaseType() DatabaseType {
	return logDatabaseType
}

func SetLogDatabaseType(databaseType DatabaseType) {
	logDatabaseType = databaseType
}

func UsingLogDatabase(databaseType DatabaseType) bool {
	return logDatabaseType == databaseType
}
