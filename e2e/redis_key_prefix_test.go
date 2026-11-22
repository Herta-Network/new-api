package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/cachex"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisKeyPrefixCoversKeysAndChannels runs new-api's Redis paths without
// REDIS_KEY_PREFIX and with it. The prefixed run must write the same keys and
// publish on the same channels, each under the prefix, which also opens every
// hash tag so keys keep sharing a cluster slot.
func TestRedisKeyPrefixCoversKeysAndChannels(t *testing.T) {
	require.NoError(t, i18n.Init())
	plain := runRedisPaths(t, "")
	userID := strconv.Itoa(plain.userID)
	assert.Subset(t, plain.keys, []string{"user:" + userID, "auth:user:version:{user:" + userID + "}",
		"rateLimit:v2:ip:DW:192.0.2.1", "rateLimit:MRRLS:" + userID, "notify_limit:" + userID + ":quota"})
	assert.Equal(t, []string{"new-api:wsmanager:channel-close:db:0"}, plain.published)

	prefixed := runRedisPaths(t, "newapi:")
	expected := make([]string, 0, len(plain.keys))
	for _, key := range plain.keys {
		expected = append(expected, "newapi:"+strings.Replace(key, "{", "{newapi:", 1))
	}
	slices.Sort(expected)
	assert.Equal(t, expected, prefixed.keys)
	assert.Equal(t, []string{"newapi:new-api:wsmanager:channel-close:db:0"}, prefixed.published)

	// The token bucket limiter keeps the first client it is given, so only the
	// prefixed client uses it: SCRIPT LOAD, then EVALSHA with a prefixed key.
	allowed, err := limiter.New(context.Background(), prefixed.client).Allow(context.Background(), "rateLimit:redis-prefix")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.True(t, prefixed.server.Exists("newapi:rateLimit:redis-prefix"))

	// SUBSCRIBE bypasses go-redis hooks; the subscriber must still listen on
	// the prefixed channel. common.RDB is the prefixed client here.
	closed := make(chan string, 1)
	unregister := wsmanager.Register(7, wsmanager.KindRealtime, func(reason string) { closed <- reason })
	t.Cleanup(unregister)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsmanager.StartSubscriber(ctx)
	require.Eventually(t, func() bool { return len(prefixed.server.PubSubChannels("")) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"newapi:new-api:wsmanager:channel-close:db:0"}, prefixed.server.PubSubChannels(""))
	prefixed.server.Publish("newapi:new-api:wsmanager:channel-close:db:0", `{"channel_ids":[7],"reason":"closed elsewhere","origin":"another-node"}`)
	select {
	case reason := <-closed:
		assert.Equal(t, "closed elsewhere", reason)
	case <-time.After(5 * time.Second):
		t.Fatal("the subscriber did not receive the close event on the prefixed channel")
	}

	invalid := redis.NewClient(&redis.Options{Addr: prefixed.server.Addr()})
	t.Cleanup(func() { _ = invalid.Close() })
	require.Error(t, common.UseRedisKeyPrefix(invalid, "new{api}:"))
	assert.Equal(t, "newapi:", common.RedisKeyPrefix)
	assert.Empty(t, prefixed.failures.list(), "Redis commands failed")
	require.ErrorContains(t, prefixed.client.Do(context.Background(), "randomkey").Err(), `command "randomkey" is not in redisKeyRanges`)
}

type redisPathsResult struct {
	server   *miniredis.Miniredis
	client   *redis.Client
	failures *redisFailures
	userID   int
	// keys are the keys left in Redis, with the hour of the performance bucket
	// replaced so runs can be compared.
	keys      []string
	published []string
}

func runRedisPaths(t *testing.T, prefix string) redisPathsResult {
	t.Helper()
	pgtest.Reset(t, model.DB)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	failures := &redisFailures{}
	client.AddHook(failures)
	require.NoError(t, common.UseRedisKeyPrefix(client, prefix))

	previousClient, previousEnabled, previousPrefix := common.RDB, common.RedisEnabled, common.RedisKeyPrefix
	previousDownloadNum, previousDownloadDuration := common.DownloadRateLimitNum, common.DownloadRateLimitDuration
	previousModelEnabled, previousModelCount := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount
	previousModelSuccess, previousModelMinutes := setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes
	common.RDB, common.RedisEnabled = client, true
	common.DownloadRateLimitNum, common.DownloadRateLimitDuration = 1, 60
	setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount = true, 0
	setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = 1, 1
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, common.RedisKeyPrefix = previousClient, previousEnabled, previousPrefix
		common.DownloadRateLimitNum, common.DownloadRateLimitDuration = previousDownloadNum, previousDownloadDuration
		setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount = previousModelEnabled, previousModelCount
		setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = previousModelSuccess, previousModelMinutes
		_ = client.Close()
	})

	watcher := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = watcher.Close() })
	published := watcher.PSubscribe(context.Background(), "*")
	_, err := published.Receive(context.Background())
	require.NoError(t, err)

	user := model.User{Username: "redis-prefix", Password: "unused-password-hash", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: 1000, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "redisprefixtokenkey0123456789abcdefghijklmnop", Name: "redis-prefix",
		Status: common.TokenStatusEnabled, RemainQuota: 1000, ExpiredTime: -1, Group: "default"}
	require.NoError(t, model.DB.Create(&token).Error)

	// Token and user caches with the quota reserve scripts.
	for range 2 {
		_, err = model.ValidateUserToken(token.Key)
		require.NoError(t, err)
	}
	reserved, err := model.TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err)
	require.True(t, reserved)
	reserved, err = model.TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err)
	require.True(t, reserved)
	quota, err := model.GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 900, quota)
	_, err = model.BumpUserAuthVersion(user.Id)
	require.NoError(t, err)

	// Browser session cache.
	session := model.UserSession{SID: "redis-prefix-session", UserID: user.Id, UserAuthVersion: 2,
		RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, model.CreateUserSession(&session))
	cachedSession, err := model.GetUserSessionCached(session.SID)
	require.NoError(t, err)
	assert.Equal(t, user.Id, cachedSession.UserID)
	revoked, err := model.RevokeUserSession(user.Id, session.SID, "logout")
	require.NoError(t, err)
	require.True(t, revoked)

	// The IP fixed-window limiter and the model request success limiter each
	// admit one request.
	engine := gin.New()
	engine.GET("/download", middleware.DownloadRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/model", func(c *gin.Context) { c.Set("id", user.Id) }, middleware.ModelRequestRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, path := range []string{"/download", "/model"} {
		for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.RemoteAddr = "192.0.2.1:1234"
			engine.ServeHTTP(recorder, request)
			require.Equal(t, status, recorder.Code, path)
		}
	}

	// A hybrid cache lists its keys with SCAN and purges them.
	cache := cachex.NewHybridCache[string](cachex.HybridCacheConfig[string]{
		Namespace: "redis-prefix-cache", Redis: client, RedisCodec: cachex.StringCodec{}})
	require.NoError(t, cache.SetWithTTL("a", "1", time.Minute))
	cacheKeys, err := cache.Keys()
	require.NoError(t, err)
	assert.Equal(t, []string{"redis-prefix-cache:a"}, cacheKeys)
	require.NoError(t, cache.Purge())
	cacheKeys, err = cache.Keys()
	require.NoError(t, err)
	assert.Empty(t, cacheKeys)

	// Performance metrics are written in the background.
	perfmetrics.Record(perfmetrics.Sample{Model: "redis-prefix-model", Group: "default", Success: true, LatencyMs: 5})
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(server.Keys(), func(key string) bool { return strings.Contains(key, "perf:redis-prefix-model:") })
	}, 5*time.Second, 10*time.Millisecond)

	release, acquired, err := service.AcquireLogExportLock(user.Id)
	require.NoError(t, err)
	require.True(t, acquired)
	release()
	_, err = service.CheckNotificationLimit(user.Id, "quota")
	require.NoError(t, err)

	require.NoError(t, wsmanager.PublishCloseChannels(context.Background(), []int{7}, "redis prefix"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message, err := published.ReceiveMessage(ctx)
	require.NoError(t, err)

	require.Empty(t, failures.list(), "Redis commands failed")
	var keys []string
	for _, key := range server.Keys() {
		require.True(t, strings.HasPrefix(key, prefix), key)
		if strings.HasPrefix(key, prefix+"perf:redis-prefix-model:default:") {
			key = prefix + "perf:redis-prefix-model:default:<hour>"
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return redisPathsResult{server: server, client: client, failures: failures, userID: user.Id,
		keys: keys, published: []string{message.Channel}}
}

// redisFailures records the commands that failed with anything but a missing
// key, including commands the key prefix hook refused, which callers with a
// database fallback would otherwise hide.
type redisFailures struct {
	mu     sync.Mutex
	failed []string
}

func (f *redisFailures) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.failed)
}

func (f *redisFailures) record(cmd redis.Cmder) {
	if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.failed = append(f.failed, cmd.String())
	}
}

func (f *redisFailures) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (f *redisFailures) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	f.record(cmd)
	return nil
}

func (f *redisFailures) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (f *redisFailures) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	for _, cmd := range cmds {
		f.record(cmd)
	}
	return nil
}
