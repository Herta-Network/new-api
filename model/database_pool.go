package model

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"
)

// configureDatabasePool keeps per-process connections bounded. A separate log
// database inherits SQL_* unless the corresponding LOG_SQL_* value is set.
func configureDatabasePool(db *sql.DB, envName string) error {
	values := make(map[string]int, 4)
	for _, option := range []struct {
		name                       string
		fallback, minimum, maximum int
	}{
		{"MAX_OPEN_CONNS", 20, 1, 10000},
		{"MAX_IDLE_CONNS", 5, 0, 10000},
		{"MAX_LIFETIME", 1800, 0, 604800},
		{"MAX_IDLE_TIME", 300, 0, 604800},
	} {
		key := "SQL_" + option.name
		raw := os.Getenv(key)
		if envName == "LOG_SQL_DSN" {
			if override, ok := os.LookupEnv("LOG_" + key); ok {
				key, raw = "LOG_"+key, override
			}
		}
		value := option.fallback
		if raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < option.minimum || parsed > option.maximum {
				return fmt.Errorf("%s must be an integer between %d and %d", key, option.minimum, option.maximum)
			}
			value = parsed
		}
		values[option.name] = value
	}
	db.SetMaxOpenConns(values["MAX_OPEN_CONNS"])
	db.SetMaxIdleConns(min(values["MAX_IDLE_CONNS"], values["MAX_OPEN_CONNS"]))
	db.SetConnMaxLifetime(time.Duration(values["MAX_LIFETIME"]) * time.Second)
	db.SetConnMaxIdleTime(time.Duration(values["MAX_IDLE_TIME"]) * time.Second)
	return nil
}

type DatabasePoolStats struct {
	Name               string `json:"name"`
	MaxOpenConnections int    `json:"max_open_connections"`
	OpenConnections    int    `json:"open_connections"`
	InUse              int    `json:"in_use"`
	Idle               int    `json:"idle"`
	WaitCount          int64  `json:"wait_count"`
	WaitDurationMS     int64  `json:"wait_duration_ms"`
	MaxIdleClosed      int64  `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64  `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64  `json:"max_lifetime_closed"`
}

// GetDatabasePoolStats performs no database query and never exposes DSNs. A
// shared primary/log pool appears once; counters are cumulative since startup.
func GetDatabasePoolStats() []DatabasePoolStats {
	result := make([]DatabasePoolStats, 0, 2)
	var mainPool *sql.DB
	for _, name := range []string{"main", "log"} {
		db := DB
		if name == "log" {
			db = LOG_DB
		}
		if db == nil {
			continue
		}
		pool, err := db.DB()
		if err != nil || pool == mainPool {
			continue
		}
		if name == "main" {
			mainPool = pool
		}
		stats := pool.Stats()
		result = append(result, DatabasePoolStats{
			Name: name, MaxOpenConnections: stats.MaxOpenConnections, OpenConnections: stats.OpenConnections,
			InUse: stats.InUse, Idle: stats.Idle, WaitCount: stats.WaitCount, WaitDurationMS: stats.WaitDuration.Milliseconds(),
			MaxIdleClosed: stats.MaxIdleClosed, MaxIdleTimeClosed: stats.MaxIdleTimeClosed, MaxLifetimeClosed: stats.MaxLifetimeClosed,
		})
	}
	return result
}
