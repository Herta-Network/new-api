package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reasoningHistoryInfo(userID int, channelID int, apiKey string, upstreamModel string) *RelayInfo {
	return &RelayInfo{
		UserId: userID,
		ChannelMeta: &ChannelMeta{
			ChannelId:         channelID,
			ApiKey:            apiKey,
			UpstreamModelName: upstreamModel,
		},
	}
}

func TestReasoningHistorySealerBindsRoute(t *testing.T) {
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "reasoning-history-test-secret"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	plaintext := []byte(`{"v":1,"kind":"claude","items":[]}`)
	sealer := reasoningHistorySealer{info: reasoningHistoryInfo(7, 3, "sk-a", "gpt-x")}
	token, err := sealer.Seal(plaintext)
	require.NoError(t, err)
	require.True(t, sealer.Owns(token))
	assert.NotContains(t, token, "gpt-x")

	again, err := sealer.Seal(plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, token, again, "every seal uses a fresh nonce")

	opened, ok := sealer.Open(token)
	require.True(t, ok)
	assert.Equal(t, plaintext, opened)

	encoded := strings.TrimPrefix(token, reasoningHistoryTokenPrefix)
	flipped := []byte(encoded)
	if flipped[len(flipped)/2] == 'A' {
		flipped[len(flipped)/2] = 'B'
	} else {
		flipped[len(flipped)/2] = 'A'
	}

	tests := []struct {
		name  string
		info  *RelayInfo
		token string
	}{
		{name: "another user", info: reasoningHistoryInfo(8, 3, "sk-a", "gpt-x"), token: token},
		{name: "another channel", info: reasoningHistoryInfo(7, 4, "sk-a", "gpt-x"), token: token},
		{name: "another upstream key", info: reasoningHistoryInfo(7, 3, "sk-b", "gpt-x"), token: token},
		{name: "another upstream model", info: reasoningHistoryInfo(7, 3, "sk-a", "gpt-y"), token: token},
		{name: "altered token", info: sealer.info, token: reasoningHistoryTokenPrefix + string(flipped)},
		{name: "provider ciphertext", info: sealer.info, token: "gAAAAABnative"},
		{name: "no channel", info: &RelayInfo{UserId: 7}, token: token},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := reasoningHistorySealer{info: tt.info}.Open(tt.token)
			assert.False(t, ok)
		})
	}

	t.Run("CRYPTO_SECRET changed", func(t *testing.T) {
		common.CryptoSecret = "rotated-secret"
		_, ok := sealer.Open(token)
		assert.False(t, ok)
	})
}

func TestRemoveClaudeReasoningHistory(t *testing.T) {
	var request dto.ClaudeRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-x","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"from gpt","signature":"narc1.issued"},
			{"type":"redacted_thinking","data":"narc1.issued-too"},
			{"type":"thinking","thinking":"from claude","signature":"native-sig"},
			{"type":"text","text":"answer"}
		]},
		{"role":"user","content":"again"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"only issued","signature":"narc1.issued"}]},
		{"role":"user","content":"last"}
	]}`), &request))

	RemoveClaudeReasoningHistory(&request)

	require.Len(t, request.Messages, 4)
	blocks, err := request.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "native-sig", blocks[0].Signature)
	assert.Equal(t, "text", blocks[1].Type)
	assert.Equal(t, "again", request.Messages[2].GetStringContent())
	assert.Equal(t, "last", request.Messages[3].GetStringContent())
}

func TestRemoveResponsesReasoningHistory(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{Input: json.RawMessage(`[
		{"role":"user","content":"hi"},
		{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"narc1.issued"},
		{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"gAAAAABnative"},
		{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"}
	]`)}
	require.NoError(t, RemoveResponsesReasoningHistory(request))
	assert.JSONEq(t, `[
		{"role":"user","content":"hi"},
		{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"gAAAAABnative"},
		{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"}
	]`, string(request.Input))

	text := &dto.OpenAIResponsesRequest{Input: json.RawMessage(`"narc1.just text"`)}
	require.NoError(t, RemoveResponsesReasoningHistory(text))
	assert.Equal(t, `"narc1.just text"`, string(text.Input))
}

// Only a Responses client that asked for reasoning.encrypted_content receives
// sealed reasoning in that field.
func TestResponsesRequestIncludesEncryptedReasoning(t *testing.T) {
	tests := []struct {
		name    string
		request any
		want    bool
	}{
		{name: "asked", request: &dto.OpenAIResponsesRequest{Include: json.RawMessage(`["file_search_call.results","reasoning.encrypted_content"]`)}, want: true},
		{name: "other includes", request: &dto.OpenAIResponsesRequest{Include: json.RawMessage(`["file_search_call.results"]`)}},
		{name: "no include", request: &dto.OpenAIResponsesRequest{}},
		{name: "Messages client", request: &dto.ClaudeRequest{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, responsesRequestIncludesEncryptedReasoning(tt.request))
		})
	}
}
