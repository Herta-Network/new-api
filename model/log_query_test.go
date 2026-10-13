package model

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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

func TestSearchCountsRespectBoundsAndOwnership(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}, &TopUp{}))
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	previousLimit := operation_setting.GetTokenSetting().MaxUserTokens
	operation_setting.GetTokenSetting().MaxUserTokens = 3
	t.Cleanup(func() { operation_setting.GetTokenSetting().MaxUserTokens = previousLimit })
	tokens := []Token{
		{UserId: 1, Key: "cap-token-1", Name: "match"}, {UserId: 1, Key: "cap-token-2", Name: "match"},
		{UserId: 1, Key: "cap-token-3", Name: "match"}, {UserId: 1, Key: "cap-token-4", Name: "match"},
		{UserId: 2, Key: "other-token", Name: "match"},
	}
	require.NoError(t, db.Create(&tokens).Error)
	found, total, err := SearchUserTokens(1, "match", "", 0, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, found, 2)
	for _, token := range found {
		assert.Equal(t, 1, token.UserId)
	}
	_, _, err = SearchUserTokens(1, "mat%", "", 0, 2)
	assert.Error(t, err, "an over-limit user cannot bypass the fuzzy-search guard")
	require.NoError(t, db.Exec(`INSERT INTO top_ups (user_id,trade_no,create_time) SELECT 1,'cap-'||i,? FROM generate_series(1,10002) i`, common.GetTimestamp()).Error)
	require.NoError(t, db.Create(&TopUp{UserId: 2, TradeNo: "cap-other", CreateTime: common.GetTimestamp()}).Error)
	page := &common.PageInfo{Page: 1, PageSize: 2}
	topups, total, err := SearchUserTopUps(1, "cap%", page)
	require.NoError(t, err)
	assert.EqualValues(t, 10000, total)
	require.Len(t, topups, 2)
	for _, topup := range topups {
		assert.Equal(t, 1, topup.UserId)
	}
	_, total, err = SearchUserTopUps(2, "cap%", page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	_, total, err = SearchAllTopUps("cap%", page)
	require.NoError(t, err)
	assert.EqualValues(t, 10000, total)
}

func TestLogExportKeysetReleasesConnectionAndPreservesOrder(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	previous := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previous })
	for _, all := range []bool{true, false} {
		t.Run(fmt.Sprint("all=", all), func(t *testing.T) {
			require.NoError(t, db.Exec("TRUNCATE logs RESTART IDENTITY").Error)
			rows := make([]Log, 501)
			for i := range rows {
				rows[i] = Log{UserId: 1, CreatedAt: int64(i % 13), RequestId: fmt.Sprint(i), ModelName: "export-batch"}
			}
			require.NoError(t, db.CreateInBatches(&rows, 100).Error)
			require.NoError(t, db.Create(&Log{UserId: 2, CreatedAt: 20, ModelName: "other"}).Error)
			slices.SortFunc(rows, func(a, b Log) int {
				if all {
					return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(b.Id, a.Id))
				}
				return cmp.Compare(b.Id, a.Id)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			seen := make([]string, 0, len(rows))
			consume := func(log *Log) error {
				if len(seen) == 0 {
					// This would exhaust the pool if export still held an open Rows.
					if err := db.WithContext(ctx).Create(&Log{UserId: 1, CreatedAt: 0, ModelName: "export-batch", RequestId: "inserted-during-export"}).Error; err != nil {
						return err
					}
				}
				seen = append(seen, log.RequestId)
				if !all {
					assert.Equal(t, len(seen), log.Id)
				}
				return nil
			}
			filter := LogFilter{ModelName: "export-batch"}
			if all {
				err = StreamAllLogs(ctx, filter, common.RoleRootUser, consume)
			} else {
				err = StreamUserLogs(ctx, 1, filter, consume)
			}
			require.NoError(t, err)
			want := make([]string, len(rows))
			for i := range rows {
				want[i] = rows[i].RequestId
			}
			assert.Equal(t, want, seen)
			stop := errors.New("client disconnected")
			assert.ErrorIs(t, StreamUserLogs(ctx, 1, filter, func(*Log) error { return stop }), stop)
			cancel()
			assert.ErrorIs(t, StreamUserLogs(ctx, 1, filter, consume), context.Canceled)
		})
	}
}

func TestCursorPagesPreserveOrderingOwnershipAndBoundaries(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Task{}, &Channel{}))
	logDB, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	require.NoError(t, db.Create(&Channel{Id: 9, Name: "primary"}).Error)
	previousMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemory })
	oldDB, oldLog := DB, LOG_DB
	DB, LOG_DB = db, logDB
	t.Cleanup(func() { DB, LOG_DB = oldDB, oldLog })
	rows := []Log{{UserId: 1, CreatedAt: 30, RequestId: "a", ChannelId: 9, Other: `{"admin_info":{"marker":"private-marker"},"root_info":{"node_name":"private-node"}}`}, {UserId: 2, CreatedAt: 40, RequestId: "foreign"}, {UserId: 1, CreatedAt: 20, RequestId: "b"}, {UserId: 1, CreatedAt: 30, RequestId: "c"}, {UserId: 1, CreatedAt: 10, RequestId: "d"}}
	require.NoError(t, logDB.Create(&rows).Error)
	ctx := context.Background()
	first, err := GetLogCursorPage(ctx, 1, false, LogFilter{}, "", 2)
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	assert.Equal(t, []string{"d", "c"}, []string{first.Items[0].RequestId, first.Items[1].RequestId})
	assert.Empty(t, first.PreviousCursor)
	require.NotEmpty(t, first.NextCursor)
	require.NoError(t, logDB.Create(&Log{UserId: 1, CreatedAt: 5, RequestId: "new"}).Error)
	second, err := GetLogCursorPage(ctx, 1, false, LogFilter{}, first.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	assert.Equal(t, []string{"b", "a"}, []string{second.Items[0].RequestId, second.Items[1].RequestId})
	assert.Equal(t, 3, second.Items[0].Id)
	assert.NotContains(t, second.Items[1].Other, "private-marker")
	assert.NotContains(t, second.Items[1].Other, "private-node")
	assert.Empty(t, second.Items[1].ChannelName)
	assert.Empty(t, second.NextCursor)
	previous, err := GetLogCursorPage(ctx, 1, false, LogFilter{}, second.PreviousCursor, 2)
	require.NoError(t, err)
	assert.Equal(t, first.Items, previous.Items)
	_, err = GetLogCursorPage(ctx, 2, false, LogFilter{}, first.NextCursor, 2)
	require.Error(t, err)
	_, err = GetLogCursorPage(ctx, 1, false, LogFilter{}, first.NextCursor, 3)
	require.Error(t, err)
	for _, invalid := range []string{"invalid", strings.Repeat("a", 513)} {
		_, err = GetLogCursorPage(ctx, 1, false, LogFilter{}, invalid, 2)
		require.Error(t, err)
	}
	admin, err := GetLogCursorPage(ctx, 1, true, LogFilter{}, "", 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"foreign", "c", "a"}, []string{admin.Items[0].RequestId, admin.Items[1].RequestId, admin.Items[2].RequestId})
	assert.Equal(t, "primary", admin.Items[2].ChannelName)
	next, err := GetLogCursorPage(ctx, 1, true, LogFilter{}, admin.NextCursor, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "d", "new"}, []string{next.Items[0].RequestId, next.Items[1].RequestId, next.Items[2].RequestId})

	tasks := []Task{{TaskID: "own-a", UserId: 1, Status: TaskStatusInProgress, Data: []byte(`{"large":"snapshot"}`)}, {TaskID: "foreign", UserId: 2, Status: TaskStatusInProgress}, {TaskID: "own-b", UserId: 1, Status: TaskStatusInProgress}, {TaskID: "completed", UserId: 1, Status: TaskStatusSuccess}}
	require.NoError(t, db.Create(&tasks).Error)
	filter := SyncTaskQueryParams{Status: string(TaskStatusInProgress)}
	taskPage, err := GetTaskCursorPage(ctx, 1, false, filter, "", 1)
	require.NoError(t, err)
	assert.Equal(t, "own-b", taskPage.Items[0].TaskID)
	taskPage, err = GetTaskCursorPage(ctx, 1, false, filter, taskPage.NextCursor, 1)
	require.NoError(t, err)
	assert.Equal(t, "own-a", taskPage.Items[0].TaskID)
	assert.Empty(t, taskPage.Items[0].Data)
	assert.Empty(t, taskPage.NextCursor)
}
