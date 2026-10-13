package model

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createReserveTestUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{
		Username:    "reserve-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       quota,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createReserveTestToken(t *testing.T, remainQuota int) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         "reserve-token-" + common.GetRandomString(8),
		Name:        "reserve-test",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: remainQuota,
	}
	require.NoError(t, token.Insert())
	return token
}

func getUserQuotaFromDB(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

func getTokenFromDB(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func resetBatchUpdateTestState(t *testing.T) {
	t.Helper()
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	pendingQuotaBatches = nil
	quotaBatchWriterID, quotaBatchSequence = common.GetUUID(), 0
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		pendingQuotaBatches = nil
		quotaBatchWriterID, quotaBatchSequence = common.GetUUID(), 0
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestTryReserveQuotaWithoutRedis(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	user := createReserveTestUser(t, 100)
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	token := createReserveTestToken(t, 80)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 25, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reloaded := getTokenFromDB(t, token.Id)
	assert.Equal(t, 55, reloaded.RemainQuota)
	assert.Equal(t, 25, reloaded.UsedQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 56, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 55, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRedisBatchReserveNeverFallsBackToStaleDatabaseBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 10)
	reserved, err := TryReserveUserQuota(user.Id, 8)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id), "batch delta is not flushed yet")

	reserved, err = TryReserveUserQuota(user.Id, 3)
	require.NoError(t, err)
	assert.False(t, reserved, "stale DB balance must not authorize a second spend")
	cachedUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, cachedUser.Quota)

	token := createReserveTestToken(t, 9)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 3, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 9, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id))
	reloadedToken := getTokenFromDB(t, token.Id)
	assert.Equal(t, 2, reloadedToken.RemainQuota)
	assert.Equal(t, 7, reloadedToken.UsedQuota)
}

func TestBatchUpdateAccumulatesTwoMaximumRequestCharges(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, common.MaxQuota*2+100)
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))

	batchUpdate()
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
}

func TestBatchUpdateAccumulatorSaturatesOverflow(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MaxInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MaxInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUserQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MinInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MinInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
}

func TestReserveFallsBackToDatabaseWhenRedisIsUnavailable(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 20)
	require.NoError(t, populateUserCache(user))
	server.Close()

	// Redis 故障时降级为数据库条件更新：服务保持可用且不会超扣。
	reserved, err := TryReserveUserQuota(user.Id, 5)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 16)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))
}

func TestSynchronousReserveCompensatesCacheWhenPersistenceFails(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 6)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota)

	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cachedToken, cacheErr := cacheGetTokenByKey(token.Key)
	require.NoError(t, cacheErr)
	assert.Equal(t, 12, cachedToken.RemainQuota)
	assert.Zero(t, cachedToken.UsedQuota)
}

func TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	loaded, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	stale := *loaded

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// 已存在的哈希只刷新 TTL：数据库快照不得覆盖已被原子预扣的余额。
	code, err := cacheInitToken(stale)
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 30, cached.RemainQuota)

	// 变更期间：fence 删除缓存并拦截并发读者手中的过期快照。
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	code, err = cacheInitToken(stale)
	require.NoError(t, err)
	assert.Zero(t, code, "the pre-mutation snapshot must not be published while fenced")
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	// fence 过期后可重新从数据库水合。
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	cached, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
}

func TestQuotaBatchFailureRecovery(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Channel{}, &QuotaBatch{}, &QuotaBatchReceipt{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	resetBatchUpdateTestState(t)
	user := createReserveTestUser(t, 100)
	token := createReserveTestToken(t, 100)
	channel := Channel{}
	require.NoError(t, db.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -10)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 10)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -10)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 10)

	// A failed queue write must retain the immutable batch alongside new traffic.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, FlushQuotaBatches(ctx), context.Canceled)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -5)

	// Reject the last table update: balances, counters and receipt must roll back.
	require.NoError(t, db.Exec(`ALTER TABLE channels ADD CONSTRAINT reject_test_usage CHECK (used_quota = 0)`).Error)
	require.Error(t, FlushQuotaBatches(context.Background()))
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	var queued []QuotaBatch
	require.NoError(t, db.Order("id").Find(&queued).Error)
	require.Len(t, queued, 2)
	var receipts int64
	require.NoError(t, db.Model(&QuotaBatchReceipt{}).Count(&receipts).Error)
	assert.Zero(t, receipts)
	// Simulate a new process. Persisted work is recovered without the old buffers.
	quotaBatchWriterID, quotaBatchSequence = common.GetUUID(), 0
	require.NoError(t, db.Exec(`ALTER TABLE channels DROP CONSTRAINT reject_test_usage`).Error)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	var saved User
	require.NoError(t, db.First(&saved, user.Id).Error)
	assert.Equal(t, 85, saved.Quota)
	assert.Equal(t, 10, saved.UsedQuota)
	assert.Equal(t, 1, saved.RequestCount)
	assert.Equal(t, 90, getTokenFromDB(t, token.Id).RemainQuota)
	assert.Equal(t, 10, getTokenFromDB(t, token.Id).UsedQuota)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, int64(10), channel.UsedQuota)

	// A lost COMMIT acknowledgement may replay the same writer/sequence.
	queued[0].ID = 0
	require.NoError(t, db.Create(&queued[0]).Error)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	assert.Equal(t, 85, getUserQuotaFromDB(t, user.Id))
	assert.Equal(t, 90, getTokenFromDB(t, token.Id).RemainQuota)
	var remaining int64
	require.NoError(t, db.Model(&QuotaBatch{}).Count(&remaining).Error)
	assert.Zero(t, remaining)

	// Multiple consumers must apply each persisted delta once, in writer order.
	for _, writer := range []string{"worker-a", "worker-b"} {
		for sequence := range int64(2) {
			payload, err := common.Marshal(quotaBatchPayload{Users: []quotaUserDelta{{ID: user.Id, Quota: -1}}})
			require.NoError(t, err)
			require.NoError(t, db.Create(&QuotaBatch{WriterID: writer, Sequence: sequence + 1, Payload: string(payload)}).Error)
		}
	}
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	for range 2 {
		workers.Go(func() {
			for {
				id, err := applyNextQuotaBatch(context.Background(), nil)
				if err != nil || id == 0 {
					failures <- err
					return
				}
			}
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	assert.Equal(t, 81, getUserQuotaFromDB(t, user.Id))
}

func TestQuotaBatchLimitsAndDeletedRows(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Channel{}, &QuotaBatch{}, &QuotaBatchReceipt{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	resetBatchUpdateTestState(t)
	user := createReserveTestUser(t, common.MaxWalletQuota)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, 1)
	require.ErrorIs(t, FlushQuotaBatches(context.Background()), ErrWalletQuotaLimitExceeded)
	assert.Equal(t, common.MaxWalletQuota, getUserQuotaFromDB(t, user.Id))
	// An administrator correction allows the unchanged pending credit to retry.
	require.NoError(t, db.Model(&user).Update("quota", common.MaxWalletQuota-1).Error)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	assert.Equal(t, common.MaxWalletQuota, getUserQuotaFromDB(t, user.Id))
	// Aggregated refunds can exceed the positive wallet limit when repaying debt.
	require.NoError(t, db.Model(&user).Update("quota", -common.MaxWalletQuota).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, common.MaxWalletQuota)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, common.MaxWalletQuota)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	assert.Equal(t, common.MaxWalletQuota, getUserQuotaFromDB(t, user.Id))
	// Debt is allowed; credits cannot resurrect a deleted account or API token.
	require.NoError(t, db.Model(&user).Update("quota", 0).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -2)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	assert.Equal(t, -2, getUserQuotaFromDB(t, user.Id))
	token := createReserveTestToken(t, 5)
	require.NoError(t, db.Delete(&user).Error)
	require.NoError(t, db.Delete(&token).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -1)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	require.NoError(t, db.Unscoped().First(&user, user.Id).Error)
	require.NoError(t, db.Unscoped().First(&token, token.Id).Error)
	assert.Equal(t, -2, user.Quota)
	assert.Equal(t, 5, token.RemainQuota)
	assert.True(t, user.DeletedAt.Valid)
	assert.True(t, token.DeletedAt.Valid)
}

func TestQuotaBatchSpansMultipleFlushUnits(t *testing.T) {
	db, err := testutil.OpenDB(t, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &QuotaBatch{}, &QuotaBatchReceipt{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	resetBatchUpdateTestState(t)
	users := make([]User, 501)
	for i := range users {
		users[i] = User{Username: fmt.Sprintf("batch-%d", i), Quota: 10, AffCode: fmt.Sprintf("batch-aff-%d", i)}
	}
	require.NoError(t, db.CreateInBatches(&users, 100).Error)
	for _, user := range users {
		addNewRecord(BatchUpdateTypeUserQuota, user.Id, -2)
		addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 2)
		addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	}
	require.NoError(t, FlushQuotaBatches(context.Background()))
	var applied int64
	require.NoError(t, db.Model(&User{}).Where("quota = 8 AND used_quota = 2 AND request_count = 1").Count(&applied).Error)
	assert.EqualValues(t, len(users), applied)
	require.NoError(t, FlushQuotaBatches(context.Background()))
	require.NoError(t, db.Model(&User{}).Where("quota = 8 AND used_quota = 2 AND request_count = 1").Count(&applied).Error)
	assert.EqualValues(t, len(users), applied)
}
