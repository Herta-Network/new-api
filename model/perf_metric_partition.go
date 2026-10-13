package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigratePerfMetrics targets a fresh schema. The dimensional primary key
// includes bucket_ts, so PostgreSQL can enforce it on a range-partitioned table.
func MigratePerfMetrics(db *gorm.DB) error {
	var kind string
	if err := db.Raw("SELECT relkind::text FROM pg_class WHERE oid = to_regclass('perf_metrics')").Scan(&kind).Error; err != nil {
		return err
	}
	if kind != "" && kind != "p" {
		return fmt.Errorf("perf_metrics requires a fresh partitioned PostgreSQL schema")
	}
	if err := db.Set("gorm:table_options", "PARTITION BY RANGE (bucket_ts)").AutoMigrate(&PerfMetric{}); err != nil {
		return err
	}
	now := time.Now().UTC()
	now = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return db.Transaction(func(tx *gorm.DB) error {
		return ensurePerfMetricPartitions(tx, []PerfMetric{{BucketTs: now.AddDate(0, -1, 0).Unix()}, {BucketTs: now.Unix()}, {BucketTs: now.AddDate(0, 1, 0).Unix()}})
	})
}

func ensurePerfMetricPartitions(tx *gorm.DB, rows []PerfMetric) error {
	starts := make([]int64, 0)
	for _, row := range rows {
		if row.BucketTs < 0 || row.BucketTs > 253402300799 {
			return fmt.Errorf("performance metric timestamp is out of range")
		}
		date := time.Unix(row.BucketTs, 0).UTC()
		start := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
		if !slices.Contains(starts, start) {
			starts = append(starts, start)
		}
	}
	slices.Sort(starts)
	for _, start := range starts {
		date := time.Unix(start, 0).UTC()
		name := "perf_metrics_" + date.Format("2006_01")
		var exists bool
		if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM pg_inherits WHERE inhparent = to_regclass('perf_metrics') AND inhrelid = to_regclass(?))", name).Scan(&exists).Error; err != nil {
			return err
		}
		if exists {
			continue
		}
		// The lock covers DDL and the following insert. Writers never race to create
		// a month's partition, and retention cannot detach it during that write.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.perf_metrics', 0))").Error; err != nil {
			return err
		}
		end := date.AddDate(0, 1, 0).Unix()
		// Only an internally generated identifier and validated integer bounds enter DDL.
		if err := tx.Exec(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF perf_metrics FOR VALUES FROM (%d) TO (%d)", name, start, end)).Error; err != nil {
			return err
		}
	}
	return nil
}

var perfMetricPartitionBounds = regexp.MustCompile(`^FOR VALUES FROM \('(-?[0-9]+)'\) TO \('(-?[0-9]+)'\)$`)

// DeletePerfMetricsBefore removes complete expired months through DDL, then
// deletes the boundary month's rows in short, bounded transactions.
func DeletePerfMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '1s'").Error; err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.perf_metrics', 0))").Error; err != nil {
			return err
		}
		var partitions []struct{ Name, Bound string }
		if err := tx.Raw(`SELECT c.relname AS name, pg_get_expr(c.relpartbound,c.oid) AS bound
   FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
   WHERE i.inhparent=to_regclass('perf_metrics')
     AND c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname=current_schema())
   ORDER BY c.relname`).Scan(&partitions).Error; err != nil {
			return err
		}
		for _, partition := range partitions {
			bounds := perfMetricPartitionBounds.FindStringSubmatch(partition.Bound)
			if len(bounds) != 3 {
				continue
			}
			start, err := strconv.ParseInt(bounds[1], 10, 64)
			if err != nil {
				return err
			}
			end, err := strconv.ParseInt(bounds[2], 10, 64)
			if err != nil {
				return err
			}
			date := time.Unix(start, 0).UTC()
			if end > cutoffTs || end != date.AddDate(0, 1, 0).Unix() || partition.Name != "perf_metrics_"+date.Format("2006_01") {
				continue
			}
			if err := tx.Exec("DROP TABLE ?", clause.Table{Name: partition.Name}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for range 100 {
		candidates := DB.Model(&PerfMetric{}).Select(`model_name, "group", bucket_ts`).Where("bucket_ts < ?", cutoffTs).Order("bucket_ts").Limit(1000)
		result := DB.Where("bucket_ts < ?", cutoffTs).Where(`(model_name, "group", bucket_ts) IN (?)`, candidates).Delete(&PerfMetric{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected < 1000 {
			return nil
		}
	}
	return nil
}
