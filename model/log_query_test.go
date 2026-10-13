package model

import (
	"context"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBuildLogLikeConditionUsesStandardEscape(t *testing.T) {
	condition, pattern, err := buildLogLikeCondition("logs.model_name", "gpt_4%")

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ? ESCAPE '!'", condition)
	assert.Equal(t, "gpt!_4%", pattern)
}

func TestEnsureLogRequestId(t *testing.T) {
	empty := &Log{}
	ensureLogRequestId(empty)
	assert.NotEmpty(t, empty.RequestId, "empty request id should be backfilled")

	existing := &Log{RequestId: "fixed-request-id"}
	ensureLogRequestId(existing)
	assert.Equal(t, "fixed-request-id", existing.RequestId, "existing request id must be preserved")

	assert.NotPanics(t, func() { ensureLogRequestId(nil) })
}

func TestAssignDisplayLogIds(t *testing.T) {
	logs := []*Log{{}, {}, {}}

	assignDisplayLogIds(logs, 0)
	assert.Equal(t, []int{1, 2, 3}, []int{logs[0].Id, logs[1].Id, logs[2].Id})

	assignDisplayLogIds(logs, 20)
	assert.Equal(t, []int{21, 22, 23}, []int{logs[0].Id, logs[1].Id, logs[2].Id})

	assert.NotPanics(t, func() { assignDisplayLogIds(nil, 0) })
}

func TestPostgreSQLLogCountBoundsAndBatchDeletion(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}, &AuditLog{}))
	previous := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previous })
	initCol()
	t.Run("count applies the cap before aggregation and preserves user filters", func(t *testing.T) {
		require.NoError(t, db.Exec(`INSERT INTO logs (user_id, created_at, type, model_name) SELECT 1, 100, 2, 'count-model' FROM generate_series(1, 10002)`).Error)
		require.NoError(t, db.Create(&Log{UserId: 2, CreatedAt: 100, Type: LogTypeConsume, ModelName: "count-model"}).Error)
		logs, total, err := GetUserLogs(1, LogFilter{Type: LogTypeConsume, ModelName: "count-model"}, 0, 20)
		require.NoError(t, err)
		assert.EqualValues(t, 10000, total)
		require.Len(t, logs, 20)
		for _, log := range logs {
			assert.Equal(t, 1, log.UserId)
		}
		_, total, err = GetUserLogs(2, LogFilter{Type: LogTypeConsume, ModelName: "count-model"}, 0, 20)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		_, total, err = GetAllLogs(LogFilter{ModelName: "count-model"}, 0, 20)
		require.NoError(t, err)
		assert.EqualValues(t, 10003, total, "administrator counts remain exact")
		require.NoError(t, db.Exec("TRUNCATE logs").Error)
	})
	t.Run("each delete respects the batch boundary and retains audit logs", func(t *testing.T) {
		rows := make([]Log, 0, 9)
		for i := range 9 {
			rows = append(rows, Log{CreatedAt: int64(i + 1), RequestId: fmt.Sprint(i)})
		}
		require.NoError(t, db.Create(&rows).Error)
		require.NoError(t, db.Create(&AuditLog{EventId: "keep-audit", CreatedAt: 0}).Error)
		for _, want := range []int64{3, 3, 1, 0} {
			deleted, err := DeleteOldLogBatch(context.Background(), 8, 3)
			require.NoError(t, err)
			require.Equal(t, want, deleted)
		}
		var remaining []Log
		require.NoError(t, db.Order("created_at").Find(&remaining).Error)
		require.Len(t, remaining, 2)
		assert.EqualValues(t, 8, remaining[0].CreatedAt)
		assert.EqualValues(t, 9, remaining[1].CreatedAt)
		var audits int64
		require.NoError(t, db.Model(&AuditLog{}).Count(&audits).Error)
		assert.EqualValues(t, 1, audits)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		deleted, err := DeleteOldLogBatch(ctx, 100, 1)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, deleted)
	})
}
