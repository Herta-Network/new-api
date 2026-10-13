package model

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedFlowQuotaData(t *testing.T, quotaData QuotaData) {
	t.Helper()
	require.NoError(t, DB.Create(&quotaData).Error)
}

func seedFlowLookupData(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "west"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, DB.Delete(&Token{Id: 11}).Error)
}

func TestGetFlowQuotaDataUsesQuotaDataRoleSpecificDimensions(t *testing.T) {
	truncateTables(t)
	seedFlowLookupData(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1000,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1100,
		Count:     1,
		Quota:     50,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 2,
		CreatedAt: 1200,
		Count:     1,
		Quota:     25,
		TokenUsed: 10,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "default",
		ModelName: "gpt-b",
		ChannelID: 1,
		CreatedAt: 1300,
		Count:     3,
		Quota:     70,
		TokenUsed: 30,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "legacy",
		CreatedAt: 1400,
		Count:     99,
		Quota:     999,
		TokenUsed: 999,
	})

	rootRows, err := GetFlowQuotaData(900, 2000, "", 0, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rootRows, 3)
	// Token 11 was soft-deleted, so its name is intentionally left empty for the
	// frontend to render a localized "deleted (id)" label instead.
	require.Equal(t, FlowQuotaData{
		UserID:      1,
		Username:    "alice",
		NodeName:    "node-a",
		TokenID:     11,
		TokenName:   "",
		UseGroup:    "vip",
		ChannelID:   1,
		ChannelName: "east",
		ModelName:   "gpt-a",
		TokenUsed:   60,
		Count:       3,
		Quota:       150,
	}, *rootRows[0])
	// A token that still exists resolves to its current name.
	require.Equal(t, 22, rootRows[1].TokenID)
	require.Equal(t, "backup", rootRows[1].TokenName)

	adminRows, err := GetFlowQuotaData(900, 2000, "alice", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, adminRows, 2)
	require.Equal(t, 0, adminRows[0].TokenID)
	require.Empty(t, adminRows[0].TokenName)
	require.Empty(t, adminRows[0].NodeName)
	require.Equal(t, "alice", adminRows[0].Username)
	require.Equal(t, "vip", adminRows[0].UseGroup)
	require.Equal(t, "east", adminRows[0].ChannelName)
	require.Equal(t, 150, adminRows[0].Quota)

	selfRows, err := GetFlowQuotaData(900, 2000, "", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Empty(t, selfRows[0].Username)
	require.Equal(t, 0, selfRows[0].ChannelID)
	require.Empty(t, selfRows[0].ChannelName)
	require.Empty(t, selfRows[0].TokenName)
	require.Equal(t, "vip", selfRows[0].UseGroup)
	require.Equal(t, 175, selfRows[0].Quota)
}

func TestLogQuotaDataSplitsRowsByUseGroupTokenChannelAndNode(t *testing.T) {
	truncateTables(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3661,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     100,
		TokenUsed: 40,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     50,
		TokenUsed: 20,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     25,
		TokenUsed: 10,
	})

	SaveQuotaDataCache()

	var rows []QuotaData
	require.NoError(t, DB.Order("quota DESC").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, int64(3600), rows[0].CreatedAt)
	require.Equal(t, "vip", rows[0].UseGroup)
	require.Equal(t, 11, rows[0].TokenID)
	require.Equal(t, 1, rows[0].ChannelID)
	require.Equal(t, "node-a", rows[0].NodeName)
	require.Equal(t, 2, rows[0].Count)
	require.Equal(t, 150, rows[0].Quota)
	require.Equal(t, 60, rows[0].TokenUsed)
	require.Equal(t, "default", rows[1].UseGroup)
	require.Equal(t, 25, rows[1].Quota)
}

func TestQuotaDataBatchRetryPreservesPendingAndNewSamples(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&QuotaData{}, &StatisticsWriteCheckpoint{}))
	previousDB, previousCache, previousPending, previousWriter := DB, CacheQuotaData, pendingQuotaData, quotaDataWriter
	DB = db
	CacheQuotaData = map[string]*QuotaData{}
	pendingQuotaData = nil
	quotaDataWriter = StatisticsBatch{WriterID: common.GetUUID()}
	t.Cleanup(func() {
		DB, CacheQuotaData, pendingQuotaData, quotaDataWriter = previousDB, previousCache, previousPending, previousWriter
	})
	sample := QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt", CreatedAt: 3600, Quota: 10, TokenUsed: 5}
	LogQuotaData(sample)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:quota_flush_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "quota_data" {
			LogQuotaData(sample) // Collection remains available while the database write runs.
			tx.AddError(errors.New("write failed"))
		}
	}))
	SaveQuotaDataCache()
	require.NoError(t, db.Callback().Create().Remove("test:quota_flush_failure"))
	var count int64
	require.NoError(t, db.Model(&QuotaData{}).Count(&count).Error)
	assert.Zero(t, count, "a failed flush must not partially commit")
	require.NoError(t, db.Model(&StatisticsWriteCheckpoint{}).Count(&count).Error)
	assert.Zero(t, count, "a rolled-back flush must not acknowledge the batch")
	SaveQuotaDataCache()
	SaveQuotaDataCache()
	var rows []QuotaData
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Count)
	assert.Equal(t, 20, rows[0].Quota)
	assert.Equal(t, 10, rows[0].TokenUsed)
}

func TestStatisticsBatchReplayIsAppliedOnce(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&StatisticsWriteCheckpoint{}))
	require.NoError(t, MigratePerfMetrics(db))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	batch := StatisticsBatch{WriterID: common.GetUUID(), Sequence: 1}
	rows := []PerfMetric{{ModelName: "gpt", Group: "a", BucketTs: 3600, RequestCount: 3, SuccessCount: 2, TotalLatencyMs: 120}}
	for range 2 {
		require.NoError(t, SavePerfMetricBatch(batch, rows))
	}
	batch.Sequence++
	require.NoError(t, SavePerfMetricBatch(batch, rows))
	batch.Sequence--
	require.NoError(t, SavePerfMetricBatch(batch, rows), "a delayed retry cannot overwrite a later checkpoint")
	var stored PerfMetric
	require.NoError(t, db.First(&stored).Error)
	assert.EqualValues(t, 6, stored.RequestCount)
	assert.EqualValues(t, 4, stored.SuccessCount)
	assert.EqualValues(t, 240, stored.TotalLatencyMs)
}

func TestStatisticsBatchRollsBackEveryChunk(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&StatisticsWriteCheckpoint{}))
	require.NoError(t, MigratePerfMetrics(db))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	// 501 distinct aggregates cross the SQL chunk boundary; the final row fails.
	rows := make([]PerfMetric, 501)
	for i := range rows {
		rows[i] = PerfMetric{ModelName: fmt.Sprintf("model-%d", i), Group: "a", BucketTs: 3600, RequestCount: 1}
	}
	rows[500].ModelName = strings.Repeat("x", 129)
	batch := StatisticsBatch{WriterID: common.GetUUID(), Sequence: 1}
	require.Error(t, SavePerfMetricBatch(batch, rows))
	var count int64
	require.NoError(t, db.Model(&PerfMetric{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&StatisticsWriteCheckpoint{}).Count(&count).Error)
	assert.Zero(t, count)
	rows[500].ModelName = "model-500"
	require.NoError(t, SavePerfMetricBatch(batch, rows))
	require.NoError(t, SavePerfMetricBatch(batch, rows))
	require.NoError(t, db.Model(&PerfMetric{}).Count(&count).Error)
	assert.EqualValues(t, 501, count)
	var total int64
	require.NoError(t, db.Model(&PerfMetric{}).Select("SUM(request_count)").Scan(&total).Error)
	assert.EqualValues(t, 501, total)
}

func TestPerfMetricPartitionsPruneMonthsAndKeepBoundaryRows(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, MigratePerfMetrics(db))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	january := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	february := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, timestamp := range []int64{january, february, february + 3600, march} {
		require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: "partition-test", Group: "a", BucketTs: timestamp, RequestCount: 1}))
	}
	require.NoError(t, MigratePerfMetrics(db))
	var physical []string
	require.NoError(t, db.Raw("SELECT DISTINCT tableoid::regclass::text FROM perf_metrics ORDER BY 1").Scan(&physical).Error)
	assert.Len(t, physical, 3)
	// Dimensional uniqueness still applies across retry and initialization.
	require.Error(t, db.Create(&PerfMetric{ModelName: "partition-test", Group: "a", BucketTs: january, RequestCount: 1}).Error)
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: "partition-test", Group: "a", BucketTs: march, RequestCount: 2}))
	require.NoError(t, DeletePerfMetricsBefore(february+3600))
	var exists bool
	require.NoError(t, db.Raw("SELECT to_regclass('perf_metrics_2026_01') IS NOT NULL").Scan(&exists).Error)
	assert.False(t, exists, "fully expired months are dropped, not row-deleted")
	var remaining []PerfMetric
	require.NoError(t, db.Order("bucket_ts").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.Equal(t, february+3600, remaining[0].BucketTs)
	assert.EqualValues(t, 3, remaining[1].RequestCount)
	// Two independent writers can create and upsert the same missing month.
	var writers sync.WaitGroup
	failures := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		writers.Go(func() {
			<-start
			failures <- UpsertPerfMetric(&PerfMetric{ModelName: "concurrent", Group: "a", BucketTs: january, RequestCount: 1})
		})
	}
	close(start)
	writers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var concurrent PerfMetric
	require.NoError(t, db.Where("model_name = ?", "concurrent").First(&concurrent).Error)
	assert.EqualValues(t, 2, concurrent.RequestCount)
	// Late samples create the missing month and still obey the next cleanup.
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: "partition-test", Group: "a", BucketTs: january, RequestCount: 1}))
	require.NoError(t, DeletePerfMetricsBefore(february+3600))
	var count int64
	require.NoError(t, db.Model(&PerfMetric{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	var plans []string
	require.NoError(t, db.Raw("EXPLAIN SELECT * FROM perf_metrics WHERE bucket_ts >= ? AND bucket_ts < ?", february, march).Scan(&plans).Error)
	assert.Contains(t, strings.Join(plans, "\n"), "perf_metrics_2026_02")
	assert.NotContains(t, strings.Join(plans, "\n"), "perf_metrics_2026_03")
}
