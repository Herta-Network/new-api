package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// templateRouterForTest is a request that matched a session rule carrying
// template.
func templateRouterForTest(template map[string]any) (*gin.Context, *ChannelRouter) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	router := NewChannelRouter(ctx)
	router.inspected = true
	router.request.rule = &routeRule{name: "rule-with-template", mode: "prefer", modeSource: "session_rule", template: template}
	return ctx, router
}

func TestApplyChannelAffinityOverrideTemplate_NoTemplate(t *testing.T) {
	ctx, _ := templateRouterForTest(nil)
	base := map[string]any{
		"temperature": 0.7,
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.False(t, applied)
	require.Equal(t, base, merged)

	unrouted, _ := gin.CreateTestContext(httptest.NewRecorder())
	merged, applied = ApplyChannelAffinityOverrideTemplate(unrouted, base)
	require.False(t, applied)
	require.Equal(t, base, merged)
}

func TestApplyChannelAffinityOverrideTemplate_MergeTemplate(t *testing.T) {
	ctx, router := templateRouterForTest(map[string]any{
		"temperature": 0.2,
		"top_p":       0.95,
	})
	base := map[string]any{
		"temperature": 0.7,
		"max_tokens":  2000,
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.True(t, applied)
	require.Equal(t, 0.7, merged["temperature"])
	require.Equal(t, 0.95, merged["top_p"])
	require.Equal(t, 2000, merged["max_tokens"])
	require.Equal(t, 0.7, base["temperature"])

	router.record(routeAttempt{Group: "default", ChannelID: 3})
	other := model.NewLogOther()
	AppendRoutingAdminInfo(ctx, other)
	record, ok := other.Snapshot()["admin_info"].(map[string]any)["routing"].(routeLog)
	require.True(t, ok)
	require.NotNil(t, record.Rule)
	assert.Equal(t, routeLogRule{Name: "rule-with-template", Mode: "prefer", TemplateApplied: true, ParamOverrideKeys: 2}, *record.Rule)
}

func TestApplyChannelAffinityOverrideTemplate_MergeOperations(t *testing.T) {
	ctx, _ := templateRouterForTest(map[string]any{
		"operations": []map[string]any{
			{
				"mode":  "pass_headers",
				"value": []string{"Originator"},
			},
		},
	})
	base := map[string]any{
		"temperature": 0.7,
		"operations": []map[string]any{
			{
				"path":  "model",
				"mode":  "trim_prefix",
				"value": "openai/",
			},
		},
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.True(t, applied)
	require.Equal(t, 0.7, merged["temperature"])

	opsAny, ok := merged["operations"]
	require.True(t, ok)
	ops, ok := opsAny.([]any)
	require.True(t, ok)
	require.Len(t, ops, 2)

	firstOp, ok := ops[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pass_headers", firstOp["mode"])

	secondOp, ok := ops[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "trim_prefix", secondOp["mode"])
}

func TestExtractChannelAffinityValue_RequestHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("X-Affinity-Key", " tenant-123 ")

	value := extractChannelAffinityValue(ctx, operation_setting.ChannelAffinityKeySource{
		Type: "request_header",
		Key:  "X-Affinity-Key",
	})

	require.Equal(t, "tenant-123", value)
}

// The conversation module is not imported here: its middleware hands the
// session over through constant.ContextKeyConversationSessionID, stubbed below.
func TestExtractChannelAffinityValue_ConversationSession(t *testing.T) {
	tests := []struct {
		name    string
		session func(*gin.Context) string
		want    string
	}{
		{name: "the session the conversation module recognized", session: func(*gin.Context) string { return "cs_1" }, want: "cs_1"},
		{name: "a request the conversation middleware did not run for", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.session != nil {
				common.SetContextKey(ctx, constant.ContextKeyConversationSessionID, tt.session)
			}

			value := extractChannelAffinityValue(ctx, operation_setting.ChannelAffinityKeySource{Type: "conversation_session"})

			assert.Equal(t, tt.want, value)
		})
	}
}

func TestExtractChannelAffinityValue_JSONFile(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"image":"`+strings.Repeat("A", 1<<20)+`","metadata":{"key":" tenant-123 ","count":0,"enabled":false},"items":[{"id":"chosen"}]}`))
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	for _, tc := range []struct{ path, want string }{
		{"metadata.key", "tenant-123"},
		{"metadata.count", "0"},
		{"metadata.enabled", "false"},
		{"items.0.id", "chosen"},
		{`items.#(id=="chosen").id`, "chosen"},
		{"absent", ""},
	} {
		assert.Equal(t, tc.want, extractChannelAffinityValue(c, operation_setting.ChannelAffinityKeySource{Type: "gjson", Path: tc.path}), tc.path)
	}
}

func TestChannelAffinityHitCodexTemplatePassHeadersEffective(t *testing.T) {
	gin.SetMode(gin.TestMode)

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var codexRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			codexRule = rule
			break
		}
	}
	require.NotNil(t, codexRule)

	affinityValue := fmt.Sprintf("pc-hit-%d", time.Now().UnixNano())
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"prompt_cache_key":"%s"}`, affinityValue)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	router := NewChannelRouter(ctx)
	router.inspect(ctx, "gpt-5")
	require.Equal(t, "rule:"+codexRule.Name, router.request.seed.source, "the rule's key seeds the routing")

	baseOverride := map[string]any{
		"temperature": 0.2,
	}
	mergedOverride, applied := ApplyChannelAffinityOverrideTemplate(ctx, baseOverride)
	require.True(t, applied)
	require.Equal(t, 0.2, mergedOverride["temperature"])

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
			"User-Agent": "codex-cli-test",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: mergedOverride,
			HeadersOverride: map[string]any{
				"X-Static": "legacy-static",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-5"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)

	require.Equal(t, "legacy-static", info.RuntimeHeadersOverride["x-static"])
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	require.Equal(t, "codex-cli-test", info.RuntimeHeadersOverride["user-agent"])

	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	_, exists = info.RuntimeHeadersOverride["x-codex-turn-metadata"]
	require.False(t, exists)
}

func TestMidjourneyPolicyAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       int
		properties map[string]any
		requestErr error
		accepted   bool
		reason     string
	}{
		{name: "submitted", status: 200, code: 1, accepted: true},
		{name: "existing task", status: 200, code: 21, accepted: true},
		{name: "queued", status: 200, code: 22, accepted: true},
		{name: "HTTP 200 with rejected prompt", status: 200, code: 24, reason: "non_retryable_error"},
		{name: "failed existing task", status: 200, code: 21, properties: map[string]any{"status": "FAILURE"}, reason: "task_accepted"},
		{name: "HTTP failure", status: 502, code: 1, reason: "non_retryable_error"},
		{name: "ambiguous transport failure", status: 502, requestErr: errors.New("connection reset"), reason: "non_retryable_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
			ctx.Set("channel_id", 7)
			state := RequestPolicy(ctx)
			state.BeginAttempt(&model.Channel{Id: 7}, "default")
			response := &dto.MidjourneyResponseWithStatusCode{StatusCode: tc.status, Response: dto.MidjourneyResponse{Code: tc.code, Properties: tc.properties}}
			accepted := RecordMidjourneyPolicyResponse(ctx, response, tc.requestErr)
			assert.Equal(t, tc.accepted, accepted)
			assert.False(t, state.Successful, "acceptance alone must not bind before local processing finishes")
			if accepted {
				MarkRequestPolicySuccess(ctx, nil)
				assert.True(t, state.Successful)
				return
			}
			events := state.Events()
			require.Len(t, events, 3)
			assert.Equal(t, PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}, events[1].Decision)
			assert.Equal(t, tc.status, events[1].Status)
			assert.Equal(t, PolicyDecision{Action: "stop", Reason: tc.reason, Source: "system"}, events[2].Decision, "submissions are never replayed")
		})
	}
}

// A rule's session mode inherits or overrides the global default. Strict
// holds a request on its seed's first choice only when the request carries
// earlier turns; a first turn fails over like prefer. Off supplies no seed
// but keeps the rule's template.
func TestSessionRulesInheritOrOverrideGlobalDefault(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	for _, globalMode := range []string{"", "off", "prefer", "strict"} {
		for _, tc := range []struct {
			name, mode, expected string
			skipRetry            bool
		}{
			{name: "inherit", mode: "inherit", expected: globalMode},
			{name: "override-strict", mode: "strict", expected: "strict"},
			{name: "override-prefer", mode: "prefer", expected: "prefer", skipRetry: true},
			{name: "override-off", mode: "off", expected: "off", skipRetry: true},
			{name: "legacy-strict", expected: "strict", skipRetry: true},
			{name: "legacy-prefer", expected: "prefer"},
		} {
			for _, history := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/history=%v", globalMode, tc.name, history), func(t *testing.T) {
					configured := tc.expected
					if configured == "" {
						configured = "prefer"
					}
					effective := "prefer"
					if configured == "strict" && history {
						effective = "strict"
					}
					source := "session_rule"
					if tc.mode == "inherit" {
						source = "global"
					}
					rule := operation_setting.ChannelAffinityRule{
						Name: tc.name, ModelRegex: []string{"^test-model$"},
						KeySources:  []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}},
						SessionMode: tc.mode, SkipRetryOnFailure: tc.skipRetry,
						ParamOverrideTemplate: map[string]any{"temperature": 0},
					}
					encoded, err := common.Marshal([]operation_setting.ChannelAffinityRule{rule})
					require.NoError(t, err)
					snapshot, err := model.BuildRequestPolicy(map[string]string{"RetryTimes": "2", "channel_affinity_setting.enabled": "true", "channel_affinity_setting.session_mode": globalMode, "channel_affinity_setting.rules": string(encoded)})
					require.NoError(t, err)
					*setting = snapshot.Affinity
					body := `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`
					if history {
						body = `{"model":"test-model","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"again"}]}`
					}
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
					ctx.Request.Header.Set("X-Session", t.Name())
					state := RequestPolicy(ctx)
					router := NewChannelRouter(ctx)
					router.inspect(ctx, "test-model")

					assert.Equal(t, configured, router.request.rule.mode)
					assert.Equal(t, configured != "off", router.request.seed.source == "rule:"+tc.name, "off supplies no seed")
					assert.Equal(t, effective, state.SessionMode)
					assert.Equal(t, source, state.SessionModeSource)
					assert.Equal(t, effective == "strict", IsStrictRoute(ctx))
					if configured == "strict" && !history {
						assert.Equal(t, "no_history", router.modeReason)
					}
					decision := DecideRelayRetry(ctx, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), 2)
					if effective == "strict" {
						assert.Equal(t, PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}, decision)
					} else {
						assert.Equal(t, "retry", decision.Action)
					}
					events := state.Events()
					require.Len(t, events, 1)
					assert.Equal(t, PolicyDecision{Action: "match", Reason: "session_rule_matched", Source: "session_rule"}, events[0].Decision)
					assert.Equal(t, tc.name, events[0].Rule)
					transformed, applied := ApplyChannelAffinityOverrideTemplate(ctx, nil)
					assert.True(t, applied, "session behavior does not disable request transforms")
					assert.EqualValues(t, 0, transformed["temperature"])
				})
			}
		}
	}
}
