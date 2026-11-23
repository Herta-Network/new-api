package model

import (
	"strconv"
	"strings"
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
// An upstream 429 pauses one channel, or one key of a multi-key channel, for
// one model. The pause only steers selection toward the other candidates: it
// never changes the channel's status, and it ends on its own once its
// deadline passes.

const (
	// v2 keys cooldowns by target string so a multi-key channel can pause one
	// key; v1 entries expire on their own.
	channelRateLimitCacheNamespace = "new-api:channel_rate_limit:v2"
	// channelRateLimitCacheCapacity bounds the in-memory store, which holds
	// one entry per model that has a channel cooling down.
	channelRateLimitCacheCapacity = 10_000
)

// channelRateLimits maps a cooling target to the Unix millisecond at which
// its cooldown for one model ends. A target is a channel ID, or
// "<channel ID>/<key fingerprint>" for one key of a multi-key channel. A
// stored map is never modified, so readers of the in-memory cache can share
// it.
type channelRateLimits map[string]int64

// channelRateLimitTarget names what a cooldown pauses: the whole channel when
// keyFingerprint is empty, else that key alone.
func channelRateLimitTarget(channelID int, keyFingerprint string) string {
	if keyFingerprint == "" {
		return strconv.Itoa(channelID)
	}
	return strconv.Itoa(channelID) + "/" + keyFingerprint
}

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

// RecordChannelRateLimit keeps a channel, or with keyFingerprint
// (ChannelKeyFingerprint) one key of a multi-key channel, out of selection
// for modelName until cooldown has passed, unless a later deadline is already
// recorded.
func RecordChannelRateLimit(channelID int, keyFingerprint string, modelName string, cooldown time.Duration) {
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
	target := channelRateLimitTarget(channelID, keyFingerprint)
	limits := channelRateLimits{target: now + cooldown.Milliseconds()}
	latest := limits[target]
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

// ClearChannelRateLimits ends the cooldowns of a channel, and of its keys,
// for modelNames: its credentials were replaced, and the new ones have not
// failed.
func ClearChannelRateLimits(channelID int, modelNames []string) {
	if channelID <= 0 {
		return
	}
	channelRateLimitLock.Lock()
	defer channelRateLimitLock.Unlock()

	cache := getChannelRateLimitCache()
	now := channelRateLimitNow().UnixMilli()
	channel := channelRateLimitTarget(channelID, "")
	for _, modelName := range modelNames {
		current, found, err := cache.Get(modelName)
		if err != nil {
			common.SysError(common.LogText("channel rate limit cache get failed: model=%s, err=%v", modelName, err))
			continue
		}
		if !found {
			continue
		}
		kept := channelRateLimits{}
		latest := now
		for target, until := range current {
			if until <= now || target == channel || strings.HasPrefix(target, channel+"/") {
				continue
			}
			kept[target] = until
			latest = max(latest, until)
		}
		if len(kept) == len(current) {
			continue
		}
		if len(kept) == 0 {
			_, err = cache.DeleteMany([]string{modelName})
		} else {
			err = cache.SetWithTTL(modelName, kept, time.Duration(latest-now)*time.Millisecond)
		}
		if err != nil {
			common.SysError(common.LogText("channel rate limit cache set failed: model=%s, err=%v", modelName, err))
		}
	}
}

// ChannelCooldowns is the set of targets cooling down for one model at the
// moment it was read.
type ChannelCooldowns struct {
	targets  map[string]bool
	channels map[int]bool
}

// Cooling reports whether the channel, or with keyFingerprint that key of it,
// is cooling down. A whole-channel cooldown covers every key.
func (cooldowns ChannelCooldowns) Cooling(channelID int, keyFingerprint string) bool {
	if cooldowns.targets[channelRateLimitTarget(channelID, "")] {
		return true
	}
	return keyFingerprint != "" && cooldowns.targets[channelRateLimitTarget(channelID, keyFingerprint)]
}

// HasChannel reports whether any cooldown names the channel or one of its
// keys, so a caller can skip fingerprinting the keys of the others.
func (cooldowns ChannelCooldowns) HasChannel(channelID int) bool {
	return cooldowns.channels[channelID]
}

// IsChannelRateLimited reports whether the channel, or with keyFingerprint
// that key of it, is cooling down for modelName.
func IsChannelRateLimited(channelID int, keyFingerprint string, modelName string) bool {
	return ChannelCooldownsFor(modelName).Cooling(channelID, keyFingerprint)
}

// ChannelCooldownsFor returns the targets cooling down for modelName. It may
// reach Redis, so callers read it before taking channelSyncLock.
func ChannelCooldownsFor(modelName string) ChannelCooldowns {
	limits, found, err := getChannelRateLimitCache().Get(modelName)
	if err != nil {
		common.SysError(common.LogText("channel rate limit cache get failed: model=%s, err=%v", modelName, err))
		return ChannelCooldowns{}
	}
	if !found {
		return ChannelCooldowns{}
	}
	now := channelRateLimitNow().UnixMilli()
	cooldowns := ChannelCooldowns{targets: make(map[string]bool, len(limits)), channels: make(map[int]bool, len(limits))}
	for target, until := range limits {
		if until <= now {
			continue
		}
		cooldowns.targets[target] = true
		channel, _, _ := strings.Cut(target, "/")
		if id, err := strconv.Atoi(channel); err == nil {
			cooldowns.channels[id] = true
		}
	}
	return cooldowns
}
