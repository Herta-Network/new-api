package opencodego

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionRequest struct {
	tokenID int
	format  types.RelayFormat
	headers map[string]string
	body    string
}

func sessionIDFor(t *testing.T, request sessionRequest) string {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(request.body))
	for name, value := range request.headers {
		c.Request.Header.Set(name, value)
	}
	storage, err := common.CreateBodyStorage([]byte(request.body))
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	c.Set(common.KeyBodyStorage, storage)
	info := &relaycommon.RelayInfo{UserId: 1, TokenId: request.tokenID, RelayFormat: request.format}
	return SessionID(c, info)
}

func TestSessionIDIsStablePerConversation(t *testing.T) {
	const chatTurn1 = `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"fix the bug"}]}`
	const chatTurn2 = `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"fix the bug"},{"role":"assistant","content":"done"},{"role":"user","content":"now add a test"}]}`
	tests := []struct {
		name  string
		a     sessionRequest
		b     sessionRequest
		equal bool
	}{
		{
			name:  "chat turns of one conversation share a session",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAI, body: chatTurn1},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAI, body: chatTurn2},
			equal: true,
		},
		{
			name:  "a different first user message is another conversation",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAI, body: chatTurn1},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"write docs"}]}`},
			equal: false,
		},
		{
			name:  "the same conversation from another token is another session",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAI, body: chatTurn1},
			b:     sessionRequest{tokenID: 8, format: types.RelayFormatOpenAI, body: chatTurn1},
			equal: false,
		},
		{
			name:  "anthropic text blocks ignore cache_control added on later turns",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatClaude, body: `{"system":"be brief","messages":[{"role":"user","content":[{"type":"text","text":"fix the bug","cache_control":{"type":"ephemeral"}}]}]}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatClaude, body: `{"system":"be brief","messages":[{"role":"user","content":[{"type":"text","text":"fix the bug"}]},{"role":"assistant","content":"done"},{"role":"user","content":"again"}]}`},
			equal: true,
		},
		{
			name:  "responses turns of one conversation share a session",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, body: `{"instructions":"be brief","input":[{"role":"user","content":[{"type":"input_text","text":"fix the bug"}]}]}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, body: `{"instructions":"be brief","input":[{"role":"user","content":[{"type":"input_text","text":"fix the bug"}]},{"role":"assistant","content":"done"},{"role":"user","content":"again"}]}`},
			equal: true,
		},
		{
			name:  "gemini turns of one conversation share a session",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatGemini, body: `{"systemInstruction":{"parts":[{"text":"be brief"}]},"contents":[{"role":"user","parts":[{"text":"fix the bug"}]}]}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatGemini, body: `{"systemInstruction":{"parts":[{"text":"be brief"}]},"contents":[{"role":"user","parts":[{"text":"fix the bug"}]},{"role":"model","parts":[{"text":"done"}]},{"role":"user","parts":[{"text":"again"}]}]}`},
			equal: true,
		},
		{
			name:  "prompt_cache_key names the conversation over its content",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, body: `{"prompt_cache_key":"thread-1","input":"fix the bug"}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, body: `{"prompt_cache_key":"thread-1","input":"write docs"}`},
			equal: true,
		},
		{
			name:  "a client session header names the conversation over the body",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatClaude, headers: map[string]string{"X-Claude-Code-Session-Id": "cc-1"}, body: `{"messages":[{"role":"user","content":"fix the bug"}]}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatClaude, headers: map[string]string{"X-Claude-Code-Session-Id": "cc-1"}, body: `{"messages":[{"role":"user","content":"write docs"}]}`},
			equal: true,
		},
		{
			name:  "codex session_id headers tell conversations apart",
			a:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, headers: map[string]string{"session_id": "codex-1"}, body: `{"input":"fix the bug"}`},
			b:     sessionRequest{tokenID: 7, format: types.RelayFormatOpenAIResponses, headers: map[string]string{"session_id": "codex-2"}, body: `{"input":"fix the bug"}`},
			equal: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := sessionIDFor(t, tt.a)
			b := sessionIDFor(t, tt.b)
			assert.Regexp(t, `^newapi_[0-9a-f]{32}$`, a)
			if tt.equal {
				assert.Equal(t, a, b)
			} else {
				assert.NotEqual(t, a, b)
			}
		})
	}
}

// conversationTurn relays one chat call with conversation recognition on, the
// way middleware.ConversationSession runs it, and returns the session header
// OpenCode Go would receive.
func conversationTurn(t *testing.T, headers map[string]string, body string, response string) string {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	for name, value := range headers {
		c.Request.Header.Set(name, value)
	}
	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyTokenId, 31)
	require.True(t, service.BeginConversationSession(c))
	session := SessionID(c, &relaycommon.RelayInfo{UserId: 1, TokenId: 31, RelayFormat: types.RelayFormatOpenAI})
	c.Header("Content-Type", "application/json")
	_, err := c.Writer.WriteString(response)
	require.NoError(t, err)
	service.FinishConversationSession(c)
	return session
}

func TestSessionIDFollowsRecognizedConversation(t *testing.T) {
	const toolCall = `{"id":"call_3c4d5e6f7a8b9c0d1e2f","type":"function","function":{"name":"grep","arguments":"{}"}}`
	// The client keeps only the newest messages, so the opening the fallback
	// hashes is gone; the echoed tool call id still names the conversation.
	const trimmed = `{"messages":[{"role":"assistant","tool_calls":[` + toolCall + `]},{"role":"tool","tool_call_id":"call_3c4d5e6f7a8b9c0d1e2f","content":"a.go:1 TODO"}]}`

	first := conversationTurn(t, nil, `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"list the TODOs"}]}`,
		`{"id":"chatcmpl-o1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[`+toolCall+`]}}]}`)
	next := conversationTurn(t, nil, trimmed, `{"id":"chatcmpl-o2","choices":[{"index":0,"message":{"role":"assistant","content":"done"}}]}`)
	assert.Regexp(t, `^newapi_[0-9a-f]{32}$`, first)
	assert.Equal(t, first, next)
	assert.NotEqual(t, first, sessionIDFor(t, sessionRequest{tokenID: 31, format: types.RelayFormatOpenAI, body: trimmed}),
		"without recognition the trimmed turn falls back to another session")

	headerSession := map[string]string{"X-Claude-Code-Session-Id": "cc-7"}
	assert.Equal(t,
		sessionIDFor(t, sessionRequest{tokenID: 31, format: types.RelayFormatOpenAI, headers: headerSession, body: trimmed}),
		conversationTurn(t, headerSession, trimmed, `{"id":"chatcmpl-o3","choices":[]}`),
		"a client session header still wins over the recognized conversation")
}

func TestFetchUsage(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      int
		body        string
		wantUsage   *Usage
		wantMessage string
	}{
		{
			name:   "maps the three windows and drops the placeholder reset of an unused one",
			status: http.StatusOK,
			body: `{"usage":{
				"rolling":{"status":"ok","percent":0,"resetsAt":"2026-09-22T17:00:00Z"},
				"weekly":{"status":"rate-limited","percent":100,"resetsAt":"2026-09-25T00:00:00Z"},
				"monthly":{"status":"ok","percent":"63.5","resetsAt":"2026-10-01T00:00:00Z"}}}`,
			wantUsage: &Usage{
				FiveHour:     &UsageWindow{Status: "ok", UsedPercent: 0, LimitWindowSeconds: 5 * 3600},
				Weekly:       &UsageWindow{Status: "rate-limited", UsedPercent: 100, LimitWindowSeconds: 7 * 86400, ResetAt: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC).Unix(), ResetAfterSeconds: 60 * 3600},
				Monthly:      &UsageWindow{Status: "ok", UsedPercent: 63.5, LimitWindowSeconds: 30 * 86400, ResetAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(), ResetAfterSeconds: 8*86400 + 12*3600},
				LimitReached: true,
			},
		},
		{name: "a key without a Go subscription", status: http.StatusForbidden, body: `{}`, wantMessage: "This API key has no OpenCode Go subscription"},
		{name: "a rejected key", status: http.StatusUnauthorized, body: `{}`, wantMessage: "OpenCode Go rejected the API key"},
		{name: "another upstream status", status: http.StatusBadGateway, body: `oops`, wantMessage: "Upstream returned status {{status}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/zen/go/v1/usage", r.URL.Path)
				assert.Equal(t, "Bearer go-key", r.Header.Get("Authorization"))
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()

			usage, err := FetchUsage(context.Background(), upstream.Client(), upstream.URL+"/inference/go", " go-key ", now)
			if tt.wantMessage == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantUsage, usage)
				return
			}
			var message *common.Message
			require.True(t, errors.As(err, &message))
			assert.Equal(t, tt.wantMessage, message.Key)
			assert.Nil(t, usage)
		})
	}
}

func TestFetchUsageRejectsPayloadWithoutWindows(t *testing.T) {
	for _, body := range []string{`{}`, `{"usage":{}}`, `{"rollingUsage":1,"usagePercent":5}`, `nope`} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()

			usage, err := FetchUsage(context.Background(), upstream.Client(), upstream.URL, "go-key", time.Now())
			require.Error(t, err)
			var message *common.Message
			assert.False(t, errors.As(err, &message))
			assert.Nil(t, usage)
		})
	}
}
