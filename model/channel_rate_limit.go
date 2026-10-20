package model

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"

	"github.com/samber/hot"
)

// Channel rate-limit cooldowns are adapted from AstrLink's per-route 429
// cooldowns in core/internal/endpoint/health.go
// (https://github.com/Calcium-Ion/AstrLink, Copyright 2026 AstrLink
// contributors, Apache License 2.0).
//
// An upstream 429 pauses one channel for one model. The pause only steers
// selection toward the other candidates: it never changes the channel's
// status, and it ends on its own once its deadline passes.

const (
	channelRateLimitCacheNamespace = "new-api:channel_rate_limit:v1"
	// channelRateLimitCacheCapacity bounds the in-memory store, which holds
	// one entry per model that has a channel cooling down.
	channelRateLimitCacheCapacity = 10_000
)

// channelRateLimits maps a channel ID to the Unix millisecond at which its
// cooldown for one model ends. A stored map is never modified, so readers of
// the in-memory cache can share it.
type channelRateLimits map[int]int64

var (
	channelRateLimitCacheOnce sync.Once
	channelRateLimitCache     *cachex.HybridCache[channelRateLimits]
	// channelRateLimitLock serializes this instance's read-modify-write of a
	// model's entry. Instances sharing Redis can still overwrite each other's
	// writes for the same model at the same moment; a channel lost that way
	// is recorded again on its next 429.
	channelRateLimitLock sync.Mutex
	// channelRateLimitNow is the cooldown clock. Tests replace it.
	channelRateLimitNow = time.Now
)

func getChannelRateLimitCache() *cachex.HybridCache[channelRateLimits] {
	channelRateLimitCacheOnce.Do(func() {
		channelRateLimitCache = cachex.NewHybridCache[channelRateLimits](cachex.HybridCacheConfig[channelRateLimits]{
			Namespace: cachex.Namespace(channelRateLimitCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[channelRateLimits]{},
			Memory: func() *hot.HotCache[string, channelRateLimits] {
				// Every entry is written with its own TTL; the default only
				// sets how often the janitor sweeps.
				return hot.NewHotCache[string, channelRateLimits](hot.LRU, channelRateLimitCacheCapacity).
					WithTTL(time.Minute).
					WithJanitor().
					Build()
			},
		})
	})
	return channelRateLimitCache
}

// RecordChannelRateLimit keeps channelID out of selection for modelName until
// cooldown has passed, unless a later deadline is already recorded.
func RecordChannelRateLimit(channelID int, modelName string, cooldown time.Duration) {
	if channelID <= 0 || modelName == "" || cooldown < time.Millisecond {
		return
	}
	channelRateLimitLock.Lock()
	defer channelRateLimitLock.Unlock()

	cache := getChannelRateLimitCache()
	now := channelRateLimitNow().UnixMilli()
	current, _, err := cache.Get(modelName)
	if err != nil {
		common.SysError(common.LogText("channel rate limit cache get failed: model=%s, err=%v", modelName, err))
	}
	limits := channelRateLimits{channelID: now + cooldown.Milliseconds()}
	latest := limits[channelID]
	for id, until := range current {
		if until <= now {
			continue
		}
		limits[id] = max(limits[id], until)
		latest = max(latest, until)
	}
	if err := cache.SetWithTTL(modelName, limits, time.Duration(latest-now)*time.Millisecond); err != nil {
		common.SysError(common.LogText("channel rate limit cache set failed: model=%s, err=%v", modelName, err))
	}
}

// IsChannelRateLimited reports whether channelID is cooling down for modelName.
func IsChannelRateLimited(channelID int, modelName string) bool {
	return rateLimitedChannels(modelName)[channelID]
}

// rateLimitedChannels returns the channels cooling down for modelName. It may
// reach Redis, so selection calls it before taking channelSyncLock.
func rateLimitedChannels(modelName string) map[int]bool {
	limits, found, err := getChannelRateLimitCache().Get(modelName)
	if err != nil {
		common.SysError(common.LogText("channel rate limit cache get failed: model=%s, err=%v", modelName, err))
		return nil
	}
	if !found {
		return nil
	}
	now := channelRateLimitNow().UnixMilli()
	cooling := make(map[int]bool, len(limits))
	for id, until := range limits {
		if until > now {
			cooling[id] = true
		}
	}
	return cooling
}

// readyPriorityLevel picks the priority level to draw from once cooling
// channels are set aside. The retry index has already chosen target from all
// candidates, so the attempt after a 429 still moves down one level as it did
// before. target is kept when a ready channel sits there; otherwise the
// nearest lower level with one is used, and failing that the nearest higher
// level. readyPriorities must not be empty.
func readyPriorityLevel(target int64, readyPriorities []int64) int64 {
	var below, above int64
	foundBelow, foundAbove := false, false
	for _, priority := range readyPriorities {
		if priority <= target {
			if !foundBelow || priority > below {
				below, foundBelow = priority, true
			}
		} else if !foundAbove || priority < above {
			above, foundAbove = priority, true
		}
	}
	if foundBelow {
		return below
	}
	return above
}
