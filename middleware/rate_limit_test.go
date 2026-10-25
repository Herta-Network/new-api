package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRateLimitMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())

	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() {
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	return redisServer, redisClient
}

func performRateLimitRequest(router http.Handler, path string, remoteAddr string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remoteAddr
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRedisIPRateLimiterThresholdTTLAndNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/limited", rateLimitFactory(2, 37, "TEST"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.10:12345"
	legacyKey := "rateLimit:TEST192.0.2.10"
	_, err := redisServer.Push(legacyKey, "legacy-list-entry")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	limitedResponse := performRateLimitRequest(router, "/limited", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, limitedResponse.Code)
	assert.Equal(t, "37", limitedResponse.Header().Get("Retry-After"))

	key := redisIPRateLimitKey("TEST", "192.0.2.10")
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "3", count)
	assert.Equal(t, 37*time.Second, redisServer.TTL(key))
	assert.True(t, redisServer.Exists(legacyKey), "the v2 counter must not touch an old list key")
}

func TestRedisUserRateLimiterUsesSharedFixedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	router.GET(
		"/limited",
		func(c *gin.Context) { c.Set("id", 42) },
		userRateLimitFactory(1, 23, "USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", "192.0.2.20:12345").Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/limited", "198.51.100.20:12345").Code)

	key := redisUserRateLimitKey("USER", 42)
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, 23*time.Second, redisServer.TTL(key))
}

func TestRedisEmailVerificationRateLimiterPreservesResponseAndTTL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/verify", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.30:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	response := performRateLimitRequest(router, "/verify", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.JSONEq(t, `{"success":false,"message":"Please wait 30 seconds before requesting another verification code.","message_key":"Please wait {{seconds}} seconds before requesting another verification code.","message_params":{"seconds":30}}`, response.Body.String())

	key := redisIPRateLimitKey(EmailVerificationRateLimitMark, "192.0.2.30")
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, time.Duration(EmailVerificationDuration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowIsAtomicUnderConcurrency(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const (
		requestCount = 20
		maximumCount = 7
		duration     = int64(41)
	)
	key := redisIPRateLimitKey("CONCURRENT", "192.0.2.40")

	var allowedCount atomic.Int64
	errorsFound := make(chan error, requestCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for range requestCount {
		go func() {
			defer waitGroup.Done()
			allowed, _, _, err := redisFixedWindowTake(context.Background(), key, maximumCount, duration)
			if err != nil {
				errorsFound <- err
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}

	assert.Equal(t, int64(maximumCount), allowedCount.Load())
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "20", count)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowResetsAtBoundary(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(10)
	key := redisIPRateLimitKey("BOUNDARY", "192.0.2.50")

	for range 2 {
		allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
	allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
	require.NoError(t, err)
	assert.False(t, allowed)

	// This reset is intentional fixed-window behavior. A client can consume one
	// full allowance immediately before and another immediately after a boundary.
	redisServer.FastForward(time.Duration(duration) * time.Second)
	for range 2 {
		allowed, _, _, err = redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
}

func TestRedisFixedWindowRepairsCounterWithoutTTL(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(29)
	key := redisIPRateLimitKey("MISSING-TTL", "192.0.2.51")
	redisServer.Set(key, "5")

	allowed, count, ttl, err := redisFixedWindowTake(context.Background(), key, 3, duration)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, int64(6), count)
	assert.Equal(t, duration, ttl)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))

	redisServer.FastForward(time.Duration(duration) * time.Second)
	assert.False(t, redisServer.Exists(key), "a recovered counter must not remain permanently rate-limited")
}

func TestRedisFailurePolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, redisClient := useRateLimitMiniRedis(t)
	require.NoError(t, redisClient.Close())

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/ip", rateLimitFactory(1, 30, "FAIL-IP"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET(
		"/user",
		func(c *gin.Context) { c.Set("id", 7) },
		userRateLimitFactory(1, 30, "FAIL-USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET("/email", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	ipResponse := performRateLimitRequest(router, "/ip", "192.0.2.60:12345")
	assert.Equal(t, http.StatusInternalServerError, ipResponse.Code)
	assert.Empty(t, ipResponse.Body.String())
	userResponse := performRateLimitRequest(router, "/user", "192.0.2.61:12345")
	assert.Equal(t, http.StatusInternalServerError, userResponse.Code)
	assert.Empty(t, userResponse.Body.String())
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/email", "192.0.2.62:12345").Code)
}

func TestGlobalWebRateLimitLimitsFallbackPathsPerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousEnable, previousNum, previousDuration := common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration = previousEnable, previousNum, previousDuration
	})
	common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration = true, 2, 41
	redisServer, _ := useRateLimitMiniRedis(t)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.NoRoute(GlobalWebRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const clientIP = "192.0.2.70"
	const remoteAddr = clientIP + ":12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/channels", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/oauth/github", remoteAddr).Code)
	limited := performRateLimitRequest(router, "/channels", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	assert.Equal(t, "41", limited.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/channels", "192.0.2.71:12345").Code)

	webCount, err := redisServer.Get(redisIPRateLimitKey("GW", clientIP))
	require.NoError(t, err)
	assert.Equal(t, "3", webCount)
}

// Buyer payment calls use per-user buckets, so a buyer polling the payment
// result, or several buyers behind one NAT, leave the IP-keyed critical
// bucket that guards login untouched. The routes mirror router/api-router.go.
func TestPaymentRateLimitLeavesLoginBucketAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	previousCriticalEnable, previousCriticalNum, previousCriticalDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousPaymentEnable, previousPaymentNum, previousPaymentDuration := common.PaymentRateLimitEnable, common.PaymentRateLimitNum, common.PaymentRateLimitDuration
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousCriticalEnable, previousCriticalNum, previousCriticalDuration
		common.PaymentRateLimitEnable, common.PaymentRateLimitNum, common.PaymentRateLimitDuration = previousPaymentEnable, previousPaymentNum, previousPaymentDuration
	})
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 20, 20*60
	common.PaymentRateLimitEnable, common.PaymentRateLimitNum, common.PaymentRateLimitDuration = true, 60, 10*60

	signedIn := func(c *gin.Context) {
		userID, _ := strconv.Atoi(c.GetHeader("X-User"))
		c.Set("id", userID)
	}
	ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/api/user/login", CriticalRateLimit(), ok)
	router.POST("/api/payment/orders", signedIn, UserCriticalRateLimit("payment-order"), ok)
	router.POST("/api/payment/orders/:no/checkout", signedIn, PaymentRateLimit(), ok)
	router.POST("/api/payment/orders/:no/sync", signedIn, PaymentRateLimit(), ok)
	router.POST("/api/payment/orders/:no/close", signedIn, PaymentRateLimit(), ok)

	const clientIP = "192.0.2.80"
	post := func(path string, userID int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.RemoteAddr = clientIP + ":12345"
		if userID != 0 {
			request.Header.Set("X-User", strconv.Itoa(userID))
		}
		router.ServeHTTP(recorder, request)
		return recorder
	}

	// Two buyers on one IP each pay five orders the way the wallet does:
	// create, the result page's four scheduled syncs plus one by hand,
	// resume the checkout, and close.
	for _, userID := range []int{101, 102} {
		for range 5 {
			assert.Equal(t, http.StatusNoContent, post("/api/payment/orders", userID).Code)
			for range 5 {
				assert.Equal(t, http.StatusNoContent, post("/api/payment/orders/P1/sync", userID).Code)
			}
			assert.Equal(t, http.StatusNoContent, post("/api/payment/orders/P1/checkout", userID).Code)
			assert.Equal(t, http.StatusNoContent, post("/api/payment/orders/P1/close", userID).Code)
		}
	}
	assert.Equal(t, http.StatusNoContent, post("/api/user/login", 0).Code)

	loginCount, err := redisServer.Get(redisIPRateLimitKey("CT", clientIP))
	require.NoError(t, err)
	assert.Equal(t, "1", loginCount, "payment calls must not count against the login bucket")
	paymentCount, err := redisServer.Get(redisUserRateLimitKey("PM", 101))
	require.NoError(t, err)
	assert.Equal(t, "35", paymentCount)
	assert.Equal(t, 10*time.Minute, redisServer.TTL(redisUserRateLimitKey("PM", 101)))

	// Order creation stays bounded per user by the critical allowance.
	for range 15 {
		assert.Equal(t, http.StatusNoContent, post("/api/payment/orders", 101).Code)
	}
	limited := post("/api/payment/orders", 101)
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	assert.Equal(t, "1200", limited.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusNoContent, post("/api/payment/orders", 102).Code)
	assert.Equal(t, http.StatusNoContent, post("/api/user/login", 0).Code)
}
