package relay

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareResponsesRequestRetainsConvertedAdaptor(t *testing.T) {
	type capturedRequest struct {
		path string
		body []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- capturedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat,
		}}},
	})
	request := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Input: []byte(`"hello"`)}
	info := relaycommon.GenRelayInfoResponses(c, request)
	adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	defer closer.Close()
	response, err := adaptor.DoRequest(c, info, body)
	require.NoError(t, err)
	httpResponse, ok := response.(*http.Response)
	require.True(t, ok)
	usage, apiErr := adaptor.DoResponse(c, httpResponse, info)
	require.Nil(t, apiErr)
	require.IsType(t, &dto.Usage{}, usage)
	assert.Equal(t, 5, usage.(*dto.Usage).TotalTokens)

	upstream := <-captured
	assert.Equal(t, "/v1/chat/completions", upstream.path)
	var upstreamRequest dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(upstream.body, &upstreamRequest))
	require.Len(t, upstreamRequest.Messages, 1)
	assert.Equal(t, "hello", upstreamRequest.Messages[0].StringContent())
	assert.Equal(t, []relaytypes.RelayFormat{relaytypes.RelayFormatOpenAIResponses, relaytypes.RelayFormatOpenAI}, info.RequestConversionChain)
}

// A converted stream ends the client's response as finished only after the
// upstream's own terminal event (message_stop, a finish_reason, Gemini's
// finishReason, a Responses terminal event). When the upstream stops first the
// client is told the response failed, never a synthesized response.completed
// or message_stop, and the output it already received is still billed.
func TestConvertedStreamsReportUpstreamEndingEarly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	frames := func(events ...string) string {
		return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
	}
	claudeStart := frames(
		`{"type":"message_start","message":{"id":"msg_1","type":"message","model":"m","role":"assistant","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello there"}}`,
	)
	claudeEnd := frames(
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		`{"type":"message_stop"}`,
	)
	chatStart := frames(`{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hello there"},"finish_reason":null}]}`)
	chatEnd := frames(`{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	geminiStart := frames(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello there"}]}}]}`)
	geminiEnd := frames(`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}`)
	responsesStart := frames(`{"type":"response.created","response":{"id":"resp_1","model":"m"}}`, `{"type":"response.output_text.delta","delta":"hello there"}`)
	responsesToolStart := frames(
		`{"type":"response.created","response":{"id":"resp_1","model":"m"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"hello there\"}"}`,
	)
	responsesEnd := frames(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`)
	claudeToResponses := func(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *relaytypes.NewAPIError) {
		return claude.ClaudeResponsesStreamHandler(c, resp, info)
	}
	for _, tc := range []struct {
		name, start, end, finished, failed string
		format                             relaytypes.RelayFormat
		handler                            func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *relaytypes.NewAPIError)
	}{
		{"claude to responses", claudeStart, claudeEnd, "event: response.completed", "event: response.failed", relaytypes.RelayFormatOpenAIResponses, claudeToResponses},
		{"chat to responses", chatStart, chatEnd, "event: response.completed", "event: response.failed", relaytypes.RelayFormatOpenAIResponses, openaichannel.OaiChatToResponsesStreamHandler},
		{"gemini to responses", geminiStart, geminiEnd, "event: response.completed", "event: response.failed", relaytypes.RelayFormatOpenAIResponses, gemini.GeminiResponsesStreamHandler},
		{"gemini to claude", geminiStart, geminiEnd, "event: message_stop", "event: error", relaytypes.RelayFormatClaude, gemini.GeminiChatStreamHandler},
		{"responses to claude", responsesStart, responsesEnd, "event: message_stop", "event: error", relaytypes.RelayFormatClaude, openaichannel.OaiResponsesToChatStreamHandler},
		{"responses tool call to chat", responsesToolStart, responsesEnd, `"finish_reason":"tool_calls"`, `data: {"error"`, relaytypes.RelayFormatOpenAI, openaichannel.OaiResponsesToChatStreamHandler},
	} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s truncated=%t", tc.name, truncated), func(t *testing.T) {
				body := tc.start
				if !truncated {
					body += tc.end
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				info := &relaycommon.RelayInfo{
					RelayFormat: tc.format, RelayMode: relayconstant.RelayModeChatCompletions,
					IsStream: true, DisablePing: true, StartTime: time.Now(),
					ChannelMeta:       &relaycommon.ChannelMeta{UpstreamModelName: "m"},
					ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
				}
				info.SetEstimatePromptTokens(7)

				usage, apiErr := tc.handler(c, info, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})

				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 7, usage.PromptTokens)
				assert.Positive(t, usage.CompletionTokens)
				got := recorder.Body.String()
				assert.Contains(t, got, "hello there")
				// The error ends the stream the client is already reading.
				for line := range strings.SplitSeq(got, "\n") {
					if line != "" && !strings.HasPrefix(line, "event: ") && !strings.HasPrefix(line, "data: ") && !strings.HasPrefix(line, ":") {
						assert.Failf(t, "line is not an SSE field", "%q in %s", line, got)
					}
				}
				if truncated {
					assert.Contains(t, got, tc.failed)
					assert.NotContains(t, got, tc.finished)
					assert.True(t, info.StreamStatus.HasErrors())
					return
				}
				assert.Contains(t, got, tc.finished)
				assert.NotContains(t, got, tc.failed)
				assert.False(t, info.StreamStatus.HasErrors())
			})
		}
	}
}

// A prompt Gemini blocks streams promptFeedback alone, which is the stream's
// terminal: converted streams end it as content filtered, not as an upstream
// that stopped early.
func TestGeminiBlockedPromptStreamEndsAsContentFiltered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	body := "data: " + `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":7,"totalTokenCount":7}}` + "\n\n"
	for _, tc := range []struct {
		name    string
		format  relaytypes.RelayFormat
		handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *relaytypes.NewAPIError)
		want    []string
	}{
		{"gemini to responses", relaytypes.RelayFormatOpenAIResponses, gemini.GeminiResponsesStreamHandler, []string{"event: response.incomplete", `"reason":"content_filter"`}},
		{"gemini to claude", relaytypes.RelayFormatClaude, gemini.GeminiChatStreamHandler, []string{"event: message_stop"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{
				RelayFormat: tc.format, RelayMode: relayconstant.RelayModeChatCompletions,
				IsStream: true, DisablePing: true, StartTime: time.Now(),
				ChannelMeta:       &relaycommon.ChannelMeta{UpstreamModelName: "m"},
				ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
			}

			_, apiErr := tc.handler(c, info, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})

			require.Nil(t, apiErr)
			got := recorder.Body.String()
			for _, want := range tc.want {
				assert.Contains(t, got, want)
			}
			assert.NotContains(t, got, "event: error")
			assert.NotContains(t, got, "event: response.failed")
			assert.False(t, info.StreamStatus.HasErrors())
		})
	}
}

func TestIsResponsesEventStreamContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "plain", contentType: "text/event-stream", want: true},
		{name: "mixed case with charset", contentType: "Text/Event-Stream; charset=utf-8", want: true},
		{name: "json", contentType: "application/json", want: false},
		{name: "empty", contentType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isResponsesEventStreamContentType(tt.contentType))
		})
	}
}

func TestRecalcQuotaFromRatiosIgnoresInvalidMultipliers(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: hosttypes.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"duration": 3,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.True(t, ok)
	assert.Equal(t, 150, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}

func TestRecalcQuotaFromRatiosRejectsAllInvalidAdjustedRatios(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: hosttypes.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.False(t, ok)
	assert.Equal(t, 0, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}

func TestTextRequestViaResponsesConvertsClaudeDirectly(t *testing.T) {
	type capturedRequest struct {
		path string
		body []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- capturedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_1",
			"object":"response",
			"status":"completed",
			"model":"gpt-5.6-sol",
			"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		RelayMode:              relayconstant.RelayModeChatCompletions,
		RelayFormat:            relaytypes.RelayFormatClaude,
		OriginModelName:        "gpt-5.6-sol",
		RequestConversionChain: []relaytypes.RelayFormat{relaytypes.RelayFormatClaude},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    server.URL,
			ApiKey:            "test-key",
			UpstreamModelName: "gpt-5.6-sol",
		},
	}
	adaptor := &openaichannel.Adaptor{}
	adaptor.Init(info)
	request := &dto.ClaudeRequest{
		Model:    "gpt-5.6-sol",
		Thinking: &dto.Thinking{Type: "adaptive", Display: "summarized"},
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	usage, apiErr := textRequestViaResponses(c, info, adaptor, request)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, []relaytypes.RelayFormat{relaytypes.RelayFormatClaude, relaytypes.RelayFormatOpenAIResponses}, info.RequestConversionChain)

	upstream := <-captured
	assert.Equal(t, "/v1/responses", upstream.path)
	var upstreamBody map[string]any
	require.NoError(t, common.Unmarshal(upstream.body, &upstreamBody))
	assert.NotContains(t, upstreamBody, "messages")
	reasoning, ok := upstreamBody["reasoning"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "high", reasoning["effort"])
	assert.Equal(t, "detailed", reasoning["summary"])

	var response dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Content, 1)
	assert.Equal(t, "ok", response.Content[0].GetText())
}

// When a Responses upstream rejects the reasoning restored from a Messages
// client's history, the turn is retried once without it, as it was sent
// before reasoning was carried.
func TestTextRequestViaResponsesRetriesWithoutRestoredReasoning(t *testing.T) {
	var upstreamBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		upstreamBodies = append(upstreamBodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(upstreamBodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Item 'rs_1' of type 'reasoning' was provided without its required following item.","type":"invalid_request_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_2","object":"response","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		RelayMode:              relayconstant.RelayModeChatCompletions,
		RelayFormat:            relaytypes.RelayFormatClaude,
		OriginModelName:        "gpt-5.6-sol",
		UserId:                 5,
		RequestConversionChain: []relaytypes.RelayFormat{relaytypes.RelayFormatClaude},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         9,
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    server.URL,
			ApiKey:            "test-key",
			UpstreamModelName: "gpt-5.6-sol",
		},
	}

	// The previous turn's thinking block, as the client received it.
	var previous dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.6-sol","output":[
		{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Need the file list."}],"encrypted_content":"openai-encrypted-E"},
		{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{}","status":"completed"}
	]}`), &previous))
	converted, err := relayconvert.ConvertResponse(c, info, relaytypes.RelayFormatClaude, &previous)
	require.NoError(t, err)
	previousTurn := converted.Value.(*dto.ClaudeResponse).Content
	require.Equal(t, "thinking", previousTurn[0].Type)
	require.NotEmpty(t, previousTurn[0].Signature)

	adaptor := &openaichannel.Adaptor{}
	adaptor.Init(info)
	request := &dto.ClaudeRequest{
		Model:    "gpt-5.6-sol",
		Thinking: &dto.Thinking{Type: "adaptive", Display: "summarized"},
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "list files"},
			{Role: "assistant", Content: previousTurn},
			{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "tool_result", ToolUseId: "call_1", Content: "a.txt"}}},
		},
	}

	usage, apiErr := textRequestViaResponses(c, info, adaptor, request)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	require.Len(t, upstreamBodies, 2)
	first, retried := ojson.GetBytes(upstreamBodies[0], "input"), ojson.GetBytes(upstreamBodies[1], "input")
	assert.False(t, ojson.GetBytes(upstreamBodies[0], "store").Bool())
	assert.True(t, ojson.GetBytes(upstreamBodies[0], "store").Exists())
	assert.Equal(t, "reasoning", first.Get("1.type").String())
	assert.Equal(t, "rs_1", first.Get("1.id").String())
	assert.Equal(t, "openai-encrypted-E", first.Get("1.encrypted_content").String())
	assert.Equal(t, first.Get("#").Int()-1, retried.Get("#").Int())
	assert.NotContains(t, retried.Raw, `"reasoning"`)
	assert.Equal(t, "function_call", retried.Get("1.type").String())

	var response dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "ok", response.Content[0].GetText())
}

func TestApplySystemPromptIfNeededSkipsToolLoadingMessages(t *testing.T) {
	tools := json.RawMessage(`[{"type":"function","function":{"name":"get_current_time","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`)
	toolLoading := dto.Message{Role: "system", Tools: tools}
	user := dto.Message{Role: "user", Content: "What time is it in Beijing?"}

	tests := []struct {
		name         string
		messages     []dto.Message
		wantMessages []dto.Message
		wantOverride bool
	}{
		{
			name:     "tool loading message alone is not a system prompt",
			messages: []dto.Message{toolLoading, user},
			wantMessages: []dto.Message{
				{Role: "system", Content: "Answer in English."},
				toolLoading,
				user,
			},
		},
		{
			name:     "override targets the real system prompt only",
			messages: []dto.Message{toolLoading, {Role: "system", Content: "You are Kimi."}, user},
			wantMessages: []dto.Message{
				toolLoading,
				{Role: "system", Content: "Answer in English.\nYou are Kimi."},
				user,
			},
			wantOverride: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelSetting: dto.ChannelSettings{
						SystemPrompt:         "Answer in English.",
						SystemPromptOverride: true,
					},
				},
			}
			request := &dto.GeneralOpenAIRequest{
				Model:    "kimi-k3",
				Messages: append([]dto.Message(nil), tt.messages...),
			}

			applySystemPromptIfNeeded(c, info, request)

			require.Len(t, request.Messages, len(tt.wantMessages))
			for i, want := range tt.wantMessages {
				got := request.Messages[i]
				assert.Equal(t, want.Role, got.Role, "message %d role", i)
				assert.Equal(t, want.Content, got.Content, "message %d content", i)
				if len(want.Tools) > 0 {
					assert.JSONEq(t, string(want.Tools), string(got.Tools), "message %d tools", i)
				} else {
					assert.Empty(t, got.Tools, "message %d tools", i)
				}
			}
			_, overrideSet := common.GetContextKey(c, constant.ContextKeySystemPromptOverride)
			assert.Equal(t, tt.wantOverride, overrideSet)
		})
	}
}

func TestChannelSystemPromptInUpstreamRequest(t *testing.T) {
	const (
		prompt      = "Answer in English."
		system      = `{"role":"system","content":"You are helpful."}`
		arraySystem = `{"role":"system","content":[{"type":"text","text":"You are helpful."}]}`
		codexKey    = `{"access_token":"fixture-token","account_id":"fixture-account"}`
	)
	settings := model_setting.GetGlobalSettings()
	previousPolicy := settings.ChatCompletionsToResponsesPolicy
	t.Cleanup(func() { settings.ChatCompletionsToResponsesPolicy = previousPolicy })

	tests := []struct {
		name        string
		channelType int
		model       string
		// endpoint is /v1/chat/completions when empty; body is the client
		// request, a chat request of system and a user message when empty.
		endpoint     string
		body         string
		system       string
		override     bool
		passThrough  bool
		viaResponses bool
		// path is where the upstream request carries the system prompt and
		// want its exact JSON there, absent when empty; wantContains lists
		// texts it must hold instead.
		path         string
		want         string
		wantContains []string
		wantOverride bool
	}{
		{name: "claude without system prompt", channelType: constant.ChannelTypeAnthropic, model: "claude-sonnet-4-5",
			path: "system", want: `[{"type":"text","text":"Answer in English."}]`},
		{name: "claude keeps its system prompt", channelType: constant.ChannelTypeAnthropic, model: "claude-sonnet-4-5", system: system,
			path: "system", want: `[{"type":"text","text":"You are helpful."}]`},
		{name: "claude override", channelType: constant.ChannelTypeAnthropic, model: "claude-sonnet-4-5", system: system, override: true,
			path: "system", want: `[{"type":"text","text":"Answer in English.\nYou are helpful."}]`, wantOverride: true},
		{name: "claude override of array content", channelType: constant.ChannelTypeAnthropic, model: "claude-sonnet-4-5", system: arraySystem, override: true,
			path: "system", want: `[{"type":"text","text":"Answer in English."},{"type":"text","text":"You are helpful."}]`, wantOverride: true},
		{name: "claude pass-through", channelType: constant.ChannelTypeAnthropic, model: "claude-sonnet-4-5", passThrough: true,
			path: "system"},
		{name: "gemini without system prompt", channelType: constant.ChannelTypeGemini, model: "gemini-2.5-flash",
			path: "systemInstruction.parts", want: `[{"text":"Answer in English."}]`},
		{name: "gemini keeps its system prompt", channelType: constant.ChannelTypeGemini, model: "gemini-2.5-flash", system: system,
			path: "systemInstruction.parts", want: `[{"text":"You are helpful."}]`},
		{name: "gemini override", channelType: constant.ChannelTypeGemini, model: "gemini-2.5-flash", system: system, override: true,
			path: "systemInstruction.parts", want: `[{"text":"Answer in English.\nYou are helpful."}]`, wantOverride: true},
		{name: "gemini override of array content", channelType: constant.ChannelTypeGemini, model: "gemini-2.5-flash", system: arraySystem, override: true,
			path: "systemInstruction.parts.0.text", wantContains: []string{prompt, "You are helpful."}, wantOverride: true},
		{name: "openai injects once", channelType: constant.ChannelTypeOpenAI, model: "gpt-4o",
			path: "messages", want: `[{"role":"system","content":"Answer in English."},{"role":"user","content":"hi"}]`},
		{name: "openai override applies once", channelType: constant.ChannelTypeOpenAI, model: "gpt-4o", system: system, override: true,
			path: "messages", want: `[{"role":"system","content":"Answer in English.\nYou are helpful."},{"role":"user","content":"hi"}]`, wantOverride: true},
		{name: "openai developer-role model keeps the client's system prompt", channelType: constant.ChannelTypeOpenAI, model: "gpt-5", system: system,
			path: "messages", want: `[{"role":"developer","content":"You are helpful."},{"role":"user","content":"hi"}]`},
		{name: "openai via responses override applies once", channelType: constant.ChannelTypeOpenAI, model: "gpt-4o", system: system, override: true, viaResponses: true,
			path: "instructions", want: `"Answer in English.\nYou are helpful."`, wantOverride: true},
		{name: "openai completions left alone", channelType: constant.ChannelTypeOpenAI, model: "gpt-3.5-turbo-instruct", override: true,
			endpoint: "/v1/completions", body: `{"model":"gpt-3.5-turbo-instruct","prompt":"hi"}`, path: "messages"},
		{name: "openai moderations left alone", channelType: constant.ChannelTypeOpenAI, model: "omni-moderation-latest", override: true,
			endpoint: "/v1/moderations", body: `{"model":"omni-moderation-latest","input":"hi"}`, path: "messages"},
		{name: "openai edits left alone", channelType: constant.ChannelTypeOpenAI, model: "text-davinci-edit-001", override: true,
			endpoint: "/v1/edits", body: `{"model":"text-davinci-edit-001","instruction":"fix","input":"hi"}`, path: "messages"},
		{name: "codex chat via responses override applies once", channelType: constant.ChannelTypeCodex, model: "gpt-5-codex", system: system, override: true, viaResponses: true,
			path: "instructions", want: `"Answer in English.\nYou are helpful."`, wantOverride: true},
		{name: "codex claude messages via responses override applies once", channelType: constant.ChannelTypeCodex, model: "gpt-5-codex", override: true, viaResponses: true,
			endpoint: "/v1/messages", body: `{"model":"gpt-5-codex","max_tokens":16,"system":"You are helpful.","messages":[{"role":"user","content":"hi"}]}`,
			path: "instructions", want: `"Answer in English.\nYou are helpful."`, wantOverride: true},
		{name: "codex responses gets the prompt from the adaptor", channelType: constant.ChannelTypeCodex, model: "gpt-5-codex",
			endpoint: "/v1/responses", body: `{"model":"gpt-5-codex","input":"hi"}`, path: "instructions", want: `"Answer in English."`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
				Enabled: tt.viaResponses, AllChannels: true, ModelPatterns: []string{".*"},
			}
			received := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err == nil {
					received <- body
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture upstream failure","type":"upstream_error"}}`)
			}))
			t.Cleanup(upstream.Close)

			endpoint, clientBody := cmp.Or(tt.endpoint, "/v1/chat/completions"), tt.body
			if clientBody == "" {
				messages := `{"role":"user","content":"hi"}`
				if tt.system != "" {
					messages = tt.system + "," + messages
				}
				clientBody = `{"model":"` + tt.model + `","messages":[` + messages + `]}`
			}
			key := "fixture-key"
			if tt.channelType == constant.ChannelTypeCodex {
				key = codexKey
			}
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(clientBody))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, tt.channelType)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, key)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, tt.model)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
				SystemPrompt:           prompt,
				SystemPromptOverride:   tt.override,
				PassThroughBodyEnabled: tt.passThrough,
			})

			var apiErr *relaytypes.NewAPIError
			switch endpoint {
			case "/v1/responses":
				request, err := helper.GetAndValidateResponsesRequest(c)
				require.NoError(t, err)
				apiErr = ResponsesHelper(c, relaycommon.GenRelayInfoResponses(c, request))
			case "/v1/messages":
				request, err := helper.GetAndValidateClaudeRequest(c)
				require.NoError(t, err)
				apiErr = ClaudeHelper(c, relaycommon.GenRelayInfoClaude(c, request))
			default:
				request, err := helper.GetAndValidateTextRequest(c, relayconstant.Path2RelayMode(endpoint))
				require.NoError(t, err)
				apiErr = TextHelper(c, relaycommon.GenRelayInfoOpenAI(c, request))
			}

			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode, apiErr.Error())
			require.Len(t, received, 1)
			body := <-received
			if tt.passThrough {
				assert.Equal(t, clientBody, string(body))
			}
			got := ojson.GetBytes(body, tt.path)
			switch {
			case len(tt.wantContains) > 0:
				for _, text := range tt.wantContains {
					assert.Contains(t, got.String(), text, string(body))
				}
			case tt.want == "":
				assert.False(t, got.Exists(), string(body))
			default:
				require.True(t, got.Exists(), string(body))
				assert.JSONEq(t, tt.want, got.Raw)
			}
			assert.Equal(t, tt.wantOverride, common.GetContextKeyBool(c, constant.ContextKeySystemPromptOverride))
		})
	}
}
