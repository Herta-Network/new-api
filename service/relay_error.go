package service

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

const (
	// defaultUpstreamRateLimitCooldown applies when a 429 names no usable
	// wait. It is deliberately short: long enough to keep the retry of the
	// failed request and the requests already in flight off the limited
	// channel, short enough that a guess costs little capacity. Upstreams that
	// know how long to wait say so with Retry-After or a reset header.
	defaultUpstreamRateLimitCooldown = 10 * time.Second
	// maxUpstreamRateLimitCooldown bounds one cooldown so a malformed or
	// day-long reset header cannot keep a channel out of rotation for long;
	// the next 429 after it ends starts a new cooldown.
	maxUpstreamRateLimitCooldown = 10 * time.Minute
)

// upstreamRateLimit is the cooldown an upstream 429 asked for on channelID.
type upstreamRateLimit struct {
	channelID int
	cooldown  time.Duration
}

// NoteUpstreamRateLimit keeps the wait an upstream 429 asked for until
// ProcessChannelError handles the failed attempt. It runs where the upstream
// response is received, before a channel's status code mapping can rewrite
// the 429.
func NoteUpstreamRateLimit(c *gin.Context, channelID int, header http.Header) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamRateLimit, upstreamRateLimit{
		channelID: channelID,
		cooldown:  upstreamRateLimitCooldown(header, time.Now()),
	})
}

// upstreamRateLimitCooldown returns how long a channel sits out after an
// upstream 429 carrying header. An explicit wait wins: retry-after-ms, then
// Retry-After in seconds or as an HTTP date. Otherwise the latest of the
// rate-limit reset headers is used, because the bucket that ran out is usually
// the one that refills last. Reset values may be RFC 3339 times (Anthropic),
// durations such as "6m0s" (OpenAI, Groq), or numbers: a Unix time in
// milliseconds or seconds when large, else a delay in seconds. The Retry-After
// handling follows parseRetryAfter in AstrLink's core/internal/ingress/recovery.go
// (https://github.com/Calcium-Ion/AstrLink, Copyright 2026 AstrLink
// contributors, Apache License 2.0).
func upstreamRateLimitCooldown(header http.Header, now time.Time) time.Duration {
	var seconds float64
	if ms, err := strconv.ParseFloat(strings.TrimSpace(header.Get("Retry-After-Ms")), 64); err == nil && ms > 0 && !math.IsInf(ms, 0) {
		seconds = ms / 1000
	} else if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if delay, err := strconv.ParseInt(value, 10, 64); err == nil {
			seconds = float64(delay)
		} else if at, err := http.ParseTime(value); err == nil {
			seconds = at.Sub(now).Seconds()
		}
	}
	if !(seconds > 0) {
		seconds = 0
		for name, values := range header {
			lower := strings.ToLower(name)
			if len(values) == 0 || !strings.Contains(lower, "ratelimit") || !strings.Contains(lower, "reset") {
				continue
			}
			value := strings.TrimSpace(values[0])
			if at, err := time.Parse(time.RFC3339, value); err == nil {
				seconds = max(seconds, at.Sub(now).Seconds())
			} else if wait, err := time.ParseDuration(value); err == nil {
				seconds = max(seconds, wait.Seconds())
			} else if number, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
				switch {
				case number >= 1e12:
					seconds = max(seconds, time.UnixMilli(int64(number)).Sub(now).Seconds())
				case number >= 1e9:
					seconds = max(seconds, time.Unix(int64(number), 0).Sub(now).Seconds())
				default:
					seconds = max(seconds, number)
				}
			}
		}
	}
	if !(seconds > 0) {
		return defaultUpstreamRateLimitCooldown
	}
	return time.Duration(min(seconds, maxUpstreamRateLimitCooldown.Seconds()) * float64(time.Second))
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	upstreamURLs := common.UpstreamAddresses(c)
	logger.LogError(c, common.LogText("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode(upstreamURLs...))))
	if limit, ok := common.GetContextKeyType[upstreamRateLimit](c, constant.ContextKeyUpstreamRateLimit); ok && limit.channelID != 0 {
		common.SetContextKey(c, constant.ContextKeyUpstreamRateLimit, upstreamRateLimit{})
		// Channel tests pass no relay info and stay out of routing. A
		// multi-key channel is left alone: the 429 belongs to one of its keys,
		// and the next request already moves to another key.
		if relayInfo != nil && !channelError.IsMultiKey && limit.channelID == channelError.ChannelId {
			model.RecordChannelRateLimit(channelError.ChannelId, relayInfo.OriginModelName, limit.cooldown)
			logger.LogInfo(c, common.LogText("channel #%d is cooling down for model %s for %s after an upstream 429", channelError.ChannelId, relayInfo.OriginModelName, limit.cooldown))
		}
	}
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode(upstreamURLs...)
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(upstreamURLs...), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
