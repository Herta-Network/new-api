package perfmetrics

import (
	"cmp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
)

func flushLoop() {
	for {
		interval := perf_metrics_setting.GetFlushIntervalMinutes()
		time.Sleep(time.Duration(interval) * time.Minute)
		setting := perf_metrics_setting.GetSetting()
		if !setting.Enabled {
			continue
		}
		flushCompletedBuckets()
		cleanupExpiredMetrics(setting.RetentionDays)
	}
}

type pendingMetric struct {
	key    bucketKey
	bucket *atomicBucket
	counts counters
}

var flushMutex sync.Mutex
var metricWriter = model.StatisticsBatch{WriterID: common.GetUUID()}
var pendingMetrics []pendingMetric

func flushCompletedBuckets() {
	flushMutex.Lock()
	defer flushMutex.Unlock()
	currentBucket := bucketStart(time.Now().Unix())
	for range 2 {
		if len(pendingMetrics) == 0 {
			hotBuckets.Range(func(key, value any) bool {
				k := key.(bucketKey)
				if k.bucketTs >= currentBucket {
					return true
				}
				bucket := value.(*atomicBucket)
				counts := bucket.snapshot()
				if counts.requestCount == 0 {
					deleteOldEmptyBucket(k, key)
					return true
				}
				pendingMetrics = append(pendingMetrics, pendingMetric{key: k, bucket: bucket, counts: counts})
				return true
			})
			if len(pendingMetrics) == 0 {
				return
			}
			slices.SortFunc(pendingMetrics, func(a, b pendingMetric) int {
				return cmp.Or(cmp.Compare(a.key.model, b.key.model), cmp.Compare(a.key.group, b.key.group), cmp.Compare(a.key.bucketTs, b.key.bucketTs))
			})
			metricWriter.Sequence++
		}
		rows := make([]model.PerfMetric, 0, len(pendingMetrics))
		for _, pending := range pendingMetrics {
			c := pending.counts
			rows = append(rows, model.PerfMetric{
				ModelName: pending.key.model, Group: pending.key.group, BucketTs: pending.key.bucketTs,
				RequestCount: c.requestCount, SuccessCount: c.successCount, TotalLatencyMs: c.totalLatencyMs,
				TtftSumMs: c.ttftSumMs, TtftCount: c.ttftCount, OutputTokens: c.outputTokens, GenerationMs: c.generationMs,
			})
		}
		if err := model.SavePerfMetricBatch(metricWriter, rows); err != nil {
			common.SysError(common.LogText("failed to flush performance metrics: %s", err.Error()))
			return
		}
		for _, pending := range pendingMetrics {
			c := pending.counts
			// Subtract only the acknowledged snapshot; samples arriving during I/O remain.
			pending.bucket.addCounters(counters{
				requestCount: -c.requestCount, successCount: -c.successCount, totalLatencyMs: -c.totalLatencyMs,
				ttftSumMs: -c.ttftSumMs, ttftCount: -c.ttftCount, outputTokens: -c.outputTokens, generationMs: -c.generationMs,
			})
			if pending.bucket.requestCount.Load() == 0 {
				deleteOldEmptyBucket(pending.key, pending.key)
			}
		}
		pendingMetrics = nil
	}
}

func deleteOldEmptyBucket(k bucketKey, rawKey any) {
	if k.bucketTs < bucketStart(time.Now().Add(-24*time.Hour).Unix()) {
		hotBuckets.Delete(rawKey)
	}
}

func cleanupExpiredMetrics(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	if err := model.DeletePerfMetricsBefore(cutoff); err != nil {
		common.SysError(common.LogText("failed to cleanup expired perf metrics: %s", err.Error()))
	}
}

func redisCounters(values map[string]string) counters {
	return counters{
		requestCount:   parseRedisInt(values["req"]),
		successCount:   parseRedisInt(values["ok"]),
		totalLatencyMs: parseRedisInt(values["lat"]),
		ttftSumMs:      parseRedisInt(values["ttft"]),
		ttftCount:      parseRedisInt(values["ttft_n"]),
		outputTokens:   parseRedisInt(values["out"]),
		generationMs:   parseRedisInt(values["gen_ms"]),
	}
}

func parseRedisInt(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
