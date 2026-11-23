package kiro

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(string) error {
		common.IsMasterNode = true
		common.RedisEnabled = false
		return model.InitDB()
	}))
}

const testProfile = "arn:aws:codewhisperer:us-east-1:123456789012:profile/P"

func members(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, common.UnmarshalJsonStr(raw, &fields))
	return fields
}

func marshalJSON(t *testing.T, v any) gjson.Result {
	t.Helper()
	raw, err := common.Marshal(v)
	require.NoError(t, err)
	return gjson.ParseBytes(raw)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRefreshRequestsByMethod(t *testing.T) {
	for _, method := range []string{"social", "builder_id", "iam_sso"} {
		t.Run(method, func(t *testing.T) {
			grant := Credential{Method: method, RefreshToken: "old"}
			if method != "social" {
				grant.Region, grant.ClientID, grant.ClientSecret = "eu-central-1", "client", "secret"
			}
			require.NoError(t, grant.Validate())
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				var body map[string]string
				require.NoError(t, common.DecodeJson(r.Body, &body))
				assert.Equal(t, "old", body["refreshToken"])
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Contains(t, r.Header.Get("User-Agent"), "KiroIDE-")
				if method == "social" {
					assert.Equal(t, "prod.us-east-1.auth.desktop.kiro.dev", r.URL.Host)
					assert.Equal(t, "/refreshToken", r.URL.Path)
					assert.Len(t, body, 1)
				} else {
					assert.Equal(t, "oidc.eu-central-1.amazonaws.com", r.URL.Host)
					assert.Equal(t, "/token", r.URL.Path)
					assert.Equal(t, "refresh_token", body["grantType"])
					assert.Equal(t, "secret", body["clientSecret"])
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"accessToken":"fresh","refreshToken":"rotated","expiresIn":3600}`))}, nil
			})}
			fresh, err := grant.Refresh(context.Background(), client)
			require.NoError(t, err)
			assert.True(t, fresh.Usable(time.Now()))
			assert.Equal(t, "rotated", fresh.RefreshToken)
			assert.Equal(t, "fresh", fresh.AccessToken)
		})
	}
}

// Kiro spends the old refresh token as soon as it answers, so the rotated one
// must come back even when the rest of the answer is unusable.
func TestRefreshKeepsRotatedGrant(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		wantErr    error
		rotated    bool
	}{
		{"malformed profile is ignored", `{"accessToken":"fresh","refreshToken":"rotated","expiresIn":3600,"profileArn":"not-an-arn"}`, 200, nil, true},
		{"no access token", `{"refreshToken":"rotated","expiresIn":3600}`, 200, ErrCredential, true},
		{"expiry out of range", `{"accessToken":"fresh","refreshToken":"rotated","expiresIn":0}`, 200, ErrCredential, true},
		{"revoked grant", `{"error":"invalid_grant"}`, 400, ErrRejected, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grant := Credential{Method: "social", RefreshToken: "old", ProfileArn: testProfile}
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			fresh, err := grant.Refresh(context.Background(), client)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
			assert.Equal(t, tc.rotated, fresh.RefreshToken == "rotated")
			if tc.rotated {
				assert.NoError(t, fresh.Validate())
				assert.Equal(t, testProfile, fresh.ProfileArn)
			}
			if err != nil {
				assert.Empty(t, fresh.AccessToken)
			}
		})
	}
	outage := Credential{Method: "social", RefreshToken: "old"}
	_, err := outage.Refresh(context.Background(), &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	require.Error(t, err)
	assert.False(t, permanentFailure(err), "a provider outage is transient")
}

func TestNormalizeKeyDropsForeignMembers(t *testing.T) {
	key, err := NormalizeKey("{\n  \"method\": \"social\",\n  \"refreshToken\": \"r\",\n  \"email\": \"someone@example.com\"\n}")
	require.NoError(t, err)
	assert.Equal(t, `{"method":"social","refreshToken":"r"}`, key)

	batch, err := NormalizeKey(`{"method":"social","refreshToken":"a"}` + "\n\n" + `{"method":"access_token","accessToken":"b"}`)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(batch, "\n")+1)

	for _, invalid := range []string{``, `{"method":"social"}`, `{"method":"builder_id","refreshToken":"r"}`, `{"method":"social","refreshToken":"r","clientId":"c"}`} {
		_, err := NormalizeKey(invalid)
		assert.Error(t, err, invalid)
	}
	summary := KeySummary(`{"method":"social","refreshToken":"secret-refresh","accessToken":"secret-access","profileArn":"` + testProfile + `"}`)
	assert.NotContains(t, summary, "secret")
	assert.Equal(t, "social", gjson.Get(summary, "method").Str)
}

// Claude Code marks cache breakpoints and sends metadata on every request; Kiro
// caches prefixes by itself, so both are dropped instead of refused.
func TestMessagesDropCacheHintsAndMetadata(t *testing.T) {
	fields := members(t, `{
		"model":"kiro-sonnet","metadata":{"user_id":"user-1"},"cache_control":{"type":"ephemeral"},
		"system":[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}],
		"tools":[{"name":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"read it","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1"},{"type":"text","text":"go on","cache_control":{"type":"ephemeral"}}]}]}`)
	body, err := generateAssistantResponse(fields, testProfile, "scope", Model{ID: "claude-sonnet-4.5"})
	require.NoError(t, err)
	request := marshalJSON(t, body)
	assert.NotContains(t, request.Raw, "cache_control")
	assert.NotContains(t, request.Raw, "user_id")
	current := request.Get("conversationState.currentMessage.userInputMessage")
	assert.Equal(t, "go on", current.Get("content").Str)
	assert.Equal(t, `""`, current.Get("userInputMessageContext.toolResults.0.content.0.text").Raw)
	assert.Equal(t, "read", current.Get("userInputMessageContext.tools.0.toolSpecification.name").Str)
	assert.Equal(t, "be brief\n\nread it", request.Get("conversationState.history.0.userInputMessage.content").Str)
}

// Claude Code sends context_management and system messages in the middle of
// the conversation: its environment, reminders after tool results, effort and
// tool changes. Kiro has no system turn, so a system message is user text
// where it stands, and tool results still answer the turn that called them.
func TestClaudeCodeSystemMessages(t *testing.T) {
	haiku := Model{ID: "claude-haiku-5.5", Schema: json.RawMessage(`{"properties":{
		"thinking":{"properties":{"type":{"enum":["adaptive","disabled"]}}},
		"output_config":{"properties":{"effort":{"enum":["low","medium","high"]}}}}}`)}
	const (
		system   = `"system":[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}]`
		ask      = `{"role":"user","content":[{"type":"text","text":"list files"}]}`
		call     = `{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"toolu_1","name":"ls","input":{}}]}`
		result   = `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.go"}]}`
		env      = `{"role":"system","content":"cwd is /repo"}`
		reminder = `{"role":"system","content":[{"type":"text","text":"keep going","cache_control":{"type":"ephemeral"}}]}`
	)
	cases := []struct {
		name, messages string
		enabled        bool
		// history is each history turn's kind and content; current the
		// current message's content.
		history          []string
		current          string
		results          int
		systemPrompt, oc string
		tools            []string
	}{
		{name: "environment after the question", messages: ask + `,` + env,
			current: "be brief\n\nlist files\n\ncwd is /repo"},
		{name: "reminder after tool results", messages: ask + `,` + call + `,` + result + `,` + reminder,
			history: []string{"user: be brief\n\nlist files", "assistant: "}, current: "keep going", results: 1},
		{name: "between the call and its results", messages: ask + `,` + call + `,` + env + `,` + result,
			history: []string{"user: be brief\n\nlist files", "assistant: "}, current: "cwd is /repo", results: 1},
		{name: "after tool results before the answer", messages: ask + `,` + call + `,` + result + `,` + reminder + `,{"role":"assistant","content":"a.go"},{"role":"user","content":"thanks"}`,
			history: []string{"user: be brief\n\nlist files", "assistant: ", "user with results: keep going", "assistant: a.go"}, current: "thanks"},
		{name: "leading system message", messages: env + `,` + ask, enabled: true,
			current: "list files", systemPrompt: "be brief\n\ncwd is /repo"},
		{name: "effort only", messages: ask + `,{"role":"system","content":[],"output_config":{"effort":"low","timing":{"now":"x"}}}`,
			current: "be brief\n\nlist files", oc: `{"effort":"low"}`},
		{name: "tool changes", messages: ask + `,{"role":"system","content":[{"type":"tool_removal","tool":{"type":"tool_reference","name":"ls"}},{"type":"tool_addition","tool":{"type":"tool_definition","definition":{"name":"mcp__db__query","input_schema":{"type":"object"}}}}]}`,
			current: "be brief\n\nlist files", tools: []string{"read", "mcp__db__query"}},
	}
	for _, tc := range cases {
		var request dto.ClaudeRequest
		require.NoError(t, common.UnmarshalJsonStr(`{"model":"claude-haiku-5.5","max_tokens":32000,"stream":true,`+system+`,
			"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919","trigger":{"type":"tool_uses","value":30}}]},
			"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"metadata":{"user_id":"u"},
			"tools":[{"name":"ls","input_schema":{"type":"object"}},{"name":"read","input_schema":{"type":"object"},"defer_loading":true}],
			"messages":[`+tc.messages+`]}`, &request), tc.name)
		fields, err := fields(&request)
		require.NoError(t, err, tc.name)
		model := haiku
		model.SystemPrompt = tc.enabled
		body, err := generateAssistantResponse(fields, testProfile, "scope", model)
		require.NoError(t, err, tc.name)
		state := marshalJSON(t, body).Get("conversationState")
		var history []string
		for _, entry := range state.Get("history").Array() {
			if user := entry.Get("userInputMessage"); user.Exists() {
				label := "user: "
				if user.Get("userInputMessageContext.toolResults").Exists() {
					label = "user with results: "
				}
				history = append(history, label+user.Get("content").Str)
			} else {
				history = append(history, "assistant: "+entry.Get("assistantResponseMessage.content").Str)
			}
		}
		assert.Equal(t, tc.history, history, tc.name)
		current := state.Get("currentMessage.userInputMessage")
		assert.Equal(t, tc.current, current.Get("content").Str, tc.name)
		results := current.Get("userInputMessageContext.toolResults").Array()
		if assert.Len(t, results, tc.results, tc.name) && tc.results > 0 {
			assert.Equal(t, "toolu_1", results[0].Get("toolUseId").Str, tc.name)
			assert.Equal(t, "toolu_1", state.Get("history.1.assistantResponseMessage.toolUses.0.toolUseId").Str, tc.name)
		}
		systemPrompt, _ := body["systemPrompt"].(string)
		assert.Equal(t, tc.systemPrompt, systemPrompt, tc.name)
		amrf := marshalJSON(t, body["additionalModelRequestFields"])
		assert.JSONEq(t, cmp.Or(tc.oc, `{"effort":"high"}`), amrf.Get("output_config").Raw, tc.name)
		var tools []string
		for _, tool := range current.Get("userInputMessageContext.tools").Array() {
			tools = append(tools, tool.Get("toolSpecification.name").Str)
		}
		if tc.tools == nil {
			tc.tools = []string{"ls", "read"}
		}
		assert.Equal(t, tc.tools, tools, tc.name)
		assert.NotContains(t, marshalJSON(t, body).Raw, "cache_control", tc.name)
	}
	for _, refused := range []string{
		`{"role":"system","content":[{"type":"tool_use","id":"toolu_2","name":"ls","input":{}}]}`,
		`{"role":"system","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}`,
		`{"role":"system","content":[{"type":"tool_addition","tool":{"type":"tool_reference","name":"missing"}}]}`,
	} {
		_, err := generateAssistantResponse(members(t, `{"model":"m","messages":[`+ask+`,`+refused+`]}`), testProfile, "scope", haiku)
		assert.Error(t, err, refused)
	}
}

func TestRefusedOptions(t *testing.T) {
	for _, extra := range []string{`"stop_sequences":["x"]`, `"output_format":{"type":"json_schema"}`} {
		fields := members(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],`+extra+`}`)
		_, err := generateAssistantResponse(fields, testProfile, "scope", Model{})
		assert.Error(t, err, extra)
	}
	image := members(t, `{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`)
	_, err := generateAssistantResponse(image, testProfile, "scope", Model{})
	assert.Error(t, err)

	base := `"model":"m","input":"hi","prompt_cache_key":"thread-1","stream":true`
	body, err := createResponse(members(t, `{`+base+`}`), testProfile, "scope", Model{})
	require.NoError(t, err)
	assert.NotContains(t, body, "prompt_cache_key")
	for _, extra := range []string{`"text":{"format":{"type":"json_object"}}`, `"store":true`, `"previous_response_id":"r"`, `"service_tier":"flex"`} {
		_, err := createResponse(members(t, `{`+base+`,`+extra+`}`), testProfile, "scope", Model{})
		assert.Error(t, err, extra)
	}
	_, err = createResponse(members(t, `{"model":"m","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`), testProfile, "scope", Model{})
	assert.Error(t, err)
}

// An account without system_field_injection gets its system prompt, and the
// leading developer messages Kiro refuses as input items, at the start of the
// first user message, where they stay in the conversation's stable prefix; an
// account with it keeps them as system fields.
func TestSystemPromptFolding(t *testing.T) {
	messages := members(t, `{"system":"be brief","messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ok"},{"role":"user","content":"second"}]}`)
	for _, enabled := range []bool{false, true} {
		body, err := generateAssistantResponse(messages, testProfile, "scope", Model{ID: "claude-haiku-5.5", SystemPrompt: enabled})
		require.NoError(t, err)
		request := marshalJSON(t, body)
		first := request.Get("conversationState.history.0.userInputMessage.content").Str
		assert.Equal(t, "second", request.Get("conversationState.currentMessage.userInputMessage.content").Str)
		if enabled {
			assert.Equal(t, "be brief", request.Get("systemPrompt").Str)
			assert.Equal(t, "first", first)
		} else {
			assert.False(t, request.Get("systemPrompt").Exists())
			assert.Equal(t, "be brief\n\nfirst", first)
		}
	}
	developer := `{"type":"message","role":"developer","content":[{"type":"input_text","text":"read-only"}]}`
	cases := []struct {
		name, input          string
		enabled              bool
		instructions, folded string
	}{
		{"string input", `"hi"`, false, "", `[{"content":"be brief\n\nhi","role":"user"}]`},
		{"text parts", `[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`, false, "",
			`[{"content":[{"text":"be brief","type":"input_text"},{"type":"input_text","text":"hi"}],"role":"user","type":"message"}]`},
		{"no user message", `[{"type":"function_call_output","call_id":"c","output":"x"}]`, false, "",
			`[{"content":"be brief","role":"user"},{"call_id":"c","output":"x","type":"function_call_output"}]`},
		{"leading developer message", `[` + developer + `,{"role":"user","content":"hi"}]`, false, "", `[{"content":"be brief\n\nread-only\n\nhi","role":"user"}]`},
		{"later developer message", `[{"role":"user","content":"hi"},` + developer + `]`, false, "",
			`[{"content":"be brief\n\nhi","role":"user"},{"content":[{"text":"read-only","type":"input_text"}],"role":"user","type":"message"}]`},
		{"feature enabled", `[` + developer + `,{"role":"user","content":"hi"}]`, true, "be brief\n\nread-only", `[{"content":"hi","role":"user"}]`},
	}
	for _, tc := range cases {
		body, err := createResponse(members(t, `{"instructions":"be brief","input":`+tc.input+`}`), testProfile, "scope", Model{SystemPrompt: tc.enabled})
		require.NoError(t, err, tc.name)
		instructions, _ := body["instructions"].(string)
		assert.Equal(t, tc.instructions, instructions, tc.name)
		assert.JSONEq(t, tc.folded, marshalJSON(t, body["input"]).Raw, tc.name)
	}
}

// Codex asks for reasoning summaries with OpenAI's values. Kiro's Claude models
// take them as thinking.display with a thinking type; models without a display
// option refuse the field.
func TestResponsesReasoningSummary(t *testing.T) {
	claude := json.RawMessage(`{"properties":{"thinking":{"properties":{"type":{"enum":["adaptive","disabled"]},"display":{"enum":["summarized","omitted"]}}}}}`)
	gpt := json.RawMessage(`{"properties":{"reasoning":{"properties":{"effort":{"enum":["low","high"]}}}}}`)
	cases := []struct {
		name   string
		schema json.RawMessage
		in     string
		want   string
	}{
		{"claude summary auto", claude, `{"effort":"low","summary":"auto"}`, `{"effort":"low","summary":"summarized","type":"adaptive"}`},
		{"claude summary detailed", claude, `{"summary":"detailed"}`, `{"summary":"summarized","type":"adaptive"}`},
		{"claude summary omitted", claude, `{"type":"adaptive","summary":"omitted"}`, `{"type":"adaptive","summary":"omitted"}`},
		{"claude summary null", claude, `{"effort":"low","summary":null}`, `{"effort":"low"}`},
		{"gpt summary", gpt, `{"effort":"low","summary":"detailed"}`, `{"effort":"low"}`},
		{"no schema", nil, `{"effort":"low","summary":"auto"}`, `{"effort":"low"}`},
	}
	for _, tc := range cases {
		out, err := responsesReasoning(json.RawMessage(tc.in), Model{Schema: tc.schema})
		require.NoError(t, err, tc.name)
		assert.JSONEq(t, tc.want, string(out), tc.name)
	}
	_, err := responsesReasoning(json.RawMessage(`"high"`), Model{Schema: claude})
	assert.Error(t, err)
}

// Kiro has no sampling controls and caps output only where a model takes
// max_tokens on GenerateAssistantResponse; clients that send them are served.
func TestRelaxedClientOptions(t *testing.T) {
	haiku := Model{ID: "claude-haiku-5.5", Schema: json.RawMessage(`{"properties":{
		"thinking":{"properties":{"type":{"enum":["adaptive","disabled"]},"display":{"enum":["summarized","omitted"]}}},
		"output_config":{"properties":{"effort":{"enum":["low","medium","high"]}}},
		"max_tokens":{"type":"integer","minimum":1024,"maximum":64000}}}`)}
	sonnet := Model{ID: "claude-sonnet-5.5", Schema: json.RawMessage(`{"properties":{"thinking":{"properties":{"type":{"enum":["adaptive","between_tools"]}}}}}`)}
	gpt := Model{ID: "gpt-5.6-luna", Schema: json.RawMessage(`{"properties":{"reasoning":{}}}`)}
	assert.True(t, haiku.TakesOutputLimit())
	assert.False(t, gpt.TakesOutputLimit())
	cases := []struct {
		name   string
		model  Model
		extra  string
		amrf   string
		failed bool
	}{
		{"sampling options dropped", haiku, `"temperature":0.7,"top_p":0.9,"top_k":40,"max_tokens":2048`, `{"max_tokens":2048}`, false},
		{"cap below the minimum", haiku, `"max_tokens":500`, `{"max_tokens":1024}`, false},
		{"cap above the maximum", haiku, `"max_tokens":100000`, `{"max_tokens":64000}`, false},
		{"model without a cap", gpt, `"temperature":0.7,"max_tokens":2048`, ``, false},
		{"manual thinking becomes adaptive", haiku, `"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"high"}`, `{"output_config":{"effort":"high"},"thinking":{"type":"adaptive"}}`, false},
		{"options outside the schema dropped", haiku, `"thinking":{"type":"adaptive","display":"full"},"output_config":{"effort":"minimal"}`, `{"thinking":{"type":"adaptive"}}`, false},
		{"thinking off where it cannot be turned off", sonnet, `"thinking":{"type":"disabled"}`, ``, false},
		{"thinking on a model without it", gpt, `"thinking":{"type":"enabled","budget_tokens":1024}`, ``, true},
	}
	for _, tc := range cases {
		fields := members(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],`+tc.extra+`}`)
		body, err := generateAssistantResponse(fields, testProfile, "scope", tc.model)
		if tc.failed {
			assert.Error(t, err, tc.name)
			continue
		}
		require.NoError(t, err, tc.name)
		if tc.amrf == "" {
			assert.Nil(t, body["additionalModelRequestFields"], tc.name)
			continue
		}
		assert.JSONEq(t, tc.amrf, marshalJSON(t, body["additionalModelRequestFields"]).Raw, tc.name)
	}
	body, err := createResponse(members(t, `{"model":"m","input":"hi","temperature":0.7,"top_p":0.9,"max_output_tokens":10}`), testProfile, "scope", gpt)
	require.NoError(t, err)
	assert.NotContains(t, body, "temperature")
	assert.NotContains(t, body, "top_p")
	assert.NotContains(t, body, "max_output_tokens")
}

func sse(events ...string) string {
	var body strings.Builder
	for _, e := range events {
		body.WriteString("data: " + e + "\n\n")
	}
	return body.String()
}

var responsesTestMeter = responsesMeter{prompt: 7, output: func(text string) int { return len(strings.Fields(text)) }}

// Responses output never reveals the gateway's Kiro account: the kiro:
// metadata it sent and the credit and context figures Kiro reports. Usage is
// counted locally, since Kiro reports none.
func TestResponsesStreamHidesKiroFieldsAndCountsUsage(t *testing.T) {
	body := sse(
		`{"type":"response.created","response":{"id":"r","status":"in_progress","metadata":{"kiro:conversation_id":"c","client":"kept"}}}`,
		`{"type":"kiro:keep_alive"}`,
		`{"type":"response.output_text.delta","output_index":0,"item_id":"m","delta":"hello there"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","name":"weather","call_id":"call-1","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","metadata":{"kiro:agent_mode":"vibe","client":"kept"},"usage":{"kiro:credits":{"value":0.01},"kiro:context_usage_percentage":0.2}}}`,
	)
	for _, streaming := range []bool{false, true} {
		s := newResponsesStream(io.NopCloser(strings.NewReader(body)), streaming, responsesTestMeter, 1<<20)
		var raw []byte
		var err error
		if streaming {
			raw, err = io.ReadAll(s)
		} else {
			raw, err = s.response()
		}
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "kiro:", "streaming=%v", streaming)
		assert.Contains(t, string(raw), `"client":"kept"`)
		final := gjson.ParseBytes(raw)
		if streaming {
			assert.True(t, s.completed)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			final = gjson.Get(strings.TrimPrefix(lines[len(lines)-1], "data: "), "response")
		} else {
			assert.Equal(t, "hello there", final.Get("output.0.content.0.text").Str)
			assert.Equal(t, `{"city":"Paris"}`, final.Get("output.1.arguments").Str)
		}
		assert.EqualValues(t, 7, final.Get("usage.input_tokens").Int())
		// "hello there", "weather" and the arguments.
		assert.EqualValues(t, 4, final.Get("usage.output_tokens").Int())
		assert.EqualValues(t, 11, final.Get("usage.total_tokens").Int())
	}
}

// Only response.completed or response.incomplete completes a response; a
// stream that stops before them, or one Kiro fails, never does.
func TestResponsesStreamNeedsTerminal(t *testing.T) {
	truncated := sse(`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`, `{"type":"response.output_text.delta","output_index":0,"delta":"partial"}`)
	failed := truncated + sse(`{"type":"response.failed","response":{"id":"r","status":"failed"}}`)
	for _, body := range []string{truncated, failed} {
		s := newResponsesStream(io.NopCloser(strings.NewReader(body)), true, responsesTestMeter, 0)
		_, err := io.ReadAll(s)
		require.Error(t, err)
		assert.False(t, s.completed)
		_, err = newResponsesStream(io.NopCloser(strings.NewReader(body)), false, responsesTestMeter, 0).response()
		assert.Error(t, err)
	}
	incomplete := truncated + sse(`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
	s := newResponsesStream(io.NopCloser(strings.NewReader(incomplete)), true, responsesTestMeter, 0)
	_, err := io.ReadAll(s)
	require.NoError(t, err)
	assert.True(t, s.completed)
}

func eventFrame(t *testing.T, messageType, eventType, payload string) []byte {
	t.Helper()
	var headers bytes.Buffer
	for _, h := range [][2]string{{":message-type", messageType}, {":event-type", eventType}, {":content-type", "application/json"}} {
		headers.WriteByte(byte(len(h[0])))
		headers.WriteString(h[0])
		headers.WriteByte(7)
		require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(h[1]))))
		headers.WriteString(h[1])
	}
	total := 12 + headers.Len() + len(payload) + 4
	var frame bytes.Buffer
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(total)))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	frame.Write(headers.Bytes())
	frame.WriteString(payload)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func generateEvents(t *testing.T, stop bool) []byte {
	t.Helper()
	var body bytes.Buffer
	body.Write(eventFrame(t, "event", "initial-response", `{"conversationId":""}`))
	body.Write(eventFrame(t, "event", "reasoningContentEvent", `{"text":"think"}`))
	body.Write(eventFrame(t, "event", "reasoningContentEvent", `{"signature":"sig"}`))
	body.Write(eventFrame(t, "event", "assistantResponseEvent", `{"content":"hello there","modelId":"claude-haiku-5.5"}`))
	body.Write(eventFrame(t, "event", "toolUseEvent", `{"name":"weather","toolUseId":"tooluse_1"}`))
	body.Write(eventFrame(t, "event", "toolUseEvent", `{"input":"{\"city\":","name":"weather","toolUseId":"tooluse_1"}`))
	body.Write(eventFrame(t, "event", "toolUseEvent", `{"input":"\"Paris\"}","name":"weather","toolUseId":"tooluse_1"}`))
	body.Write(eventFrame(t, "event", "toolUseEvent", `{"name":"weather","stop":true,"toolUseId":"tooluse_1"}`))
	if stop {
		body.Write(eventFrame(t, "event", "metadataEvent", `{"stopReason":"TOOL_USE"}`))
		body.Write(eventFrame(t, "event", "contextUsageEvent", `{"contextUsagePercentage":1.1}`))
		body.Write(eventFrame(t, "event", "meteringEvent", `{"unit":"credit","unitPlural":"credits","usage":0.01}`))
	}
	return body.Bytes()
}

var garTestMeter = garMeter{prompt: 9, output: func(text string, toolUses int) int { return len(strings.Fields(text)) + 40*toolUses }}

func TestGenerateAssistantResponseAsMessages(t *testing.T) {
	raw, err := newGARStream(io.NopCloser(bytes.NewReader(generateEvents(t, true))), "claude-haiku-5.5", false, garTestMeter, 1<<20).message()
	require.NoError(t, err)
	message := gjson.ParseBytes(raw)
	assert.Equal(t, "tool_use", message.Get("stop_reason").Str)
	assert.Equal(t, "thinking", message.Get("content.0.type").Str)
	assert.Equal(t, "sig", message.Get("content.0.signature").Str)
	assert.Equal(t, "hello there", message.Get("content.1.text").Str)
	assert.Equal(t, "Paris", message.Get("content.2.input.city").Str)
	assert.EqualValues(t, 9, message.Get("usage.input_tokens").Int())
	// "think", "hello there", "weather", the input, and one tool call.
	assert.EqualValues(t, 45, message.Get("usage.output_tokens").Int())
	assert.NotContains(t, string(raw), "credit")

	stream := newGARStream(io.NopCloser(bytes.NewReader(generateEvents(t, true))), "claude-haiku-5.5", true, garTestMeter, 0)
	events, err := io.ReadAll(stream)
	require.NoError(t, err)
	text := string(events)
	assert.Contains(t, text, "event: message_start")
	assert.Contains(t, text, `"partial_json":"{\"city\":"`)
	assert.True(t, strings.HasSuffix(strings.TrimSpace(text), `data: {"type":"message_stop"}`))
	assert.Contains(t, text, `"output_tokens":45`)
}

// A response that loses its stop signal ends with an error event, never a
// stop, and a message is not built from it.
func TestGenerateAssistantResponseNeedsStop(t *testing.T) {
	truncated := generateEvents(t, false)
	_, err := newGARStream(io.NopCloser(bytes.NewReader(truncated)), "m", false, garTestMeter, 1<<20).message()
	require.ErrorIs(t, err, errIncomplete)

	events, err := io.ReadAll(newGARStream(io.NopCloser(bytes.NewReader(truncated)), "m", true, garTestMeter, 0))
	require.NoError(t, err)
	assert.NotContains(t, string(events), "message_stop")
	assert.True(t, strings.HasPrefix(strings.TrimSpace(string(events[bytes.LastIndex(events, []byte("event: ")):])), "event: error"))

	exception := eventFrame(t, "exception", "", `{"message":"slow down"}`)
	events, err = io.ReadAll(newGARStream(io.NopCloser(bytes.NewReader(exception)), "m", true, garTestMeter, 0))
	require.NoError(t, err)
	assert.Contains(t, string(events), "event: error")
	assert.NotContains(t, string(events), "message_start")
}

// fakeAuth is Kiro's auth service: it rotates the refresh token on every
// refresh and refuses a spent one.
type fakeAuth struct {
	mu      sync.Mutex
	valid   map[string]bool
	next    int
	calls   atomic.Int64
	reuses  atomic.Int64
	delay   time.Duration
	refuse  bool
	status  int
	partial bool
}

func (f *fakeAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	var body map[string]string
	_ = common.DecodeJson(r.Body, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse || !f.valid[body["refreshToken"]] {
		if !f.refuse {
			f.reuses.Add(1)
		}
		w.WriteHeader(cmp.Or(f.status, http.StatusBadRequest))
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	delete(f.valid, body["refreshToken"])
	f.next++
	rotated := "refresh-" + string(rune('a'+f.next))
	f.valid[rotated] = true
	if f.partial {
		_, _ = w.Write([]byte(`{"refreshToken":"` + rotated + `","expiresIn":3600}`))
		return
	}
	_, _ = w.Write([]byte(`{"accessToken":"access-` + rotated + `","refreshToken":"` + rotated + `","expiresIn":3600,"profileArn":"` + testProfile + `"}`))
}

func useFakeAuth(t *testing.T, auth *fakeAuth) {
	t.Helper()
	server := httptest.NewServer(auth)
	t.Cleanup(server.Close)
	previous := authURL
	authURL = func(Credential) string { return server.URL }
	t.Cleanup(func() { authURL = previous })
}

func kiroChannel(t *testing.T, key string) *model.Channel {
	t.Helper()
	return kiroChannelAt(t, key, DefaultRuntime)
}

func kiroChannelAt(t *testing.T, key, base string) *model.Channel {
	t.Helper()
	pgtest.Reset(t, model.DB)
	channel := &model.Channel{Type: constant.ChannelTypeKiro, Key: key, Status: common.ChannelStatusEnabled, Name: "kiro", Models: "claude-haiku-5.5", Group: "default", BaseURL: &base}
	require.NoError(t, channel.Insert())
	return channel
}

func storedKey(t *testing.T, id int) Credential {
	t.Helper()
	channel, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	credential, err := ParseCredential(channel.Key)
	require.NoError(t, err)
	return credential
}

// Requests on several nodes that find the access token expired renew it once:
// a refresh token Kiro rotated is spent, and sending it twice fails the grant.
// Every caller gets the line stored with the rotated token.
func TestRenewalSpendsRefreshTokenOnce(t *testing.T) {
	auth := &fakeAuth{valid: map[string]bool{"refresh-0": true}, delay: 200 * time.Millisecond}
	useFakeAuth(t, auth)
	used := `{"method":"social","refreshToken":"refresh-0"}`
	channel := kiroChannel(t, used)

	results := make([]string, 8)
	var group sync.WaitGroup
	for i := range results {
		group.Go(func() {
			if i%2 == 1 {
				// Another node: its own renewal, serialized by the refresh lock.
				line, err := renewLine(http.DefaultClient, channel.Id, DefaultRuntime, used)
				assert.NoError(t, err)
				results[i] = line
				return
			}
			line, err := renew(context.Background(), http.DefaultClient, channel.Id, DefaultRuntime, used)
			assert.NoError(t, err)
			results[i] = line
		})
	}
	group.Wait()

	assert.EqualValues(t, 1, auth.calls.Load())
	assert.Zero(t, auth.reuses.Load())
	stored := storedKey(t, channel.Id)
	assert.Equal(t, "refresh-b", stored.RefreshToken)
	assert.Equal(t, "access-refresh-b", stored.AccessToken)
	assert.Equal(t, testProfile, stored.ProfileArn)
	assert.True(t, stored.Usable(time.Now()))
	for _, result := range results {
		assert.Equal(t, results[0], result)
	}
}

// A rotated refresh token is saved even when the rest of the answer cannot be
// used; a refusal saves nothing.
func TestRenewalFailures(t *testing.T) {
	auth := &fakeAuth{valid: map[string]bool{"refresh-0": true}, partial: true}
	useFakeAuth(t, auth)
	used := `{"method":"social","refreshToken":"refresh-0"}`
	channel := kiroChannel(t, used)
	_, err := renew(context.Background(), http.DefaultClient, channel.Id, DefaultRuntime, used)
	require.ErrorIs(t, err, ErrCredential)
	assert.True(t, permanentFailure(err))
	assert.Equal(t, "refresh-b", storedKey(t, channel.Id).RefreshToken)
	assert.Empty(t, storedKey(t, channel.Id).AccessToken)

	refused := &fakeAuth{valid: map[string]bool{}, refuse: true}
	useFakeAuth(t, refused)
	channel = kiroChannel(t, used)
	_, err = renew(context.Background(), http.DefaultClient, channel.Id, DefaultRuntime, used)
	require.ErrorIs(t, err, ErrRejected)
	assert.Equal(t, "refresh-0", storedKey(t, channel.Id).RefreshToken)

	// A timeout or another status that is no refusal of the grant waits out a
	// short cooldown instead of disabling the channel.
	for _, status := range []int{http.StatusRequestTimeout, http.StatusNotFound} {
		useFakeAuth(t, &fakeAuth{valid: map[string]bool{}, refuse: true, status: status})
		_, err := renew(context.Background(), http.DefaultClient, channel.Id, DefaultRuntime, used)
		require.Error(t, err)
		assert.False(t, permanentFailure(err), "status %d", status)
	}

	// The cooldown makes the next requests fail without reaching Kiro.
	failure := recordFailure(channel.Id, used, err, permanentFailure(err))
	assert.True(t, failure.permanent)
	current, failing := currentFailure(channel.Id, used)
	assert.True(t, failing)
	assert.WithinDuration(t, time.Now().Add(permanentCooldown), current.until, time.Second)
	_, failing = currentFailure(channel.Id, used+" ")
	assert.False(t, failing, "a new credential line starts clean")
	clearFailure(channel.Id, used)

	// Every request that shared one failed renewal sees the same failure: the
	// backoff grows only when the line fails again after its cooldown.
	first := recordFailure(channel.Id, used, io.ErrUnexpectedEOF, false)
	for range 50 {
		assert.Equal(t, first, recordFailure(channel.Id, used, io.ErrUnexpectedEOF, false))
	}
	assert.Equal(t, 1, first.streak)
	assert.WithinDuration(t, time.Now().Add(transientCooldown), first.until, time.Second)
	failures.Lock()
	expired := failures.byLine[failureKey(channel.Id, used)]
	expired.until = time.Now().Add(-time.Second)
	failures.byLine[failureKey(channel.Id, used)] = expired
	failures.Unlock()
	again := recordFailure(channel.Id, used, io.ErrUnexpectedEOF, false)
	assert.Equal(t, 2, again.streak)
	assert.WithinDuration(t, time.Now().Add(2*transientCooldown), again.until, time.Second)
	clearFailure(channel.Id, used)
}

// trackedBody is an upstream body that reports whether the relay closed it.
type trackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

func words(text string) int { return len(strings.Fields(text)) }

// CreateResponse takes no output cap, so the relay ends the response at the
// client's: the upstream is closed, and the client gets the output that fit
// and an incomplete response for max_output_tokens, settled on that output.
func TestResponsesStreamEndsAtTheClientCap(t *testing.T) {
	events := sse(
		`{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","status":"in_progress","model":"m","metadata":{"kiro:agent_mode":"vibe"}}}`,
		`{"type":"response.output_text.delta","sequence_number":1,"output_index":0,"item_id":"m1","delta":"one two three"}`,
		`{"type":"response.output_text.delta","sequence_number":2,"output_index":0,"item_id":"m1","delta":"four five"}`,
		`{"type":"response.completed","sequence_number":3,"response":{"id":"r","status":"completed"}}`,
	)
	meter := responsesMeter{prompt: 7, limit: 4, output: words, count: words}
	for _, streaming := range []bool{true, false} {
		body := &trackedBody{Reader: strings.NewReader(events)}
		s := newResponsesStream(body, streaming, meter, 1<<20)
		var final gjson.Result
		if streaming {
			raw, err := io.ReadAll(s)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "four")
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			assert.Equal(t, "event: response.incomplete", lines[len(lines)-2])
			last := gjson.Parse(strings.TrimPrefix(lines[len(lines)-1], "data: "))
			assert.EqualValues(t, 2, last.Get("sequence_number").Int())
			final = last.Get("response")
		} else {
			raw, err := s.response()
			require.NoError(t, err)
			final = gjson.ParseBytes(raw)
		}
		assert.True(t, body.closed.Load(), "streaming=%v", streaming)
		assert.True(t, s.completed && s.truncated)
		assert.Equal(t, "incomplete", final.Get("status").Str)
		assert.Equal(t, "max_output_tokens", final.Get("incomplete_details.reason").Str)
		assert.Equal(t, "one two three", final.Get("output.0.content.0.text").Str)
		assert.Equal(t, "incomplete", final.Get("output.0.status").Str)
		assert.EqualValues(t, 3, final.Get("usage.output_tokens").Int())
		assert.EqualValues(t, 7, final.Get("usage.input_tokens").Int())
		assert.NotContains(t, final.Raw, "kiro:")
	}
}

// GenerateAssistantResponse caps output only for a model whose schema takes
// max_tokens, so for others the relay ends the response at the client's cap
// as a max_tokens stop. A tool call the cap cuts off is no usable call.
func TestGenerateAssistantResponseEndsAtTheClientCap(t *testing.T) {
	meter := garTestMeter
	meter.count = words
	cases := []struct {
		name   string
		limit  int
		blocks []string
		output int64
	}{
		// think (1) and "hello there" (2) fit; the tool call (1 + 40) does not.
		{"before a tool call", 5, []string{"thinking", "text"}, 3},
		// The call starts within the cap and its input runs past it.
		{"inside a tool call", 45, []string{"thinking", "text"}, 45},
	}
	for _, tc := range cases {
		meter.limit = tc.limit
		body := &trackedBody{Reader: bytes.NewReader(generateEvents(t, true))}
		raw, err := newGARStream(body, "m", false, meter, 1<<20).message()
		require.NoError(t, err, tc.name)
		message := gjson.ParseBytes(raw)
		assert.True(t, body.closed.Load(), tc.name)
		assert.Equal(t, "max_tokens", message.Get("stop_reason").Str, tc.name)
		var kinds []string
		for _, block := range message.Get("content").Array() {
			kinds = append(kinds, block.Get("type").Str)
		}
		assert.Equal(t, tc.blocks, kinds, tc.name)
		assert.Equal(t, tc.output, message.Get("usage.output_tokens").Int(), tc.name)

		events, err := io.ReadAll(newGARStream(io.NopCloser(bytes.NewReader(generateEvents(t, true))), "m", true, meter, 0))
		require.NoError(t, err, tc.name)
		text := string(events)
		assert.Contains(t, text, `"stop_reason":"max_tokens"`, tc.name)
		assert.True(t, strings.HasSuffix(strings.TrimSpace(text), `data: {"type":"message_stop"}`), tc.name)
		assert.NotContains(t, text, "Paris", tc.name)
	}
}

// fakeKiro is the Runtime and management service of an account that offers
// claude-haiku-5.5 and refuses the access tokens it lists.
type fakeKiro struct {
	mu             sync.Mutex
	refused        map[string]bool
	catalogRefused map[string]bool
	exhausted      map[string]bool
	generation     []byte
	generations    atomic.Int64
}

func (f *fakeKiro) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	refused, catalogRefused, exhausted := f.refused[token], f.catalogRefused[token], f.exhausted[token]
	f.mu.Unlock()
	switch r.Header.Get("X-Amz-Target") {
	case "KiroControlPlaneBearerService.ListAvailableModels":
		if catalogRefused {
			// As Kiro's management service answers an invalid token.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"com.amazon.kiro.controlplane#AccessDeniedException","message":"Invalid token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"modelId":"claude-haiku-5.5","tokenLimits":{"maxOutputTokens":64000},"additionalModelRequestFieldsSchema":{"properties":{"max_tokens":{"type":"integer","minimum":1024,"maximum":64000}}}}]}`))
	case "AmazonCodeWhispererStreamingService.GetFeatureConfiguration":
		_, _ = w.Write([]byte(`{"configuration":{}}`))
	case "AmazonCodeWhispererStreamingService.GenerateAssistantResponse":
		f.generations.Add(1)
		if refused {
			// As Kiro's Runtime answers an invalid token.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"__type":"com.amazon.kiro.runtimeservice#AccessDeniedException","message":"The bearer token included in the request is invalid."}`))
			return
		}
		if exhausted {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"__type":"com.amazon.kiro.runtimeservice#ServiceQuotaExceededException","message":"You have reached the limit."}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(f.generation)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeKiro) refuse(tokens ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, token := range tokens {
		f.refused[token] = true
	}
}

// kiroAttempt is an attempt on a saved Kiro channel, as the relay sets one up.
func kiroAttempt(t *testing.T, channel *model.Channel, format types.RelayFormat, stream bool) (*gin.Context, *relaycommon.RelayInfo, context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{IsStream: stream, RelayFormat: format, OriginModelName: "claude-haiku-5.5", ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeKiro, ChannelId: channel.Id, ChannelBaseUrl: channel.GetBaseURL(), ApiKey: channel.Key, UpstreamModelName: "claude-haiku-5.5",
	}}
	return c, info, cancel
}

// The Runtime refusing an access token before its expiry renews it once and
// sends the request again. A refusal after that fails the attempt so that
// routing moves on, without a status that would disable the channel. A 403
// about the account keeps the token.
func TestRefusedAccessTokenIsRenewedOnce(t *testing.T) {
	auth := &fakeAuth{valid: map[string]bool{"refresh-0": true}}
	useFakeAuth(t, auth)
	upstream := &fakeKiro{refused: map[string]bool{"stale": true}, catalogRefused: map[string]bool{"stale-catalog": true}, generation: generateEvents(t, true)}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	request := &dto.ClaudeRequest{Model: "claude-haiku-5.5", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}}}

	channel := kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-0","accessToken":"stale","expiresAt":"`+expires+`","profileArn":"`+testProfile+`"}`, server.URL)
	c, info, _ := kiroAttempt(t, channel, types.RelayFormatClaude, false)
	a := &Adaptor{}
	require.Nil(t, a.PrepareAttempt(c, info))
	converted, err := a.ConvertClaudeRequest(c, info, request)
	require.NoError(t, err)
	raw, err := common.Marshal(converted)
	require.NoError(t, err)
	body, closer, err := relaycommon.NewOutboundJSONBody(raw)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	resp, err := a.DoRequest(c, info, body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.(*http.Response).StatusCode)
	assert.EqualValues(t, 1, auth.calls.Load())
	assert.EqualValues(t, 2, upstream.generations.Load())
	assert.Equal(t, "access-refresh-b", storedKey(t, channel.Id).AccessToken)
	assert.Equal(t, info.ApiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey))

	// The renewed token is refused as well.
	upstream.refuse("access-refresh-b", "access-refresh-c")
	c, info, _ = kiroAttempt(t, channel, types.RelayFormatClaude, false)
	info.ApiKey = storedKeyLine(t, channel.Id)
	a = &Adaptor{}
	require.Nil(t, a.PrepareAttempt(c, info))
	converted, err = a.ConvertClaudeRequest(c, info, request)
	require.NoError(t, err)
	raw, err = common.Marshal(converted)
	require.NoError(t, err)
	body, closer, err = relaycommon.NewOutboundJSONBody(raw)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	_, err = a.DoRequest(c, info, body)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.False(t, types.IsSkipRetryError(apiErr))
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })
	assert.False(t, service.ShouldDisableChannel(apiErr), "a refused access token never disables the channel")
	assert.EqualValues(t, 2, auth.calls.Load())
	clearFailure(channel.Id, info.ApiKey)

	// A 403 that is no token refusal is the upstream's answer, and renews nothing.
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-c","accessToken":"spent","expiresAt":"`+expires+`","profileArn":"`+testProfile+`"}`, server.URL)
	upstream.mu.Lock()
	upstream.exhausted = map[string]bool{"spent": true}
	upstream.mu.Unlock()
	c, info, _ = kiroAttempt(t, channel, types.RelayFormatClaude, false)
	a = &Adaptor{}
	require.Nil(t, a.PrepareAttempt(c, info))
	converted, err = a.ConvertClaudeRequest(c, info, request)
	require.NoError(t, err)
	raw, err = common.Marshal(converted)
	require.NoError(t, err)
	body, closer, err = relaycommon.NewOutboundJSONBody(raw)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	resp, err = a.DoRequest(c, info, body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.(*http.Response).StatusCode)
	answer, err := io.ReadAll(resp.(*http.Response).Body)
	require.NoError(t, err)
	assert.Contains(t, string(answer), "ServiceQuotaExceededException", "the relay still reads the whole answer")
	assert.False(t, revoked("spent"))
	assert.EqualValues(t, 2, auth.calls.Load())

	// The management service refusing it renews it the same way.
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-c","accessToken":"stale-catalog","expiresAt":"`+expires+`","profileArn":"`+testProfile+`"}`, server.URL)
	auth.mu.Lock()
	auth.valid["refresh-c"] = true
	auth.mu.Unlock()
	c, info, _ = kiroAttempt(t, channel, types.RelayFormatClaude, false)
	a = &Adaptor{}
	require.Nil(t, a.PrepareAttempt(c, info))
	assert.EqualValues(t, 3, auth.calls.Load())
	assert.Equal(t, "claude-haiku-5.5", a.model.ID)
}

func storedKeyLine(t *testing.T, id int) string {
	t.Helper()
	channel, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	return channel.Key
}

// A stream that stopped early is settled on what it delivered when the client
// went away, and fails without settling when Kiro stopped before its stop
// signal or terminal event.
func TestStreamEndedEarlySettlement(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	channel := kiroChannel(t, `{"method":"social","refreshToken":"r"}`)
	responses := sse(`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`, `{"type":"response.output_text.delta","output_index":0,"item_id":"m","delta":"partial"}`)
	for _, tc := range []struct {
		name     string
		generate bool
		format   types.RelayFormat
		body     func() io.ReadCloser
	}{
		{"CreateResponse", false, types.RelayFormatOpenAIResponses, func() io.ReadCloser {
			return newResponsesStream(io.NopCloser(strings.NewReader(responses)), true, responsesTestMeter, 0)
		}},
		{"GenerateAssistantResponse", true, types.RelayFormatClaude, func() io.ReadCloser {
			return newGARStream(io.NopCloser(bytes.NewReader(generateEvents(t, false))), "m", true, garTestMeter, 0)
		}},
	} {
		for _, clientGone := range []bool{true, false} {
			c, info, cancel := kiroAttempt(t, channel, tc.format, true)
			if clientGone {
				cancel()
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: tc.body()}
			_, err := (&Adaptor{generate: tc.generate}).DoResponse(c, resp, info)
			if clientGone {
				assert.Nil(t, err, "%s: a client that went away is settled", tc.name)
				assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
				continue
			}
			assert.NotNil(t, err, "%s: an upstream that stopped early fails", tc.name)
		}
	}
}

// The usage of a Kiro channel's account comes from GetUsageLimits, asked
// with the channel's credential renewed as a request renews it, and leaves
// out the account's user information. Each refusal reads as what the
// administrator has to do about it.
func TestChannelUsage(t *testing.T) {
	const limits = `{"nextDateReset":1796083200.0,"daysUntilReset":12,
		"overageConfiguration":{"overageStatus":"ENABLED"},
		"subscriptionInfo":{"subscriptionTitle":"KIRO PRO+","type":"Q_DEVELOPER_STANDALONE_PRO_PLUS","overageCapability":"OVERAGE_CAPABLE","upgradeCapability":"UPGRADE_CAPABLE"},
		"usageBreakdownList":[{"resourceType":"CREDIT","unit":"INVOCATIONS","currency":"USD","displayName":"Credit","displayNamePlural":"Credits",
			"usageLimit":841,"usageLimitWithPrecision":841.0,"currentUsage":7,"currentUsageWithPrecision":7.82,"currentOverages":0,"overageRate":0.04,"overageCap":10000,
			"freeTrialInfo":{"freeTrialStatus":"ACTIVE","freeTrialExpiry":1797000000,"usageLimit":50,"usageLimitWithPrecision":50.0,"currentUsage":3,"currentUsageWithPrecision":3.5},
			"bonuses":[{"bonusCode":"FIRST_UPGRADE_TRIAL","status":"ACTIVE","usageLimit":500.0,"currentUsage":12.5,"expiresAt":1826535962.109},
				{"bonusCode":"SPENT","displayName":"Launch bonus","status":"EXHAUSTED","usageLimit":100,"currentUsage":100,"expiresAt":1800000000},
				{"bonusCode":"OLD","status":"EXPIRED","usageLimit":100,"currentUsage":0,"expiresAt":1700000000}]}],
		"userInfo":{"userId":"d-9067a1b2c3.user-id","email":"owner@example.com"}}`
	want := &Usage{PlanTitle: "KIRO PRO+", PlanType: "Q_DEVELOPER_STANDALONE_PRO_PLUS", ResetAt: 1796083200, OverageEnabled: true,
		Resources: []UsageResource{{Type: "CREDIT", Name: "Credits", Used: 7.82, Limit: 841, OverageRate: 0.04, Currency: "USD", Bonuses: []UsageBonus{
			{FreeTrial: true, Status: "ACTIVE", Used: 3.5, Limit: 50, ExpiresAt: 1797000000},
			{Name: "FIRST_UPGRADE_TRIAL", Status: "ACTIVE", Used: 12.5, Limit: 500, ExpiresAt: 1826535962},
			{Name: "Launch bonus", Status: "EXHAUSTED", Used: 100, Limit: 100, ExpiresAt: 1800000000},
		}}}}

	var mu sync.Mutex
	status, answer := http.StatusOK, limits
	refused := map[string]bool{}
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.Header.Get("X-Amz-Target") {
		case "AmazonCodeWhispererService.ListAvailableProfiles":
			_, _ = w.Write([]byte(`{"profiles":[{"arn":"` + testProfile + `","profileName":"QDefaultProfile"}]}`))
		case "AmazonCodeWhispererService.GetUsageLimits":
			var body map[string]any
			assert.NoError(t, common.DecodeJson(r.Body, &body))
			assert.Equal(t, map[string]any{"origin": "AI_EDITOR", "isEmailRequired": false, "profileArn": testProfile}, body)
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "AI_EDITOR", r.URL.Query().Get("origin"))
			assert.Equal(t, "false", r.URL.Query().Get("isEmailRequired"))
			assert.Equal(t, testProfile, r.URL.Query().Get("profileArn"))
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, token)
			if refused[token] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"__type":"com.amazon.kiro.controlplane#AccessDeniedException","message":"Invalid token"}`))
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(answer))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	message := func(err error) string {
		var m *common.Message
		require.ErrorAs(t, err, &m)
		return m.Key
	}

	// An expired credential is renewed and the rotated refresh token saved.
	auth := &fakeAuth{valid: map[string]bool{"refresh-0": true}}
	useFakeAuth(t, auth)
	channel := kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-0"}`, server.URL)
	usage, err := ChannelUsage(context.Background(), channel)
	require.NoError(t, err)
	assert.Equal(t, want, usage)
	raw, err := common.Marshal(usage)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "example.com")
	assert.NotContains(t, string(raw), "d-9067a1b2c3")
	assert.Equal(t, []string{"access-refresh-b"}, asked)
	assert.Equal(t, "refresh-b", storedKey(t, channel.Id).RefreshToken)

	// A token Kiro refuses before its expiry is renewed once; the profile of a
	// credential that names none is looked up.
	auth.mu.Lock()
	auth.valid["refresh-x"] = true
	auth.mu.Unlock()
	refused["usage-stale"], asked = true, nil
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-x","accessToken":"usage-stale","expiresAt":"`+expires+`"}`, server.URL)
	usage, err = ChannelUsage(context.Background(), channel)
	require.NoError(t, err)
	assert.Equal(t, want, usage)
	assert.Equal(t, []string{"usage-stale", "access-refresh-c"}, asked)
	assert.EqualValues(t, 2, auth.calls.Load())

	// Refused again after the renewal, or a grant the auth service refuses:
	// the administrator has to save a new credential.
	auth.mu.Lock()
	auth.valid["refresh-y"] = true
	auth.mu.Unlock()
	refused["usage-spent"], refused["access-refresh-d"] = true, true
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"refresh-y","accessToken":"usage-spent","expiresAt":"`+expires+`","profileArn":"`+testProfile+`"}`, server.URL)
	_, err = ChannelUsage(context.Background(), channel)
	assert.Equal(t, "Kiro rejected the channel credential", message(err))
	useFakeAuth(t, &fakeAuth{valid: map[string]bool{}, refuse: true})
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"revoked"}`, server.URL)
	_, err = ChannelUsage(context.Background(), channel)
	assert.Equal(t, "Kiro rejected the channel credential", message(err))

	cases := []struct {
		name, answer, want string
		status             int
	}{
		{"an account whose plan reports no usage", `{"__type":"com.amazon.aws.codewhisperer#AccessDeniedException","message":"FEATURE_NOT_SUPPORTED","reason":"FEATURE_NOT_SUPPORTED"}`, "The Kiro account is not allowed to query its usage", http.StatusBadRequest},
		{"a suspended account", `{"__type":"com.amazon.aws.codewhisperer#AccessDeniedException","message":"Account suspended","reason":"TemporarilySuspended"}`, "The Kiro account is not allowed to query its usage", http.StatusForbidden},
		{"an outage", `{}`, "Kiro is unavailable. Please try again later", http.StatusServiceUnavailable},
		{"another refusal", `{"__type":"com.amazon.aws.codewhisperer#ValidationException","message":"bad origin"}`, "Upstream returned status {{status}}", http.StatusBadRequest},
	}
	channel = kiroChannelAt(t, `{"method":"social","refreshToken":"unused","accessToken":"usage-live","expiresAt":"`+expires+`","profileArn":"`+testProfile+`"}`, server.URL)
	for _, tc := range cases {
		mu.Lock()
		status, answer = tc.status, tc.answer
		mu.Unlock()
		_, err = ChannelUsage(context.Background(), channel)
		assert.Equal(t, tc.want, message(err), tc.name)
	}
	assert.Equal(t, "usage-live", storedKey(t, channel.Id).AccessToken, "a refusal of the account keeps the token")
}
