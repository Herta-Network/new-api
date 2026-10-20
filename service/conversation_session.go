package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/convo"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
)

const (
	ginKeyConversationSession = "conversation_session"

	// The HKDF info strings separate the keys derived from CryptoSecret.
	// Changing one invalidates what was stored under it, which only costs
	// linking across the change.
	conversationSessionFingerprintInfo = "new-api/conversation-session/fingerprint/v1"
	conversationSessionStoreKeyInfo    = "new-api/conversation-session/store-key/v1"

	// conversationSessionLookupTimeout bounds the up to three lookups one
	// request makes. A slow store degrades to a new session, never to a slow
	// request.
	conversationSessionLookupTimeout = 500 * time.Millisecond
	// conversationSessionMaxBodyBytes bounds the request body that is parsed.
	// Larger bodies are mostly inline media; such a request gets no session.
	conversationSessionMaxBodyBytes = 16 << 20
	// conversationSessionMaxResponseBytes bounds how much of a non-streaming
	// response, or of one stream line, is buffered for observation.
	conversationSessionMaxResponseBytes = 4 << 20
)

// conversationSessionHeaders are conversation identities coding agents send:
// OpenCode's own header, Claude Code's, and the ones Codex sends. A client that
// names its session decides membership; history cannot move it elsewhere.
var conversationSessionHeaders = []string{"X-Opencode-Session", "X-Claude-Code-Session-Id", "Session_id", "Session-Id", "Conversation_id"}

// conversationSessionRuntime is the process-wide recognition state: the clue
// store and the fingerprint key, both derived from CryptoSecret so every
// instance of one deployment agrees on them.
type conversationSessionRuntime struct {
	store       *conversationSessionStore
	fingerprint *convo.Fingerprinter
}

var conversationSessionRuntimeValue atomic.Pointer[conversationSessionRuntime]

func loadConversationSessionRuntime() *conversationSessionRuntime {
	if runtime := conversationSessionRuntimeValue.Load(); runtime != nil {
		return runtime
	}
	capacity := operation_setting.GetConversationSessionSetting().MaxEntries
	if capacity <= 0 {
		capacity = 100_000
	}
	// CryptoSecret is CRYPTO_SECRET, or SESSION_SECRET when that is unset: the
	// server's key for data MACs, which a multi-instance deployment already
	// shares. It is kept apart from login cookies by HKDF.
	secret := []byte(common.CryptoSecret)
	runtime := &conversationSessionRuntime{
		fingerprint: convo.NewFingerprinter(convo.DeriveFingerprintKey(secret, conversationSessionFingerprintInfo)),
		store: &conversationSessionStore{
			memory: hot.NewHotCache[string, conversationSessionRecord](hot.LRU, capacity).Build(),
			redis: func() *redis.Client {
				if common.RedisEnabled && common.RDB != nil {
					return common.RDB
				}
				return nil
			},
			keySecret: convo.DeriveFingerprintKey(secret, conversationSessionStoreKeyInfo),
			ttl:       conversationSessionWindow,
			now:       time.Now,
			runAsync:  gopool.Go,
		},
	}
	conversationSessionRuntimeValue.CompareAndSwap(nil, runtime)
	return conversationSessionRuntimeValue.Load()
}

func conversationSessionWindow() time.Duration {
	seconds := operation_setting.GetConversationSessionSetting().WindowSeconds
	if seconds <= 0 {
		seconds = 24 * 60 * 60
	}
	return time.Duration(seconds) * time.Second
}

func conversationSessionPolicy() convo.Policy {
	policy := convo.DefaultPolicy()
	policy.Window = conversationSessionWindow()
	policy.MaxResponseBytes = conversationSessionMaxResponseBytes
	return policy
}

// conversationProtocolForPath maps a relay route to the protocol whose history
// its body replays. Other routes have no conversation.
func conversationProtocolForPath(path string) (convo.Protocol, bool) {
	switch path {
	case "/v1/chat/completions":
		return convo.OpenAIChat, true
	case "/v1/responses", "/v1/responses/compact":
		return convo.OpenAIResponses, true
	case "/v1/messages":
		return convo.AnthropicMessages, true
	}
	if (strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/")) &&
		(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")) {
		return convo.GeminiGenerateContent, true
	}
	return "", false
}

// conversationSession is one request's conversation session. It is resolved on
// first use, so requests nobody asks about cost no parsing and no lookups.
type conversationSession struct {
	protocol  convo.Protocol
	principal string
	writer    *conversationSessionWriter

	resolved  bool
	sessionID string
	inbound   []convo.Cursor
	// observing is set once the session is known; the writer parses response
	// bytes only from then on. The stream keep-alive goroutine writes pings
	// through the writer, hence atomic.
	observing atomic.Bool
}

// conversationSessionWriter passes every write through unchanged and, once the
// session is resolved, also feeds the bytes to a convo response observer.
// Flush and the rest of gin.ResponseWriter are the embedded writer's.
type conversationSessionWriter struct {
	gin.ResponseWriter
	session  *conversationSession
	observer *convo.ResponseObserver
	declined bool
}

func (writer *conversationSessionWriter) Write(data []byte) (int, error) {
	n, err := writer.ResponseWriter.Write(data)
	if n > 0 && writer.session.observing.Load() {
		writer.observe(data[:n])
	}
	return n, err
}

func (writer *conversationSessionWriter) WriteString(data string) (int, error) {
	n, err := writer.ResponseWriter.WriteString(data)
	if n > 0 && writer.session.observing.Load() {
		writer.observe([]byte(data[:n]))
	}
	return n, err
}

func (writer *conversationSessionWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// observe frames the response by the Content-Type the handler chose: an event
// stream is parsed line by line, anything else as one JSON document. A
// compressed body cannot be read and is not observed.
func (writer *conversationSessionWriter) observe(data []byte) {
	if writer.declined {
		return
	}
	if writer.observer == nil {
		header := writer.Header()
		if header.Get("Content-Encoding") != "" {
			writer.declined = true
			return
		}
		streaming := strings.HasPrefix(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
		writer.observer = conversationSessionPolicy().NewResponseObserver(writer.session.protocol, streaming)
		if writer.observer == nil {
			writer.declined = true
			return
		}
	}
	_, _ = writer.observer.Write(data)
}

// BeginConversationSession attaches a conversation session to a chat,
// responses, messages or Gemini request of an authenticated caller and wraps
// the response writer so the session can learn from the reply. It reports
// false, and leaves the request untouched, when recognition is off or the
// route has no conversation.
func BeginConversationSession(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil || !operation_setting.GetConversationSessionSetting().Enabled {
		return false
	}
	protocol, ok := conversationProtocolForPath(c.Request.URL.Path)
	if !ok {
		return false
	}
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if userID <= 0 {
		return false
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	session := &conversationSession{
		protocol:  protocol,
		principal: "user:" + strconv.Itoa(userID) + ":token:" + strconv.Itoa(tokenID),
	}
	session.writer = &conversationSessionWriter{ResponseWriter: c.Writer, session: session}
	c.Writer = session.writer
	c.Set(ginKeyConversationSession, session)
	return true
}

func conversationSessionOf(c *gin.Context) *conversationSession {
	if c == nil {
		return nil
	}
	value, ok := c.Get(ginKeyConversationSession)
	if !ok {
		return nil
	}
	session, _ := value.(*conversationSession)
	return session
}

// ConversationSessionID returns the conversation session of the request, or ""
// when recognition is off, the route has no conversation, or the body cannot
// be read. The first call recognizes it: an earlier session the request
// continues (named explicitly, by ids the model produced and the client
// echoes, or by the last assistant text), else a new one. It never fails the
// request.
func ConversationSessionID(c *gin.Context) string {
	session := conversationSessionOf(c)
	if session == nil {
		return ""
	}
	if !session.resolved {
		session.resolve(c)
	}
	return session.sessionID
}

func (session *conversationSession) resolve(c *gin.Context) {
	session.resolved = true
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage.Size() > conversationSessionMaxBodyBytes {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}
	policy := conversationSessionPolicy()
	summary, err := policy.InspectBody(session.protocol, body)
	if err != nil {
		return
	}
	for _, name := range conversationSessionHeaders {
		cursor := convo.Cursor{Kind: convo.KindExplicit, Direction: convo.DirectionIn, Value: strings.TrimSpace(c.Request.Header.Get(name))}
		if cursor.Validate() == nil {
			summary.SessionCursor = cursor.Value
			break
		}
	}

	runtime := loadConversationSessionRuntime()
	now := runtime.store.now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationSessionLookupTimeout)
	defer cancel()
	lookup := func(lookupCtx context.Context, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
		return runtime.store.lookup(lookupCtx, session.principal, kind, values, scope)
	}
	decision, err := policy.Resolve(ctx, summary, runtime.fingerprint, lookup, now)
	if err != nil {
		logger.LogWarn(c, common.LogText("conversation session lookup failed, starting a new session: err=%v", err))
		// Resolve stops at the failed lookup; recompute the inbound clues
		// without storage.
		decision, _ = policy.Resolve(ctx, summary, runtime.fingerprint, nil, now)
	}
	if decision.Matched {
		session.sessionID = decision.Match.SessionID
	} else {
		session.sessionID = newConversationSessionID(session.principal, summary)
	}
	session.inbound = decision.PersistentInbound()
	session.observing.Store(true)
}

// newConversationSessionID names a session no stored clue links to. It is
// derived from what the conversation keeps on every turn: the client's
// session, an explicit conversation id, or the first user message. Turns
// whose link was lost (expired, unobserved, or too short to fingerprint)
// therefore still land in the session their first turn got; two conversations
// that open identically share one, as nothing tells their first turns apart.
func newConversationSessionID(principal string, summary convo.RequestSummary) string {
	var seed string
	switch {
	case summary.SessionCursor != "":
		seed = "header:" + summary.SessionCursor
	case len(summary.ExplicitCursors) > 0:
		explicit := summary.ExplicitCursors[0]
		if summary.Stateful && len(summary.ExplicitCursors) > 1 {
			// The first cursor of a stateful request is
			// previous_response_id, which changes every turn.
			explicit = summary.ExplicitCursors[1]
		}
		seed = "explicit:" + explicit
	case summary.FirstUserText != "":
		seed = "opening:" + summary.FirstUserText
	default:
		seed = "new:" + common.GetUUID()
	}
	sum := sha256.Sum256([]byte(principal + "\n" + seed))
	return "cs_" + hex.EncodeToString(sum[:16])
}

// FinishConversationSession stores what a successful call taught about its
// session: the explicit clue it named and the ids and assistant text its
// response produced. It does nothing for a session nobody resolved.
func FinishConversationSession(c *gin.Context) {
	session := conversationSessionOf(c)
	if session == nil || session.sessionID == "" || session.writer.Status() >= http.StatusBadRequest {
		return
	}
	runtime := loadConversationSessionRuntime()
	cursors := session.inbound
	if session.writer.observer != nil {
		output := conversationSessionPolicy().OutputCursors(session.writer.observer.Summary(), runtime.fingerprint)
		cursors = slices.Concat(cursors, output)
	}
	runtime.store.put(session.principal, session.sessionID, cursors)
}
