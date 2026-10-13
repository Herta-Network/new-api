package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PerfMetric stores aggregated relay performance metrics for the model square.
type PerfMetric struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ModelName      string `json:"model_name" gorm:"size:128;uniqueIndex:idx_perf_model_group_bucket,priority:1"`
	Group          string `json:"group" gorm:"column:group;size:64;uniqueIndex:idx_perf_model_group_bucket,priority:2"`
	BucketTs       int64  `json:"bucket_ts" gorm:"uniqueIndex:idx_perf_model_group_bucket,priority:3;index:idx_perf_bucket_ts"`
	RequestCount   int64  `json:"-" gorm:"not null;default:0"`
	SuccessCount   int64  `json:"-" gorm:"not null;default:0"`
	TotalLatencyMs int64  `json:"-" gorm:"not null;default:0"`
	TtftSumMs      int64  `json:"-" gorm:"not null;default:0"`
	TtftCount      int64  `json:"-" gorm:"not null;default:0"`
	OutputTokens   int64  `json:"-" gorm:"not null;default:0"`
	GenerationMs   int64  `json:"-" gorm:"not null;default:0"`
}

func (PerfMetric) TableName() string {
	return "perf_metrics"
}

func UpsertPerfMetric(metric *PerfMetric) error {
	if metric == nil || metric.RequestCount == 0 {
		return nil
	}
	return upsertPerfMetrics(DB, []PerfMetric{*metric})
}

func SavePerfMetricBatch(batch StatisticsBatch, metrics []PerfMetric) error {
	if len(metrics) == 0 {
		return nil
	}
	return batch.Apply(func(tx *gorm.DB) error { return upsertPerfMetrics(tx, metrics) })
}

func upsertPerfMetrics(db *gorm.DB, metrics []PerfMetric) error {
	return db.Omit("id").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "model_name"}, {Name: "group"}, {Name: "bucket_ts"}},
		DoUpdates: clause.Assignments(map[string]any{
			"request_count":    gorm.Expr("perf_metrics.request_count + EXCLUDED.request_count"),
			"success_count":    gorm.Expr("perf_metrics.success_count + EXCLUDED.success_count"),
			"total_latency_ms": gorm.Expr("perf_metrics.total_latency_ms + EXCLUDED.total_latency_ms"),
			"ttft_sum_ms":      gorm.Expr("perf_metrics.ttft_sum_ms + EXCLUDED.ttft_sum_ms"),
			"ttft_count":       gorm.Expr("perf_metrics.ttft_count + EXCLUDED.ttft_count"),
			"output_tokens":    gorm.Expr("perf_metrics.output_tokens + EXCLUDED.output_tokens"),
			"generation_ms":    gorm.Expr("perf_metrics.generation_ms + EXCLUDED.generation_ms"),
		}),
	}).CreateInBatches(&metrics, statisticsBatchSize).Error
}

func GetPerfMetrics(modelName string, group string, startTs int64, endTs int64) ([]PerfMetric, error) {
	var metrics []PerfMetric
	query := DB.Model(&PerfMetric{}).
		Where("model_name = ? AND bucket_ts >= ? AND bucket_ts <= ?", modelName, startTs, endTs)
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	err := query.Order("bucket_ts ASC").Find(&metrics).Error
	return metrics, err
}

type PerfMetricSummary struct {
	ModelName      string `json:"model_name"`
	RequestCount   int64  `json:"request_count"`
	SuccessCount   int64  `json:"success_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	OutputTokens   int64  `json:"output_tokens"`
	GenerationMs   int64  `json:"generation_ms"`
}

type PerfMetricSummaryBucket struct {
	ModelName      string `json:"model_name"`
	BucketTs       int64  `json:"bucket_ts"`
	RequestCount   int64  `json:"request_count"`
	SuccessCount   int64  `json:"success_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	OutputTokens   int64  `json:"output_tokens"`
	GenerationMs   int64  `json:"generation_ms"`
}

func GetPerfMetricsSummaryAll(startTs int64, endTs int64, groups []string) ([]PerfMetricSummary, error) {
	var summaries []PerfMetricSummary
	query := DB.Model(&PerfMetric{}).
		Select("model_name, SUM(request_count) as request_count, SUM(success_count) as success_count, SUM(total_latency_ms) as total_latency_ms, SUM(output_tokens) as output_tokens, SUM(generation_ms) as generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		if len(groups) == 0 {
			return summaries, nil
		}
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.
		Group("model_name").
		Having("SUM(request_count) > 0").
		Find(&summaries).Error
	return summaries, err
}

func GetPerfMetricsSummaryBucketsAll(startTs int64, endTs int64, groups []string) ([]PerfMetricSummaryBucket, error) {
	var summaries []PerfMetricSummaryBucket
	query := DB.Model(&PerfMetric{}).
		Select("model_name, bucket_ts, SUM(request_count) as request_count, SUM(success_count) as success_count, SUM(total_latency_ms) as total_latency_ms, SUM(output_tokens) as output_tokens, SUM(generation_ms) as generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		if len(groups) == 0 {
			return summaries, nil
		}
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.
		Group("model_name, bucket_ts").
		Having("SUM(request_count) > 0").
		Order("bucket_ts ASC").
		Find(&summaries).Error
	return summaries, err
}

// GetRecentPerfMetricSuccessBuckets reads each requested model's most recent
// nonempty hours in one database query. Ranking happens after hour aggregation
// and permission filtering, so each model keeps its own sample limit.
func GetRecentPerfMetricSuccessBuckets(modelNames []string, startTs int64, endTs int64, groups []string, limit int) ([]PerfMetricSummaryBucket, error) {
	if len(modelNames) == 0 || limit <= 0 || (groups != nil && len(groups) == 0) {
		return nil, nil
	}
	const hourBucket = "bucket_ts - (bucket_ts % 3600)"
	hourly := DB.Model(&PerfMetric{}).
		Select("model_name, "+hourBucket+" AS bucket_ts, SUM(request_count) AS request_count, SUM(success_count) AS success_count").
		Where("model_name IN ? AND bucket_ts >= ? AND bucket_ts <= ? AND request_count > 0", modelNames, startTs, endTs).
		Group("model_name, " + hourBucket)
	if groups != nil {
		hourly = hourly.Where(commonGroupCol+" IN ?", groups)
	}
	ranked := DB.Table("(?) AS hourly", hourly).
		Select("hourly.*, ROW_NUMBER() OVER (PARTITION BY model_name ORDER BY bucket_ts DESC) AS sample_rank")
	var buckets []PerfMetricSummaryBucket
	err := DB.Table("(?) AS ranked", ranked).
		Select("model_name, bucket_ts, request_count, success_count").Where("sample_rank <= ?", limit).
		Order("model_name, bucket_ts DESC").Find(&buckets).Error
	return buckets, err
}

func DeletePerfMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfMetric{}).Error
}

func PerfMetricStartTime(hours int) int64 {
	if hours <= 0 {
		hours = 24
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
}
