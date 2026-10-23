package conversation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/module/conversation/internal/convo"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conversationCall is one relay call as a client sees it: the body it sends
// and the response chunks the gateway writes back, flushed one by one.
type conversationCall struct {
	path        string
	tokenID     int
	body        string
	contentType string
	chunks      []string
	// silent leaves the session unasked, like a request no affinity rule or
	// channel needs a session for.
	silent bool
}

// serveConversationCall runs call through the same Begin/Next/Finish sequence
// as the module's ConversationSession middleware, with a handler that asks for
// the session through the kernel's service.ConversationSessionID, the way an
// affinity key source or a channel does, then writes the response.
func serveConversationCall(t *testing.T, call conversationCall) (string, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 1)
		common.SetContextKey(c, constant.ContextKeyTokenId, call.tokenID)
		if !Begin(c) {
			c.Next()
			return
		}
		c.Next()
		Finish(c)
	})
	router.POST("/*path", func(c *gin.Context) {
		if !call.silent {
			session := service.ConversationSessionID(c)
			assert.Equal(t, session, service.ConversationSessionID(c), "later calls reuse the first recognition")
			c.Header("X-Test-Session", session)
		}
		c.Header("Content-Type", call.contentType)
		c.Status(http.StatusOK)
		for _, chunk := range call.chunks {
			_, err := c.Writer.WriteString(chunk)
			require.NoError(t, err)
			c.Writer.Flush()
		}
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, call.path, strings.NewReader(call.body)))
	require.Equal(t, http.StatusOK, recorder.Code)
	return recorder.Header().Get("X-Test-Session"), recorder
}

// useConversationRuntime installs a recognition runtime whose clock is *now and
// whose Redis writes finish before put returns. redisClient may be nil.
func useConversationRuntime(t *testing.T, now *time.Time, redisClient *redis.Client) *conversationSessionRuntime {
	t.Helper()
	runtime := &conversationSessionRuntime{
		fingerprint: convo.NewFingerprinter([]byte("test fingerprint key")),
		store: &conversationSessionStore{
			memory:    hot.NewHotCache[string, conversationSessionRecord](hot.LRU, 1000).Build(),
			redis:     func() *redis.Client { return redisClient },
			keySecret: []byte("test store key"),
			ttl:       conversationSessionWindow,
			now:       func() time.Time { return *now },
			runAsync:  func(run func()) { run() },
		},
	}
	previous := conversationSessionRuntimeValue.Load()
	conversationSessionRuntimeValue.Store(runtime)
	t.Cleanup(func() { conversationSessionRuntimeValue.Store(previous) })
	return runtime
}

func useConversationSetting(t *testing.T, enabled bool, window time.Duration) {
	t.Helper()
	setting := operation_setting.GetConversationSessionSetting()
	previous := *setting
	setting.Enabled = enabled
	setting.WindowSeconds = int(window / time.Second)
	t.Cleanup(func() { *setting = previous })
}

const (
	chatToolLoopOpening = `{"role":"user","content":"List the TODOs in this repo"}`
	chatFirstCall       = `{"id":"call_7f3a9c2e1b4d4e8fa1c2","type":"function","function":{"name":"grep","arguments":"{\"q\":\"TODO\"}"}}`
	chatSecondCall      = `{"id":"call_9d8e7f6a5b4c3d2e1f0a","type":"function","function":{"name":"read","arguments":"{\"f\":\"b.go\"}"}}`
)

func chatToolCallResponse(id string, call string) []string {
	return []string{`{"id":"` + id + `","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[` + call + `]},"finish_reason":"tool_calls"}]}`}
}

// chatTrimmedFollowUp is the call after the second tool call from a client
// that keeps only the newest messages: no user text survives, so nothing but
// the echoed tool call id ties it to the conversation.
const chatTrimmedFollowUp = `{"model":"gpt-5","messages":[{"role":"assistant","tool_calls":[` + chatSecondCall + `]},{"role":"tool","tool_call_id":"call_9d8e7f6a5b4c3d2e1f0a","content":"b.go:2 TODO"}]}`

func TestConversationSessionLinksAgentToolLoop(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	useConversationSetting(t, true, time.Hour)
	useConversationRuntime(t, &now, nil)

	first, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
		chunks: chatToolCallResponse("chatcmpl-a1", chatFirstCall),
	})
	now = now.Add(5 * time.Second)
	second, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `,{"role":"assistant","tool_calls":[` + chatFirstCall + `]},{"role":"tool","tool_call_id":"call_7f3a9c2e1b4d4e8fa1c2","content":"a.go:1 TODO"}]}`,
		chunks: chatToolCallResponse("chatcmpl-a2", chatSecondCall),
	})
	now = now.Add(5 * time.Second)
	third, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   chatTrimmedFollowUp,
		chunks: []string{`{"id":"chatcmpl-a3","choices":[{"index":0,"message":{"role":"assistant","content":"Two TODOs: a.go:1 and b.go:2."}}]}`},
	})
	other, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[{"role":"user","content":"Write release notes"}]}`,
		chunks: chatToolCallResponse("chatcmpl-b1", `{"id":"call_0a1b2c3d4e5f6a7b8c9d","type":"function","function":{"name":"git_log","arguments":"{}"}}`),
	})

	assert.Regexp(t, `^cs_[0-9a-f]{32}$`, first)
	assert.Equal(t, first, second)
	assert.Equal(t, first, third, "a trimmed history must still link through the echoed tool call id")
	assert.NotEqual(t, first, other)
}

func TestConversationSessionDoesNotCrossTokens(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	useConversationSetting(t, true, time.Hour)
	useConversationRuntime(t, &now, nil)

	owner, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
		chunks: chatToolCallResponse("chatcmpl-a1", chatSecondCall),
	})
	sameOpening, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 8, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
		chunks: chatToolCallResponse("chatcmpl-c1", `{"id":"call_5b6c7d8e9f0a1b2c3d4e","type":"function","function":{"name":"grep","arguments":"{}"}}`),
	})
	replayedIDs, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 8, contentType: "application/json",
		body:   chatTrimmedFollowUp,
		chunks: []string{`{"id":"chatcmpl-c2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`},
	})

	assert.NotEqual(t, owner, sameOpening)
	assert.NotEqual(t, owner, replayedIDs, "another token replaying the owner's tool call id must not join its session")
}

func TestConversationSessionExpiresAfterWindow(t *testing.T) {
	window := time.Hour
	tests := []struct {
		name   string
		idle   time.Duration
		linked bool
	}{
		{name: "a follow-up inside the window links", idle: window - time.Minute, linked: true},
		{name: "a follow-up after the window starts a new session", idle: window + time.Minute, linked: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			useConversationSetting(t, true, window)
			useConversationRuntime(t, &now, nil)

			first, _ := serveConversationCall(t, conversationCall{
				path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
				body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
				chunks: chatToolCallResponse("chatcmpl-a1", chatSecondCall),
			})
			now = now.Add(tt.idle)
			next, _ := serveConversationCall(t, conversationCall{
				path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
				body:   chatTrimmedFollowUp,
				chunks: []string{`{"id":"chatcmpl-a2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`},
			})

			assert.Equal(t, tt.linked, first == next)
		})
	}
}

func TestConversationSessionObservesStreamingResponse(t *testing.T) {
	const goroutineAnswer = "A goroutine is a lightweight thread managed by the Go runtime, started with the go keyword."
	tests := []struct {
		name     string
		path     string
		first    string
		chunks   []string
		followUp string
	}{
		{
			name:  "anthropic tool_use id from the stream links the tool_result",
			path:  "/v1/messages",
			first: `{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"Fix the build error in main.go"}]}`,
			chunks: []string{
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01Hj7kLmNoPqRsTuVwXyZ012\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_01XyZabc123DEF456",
				"ghi789JK\",\"name\":\"Read\",\"input\":{}}}\n\n",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			},
			followUp: `{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01XyZabc123DEF456ghi789JK","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01XyZabc123DEF456ghi789JK","content":"package main"}]}]}`,
		},
		{
			name:  "chat text deltas link the replayed answer by fingerprint",
			path:  "/v1/chat/completions",
			first: `{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"What is a goroutine?"}]}`,
			chunks: []string{
				"data: {\"id\":\"chatcmpl-s1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"A goroutine is a lightweight thread \"}}]}\n\n",
				"data: {\"id\":\"chatcmpl-s1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"managed by the Go runtime, started with the go keyword.\"}}]}\n\n",
				"data: [DONE]\n\n",
			},
			followUp: `{"model":"gpt-5","stream":true,"messages":[{"role":"assistant","content":"` + goroutineAnswer + `"},{"role":"user","content":"And channels?"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			useConversationSetting(t, true, time.Hour)
			useConversationRuntime(t, &now, nil)

			first, recorder := serveConversationCall(t, conversationCall{
				path: tt.path, tokenID: 7, contentType: "text/event-stream", body: tt.first, chunks: tt.chunks,
			})
			assert.Equal(t, strings.Join(tt.chunks, ""), recorder.Body.String(), "observation must not change the stream")
			assert.True(t, recorder.Flushed, "flushes must reach the client writer")

			next, _ := serveConversationCall(t, conversationCall{
				path: tt.path, tokenID: 7, contentType: "text/event-stream", body: tt.followUp,
				chunks: []string{"data: [DONE]\n\n"},
			})
			assert.Equal(t, first, next)
		})
	}
}

func TestConversationSessionSharedAcrossInstances(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	useConversationSetting(t, true, time.Hour)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	useConversationRuntime(t, &now, client)
	first, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
		chunks: chatToolCallResponse("chatcmpl-a1", chatSecondCall),
	})

	// Another instance has an empty memory tier and reads the clue from Redis.
	useConversationRuntime(t, &now, client)
	next, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   chatTrimmedFollowUp,
		chunks: []string{`{"id":"chatcmpl-a2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`},
	})
	assert.Equal(t, first, next)

	// With Redis gone the request still gets a session and still succeeds.
	server.Close()
	useConversationRuntime(t, &now, client)
	degraded, _ := serveConversationCall(t, conversationCall{
		path: "/v1/chat/completions", tokenID: 7, contentType: "application/json",
		body:   chatTrimmedFollowUp,
		chunks: []string{`{"id":"chatcmpl-a3","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`},
	})
	assert.Regexp(t, `^cs_[0-9a-f]{32}$`, degraded)
}

func TestConversationSessionIdleDoesNoReadsOrWrites(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		silent  bool
	}{
		{name: "recognition turned off", enabled: false},
		{name: "nobody asks for the session", enabled: true, silent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			useConversationSetting(t, tt.enabled, time.Hour)
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			runtime := useConversationRuntime(t, &now, client)
			commands := server.CommandCount()

			session, recorder := serveConversationCall(t, conversationCall{
				path: "/v1/chat/completions", tokenID: 7, contentType: "application/json", silent: tt.silent,
				body:   `{"model":"gpt-5","messages":[` + chatToolLoopOpening + `]}`,
				chunks: chatToolCallResponse("chatcmpl-a1", chatFirstCall),
			})

			assert.Empty(t, session)
			assert.Equal(t, commands, server.CommandCount(), "no Redis command may run")
			assert.Zero(t, runtime.store.memory.Len(), "nothing may be stored in memory")
			assert.Contains(t, recorder.Body.String(), "call_7f3a9c2e1b4d4e8fa1c2")
		})
	}
}

func TestConversationSessionLookupScopesDirections(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	useConversationSetting(t, true, time.Hour)
	runtime := useConversationRuntime(t, &now, nil)
	store := runtime.store

	// resp_1 was produced by one call and later named by a sibling: the
	// producer wins, as convo's Lookup contract requires.
	store.put("user:1:token:7", "cs_producer", []convo.Cursor{{Kind: convo.KindExplicit, Direction: convo.DirectionOut, Value: "resp_1"}})
	now = now.Add(time.Minute)
	store.put("user:1:token:7", "cs_consumer", []convo.Cursor{{Kind: convo.KindExplicit, Direction: convo.DirectionIn, Value: "resp_1"}})

	match, ok, err := store.lookup(context.Background(), "user:1:token:7", convo.KindExplicit, []string{"resp_1"}, convo.Scope{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "cs_producer", match.SessionID)

	_, ok, err = store.lookup(context.Background(), "user:1:token:7", convo.KindEchoID, []string{"resp_1"}, convo.Scope{SamePrincipal: true})
	require.NoError(t, err)
	assert.False(t, ok, "a clue only matches its own kind")
}
