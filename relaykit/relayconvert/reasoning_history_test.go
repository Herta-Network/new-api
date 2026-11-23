package relayconvert

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeSealer stands in for the host sealer: a token opens only on the route
// it was sealed for. The host's real sealer adds encryption and a MAC.
type routeSealer struct{ route string }

const routeSealerPrefix = "test1."

func (s routeSealer) Seal(plaintext []byte) (string, error) {
	return routeSealerPrefix + base64.RawURLEncoding.EncodeToString(append([]byte(s.route+"\x00"), plaintext...)), nil
}

func (s routeSealer) Open(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, routeSealerPrefix))
	if err != nil {
		return nil, false
	}
	route, plaintext, ok := bytes.Cut(raw, []byte{0})
	if !ok || string(route) != s.route {
		return nil, false
	}
	return plaintext, true
}

func (s routeSealer) Owns(value string) bool { return strings.HasPrefix(value, routeSealerPrefix) }

// reasoningHistoryMeta models a client on route; a Responses client here asked
// for reasoning.encrypted_content, as Codex does.
func reasoningHistoryMeta(route string) *convmeta.Values {
	return &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   "upstream-model",
		Options: &convmeta.Options{
			ReasoningHistory:                    routeSealer{route: route},
			ResponsesIncludesEncryptedReasoning: true,
		},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

// claudeAssistantTurn is the history a Responses client (Codex) carries back
// after a Claude upstream answered with reasoning and a tool call.
const claudeThinkingResponse = `{
	"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-x", "stop_reason": "tool_use",
	"content": [
		{"type": "thinking", "thinking": "Need the file list.", "signature": "native-sig-A"},
		{"type": "redacted_thinking", "data": "native-redacted-B"},
		{"type": "text", "text": "Running ls."},
		{"type": "tool_use", "id": "toolu_1", "name": "shell", "input": {"cmd": "ls"}}
	],
	"usage": {"input_tokens": 10, "output_tokens": 20}
}`

const claudeOriginalThinkingBlocks = `[{"type":"thinking","thinking":"Need the file list.","signature":"native-sig-A"},{"type":"redacted_thinking","data":"native-redacted-B"}]`

func responsesRequestReplaying(t *testing.T, output []dto.ResponsesOutput) *dto.OpenAIResponsesRequest {
	t.Helper()
	input := []any{map[string]any{"role": "user", "content": "list files"}}
	for _, item := range output {
		input = append(input, item)
	}
	input = append(input, map[string]any{"type": "function_call_output", "call_id": "toolu_1", "output": "a.txt"})
	maxOutputTokens := uint(2048)
	return &dto.OpenAIResponsesRequest{
		Model:           "claude-x",
		Input:           json.RawMessage(mustJSON(t, input)),
		MaxOutputTokens: &maxOutputTokens,
	}
}

func claudeRequestAssistantTurn(t *testing.T, request *dto.ClaudeRequest) []dto.ClaudeMediaMessage {
	t.Helper()
	require.Len(t, request.Messages, 3)
	require.Equal(t, "assistant", request.Messages[1].Role)
	blocks, err := request.Messages[1].ParseContent()
	require.NoError(t, err)
	return blocks
}

func TestReasoningHistoryResponsesClientClaudeUpstream(t *testing.T) {
	ctx := context.Background()
	var claudeResponse dto.ClaudeResponse
	require.NoError(t, json.Unmarshal([]byte(claudeThinkingResponse), &claudeResponse))

	converted, err := ConvertResponse(ctx, reasoningHistoryMeta("channel-1"), types.RelayFormatOpenAIResponses, &claudeResponse)
	require.NoError(t, err)
	responsesResponse := converted.Value.(*dto.OpenAIResponsesResponse)
	require.Equal(t, "reasoning", responsesResponse.Output[0].Type)
	assert.Equal(t, "Need the file list.", responsesResponse.Output[0].Summary[0].Text)
	require.True(t, strings.HasPrefix(responsesResponse.Output[0].EncryptedContent, routeSealerPrefix))

	t.Run("client did not ask for encrypted reasoning", func(t *testing.T) {
		meta := reasoningHistoryMeta("channel-1")
		meta.Options.ResponsesIncludesEncryptedReasoning = false
		converted, err := ConvertResponse(ctx, meta, types.RelayFormatOpenAIResponses, &claudeResponse)
		require.NoError(t, err)
		output := converted.Value.(*dto.OpenAIResponsesResponse).Output
		require.Equal(t, "reasoning", output[0].Type)
		assert.Empty(t, output[0].EncryptedContent)
	})

	t.Run("text after a tool call stays out of its tool_use turn", func(t *testing.T) {
		input := []any{
			map[string]any{"role": "user", "content": "list files"},
			responsesResponse.Output[0],
			map[string]any{"type": "function_call", "call_id": "toolu_1", "name": "shell", "arguments": "{}"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "after"}}},
			map[string]any{"type": "function_call_output", "call_id": "toolu_1", "output": "a.txt"},
		}
		maxOutputTokens := uint(2048)
		result, err := ConvertRequest(ctx, reasoningHistoryMeta("channel-1"), types.RelayFormatClaude, &dto.OpenAIResponsesRequest{
			Model:           "claude-x",
			Input:           json.RawMessage(mustJSON(t, input)),
			MaxOutputTokens: &maxOutputTokens,
		})
		require.NoError(t, err)
		messages := result.Value.(*dto.ClaudeRequest).Messages
		require.Len(t, messages, 4)
		toolTurn, err := messages[1].ParseContent()
		require.NoError(t, err)
		require.Len(t, toolTurn, 3)
		assert.Equal(t, []string{"thinking", "redacted_thinking", "tool_use"}, []string{toolTurn[0].Type, toolTurn[1].Type, toolTurn[2].Type})
		assert.Equal(t, "assistant", messages[2].Role)
		assert.Equal(t, "user", messages[3].Role)
	})

	tests := []struct {
		name          string
		route         string
		wantRestored  bool
		wantBlockKind []string
	}{
		{name: "same route", route: "channel-1", wantRestored: true, wantBlockKind: []string{"thinking", "redacted_thinking", "text", "tool_use"}},
		{name: "other route drops reasoning", route: "channel-2", wantBlockKind: []string{"text", "tool_use"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertRequest(ctx, reasoningHistoryMeta(tt.route), types.RelayFormatClaude, responsesRequestReplaying(t, responsesResponse.Output))
			require.NoError(t, err)
			assert.Empty(t, result.Diagnostics)
			blocks := claudeRequestAssistantTurn(t, result.Value.(*dto.ClaudeRequest))
			kinds := make([]string, 0, len(blocks))
			for _, block := range blocks {
				kinds = append(kinds, block.Type)
			}
			assert.Equal(t, tt.wantBlockKind, kinds)
			if tt.wantRestored {
				assert.JSONEq(t, claudeOriginalThinkingBlocks, mustJSON(t, blocks[:2]))
			}
		})
	}
}

func TestReasoningHistoryResponsesClientClaudeUpstreamStream(t *testing.T) {
	ctx := context.Background()
	meta := reasoningHistoryMeta("channel-1")
	state, err := NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_1", Model: "claude-x", EmitSequenceNumber: true})
	require.NoError(t, err)
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Need the "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"file list."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"native-sig-A"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"native-redacted-B"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Running ls."}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"shell","input":{}}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls\"}"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		`{"type":"message_stop"}`,
	}
	var results []ResponseResult
	for _, raw := range events {
		var event dto.ClaudeResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		chunkResults, err := ConvertStreamResponseChunk(ctx, meta, state, &event)
		require.NoError(t, err)
		results = append(results, chunkResults...)
	}
	finalResults, err := FinalizeStreamResponse(ctx, meta, state)
	require.NoError(t, err)
	results = append(results, finalResults...)

	var reasoningDone *dto.ResponsesOutput
	var completed *dto.OpenAIResponsesResponse
	for _, result := range results {
		event := result.Value.(ChatToResponsesStreamEvent)
		if event.Type == "response.output_item.done" && event.Payload.Item.Type == "reasoning" {
			reasoningDone = event.Payload.Item
		}
		if event.Type == "response.completed" {
			completed = event.Payload.Response
		}
	}
	require.NotNil(t, reasoningDone)
	require.NotNil(t, completed)
	require.Equal(t, "reasoning", completed.Output[0].Type)
	assert.Equal(t, reasoningDone.EncryptedContent, completed.Output[0].EncryptedContent)

	result, err := ConvertRequest(ctx, meta, types.RelayFormatClaude, responsesRequestReplaying(t, completed.Output))
	require.NoError(t, err)
	blocks := claudeRequestAssistantTurn(t, result.Value.(*dto.ClaudeRequest))
	require.Len(t, blocks, 4)
	assert.JSONEq(t, claudeOriginalThinkingBlocks, mustJSON(t, blocks[:2]))
	assert.Equal(t, "tool_use", blocks[3].Type)
}

// responsesReasoningResponse is a Responses upstream's answer to a Messages
// client (Claude Code), including the encrypted reasoning it asked for.
const responsesReasoningResponse = `{
	"id": "resp_1", "object": "response", "status": "completed", "model": "gpt-x",
	"output": [
		{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "Need the file list."}], "encrypted_content": "openai-encrypted-E"},
		{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "Running ls.", "annotations": []}]},
		{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "shell", "arguments": "{\"cmd\":\"ls\"}", "status": "completed"}
	],
	"usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30}
}`

// responsesReplayedReasoningItem is the reasoning item as the upstream
// returned it, ID included, replayed with store false.
const responsesReplayedReasoningItem = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Need the file list."}],"encrypted_content":"openai-encrypted-E"}`

func claudeRequestReplaying(t *testing.T, assistantContent any) *dto.ClaudeRequest {
	t.Helper()
	body := `{
		"model": "gpt-x", "max_tokens": 4096, "thinking": {"type": "enabled", "budget_tokens": 1024},
		"messages": [
			{"role": "user", "content": "list files"},
			{"role": "assistant", "content": ` + mustJSON(t, assistantContent) + `},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "call_1", "content": "a.txt"}]}
		]
	}`
	var request dto.ClaudeRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	return &request
}

func responsesInputItems(t *testing.T, request *dto.OpenAIResponsesRequest) []json.RawMessage {
	t.Helper()
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(request.Input, &items))
	return items
}

func TestReasoningHistoryMessagesClientResponsesUpstream(t *testing.T) {
	ctx := context.Background()
	var responsesResponse dto.OpenAIResponsesResponse
	require.NoError(t, json.Unmarshal([]byte(responsesReasoningResponse), &responsesResponse))

	converted, err := ConvertResponse(ctx, reasoningHistoryMeta("channel-1"), types.RelayFormatClaude, &responsesResponse)
	require.NoError(t, err)
	claudeResponse := converted.Value.(*dto.ClaudeResponse)
	require.Equal(t, "thinking", claudeResponse.Content[0].Type)
	assert.Equal(t, "Need the file list.", *claudeResponse.Content[0].Thinking)
	require.True(t, strings.HasPrefix(claudeResponse.Content[0].Signature, routeSealerPrefix))

	result, err := ConvertRequest(ctx, reasoningHistoryMeta("channel-1"), types.RelayFormatOpenAIResponses, claudeRequestReplaying(t, claudeResponse.Content))
	require.NoError(t, err)
	request := result.Value.(*dto.OpenAIResponsesRequest)
	assert.JSONEq(t, `["reasoning.encrypted_content"]`, string(request.Include))
	assert.JSONEq(t, `false`, string(request.Store))
	items := responsesInputItems(t, request)
	require.Len(t, items, 5)
	assert.JSONEq(t, responsesReplayedReasoningItem, string(items[1]))
	assert.Contains(t, string(items[2]), `"output_text"`)
	assert.Contains(t, string(items[3]), `"function_call"`)
}

func TestReasoningHistoryMessagesClientResponsesUpstreamStream(t *testing.T) {
	ctx := context.Background()
	meta := reasoningHistoryMeta("channel-1")
	state, err := NewResponseStreamState(types.RelayFormatOpenAIResponses, types.RelayFormatClaude, ResponseStreamOptions{ID: "msg_1", Model: "gpt-x"})
	require.NoError(t, err)
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-x","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","summary_index":0,"delta":"Need the file list."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Need the file list."}],"encrypted_content":"openai-encrypted-E"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","content_index":0,"delta":"Running ls."}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Running ls.","annotations":[]}]}}`,
		`{"type":"response.completed","response":` + responsesReasoningResponse + `}`,
	}
	var claudeEvents []*dto.ClaudeResponse
	for _, raw := range events {
		var event dto.ResponsesStreamResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		chunkResults, err := ConvertStreamResponseChunk(ctx, meta, state, &event)
		require.NoError(t, err)
		for _, result := range chunkResults {
			claudeEvents = append(claudeEvents, result.Value.(*dto.ClaudeResponse))
		}
	}

	// The thinking block is replayable only if its signature arrives before
	// the block stops, as Claude streams it.
	thinking := dto.ClaudeMediaMessage{Type: "thinking", Thinking: new(string)}
	stopped := false
	for _, event := range claudeEvents {
		if event.Index == nil || *event.Index != 0 {
			continue
		}
		switch event.Type {
		case "content_block_delta":
			require.False(t, stopped)
			switch event.Delta.Type {
			case "thinking_delta":
				*thinking.Thinking += *event.Delta.Thinking
			case "signature_delta":
				thinking.Signature += event.Delta.Signature
			}
		case "content_block_stop":
			stopped = true
		}
	}
	require.True(t, stopped)
	require.True(t, strings.HasPrefix(thinking.Signature, routeSealerPrefix))

	result, err := ConvertRequest(ctx, meta, types.RelayFormatOpenAIResponses, claudeRequestReplaying(t, []dto.ClaudeMediaMessage{
		thinking,
		{Type: "text", Text: kitutil.GetPointer("Running ls.")},
		{Type: "tool_use", Id: "call_1", Name: "shell", Input: map[string]any{"cmd": "ls"}},
	}))
	require.NoError(t, err)
	items := responsesInputItems(t, result.Value.(*dto.OpenAIResponsesRequest))
	assert.JSONEq(t, responsesReplayedReasoningItem, string(items[1]))
}

// Reasoning that cannot be replayed to the upstream is dropped without an
// error or diagnostic, as before continuation existed.
func TestReasoningHistoryDropsUnusableThinking(t *testing.T) {
	ctx := context.Background()
	claudeToken, err := routeSealer{route: "channel-1"}.Seal([]byte(`{"v":1,"kind":"claude","items":[{"type":"thinking","thinking":"x","signature":"s"}]}`))
	require.NoError(t, err)
	responsesToken, err := routeSealer{route: "channel-2"}.Seal([]byte(`{"v":1,"kind":"responses","items":[` + responsesReplayedReasoningItem + `]}`))
	require.NoError(t, err)

	tests := []struct {
		name      string
		signature string
	}{
		{name: "native Claude signature", signature: "native-sig-A"},
		{name: "token of the other direction", signature: claudeToken},
		{name: "token of another route", signature: responsesToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertRequest(ctx, reasoningHistoryMeta("channel-1"), types.RelayFormatOpenAIResponses, claudeRequestReplaying(t, []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: kitutil.GetPointer("x"), Signature: tt.signature},
				{Type: "tool_use", Id: "call_1", Name: "shell", Input: map[string]any{}},
			}))
			require.NoError(t, err)
			assert.Empty(t, result.Diagnostics)
			for _, item := range responsesInputItems(t, result.Value.(*dto.OpenAIResponsesRequest)) {
				assert.NotContains(t, string(item), `"reasoning"`)
			}
		})
	}
}
