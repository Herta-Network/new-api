package claude

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestTreatsZeroMaxTokensAsUnset(t *testing.T) {
	zero := uint(0)
	req := &dto.ClaudeRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: &zero,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-sonnet-4-5",
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.MaxTokens)
	assert.Equal(t, uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(req.Model)), *converted.MaxTokens)
}

func TestConvertClaudeRequestPreservesNativeClaudeCodeThinking(t *testing.T) {
	budget := 10000
	maxTokens := uint(20000)
	temperature := 0.7
	topP := 0.9
	req := &dto.ClaudeRequest{
		Model:        "claude-opus-4-8",
		MaxTokens:    &maxTokens,
		Temperature:  &temperature,
		TopP:         &topP,
		Thinking:     &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		OutputConfig: []byte(`{"effort":"high"}`),
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: req.Model,
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.Thinking)
	assert.Equal(t, "enabled", converted.Thinking.Type)
	require.NotNil(t, converted.Thinking.BudgetTokens)
	assert.Equal(t, budget, *converted.Thinking.BudgetTokens)
	assert.Equal(t, temperature, *converted.Temperature)
	assert.Equal(t, topP, *converted.TopP)
	assert.JSONEq(t, `{"effort":"high"}`, string(converted.OutputConfig))
	assert.Empty(t, info.ConversionDiagnostics())
}

func TestConvertClaudeRequestPreservesMessageOutputConfig(t *testing.T) {
	body := `{"model":"claude-opus-5-5","max_tokens":64,"output_config":{"effort":"medium"},"messages":[` +
		`{"role":"user","content":"summary"},` +
		`{"role":"system","content":[],"output_config":{"effort":"high"}},` +
		`{"role":"assistant","content":"done"}]}`
	var req dto.ClaudeRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &req))
	info := &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: req.Model,
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, &req)
	require.NoError(t, err)
	encoded, err := common.Marshal(out)
	require.NoError(t, err)

	var upstream struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	require.Len(t, upstream.Messages, 3)
	assert.Equal(t, map[string]any{"effort": "high"}, upstream.Messages[1]["output_config"])
	assert.NotContains(t, upstream.Messages[0], "output_config")
	assert.NotContains(t, upstream.Messages[2], "output_config")
}

func TestConvertClaudeRequestZeroMaxTokensStillRaisesThinkingBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	zero := uint(0)
	original := &dto.ClaudeRequest{
		Model:     "claude-3-7-sonnet-thinking",
		MaxTokens: &zero,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet-thinking",
		Request:         original,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet-thinking",
		},
	}
	outbound, err := common.DeepCopy(original)
	require.NoError(t, err)
	require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
	err = helper.ApplyReasoningModelSuffix(nil, info, outbound)
	require.NoError(t, err)

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, outbound)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "claude-3-7-sonnet", converted.Model)
	require.NotNil(t, converted.Thinking)
	require.NotNil(t, converted.MaxTokens)
	assert.Greater(t, *converted.MaxTokens, uint(1024))
}

func TestConvertClaudeRequestDoesNotOverwriteTrimmedUpstreamModelName(t *testing.T) {
	req := &dto.ClaudeRequest{
		Model: "claude-3-7-sonnet-thinking",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet",
		},
	}

	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	assert.Equal(t, "claude-3-7-sonnet", info.UpstreamModelName)
}

func TestConvertClaudeRequestForwardsThinkingBlockBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		model        string
		thinking     string
		wantThinking string
	}{
		{
			name:         "native request keeps every thinking field",
			model:        "claude-opus-5-5",
			thinking:     `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"},"future_field":{"a":1}}`,
			wantThinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"},"future_field":{"a":1}}`,
		},
		{
			name:         "effort suffix re-renders adaptive and keeps block_binding",
			model:        "claude-opus-5-5-high",
			thinking:     `{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
			wantThinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
		},
		{
			name:         "suffix rendering between_tools does not carry block_binding",
			model:        "claude-sonnet-5-5-none",
			thinking:     `{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
			wantThinking: `{"type":"between_tools"}`,
		},
		{
			name:         "request without block_binding gets none",
			model:        "claude-opus-5-5-high",
			thinking:     `{"type":"adaptive"}`,
			wantThinking: `{"type":"adaptive","display":"summarized"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"` + tt.model + `","max_tokens":1024,"thinking":` + tt.thinking +
				`,"messages":[{"role":"user","content":"hi"}]}`
			var original dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &original))
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				Request:         &original,
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: tt.model},
			}
			// Same steps as the Claude handler before the adaptor converts.
			outbound, err := common.DeepCopy(&original)
			require.NoError(t, err)
			require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
			require.NoError(t, helper.ApplyReasoningModelSuffix(nil, info, outbound))

			out, err := (&Adaptor{}).ConvertClaudeRequest(c, info, outbound)
			require.NoError(t, err)
			encoded, err := common.Marshal(out)
			require.NoError(t, err)
			var upstream struct {
				Thinking json.RawMessage `json:"thinking"`
			}
			require.NoError(t, common.Unmarshal(encoded, &upstream))
			assert.JSONEq(t, tt.wantThinking, string(upstream.Thinking))
		})
	}
}

func TestSetupRequestHeaderForwardsEveryAnthropicBetaValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Add("anthropic-beta", "fast-mode-2026-02-01")
	c.Request.Header.Add("anthropic-beta", "thinking-binding-controls-2026-08-01")
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-5-5",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
	}

	header := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))
	assert.Equal(t, []string{"fast-mode-2026-02-01,thinking-binding-controls-2026-08-01"}, header.Values("anthropic-beta"))
}

func geminiToClaudeInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet",
		},
	}
}

func TestConvertGeminiRequestMapsSystemInstructionToolsAndMultimodal(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{Text: "What is in this image?"},
					{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "aGVsbG8="}},
				},
			},
		},
		SystemInstructions: &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: "You are a helpful assistant."}},
		},
	}
	req.SetTools([]dto.GeminiChatTool{
		{
			FunctionDeclarations: []dto.FunctionRequest{
				{
					Name:        "lookup",
					Description: "Lookup data",
					Parameters: map[string]any{
						"type":       "object",
						"properties": map[string]any{"q": map[string]any{"type": "string"}},
					},
				},
			},
		},
	})

	out, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)

	system := converted.ParseSystem()
	require.NotEmpty(t, system)
	assert.Contains(t, system[0].GetText(), "You are a helpful assistant.")
	require.NotEmpty(t, converted.Messages)
	assert.Equal(t, "user", converted.Messages[0].Role)

	blocks, parseErr := converted.Messages[0].ParseContent()
	require.NoError(t, parseErr)
	var foundImage bool
	for _, block := range blocks {
		if block.Type == "image" || (block.Source != nil && block.Source.Type == "base64") {
			foundImage = true
			break
		}
	}
	assert.True(t, foundImage)

	require.NotNil(t, converted.Tools)
	tools, err := common.Marshal(converted.Tools)
	require.NoError(t, err)
	assert.Contains(t, string(tools), `"lookup"`)
	require.NotNil(t, converted.MaxTokens)
	assert.Greater(t, *converted.MaxTokens, uint(0))
}

func TestConvertGeminiRequestThinkingConfigUsesReasoningIntent(t *testing.T) {
	budget := 1024
	maxTokens := uint(4096)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "think"}}},
		},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			MaxOutputTokens: &maxTokens,
			ThinkingConfig:  &dto.GeminiThinkingConfig{ThinkingBudget: &budget},
		},
	}

	out, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.Thinking)
	assert.Equal(t, "enabled", converted.Thinking.Type)
	require.NotNil(t, converted.Thinking.BudgetTokens)
	assert.Equal(t, 1024, *converted.Thinking.BudgetTokens)
}

func TestConvertGeminiRequestNilRequest(t *testing.T) {
	_, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), nil)
	require.Error(t, err)
}

const thinkingSignatureFailure = `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`

const signedThinkingRequest = `{"model":"claude-sonnet-4-6","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"invalid"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":9007199254740993,"thinking":"keep","signature":"keep"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]}]}`

// The repaired assistant message is re-encoded with sorted keys; every other
// value keeps its bytes, including the integer beyond float64 precision.
const rectifiedThinkingRequest = `{"max_tokens":4096,"messages":[{"role":"user","content":"hello"},{"content":[{"type":"text","text":"reasoning"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":9007199254740993,"thinking":"keep","signature":"keep"}}],"role":"assistant"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]}],"model":"claude-sonnet-4-6"}`

func TestIsThinkingSignatureError(t *testing.T) {
	for _, tt := range []struct {
		body string
		want bool
	}{
		{thinkingSignatureFailure, true},
		{"{\"error\":{\"message\":\"Invalid `signature` in `thinking` block\"}}", true},
		{`{"message":"Invalid signature in thinking block"}`, true},
		{`{"error":{"message":"messages.1.content.0.signature: Field required"}}`, true},
		{"{\"error\":{\"message\":\"Expected `thinking` or `redacted_thinking`, but found `text`\"}}", true},
		{`{"error":{"message":"thinking or redacted_thinking blocks cannot be modified"}}`, true},
		{`{"error":{"message":"each thinking block must contain thinking"}}`, true},
		{`{"error":{"message":"Invalid AWS request signature"}}`, false},
		{`{"error":{"message":"invalid request signature"}}`, false},
		{`{"error":{"message":"max_tokens: expected a value greater than thinking.budget_tokens"}}`, false},
		{`{"error":{"message":"The content[].thinking must be passed back to the API"}}`, false},
		{`{"error":{"message":"all messages must have non-empty content"}}`, false},
		{`{"error":{"message":"other"},"echo":"thinking signature"}`, false},
		{`invalid thinking signature`, false},
	} {
		t.Run(tt.body, func(t *testing.T) {
			assert.Equal(t, tt.want, isThinkingSignatureError([]byte(tt.body)))
		})
	}
}

func TestSupportsThinkingSignatureRepair(t *testing.T) {
	for _, tt := range []struct {
		name             string
		origin, upstream string
		input            types.RelayFormat
		chain            []types.RelayFormat
		passThrough      bool
		want             bool
	}{
		{name: "Claude model", origin: "claude-sonnet-4-6", upstream: "claude-sonnet-4-6", want: true},
		{name: "alias mapped to Claude", origin: "my-alias", upstream: "Anthropic/Claude-Opus-4-6", want: true},
		{name: "Claude family name", origin: "haiku-4-5", upstream: "haiku-4-5", want: true},
		{name: "Claude mapped to DeepSeek", origin: "claude-sonnet-4-6", upstream: "deepseek-v4-pro"},
		{name: "Kimi", origin: "kimi-k2.5", upstream: "kimi-k2.5"},
		{name: "unknown model", origin: "my-model", upstream: "my-model"},
		{name: "converted to Chat Completions", origin: "claude-sonnet-4-6", upstream: "claude-sonnet-4-6", chain: []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI}},
		{name: "Chat Completions client", origin: "claude-sonnet-4-6", upstream: "claude-sonnet-4-6", input: types.RelayFormatOpenAI, chain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude}},
		{name: "pass-through body", origin: "claude-sonnet-4-6", upstream: "claude-sonnet-4-6", passThrough: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			if input == "" {
				input = types.RelayFormatClaude
			}
			info := &relaycommon.RelayInfo{
				RelayFormat:            input,
				OriginModelName:        tt.origin,
				RequestConversionChain: tt.chain,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.upstream,
					ChannelSetting:    dto.ChannelSettings{PassThroughBodyEnabled: tt.passThrough},
				},
			}
			assert.Equal(t, tt.want, supportsThinkingSignatureRepair(info))
		})
	}
}

func TestRectifyThinkingSignature(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
	}{
		{name: "signed history with tool use", body: signedThinkingRequest, want: rectifiedThinkingRequest},
		{
			name: "redacted thinking and its context edit",
			body: `{"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"encrypted"}]}]}`,
			want: `{"messages":[{"content":[{"type":"text","text":"(thinking omitted)"}],"role":"assistant"}]}`,
		},
		{
			name: "blank thinking and an unrelated context edit",
			body: `{"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}},{"type":"clear_tool_uses_20250919"}]},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":" ","signature":"x"},{"type":"text","text":"answer"}]}],"thinking":{"type":"adaptive"}}`,
			want: `{"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]},"messages":[{"content":[{"type":"text","text":"answer"}],"role":"assistant"}]}`,
		},
		{
			name: "thinking enabled without thinking history",
			body: `{"model":"m","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"plain"}]}`,
			want: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"plain"}],"model":"m"}`,
		},
		{name: "already repaired", body: rectifiedThinkingRequest},
		{name: "null", body: `null`},
		{name: "array", body: `[]`},
		{name: "invalid JSON", body: `{`},
		{name: "no messages", body: `{"thinking":{},"messages":[]}`},
		{name: "user thinking block", body: `{"messages":[{"role":"user","content":[{"type":"thinking","signature":"keep"}]}]}`},
		{name: "assistant text only", body: `{"messages":[{"role":"assistant","content":[{"type":"text","text":"hi"}]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repaired, changed := rectifyThinkingSignature([]byte(tt.body))
			if tt.want == "" {
				assert.False(t, changed)
				assert.Nil(t, repaired)
				return
			}
			require.True(t, changed)
			assert.Equal(t, tt.want, string(repaired))
		})
	}
}

func TestDoRequestWithThinkingSignatureRepair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type upstreamResponse struct {
		status int
		body   string
	}
	const success = `{"type":"message","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`
	signatureRejected := upstreamResponse{http.StatusBadRequest, thinkingSignatureFailure}
	oversized := `{"error":{"message":"Invalid thinking signature ` + strings.Repeat("x", thinkingSignatureErrorLimit) + `"}}`
	for _, tt := range []struct {
		name string
		// The upstream answers in order and repeats its last response.
		model      string
		responses  []upstreamResponse
		wantSent   []string
		wantStatus int
		wantBody   string
	}{
		{
			name: "repairs once on the same channel", model: "claude-sonnet-4-6",
			responses: []upstreamResponse{signatureRejected, {http.StatusOK, success}},
			wantSent:  []string{signedThinkingRequest, rectifiedThinkingRequest}, wantStatus: http.StatusOK, wantBody: success,
		},
		{
			name: "does not repair twice", model: "claude-sonnet-4-6",
			responses: []upstreamResponse{signatureRejected},
			wantSent:  []string{signedThinkingRequest, rectifiedThinkingRequest}, wantStatus: http.StatusBadRequest, wantBody: thinkingSignatureFailure,
		},
		{
			name: "mapped DeepSeek model", model: "deepseek-v4-pro",
			responses: []upstreamResponse{signatureRejected},
			wantSent:  []string{signedThinkingRequest}, wantStatus: http.StatusBadRequest, wantBody: thinkingSignatureFailure,
		},
		{
			name: "authentication signature", model: "claude-sonnet-4-6",
			responses: []upstreamResponse{{http.StatusBadRequest, `{"error":{"message":"invalid request signature"}}`}},
			wantSent:  []string{signedThinkingRequest}, wantStatus: http.StatusBadRequest, wantBody: `{"error":{"message":"invalid request signature"}}`,
		},
		{
			name: "other status", model: "claude-sonnet-4-6",
			responses: []upstreamResponse{{http.StatusInternalServerError, thinkingSignatureFailure}},
			wantSent:  []string{signedThinkingRequest}, wantStatus: http.StatusInternalServerError, wantBody: thinkingSignatureFailure,
		},
		{
			name: "oversized error is kept whole", model: "claude-sonnet-4-6",
			responses: []upstreamResponse{{http.StatusBadRequest, oversized}},
			wantSent:  []string{signedThinkingRequest}, wantStatus: http.StatusBadRequest, wantBody: oversized,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				sent = append(sent, string(body))
				response := tt.responses[min(len(sent), len(tt.responses))-1]
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer server.Close()

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{
				RelayFormat:     types.RelayFormatClaude,
				OriginModelName: "claude-sonnet-4-6",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "sk-test", UpstreamModelName: tt.model},
			}
			body, closer, err := relaycommon.NewOutboundJSONBody([]byte(signedThinkingRequest))
			require.NoError(t, err)
			defer closer.Close()

			resp, err := DoRequestWithThinkingSignatureRepair(&Adaptor{}, c, info, body)
			require.NoError(t, err)
			httpResp, ok := resp.(*http.Response)
			require.True(t, ok)
			defer httpResp.Body.Close()
			received, err := io.ReadAll(httpResp.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, httpResp.StatusCode)
			assert.Equal(t, tt.wantBody, string(received))
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.wantSent, sent)
		})
	}
}
