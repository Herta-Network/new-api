package codingplan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/opencodego"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect(t *testing.T) {
	const (
		kimiUsage      = "https://api.kimi.com/coding/v1/usages"
		glmUsage       = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
		zaiUsage       = "https://api.z.ai/api/monitor/usage/quota/limit"
		minimaxCNUsage = "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"
		minimaxIOUsage = "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"
	)
	tests := []struct {
		name        string
		channelType int
		baseURL     string
		want        Plan
	}{
		{name: "moonshot with the kimi-coding-plan base", channelType: constant.ChannelTypeMoonshot, baseURL: "kimi-coding-plan", want: Plan{ProviderKimi, kimiUsage}},
		{name: "moonshot on the Kimi coding endpoint", channelType: constant.ChannelTypeMoonshot, baseURL: "https://api.kimi.com/coding/", want: Plan{ProviderKimi, kimiUsage}},
		{name: "anthropic on the Kimi coding endpoint", channelType: constant.ChannelTypeAnthropic, baseURL: "https://api.kimi.ai/coding", want: Plan{ProviderKimi, "https://api.kimi.ai/coding/v1/usages"}},
		{name: "moonshot open platform", channelType: constant.ChannelTypeMoonshot, baseURL: "https://api.moonshot.cn"},
		{name: "moonshot default base", channelType: constant.ChannelTypeMoonshot, baseURL: ""},
		{name: "a kimi host outside /coding", channelType: constant.ChannelTypeAnthropic, baseURL: "https://api.kimi.com/codingx"},
		{name: "zhipu with the glm-coding-plan base", channelType: constant.ChannelTypeZhipu_v4, baseURL: "glm-coding-plan", want: Plan{ProviderGLM, glmUsage}},
		{name: "zhipu with the international plan base", channelType: constant.ChannelTypeZhipu_v4, baseURL: "glm-coding-plan-international", want: Plan{ProviderGLM, zaiUsage}},
		{name: "zhipu on the coding chat endpoint", channelType: constant.ChannelTypeZhipu_v4, baseURL: "https://open.bigmodel.cn/api/coding/paas/v4", want: Plan{ProviderGLM, glmUsage}},
		{name: "anthropic on the GLM Claude Code endpoint", channelType: constant.ChannelTypeAnthropic, baseURL: "https://api.z.ai/api/anthropic", want: Plan{ProviderGLM, zaiUsage}},
		{name: "zhipu open platform", channelType: constant.ChannelTypeZhipu_v4, baseURL: "https://open.bigmodel.cn"},
		{name: "minimax default base", channelType: constant.ChannelTypeMiniMax, baseURL: "", want: Plan{ProviderMiniMax, minimaxCNUsage}},
		{name: "minimax on the CN inference host", channelType: constant.ChannelTypeMiniMax, baseURL: "https://api.minimax.cn", want: Plan{ProviderMiniMax, minimaxCNUsage}},
		{name: "anthropic on the MiniMax international host", channelType: constant.ChannelTypeAnthropic, baseURL: "https://api.minimax.io/anthropic", want: Plan{ProviderMiniMax, minimaxIOUsage}},
		{name: "minimax behind a proxy", channelType: constant.ChannelTypeMiniMax, baseURL: "https://proxy.example.com"},
		{name: "a plan endpoint on a channel type that does not use it", channelType: constant.ChannelTypeOpenAI, baseURL: "https://api.kimi.com/coding"},
		{name: "kimi coding endpoint on the zhipu type", channelType: constant.ChannelTypeZhipu_v4, baseURL: "https://api.kimi.com/coding"},
		{name: "a lookalike domain", channelType: constant.ChannelTypeAnthropic, baseURL: "https://api.notkimi.com/coding"},
		{name: "anthropic official", channelType: constant.ChannelTypeAnthropic, baseURL: ""},
		{name: "doubao coding plan has no usage route", channelType: constant.ChannelTypeVolcEngine, baseURL: "doubao-coding-plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, ok := Detect(tt.channelType, tt.baseURL)
			assert.Equal(t, tt.want.Provider != "", ok)
			assert.Equal(t, tt.want, plan)
		})
	}
}

func TestFetchUsage(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	window := func(percent float64, seconds int64, reset time.Time) *opencodego.UsageWindow {
		w := &opencodego.UsageWindow{UsedPercent: percent, LimitWindowSeconds: seconds}
		if !reset.IsZero() {
			w.ResetAt = reset.Unix()
			w.ResetAfterSeconds = int64(reset.Sub(now).Seconds())
		}
		return w
	}
	inThreeHours := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	inFiveHours := time.Date(2026, 9, 22, 17, 0, 0, 0, time.UTC)
	tomorrow := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	inThreeDays := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		provider    Provider
		status      int
		body        string
		wantUsage   *Usage
		wantMessage string
		wantParams  map[string]any
	}{
		{
			name:     "kimi rolling limit and weekly allowance",
			provider: ProviderKimi,
			status:   http.StatusOK,
			body: `{
				"limits": [{"window": {"duration": 18000}, "detail": {"limit": "200", "remaining": "150", "resetTime": "2026-09-22T15:00:00Z"}}],
				"usage": {"limit": 4000, "remaining": 1000, "resetTime": 1790294400}}`,
			wantUsage: &Usage{
				Provider: ProviderKimi,
				FiveHour: window(25, fiveHourSeconds, inThreeHours),
				Weekly:   window(75, weeklySeconds, inThreeDays),
			},
		},
		{
			name:     "glm windows by unit with the plan level",
			provider: ProviderGLM,
			status:   http.StatusOK,
			body: `{"success": true, "code": 200, "data": {"level": "pro", "limits": [
				{"type": "TOKENS_LIMIT", "percentage": 100, "nextResetTime": 1790121600000, "unit": 6, "number": 7},
				{"type": "TOKENS_LIMIT", "percentage": 12.5, "nextResetTime": 1790096400000, "unit": 3, "number": 5},
				{"type": "TIME_LIMIT", "percentage": 99}]}}`,
			wantUsage: &Usage{
				Provider:     ProviderGLM,
				Plan:         "pro",
				FiveHour:     window(12.5, fiveHourSeconds, inFiveHours),
				Weekly:       window(100, weeklySeconds, tomorrow),
				LimitReached: true,
			},
		},
		{
			name:     "glm entries without a unit fill the windows in reset order",
			provider: ProviderGLM,
			status:   http.StatusOK,
			body: `{"success": true, "data": {"level": "lite", "limits": [
				{"type": "TOKENS_LIMIT", "percentage": 5, "nextResetTime": 1790121600000},
				{"type": "TOKENS_LIMIT", "percentage": 3}]}}`,
			wantUsage: &Usage{
				Provider: ProviderGLM,
				Plan:     "lite",
				FiveHour: window(3, fiveHourSeconds, time.Time{}),
				Weekly:   window(5, weeklySeconds, tomorrow),
			},
		},
		{
			name:     "minimax inverts the remaining share of the general entry",
			provider: ProviderMiniMax,
			status:   http.StatusOK,
			body: `{"base_resp": {"status_code": 0, "status_msg": "success"}, "model_remains": [
				{"model_name": "video", "current_interval_remaining_percent": 1},
				{"model_name": "general", "current_interval_remaining_percent": 80, "end_time": 1790096400000,
				 "current_weekly_status": 1, "current_weekly_remaining_percent": 55, "weekly_end_time": 1790294400000}]}`,
			wantUsage: &Usage{
				Provider: ProviderMiniMax,
				FiveHour: window(20, fiveHourSeconds, inFiveHours),
				Weekly:   window(45, weeklySeconds, inThreeDays),
			},
		},
		{
			name:     "minimax without an active weekly window",
			provider: ProviderMiniMax,
			status:   http.StatusOK,
			body:     `{"model_remains": [{"model_name": "general", "current_interval_remaining_percent": 0, "end_time": -1, "current_weekly_status": 3, "current_weekly_remaining_percent": 100}]}`,
			wantUsage: &Usage{
				Provider:     ProviderMiniMax,
				FiveHour:     window(100, fiveHourSeconds, time.Time{}),
				LimitReached: true,
			},
		},
		{name: "a rejected key", provider: ProviderKimi, status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`, wantMessage: "{{provider}} rejected the API key", wantParams: map[string]any{"provider": "Kimi For Coding"}},
		{name: "a key without access to plan usage", provider: ProviderGLM, status: http.StatusForbidden, body: `{}`, wantMessage: "This API key cannot read {{provider}} usage. Check that it belongs to a Coding Plan", wantParams: map[string]any{"provider": "GLM Coding Plan"}},
		{name: "a rate limited lookup", provider: ProviderMiniMax, status: http.StatusTooManyRequests, body: ``, wantMessage: "{{provider}} is rate limiting usage queries. Please try again later", wantParams: map[string]any{"provider": "MiniMax Coding Plan"}},
		{name: "another upstream status", provider: ProviderKimi, status: http.StatusBadGateway, body: `oops`, wantMessage: "Upstream returned status {{status}}", wantParams: map[string]any{"status": http.StatusBadGateway}},
		{name: "glm reports an authentication failure", provider: ProviderGLM, status: http.StatusOK, body: `{"success": false, "code": 1002, "msg": "Authorization Token非法"}`, wantMessage: "{{provider}} rejected the API key", wantParams: map[string]any{"provider": "GLM Coding Plan"}},
		{name: "glm reports another error without leaking an email", provider: ProviderGLM, status: http.StatusOK, body: `{"success": false, "code": 500, "msg": "busy for owner@example.com"}`, wantMessage: "{{provider}} reported an error", wantParams: map[string]any{"provider": "GLM Coding Plan"}},
		{name: "minimax reports a failed login", provider: ProviderMiniMax, status: http.StatusOK, body: `{"base_resp": {"status_code": 1004, "status_msg": "login fail"}}`, wantMessage: "{{provider}} rejected the API key", wantParams: map[string]any{"provider": "MiniMax Coding Plan"}},
		{name: "minimax reports another error", provider: ProviderMiniMax, status: http.StatusOK, body: `{"base_resp": {"status_code": 1008, "status_msg": "insufficient balance"}}`, wantMessage: "{{provider}} reported an error: {{reason}}", wantParams: map[string]any{"provider": "MiniMax Coding Plan", "reason": "insufficient balance (code 1008)"}},
		{name: "kimi answers without windows", provider: ProviderKimi, status: http.StatusOK, body: `{}`, wantMessage: "{{provider}} reported no plan usage for this API key", wantParams: map[string]any{"provider": "Kimi For Coding"}},
		{name: "glm answers without limits", provider: ProviderGLM, status: http.StatusOK, body: `{"success": true, "data": {"limits": []}}`, wantMessage: "{{provider}} reported no plan usage for this API key", wantParams: map[string]any{"provider": "GLM Coding Plan"}},
		{name: "minimax answers without a general entry", provider: ProviderMiniMax, status: http.StatusOK, body: `{"base_resp": {"status_code": 0}, "model_remains": [{"model_name": "video", "current_interval_remaining_percent": 50}]}`, wantMessage: "{{provider}} reported no plan usage for this API key", wantParams: map[string]any{"provider": "MiniMax Coding Plan"}},
	}
	routes := map[Provider]string{
		ProviderKimi:    "/coding/v1/usages",
		ProviderGLM:     "/api/monitor/usage/quota/limit",
		ProviderMiniMax: "/v1/api/openplatform/coding_plan/remains",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, routes[tt.provider], r.URL.Path)
				if tt.provider == ProviderGLM {
					// Zhipu takes the raw key without a Bearer prefix.
					assert.Equal(t, "plan-key", r.Header.Get("Authorization"))
					assert.NotEmpty(t, r.Header.Get("Accept-Language"))
				} else {
					assert.Equal(t, "Bearer plan-key", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()

			plan := Plan{Provider: tt.provider, UsageURL: upstream.URL + routes[tt.provider]}
			usage, err := plan.FetchUsage(context.Background(), upstream.Client(), " plan-key\n", now)
			if tt.wantMessage == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantUsage, usage)
				return
			}
			var message *common.Message
			require.True(t, errors.As(err, &message), "err = %v", err)
			assert.Equal(t, tt.wantMessage, message.Key)
			assert.Equal(t, tt.wantParams, message.Params)
			assert.Nil(t, usage)
		})
	}
}

func TestFetchUsageRejectsMalformedPayloads(t *testing.T) {
	for _, provider := range []Provider{ProviderKimi, ProviderGLM, ProviderMiniMax} {
		for _, body := range []string{``, `nope`, `[]`, `null`, `{"data": {"limits": "nope"}, "limits": [1], "model_remains": [1]}`} {
			t.Run(string(provider)+" "+body, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(body))
				}))
				defer upstream.Close()

				plan := Plan{Provider: provider, UsageURL: upstream.URL}
				usage, err := plan.FetchUsage(context.Background(), upstream.Client(), "plan-key", time.Now())
				require.Error(t, err)
				var message *common.Message
				assert.False(t, errors.As(err, &message), "a malformed payload is a server log error, got %v", err)
				assert.Nil(t, usage)
			})
		}
	}
}
