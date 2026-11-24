package mistral

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// mistralStream is a reasoning model's answer as Mistral streams it: content
// as chunk lists, a tool call, and the usage on the last chunk.
const mistralStream = `data: {"id":"c1","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"Check the weather."}]}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"content":[{"type":"text","text":"Let me look."}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"tool_calls":[{"id":"Ab3dE6gH9","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"},"index":0}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":20,"total_tokens":32,"prompt_tokens_details":{"cached_tokens":8}}}

data: [DONE]

`

// mistralAbortedStream ends with Mistral's in-band abort.
const mistralAbortedStream = `data: {"id":"c3","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"role":"assistant","content":"Partial"},"finish_reason":null}]}

data: {"id":"c3","object":"chat.completion.chunk","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}

data: [DONE]

`

type upstreamCall struct {
	path   string
	header http.Header
	body   []byte
}

type relayResult struct {
	calls      []upstreamCall
	output     string
	usage      *dto.Usage
	info       *relaycommon.RelayInfo
	convertErr error
	apiErr     *types.NewAPIError
}

// relayToMistral runs a client request through the Mistral adaptor the way
// the relay helpers do, against a fake Mistral that answers with reply.
func relayToMistral(t *testing.T, endpoint, clientBody string, clientHeader map[string]string, other dto.ChannelOtherSettings, headerOverride map[string]any, reply string) relayResult {
	t.Helper()
	var result relayResult
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		result.calls = append(result.calls, upstreamCall{path: r.URL.Path, header: r.Header.Clone(), body: body})
		if strings.HasPrefix(reply, "data:") {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		// The fake compresses its answer when asked, as servers may.
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = io.WriteString(w, reply)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		coded := gzip.NewWriter(w)
		_, _ = io.WriteString(coded, reply)
		require.NoError(t, coded.Close())
	}))
	t.Cleanup(upstream.Close)

	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(clientBody))
	c.Request.Header.Set("Content-Type", "application/json")
	for name, value := range clientHeader {
		c.Request.Header.Set(name, value)
	}
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMistral)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "mistral-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, gjson.Get(clientBody, "model").Str)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, other)
	if headerOverride != nil {
		common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
	}

	format := map[string]types.RelayFormat{
		"/v1/chat/completions": types.RelayFormatOpenAI,
		"/v1/completions":      types.RelayFormatOpenAI,
		"/v1/messages":         types.RelayFormatClaude,
		"/v1/responses":        types.RelayFormatOpenAIResponses,
		"/v1/embeddings":       types.RelayFormatEmbedding,
	}[endpoint]
	request, err := helper.GetAndValidateRequest(c, format)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, format, request, nil)
	require.NoError(t, err)
	info.InitChannelMeta(c)
	require.NoError(t, helper.ModelMappedHelper(c, info, request))
	result.info = info

	adaptor := &Adaptor{}
	adaptor.Init(info)
	var converted any
	switch request := request.(type) {
	case *dto.GeneralOpenAIRequest:
		converted, result.convertErr = adaptor.ConvertOpenAIRequest(c, info, request)
	case *dto.ClaudeRequest:
		converted, result.convertErr = adaptor.ConvertClaudeRequest(c, info, request)
	case *dto.OpenAIResponsesRequest:
		converted, result.convertErr = adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
	case *dto.EmbeddingRequest:
		converted, result.convertErr = adaptor.ConvertEmbeddingRequest(c, info, *request)
	}
	if result.convertErr != nil {
		return result
	}
	jsonData, err := common.Marshal(converted)
	require.NoError(t, err)
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, false)
	require.NoError(t, err)

	resp, err := adaptor.DoRequest(c, info, bytes.NewReader(jsonData))
	require.NoError(t, err)
	httpResp := resp.(*http.Response)
	require.Equal(t, http.StatusOK, httpResp.StatusCode)
	info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
	usage, apiErr := adaptor.DoResponse(c, httpResp, info)
	result.apiErr = apiErr
	if apiErr == nil {
		result.usage = usage.(*dto.Usage)
	}
	result.output = recorder.Body.String()
	return result
}

func TestRelayToMistral(t *testing.T) {
	images := strings.TrimSuffix(strings.Repeat(`{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},`, 9), ",")
	tests := []struct {
		name           string
		endpoint       string
		body           string
		clientHeader   map[string]string
		other          dto.ChannelOtherSettings
		headerOverride map[string]any
		reply          string
		// wantBody holds JSON members of the upstream body by gjson path;
		// wantAbsent the members it must not carry.
		wantPath        string
		wantAccept      string
		wantUserAgent   string
		wantBody        map[string]string
		wantAbsent      []string
		wantOutput      []string
		wantNotOutput   []string
		wantUsage       *dto.Usage
		wantDiagnostics []string
		check           func(t *testing.T, body gjson.Result)
	}{
		{
			name:     "chat request forwards Mistral's options under its names",
			endpoint: "/v1/chat/completions",
			body: `{"model":"mistral-large-4-0","stream":true,"stream_options":{"include_usage":true},"user":"u1",
				"messages":[
					{"role":"developer","content":"Be brief."},
					{"role":"user","content":[{"type":"text","text":"Weather?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"high"}}]},
					{"role":"assistant","content":"","reasoning_content":"Need a tool.","tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
					{"role":"tool","tool_call_id":"call_abc123","content":"Sunny"}],
				"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
				"seed":42,"stop":["END"],"max_completion_tokens":256,"top_k":5,"presence_penalty":0.5,"frequency_penalty":0,
				"response_format":{"type":"json_object"},"prompt_cache_key":"pk","metadata":{"a":"b"},
				"safe_prompt":true,"prompt_mode":"reasoning","guardrails":[],"logit_bias":{"1":2},"verbosity":"low"}`,
			clientHeader:  map[string]string{"x-affinity": "11111111-2222-3333-4444-555555555555"},
			reply:         mistralStream,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "text/event-stream",
			wantUserAgent: "mistral-client-python/Mistral-Vibe/3.1.4",
			wantBody: map[string]string{
				"model": `"mistral-large-4-0"`, "stream": `true`, "temperature": `1`, "max_tokens": `256`,
				"random_seed": `42`, "stop": `["END"]`, "top_k": `5`, "presence_penalty": `0.5`,
				"frequency_penalty": `0`, "response_format": `{"type":"json_object"}`, "prompt_cache_key": `"pk"`,
				"metadata": `{"a":"b"}`, "safe_prompt": `true`, "prompt_mode": `"reasoning"`, "guardrails": `[]`,
				"parallel_tool_calls": `true`, "reasoning_effort": `"high"`,
				"messages.0":            `{"role":"system","content":"Be brief."}`,
				"messages.1.content":    `[{"type":"text","text":"Weather?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"high"}}]`,
				"messages.2.content":    `[{"type":"thinking","thinking":[{"type":"text","text":"Need a tool."}]}]`,
				"messages.2.tool_calls": `[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"},"index":0}]`,
				"messages.3":            `{"role":"tool","content":"Sunny","tool_call_id":"call_abc123","name":"get_weather"}`,
				"tools":                 `[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`,
			},
			wantAbsent:      []string{"stream_options", "user", "seed", "max_completion_tokens", "logit_bias", "verbosity"},
			wantOutput:      []string{`"reasoning_content":"Check the weather."`, `"content":"Let me look."`, `"id":"Ab3dE6gH9"`, `"finish_reason":"tool_calls"`},
			wantNotOutput:   []string{`"type":"thinking"`},
			wantUsage:       &dto.Usage{PromptTokens: 12, CompletionTokens: 20, TotalTokens: 32, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 8}},
			wantDiagnostics: []string{"logit_bias", "verbosity"},
		},
		{
			name:           "plain chat answer and the channel's header override",
			endpoint:       "/v1/chat/completions",
			body:           `{"model":"mistral-small-latest","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none","n":2}`,
			headerOverride: map[string]any{"User-Agent": "channel-agent"},
			reply: `{"id":"c2","object":"chat.completion","created":1764000000,"model":"mistral-small-latest","choices":[{"index":0,
				"message":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"Hmm."}]},{"type":"text","text":"Hello!"}]},
				"finish_reason":"model_length"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "application/json",
			wantUserAgent: "channel-agent",
			wantBody: map[string]string{"stream": `false`, "temperature": `1`, "n": `2`, "reasoning_effort": `"none"`,
				"messages": `[{"role":"user","content":"hi"}]`},
			wantAbsent: []string{"tools", "parallel_tool_calls"},
			wantOutput: []string{`"content":"Hello!"`, `"reasoning_content":"Hmm."`, `"finish_reason":"length"`},
			wantUsage:  &dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
		},
		{
			name:          "older images beyond eight are replaced",
			endpoint:      "/v1/chat/completions",
			body:          `{"model":"mistral-large-4-0","messages":[{"role":"user","content":[` + images + `]}]}`,
			reply:         `{"id":"c4","object":"chat.completion","created":1764000000,"model":"mistral-large-4-0","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "application/json",
			wantUserAgent: UserAgent,
			wantBody: map[string]string{
				"messages.0.content.0": `{"type":"text","text":"[Image removed: only the 8 most recent images are sent]"}`,
				"messages.0.content.1": `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`,
				"messages.0.content.8": `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`,
			},
			wantUsage:       &dto.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
			wantDiagnostics: []string{"messages.0.content.0"},
		},
		{
			name:     "Messages request from Claude Code",
			endpoint: "/v1/messages",
			body: `{"model":"mistral-vibe-cli-latest","max_tokens":1024,"stream":true,"system":"You are terse.",
				"thinking":{"type":"enabled","budget_tokens":4096},
				"messages":[{"role":"user","content":"Weather in Paris?"}],
				"tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]}`,
			reply:         mistralStream,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "text/event-stream",
			wantUserAgent: UserAgent,
			wantBody: map[string]string{
				"model": `"mistral-vibe-cli-latest"`, "stream": `true`, "max_tokens": `1024`, "parallel_tool_calls": `true`,
				"messages": `[{"role":"system","content":"You are terse."},{"role":"user","content":"Weather in Paris?"}]`,
				"tools":    `[{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`,
			},
			// Claude's thinking budget asks for medium, which Medium 3.5 lacks.
			check: func(t *testing.T, body gjson.Result) {
				assert.Equal(t, "high", body.Get("reasoning_effort").Str)
			},
			wantDiagnostics: []string{"reasoning_effort"},
			wantOutput:      []string{`"thinking":"Check the weather."`, `"text":"Let me look."`, `"type":"tool_use"`, `"id":"Ab3dE6gH9"`, `"stop_reason":"tool_use"`},
			wantUsage:       &dto.Usage{PromptTokens: 12, CompletionTokens: 20, TotalTokens: 32, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 8}},
		},
		{
			name:     "Responses request from Codex",
			endpoint: "/v1/responses",
			body: `{"model":"mistral-large-4-0","stream":true,"instructions":"Be helpful.","store":false,"prompt_cache_key":"thread-1",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Weather?"}]}],
				"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{}}},
					{"type":"custom","name":"apply_patch","description":"Patch files"}],
				"reasoning":{"effort":"medium"},"include":["reasoning.encrypted_content"]}`,
			reply:         mistralStream,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "text/event-stream",
			wantUserAgent: UserAgent,
			wantBody: map[string]string{
				"reasoning_effort": `"medium"`, "prompt_cache_key": `"thread-1"`, "stream": `true`,
				"messages": `[{"role":"system","content":"Be helpful."},{"role":"user","content":"Weather?"}]`,
				"tools.0":  `{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{}}}}`,
				// Codex's freeform tool goes out as a function of one input string.
				"tools.1.function.name":                `"apply_patch"`,
				"tools.1.function.parameters.required": `["input"]`,
			},
			wantAbsent: []string{"store", "include"},
			wantOutput: []string{`"call_id":"Ab3dE6gH9"`, `"delta":"Let me look."`, "response.completed"},
			wantUsage:  &dto.Usage{PromptTokens: 12, CompletionTokens: 20, TotalTokens: 32, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 8}},
		},
		{
			name:          "an answer Mistral aborts fails the Responses stream",
			endpoint:      "/v1/responses",
			body:          `{"model":"mistral-large-4-0","stream":true,"input":"hi"}`,
			reply:         mistralAbortedStream,
			wantPath:      "/v1/chat/completions",
			wantAccept:    "text/event-stream",
			wantUserAgent: UserAgent,
			wantOutput:    []string{"response.failed", `"delta":"Partial"`},
			wantNotOutput: []string{"response.completed"},
			wantUsage:     &dto.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
		},
		{
			name:          "embeddings",
			endpoint:      "/v1/embeddings",
			body:          `{"model":"codestral-embed","input":["a","b"],"dimensions":256,"encoding_format":"float","user":"u1"}`,
			reply:         `{"id":"e1","object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0},{"object":"embedding","embedding":[0.2],"index":1}],"model":"codestral-embed","usage":{"prompt_tokens":6,"total_tokens":6,"completion_tokens":0}}`,
			wantPath:      "/v1/embeddings",
			wantAccept:    "application/json",
			wantUserAgent: UserAgent,
			wantBody:      map[string]string{"": `{"model":"codestral-embed","input":["a","b"],"output_dimension":256,"encoding_format":"float"}`},
			wantOutput:    []string{`"embedding":[0.1]`},
			wantUsage:     &dto.Usage{PromptTokens: 6, TotalTokens: 6},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relayToMistral(t, tt.endpoint, tt.body, tt.clientHeader, tt.other, tt.headerOverride, tt.reply)
			require.NoError(t, result.convertErr)
			require.Len(t, result.calls, 1)
			call := result.calls[0]
			assert.Equal(t, tt.wantPath, call.path)
			assert.Equal(t, "Bearer mistral-key", call.header.Get("Authorization"))
			assert.Equal(t, "application/json", call.header.Get("Content-Type"))
			assert.Equal(t, tt.wantAccept, call.header.Get("Accept"))
			assert.Equal(t, tt.wantUserAgent, call.header.Get("User-Agent"))
			if affinity := call.header.Get("X-Affinity"); tt.endpoint == "/v1/embeddings" {
				assert.Empty(t, affinity)
			} else {
				_, err := uuid.Parse(affinity)
				assert.NoError(t, err, "the affinity is a UUID: %q", affinity)
				assert.NotEqual(t, tt.clientHeader["x-affinity"], affinity, "a client's affinity is put under the user")
			}

			body := gjson.ParseBytes(call.body)
			for path, want := range tt.wantBody {
				got := body
				if path != "" {
					got = body.Get(path)
				}
				require.True(t, got.Exists(), "%s missing from %s", path, call.body)
				assert.JSONEq(t, want, got.Raw, path)
			}
			for _, path := range tt.wantAbsent {
				assert.False(t, body.Get(path).Exists(), "%s sent in %s", path, call.body)
			}
			if tt.check != nil {
				tt.check(t, body)
			}

			for _, want := range tt.wantOutput {
				assert.Contains(t, result.output, want)
			}
			for _, unwanted := range tt.wantNotOutput {
				assert.NotContains(t, result.output, unwanted)
			}
			// An aborted answer is charged for the output the client got, as
			// any stream the upstream cut short.
			require.Nil(t, result.apiErr)
			require.NotNil(t, result.usage)
			assert.Equal(t, tt.wantUsage.PromptTokens, result.usage.PromptTokens)
			assert.Equal(t, tt.wantUsage.CompletionTokens, result.usage.CompletionTokens)
			assert.Equal(t, tt.wantUsage.TotalTokens, result.usage.TotalTokens)
			// A Responses client's usage carries the cached tokens under
			// input_tokens_details.
			cached := result.usage.PromptTokensDetails.CachedTokens
			if result.usage.InputTokensDetails != nil {
				cached = max(cached, result.usage.InputTokensDetails.CachedTokens)
			}
			assert.Equal(t, tt.wantUsage.PromptTokensDetails.CachedTokens, cached)

			var diagnosed []string
			for _, diagnostic := range result.info.ConversionDiagnostics() {
				if diagnostic.To == types.RelayFormat(ChannelName) {
					diagnosed = append(diagnosed, diagnostic.Path)
				}
			}
			assert.ElementsMatch(t, tt.wantDiagnostics, diagnosed)
		})
	}
}

// TestMistralWireMatchesVibeSDK relays the conversations of testdata/sdk_wire
// and compares what reaches Mistral with what the SDK Mistral Vibe 3.1.4 runs
// wrote for the same conversation (capture.py there records it): the body
// byte for byte, and every header but Host, Content-Length and the key, with
// the affinity a UUID of the gateway's own.
func TestMistralWireMatchesVibeSDK(t *testing.T) {
	files, err := filepath.Glob("testdata/sdk_wire/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)
			client, sdk := gjson.GetBytes(data, "client"), gjson.GetBytes(data, "sdk")
			reply := `{"id":"c9","object":"chat.completion","created":1764000000,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
			switch {
			case sdk.Get("path").Str == "/v1/embeddings":
				reply = `{"id":"e1","object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"m","usage":{"prompt_tokens":1,"total_tokens":1}}`
			case client.Get("stream").Bool():
				reply = mistralStream
			}

			result := relayToMistral(t, sdk.Get("path").Str, client.Raw, nil, dto.ChannelOtherSettings{}, nil, reply)
			require.NoError(t, result.convertErr)
			require.Len(t, result.calls, 1)
			call := result.calls[0]
			assert.Equal(t, sdk.Get("path").Str, call.path)
			assert.Equal(t, sdk.Get("body").Str, string(call.body))

			sdkHeaders := []string{"Authorization", "Content-Length"}
			for _, header := range sdk.Get("headers").Array() {
				name, value := header.Get("0").Str, header.Get("1").Str
				sdkHeaders = append(sdkHeaders, http.CanonicalHeaderKey(name))
				if strings.EqualFold(name, affinityHeader) {
					_, err := uuid.Parse(call.header.Get(name))
					assert.NoError(t, err, "the affinity is a UUID")
					continue
				}
				assert.Equal(t, value, call.header.Get(name), name)
			}
			assert.ElementsMatch(t, sdkHeaders, slices.Collect(maps.Keys(call.header)))
			assert.Equal(t, "Bearer mistral-key", call.header.Get("Authorization"))
		})
	}
}

// TestPythonJSON covers what the fixtures do not: each want is what Python's
// json.dumps(json.loads(input), separators=(",", ":")) writes.
func TestPythonJSON(t *testing.T) {
	tests := []struct{ input, want string }{
		{`{"a": 1e16, "b": 1e-5, "c": 0.0001, "d": -0.0, "e": -0, "f": 1E+2, "g": 123456789012345678901234567890, "h": 9999999999999998.0, "i": 2.5e-300, "j": 1.50}`,
			`{"a":1e+16,"b":1e-05,"c":0.0001,"d":-0.0,"e":0,"f":100.0,"g":123456789012345678901234567890,"h":9999999999999998.0,"i":2.5e-300,"j":1.5}`},
		{`["\u00e9 \ud83d\ude00 \/ \b\f\n\r\t \u0001 \u007f \u2028 \u003c&\u003e \ud800", "é😀"]`,
			`["\u00e9 \ud83d\ude00 / \b\f\n\r\t \u0001 \u007f \u2028 <&> \ud800","\u00e9\ud83d\ude00"]`},
		{`[ true , false , null , { "k" : [ ] } ]`, `[true,false,null,{"k":[]}]`},
		{`not json`, `not json`},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, string(pythonJSON([]byte(tt.input))), tt.input)
	}
}

// TestMistralDecodesCodedAnswers: the channel asks for gzip, deflate and br
// as httpx2 does, so it decodes them itself, deflate zlib-wrapped or raw.
func TestMistralDecodesCodedAnswers(t *testing.T) {
	const answer = `{"id":"c1","object":"chat.completion"}`
	code := func(coding string) []byte {
		var coded bytes.Buffer
		var writer io.WriteCloser
		switch coding {
		case "gzip":
			writer = gzip.NewWriter(&coded)
		case "deflate":
			writer = zlib.NewWriter(&coded)
		case "raw deflate":
			writer, _ = flate.NewWriter(&coded, flate.DefaultCompression)
		case "br":
			writer = brotli.NewWriter(&coded)
		}
		_, _ = io.WriteString(writer, answer)
		require.NoError(t, writer.Close())
		return coded.Bytes()
	}
	for _, coding := range []string{"gzip", "deflate", "raw deflate", "br"} {
		t.Run(coding, func(t *testing.T) {
			body := code(coding)
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
			resp.Header.Set("Content-Encoding", strings.TrimPrefix(coding, "raw "))
			resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
			decodeContent(resp)
			decoded, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, answer, string(decoded))
			assert.Empty(t, resp.Header.Get("Content-Encoding"))
			assert.Empty(t, resp.Header.Get("Content-Length"))
			assert.EqualValues(t, -1, resp.ContentLength)
		})
	}
}

// TestMistralPassThroughKeepsClientBytes: a body passed through goes out as
// the client wrote it, any other as the SDK would write it.
func TestMistralPassThroughKeepsClientBytes(t *testing.T) {
	const body = `{"model": "mistral-small-latest", "temperature": 1, "messages": [{"role": "user", "content": "hi"}]}`
	for _, passThrough := range []bool{false, true} {
		var sent []byte
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		}))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeMistral, ChannelBaseUrl: upstream.URL, ApiKey: "mistral-key",
			ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: passThrough},
		}}
		resp, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(body))
		require.NoError(t, err)
		require.NoError(t, resp.(*http.Response).Body.Close())
		upstream.Close()
		want := `{"model":"mistral-small-latest","temperature":1,"messages":[{"role":"user","content":"hi"}]}`
		if passThrough {
			want = body
		}
		assert.Equal(t, want, string(sent), "pass-through %v", passThrough)
	}
}

func TestMistralRefusesWhatItCannotServe(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		body     string
		other    dto.ChannelOtherSettings
		// wantLoss is a refusal under the channel's tool loss policy, which
		// the relay answers with 400 too.
		wantLoss    bool
		wantMessage string
	}{
		{name: "legacy completions", endpoint: "/v1/completions", body: `{"model":"codestral-latest","prompt":"def"}`,
			wantMessage: "Mistral does not support the endpoint /v1/completions"},
		{name: "a fractional seed", endpoint: "/v1/chat/completions", body: `{"model":"mistral-small-latest","seed":1.5,"messages":[{"role":"user","content":"hi"}]}`,
			wantMessage: "Mistral does not support the requested seed 1.5: it takes an integer"},
		{name: "token ids to embed", endpoint: "/v1/embeddings", body: `{"model":"mistral-embed","input":[[1,2,3]]}`,
			wantMessage: "Mistral does not support the requested input: it embeds a text or a list of texts"},
		{name: "a dropped option under the strict policy", endpoint: "/v1/chat/completions",
			body:  `{"model":"mistral-small-latest","logit_bias":{"1":2},"messages":[{"role":"user","content":"hi"}]}`,
			other: dto.ChannelOtherSettings{ToolLossPolicy: "strict"}, wantLoss: true, wantMessage: "logit_bias"},
		{name: "more choices than Mistral returns", endpoint: "/v1/chat/completions", body: `{"model":"mistral-small-latest","n":11,"messages":[{"role":"user","content":"hi"}]}`,
			wantMessage: "Mistral does not support the requested n 11: it returns 1 to 10 choices"},
		{name: "no choice", endpoint: "/v1/chat/completions", body: `{"model":"mistral-small-latest","n":0,"messages":[{"role":"user","content":"hi"}]}`,
			wantMessage: "Mistral does not support the requested n 0"},
		{name: "older images under the strict policy", endpoint: "/v1/chat/completions",
			body:  `{"model":"mistral-large-4-0","messages":[{"role":"user","content":[` + strings.TrimSuffix(strings.Repeat(`{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},`, 9), ",") + `]}]}`,
			other: dto.ChannelOtherSettings{ToolLossPolicy: "strict"}, wantLoss: true, wantMessage: "only the 8 most recent images are sent"},
		{name: "a hosted tool under the safe policy", endpoint: "/v1/responses",
			body:  `{"model":"mistral-large-4-0","input":"hi","tools":[{"type":"web_search_preview"}]}`,
			other: dto.ChannelOtherSettings{ToolLossPolicy: "safe"}, wantLoss: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relayToMistral(t, tt.endpoint, tt.body, nil, tt.other, nil, "")
			require.Error(t, result.convertErr)
			assert.Empty(t, result.calls, "nothing reaches Mistral")
			assert.Contains(t, result.convertErr.Error(), tt.wantMessage)
			if tt.wantLoss {
				var loss *types.ConversionLossError
				assert.True(t, errors.As(result.convertErr, &loss), "%v", result.convertErr)
				return
			}
			var apiErr *types.NewAPIError
			require.True(t, errors.As(result.convertErr, &apiErr), "%v", result.convertErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		})
	}

	_, err := (&Adaptor{}).ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
}

// mistralChoicesStream is two choices as Mistral streams them: each finishes
// in its own chunk, the usage rides on the last; finish is choice 0's.
func mistralChoicesStream(finish string) string {
	return `data: {"id":"c5","object":"chat.completion.chunk","created":1764000000,"model":"mistral-small-latest","choices":[{"index":1,"delta":{"role":"assistant","content":"Blue"},"finish_reason":null}]}

data: {"id":"c5","object":"chat.completion.chunk","created":1764000000,"model":"mistral-small-latest","choices":[{"index":1,"delta":{"content":""},"finish_reason":"stop"}]}

data: {"id":"c5","object":"chat.completion.chunk","created":1764000000,"model":"mistral-small-latest","choices":[{"index":0,"delta":{"role":"assistant","content":"Red"},"finish_reason":null}]}

data: {"id":"c5","object":"chat.completion.chunk","created":1764000000,"model":"mistral-small-latest","choices":[{"index":0,"delta":{"content":""},"finish_reason":"` + finish + `"}],"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}

data: [DONE]

`
}

func TestMistralStreamOfSeveralChoices(t *testing.T) {
	body := `{"model":"mistral-small-latest","stream":true,"n":2,"messages":[{"role":"user","content":"Name a color."}]}`

	t.Run("ends when every choice finished", func(t *testing.T) {
		result := relayToMistral(t, "/v1/chat/completions", body, nil, dto.ChannelOtherSettings{}, nil, mistralChoicesStream("stop"))
		require.NoError(t, result.convertErr)
		require.Nil(t, result.apiErr)
		assert.Contains(t, result.output, `"choices":[{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{},"finish_reason":"stop"}]`)
		assert.False(t, result.info.StreamStatus.EndedBeforeTerminal())
		assert.Equal(t, 11, result.usage.TotalTokens)
	})

	t.Run("is cut short when a choice aborts after another finished", func(t *testing.T) {
		result := relayToMistral(t, "/v1/chat/completions", body, nil, dto.ChannelOtherSettings{}, nil, mistralChoicesStream("error"))
		require.NoError(t, result.convertErr)
		assert.NotContains(t, result.output, `"finish_reason":"stop"`)
		assert.True(t, result.info.StreamStatus.EndedBeforeTerminal())
	})
}

func TestMistralReasoningEffort(t *testing.T) {
	const reply = `{"id":"c6","object":"chat.completion","created":1764000000,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	// The efforts Mistral accepted on 2026-11-24: Medium 3.5 and Small 4
	// high and none, Large 4 low, medium and high, Large 2512 and Codestral
	// none at all.
	tests := []struct {
		model, effort, want string
	}{
		{"mistral-vibe-cli-latest", "", "high"},
		{"mistral-vibe-cli-latest", "medium", "high"},
		{"mistral-vibe-cli-latest", "minimal", "high"},
		{"mistral-vibe-cli-latest", "none", "none"},
		{"mistral-large-4-0", "", "high"},
		{"mistral-large-4-0", "low", "low"},
		{"mistral-large-4-0", "none", "low"},
		{"mistral-large-4-0", "xhigh", "high"},
		{"mistral-small-latest", "", ""},
		{"mistral-small-latest", "low", "high"},
		{"mistral-large-2512", "high", ""},
		{"codestral-latest", "none", ""},
		{"mistral-future", "medium", "medium"},
		{"mistral-future", "none", ""},
	}
	for _, tt := range tests {
		t.Run(tt.model+" "+tt.effort, func(t *testing.T) {
			body := `{"model":"` + tt.model + `","messages":[{"role":"user","content":"hi"}]}`
			if tt.effort != "" {
				body = `{"model":"` + tt.model + `","reasoning_effort":"` + tt.effort + `","messages":[{"role":"user","content":"hi"}]}`
			}
			result := relayToMistral(t, "/v1/chat/completions", body, nil, dto.ChannelOtherSettings{}, nil, reply)
			require.NoError(t, result.convertErr)
			require.Len(t, result.calls, 1)
			assert.Equal(t, tt.want, gjson.GetBytes(result.calls[0].body, "reasoning_effort").Str)
		})
	}
}

func TestMistralToolCallsWithoutIDs(t *testing.T) {
	body := `{"model":"mistral-small-latest","messages":[
		{"role":"user","content":"Weather in Paris and Rome?"},
		{"role":"assistant","content":null,"tool_calls":[
			{"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},
			{"type":"function","function":{"name":"get_time","arguments":"{\"city\":\"Rome\"}"}}]},
		{"role":"tool","content":"Sunny"},
		{"role":"tool","content":"Noon"}]}`
	result := relayToMistral(t, "/v1/chat/completions", body, nil, dto.ChannelOtherSettings{}, nil,
		`{"id":"c7","object":"chat.completion","created":1764000000,"model":"mistral-small-latest","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	require.NoError(t, result.convertErr)
	require.Len(t, result.calls, 1)
	messages := gjson.GetBytes(result.calls[0].body, "messages")
	first, second := messages.Get("1.tool_calls.0.id").Str, messages.Get("1.tool_calls.1.id").Str
	assert.Len(t, first, 9)
	assert.Len(t, second, 9)
	assert.NotEqual(t, first, second)
	assert.Equal(t, first, messages.Get("2.tool_call_id").Str)
	assert.Equal(t, "get_weather", messages.Get("2.name").Str)
	assert.Equal(t, second, messages.Get("3.tool_call_id").Str)
	assert.Equal(t, "get_time", messages.Get("3.name").Str)

	again := relayToMistral(t, "/v1/chat/completions", body, nil, dto.ChannelOtherSettings{}, nil,
		`{"id":"c8","object":"chat.completion","created":1764000000,"model":"mistral-small-latest","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	assert.Equal(t, first, gjson.GetBytes(again.calls[0].body, "messages.1.tool_calls.0.id").Str, "a turn sent again keeps its ids")
}

func TestMistralAffinityIsPerUser(t *testing.T) {
	affinity := func(userID int) string {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request.Header.Set("x-affinity", "11111111-2222-3333-4444-555555555555")
		return affinityID(c, &relaycommon.RelayInfo{UserId: userID})
	}
	assert.Equal(t, affinity(1), affinity(1), "a conversation keeps its affinity")
	assert.NotEqual(t, affinity(1), affinity(2))
	assert.NotEqual(t, "11111111-2222-3333-4444-555555555555", affinity(1))
}

// TestMistralMembersStayOnMistral guards the other channels: Mistral's own
// request members reach Mistral only, never an OpenAI-compatible upstream.
func TestMistralMembersStayOnMistral(t *testing.T) {
	tests := []struct {
		endpoint, body string
		members        []string
	}{
		{"/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"random_seed":1,"safe_prompt":true,"prompt_mode":"reasoning","guardrails":[]}`,
			[]string{"random_seed", "safe_prompt", "prompt_mode", "guardrails"}},
		{"/v1/embeddings", `{"model":"text-embedding-3-small","input":"hi","output_dimension":256,"output_dtype":"int8"}`,
			[]string{"output_dimension", "output_dtype"}},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			c.Request = httptest.NewRequest(http.MethodPost, tt.endpoint, strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "http://127.0.0.1:9")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, gjson.Get(tt.body, "model").Str)
			format := types.RelayFormatOpenAI
			if tt.endpoint == "/v1/embeddings" {
				format = types.RelayFormatEmbedding
			}
			request, err := helper.GetAndValidateRequest(c, format)
			require.NoError(t, err)
			info, err := relaycommon.GenRelayInfo(c, format, request, nil)
			require.NoError(t, err)
			info.InitChannelMeta(c)

			adaptor := &openai.Adaptor{}
			adaptor.Init(info)
			var converted any
			switch request := request.(type) {
			case *dto.GeneralOpenAIRequest:
				converted, err = adaptor.ConvertOpenAIRequest(c, info, request)
			case *dto.EmbeddingRequest:
				converted, err = adaptor.ConvertEmbeddingRequest(c, info, *request)
			}
			require.NoError(t, err)
			sent, err := common.Marshal(converted)
			require.NoError(t, err)
			for _, member := range tt.members {
				assert.False(t, gjson.GetBytes(sent, member).Exists(), "%s sent to an OpenAI channel: %s", member, sent)
			}
		})
	}
}
