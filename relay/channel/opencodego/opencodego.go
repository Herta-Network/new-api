// Package opencodego holds what OpenCode Go needs beyond its Advanced Custom
// route preset: the conversation session its inference endpoints require and
// the usage of its subscription.
package opencodego

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// SessionHeader names the conversation. OpenCode Go rejects inference requests
// without it and routes and caches prompts by it.
const SessionHeader = "X-Opencode-Session"

// clientSessionHeaders are conversation identities coding agents already send:
// OpenCode's own header, Claude Code's, and the ones Codex sends.
var clientSessionHeaders = []string{SessionHeader, "X-Claude-Code-Session-Id", "Session_id", "Session-Id", "Conversation_id"}

// SetupRequestHeader identifies the conversation and the calling client the way
// OpenCode Go asks coding agents to: a stable session per conversation, and the
// client's own User-Agent rather than the name of an HTTP library.
func SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) {
	header.Set(SessionHeader, SessionID(c, info))
	userAgent := ""
	if c != nil && c.Request != nil {
		userAgent = strings.TrimSpace(c.Request.UserAgent())
	}
	if userAgent == "" {
		userAgent = "new-api/" + common.Version
	}
	header.Set("User-Agent", userAgent)
}

// SessionID derives a stable session for the conversation, scoped to the user
// and token so two callers never share one. It prefers an identity the client
// sends, then the conversation session the gateway recognized from the history
// and earlier responses, then prompt_cache_key, and otherwise hashes the
// opening of the conversation, which a client that resends the history repeats
// on every turn.
func SessionID(c *gin.Context, info *relaycommon.RelayInfo) string {
	scope := ""
	format := types.RelayFormat("")
	if info != nil {
		scope = fmt.Sprintf("user:%d:token:%d", info.UserId, info.TokenId)
		format = info.RelayFormat
	}

	source := ""
	if c != nil && c.Request != nil {
		for _, name := range clientSessionHeaders {
			if value := strings.TrimSpace(c.Request.Header.Get(name)); value != "" {
				source = "header:" + value
				break
			}
		}
		if source == "" {
			if id := service.ConversationSessionID(c); id != "" {
				source = "conversation:" + id
			}
		}
		if source == "" {
			if storage, err := common.GetBodyStorage(c); err == nil {
				if body, err := storage.Bytes(); err == nil {
					if key := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); key != "" {
						source = "prompt_cache_key:" + key
					} else {
						source = "opening:" + conversationOpening(body, format)
					}
				}
			}
		}
	}

	sum := sha256.Sum256([]byte(scope + "\n" + source))
	return "newapi_" + hex.EncodeToString(sum[:16])
}

// conversationOpening is the system prompt and first user message of a request
// body in the client's protocol.
func conversationOpening(body []byte, format types.RelayFormat) string {
	var system, firstUser gjson.Result
	switch format {
	case types.RelayFormatClaude:
		system = gjson.GetBytes(body, "system")
		firstUser = gjson.GetBytes(body, "messages.0.content")
	case types.RelayFormatGemini:
		system = gjson.GetBytes(body, "systemInstruction.parts")
		if !system.Exists() {
			system = gjson.GetBytes(body, "system_instruction.parts")
		}
		firstUser = gjson.GetBytes(body, "contents.0.parts")
	case types.RelayFormatOpenAIResponses:
		system = gjson.GetBytes(body, "instructions")
		firstUser = gjson.GetBytes(body, "input")
		if firstUser.IsArray() {
			firstUser = gjson.GetBytes(body, `input.#(role=="user").content`)
		}
	default:
		system = gjson.GetBytes(body, `messages.#(role=="system").content`)
		if !system.Exists() {
			system = gjson.GetBytes(body, `messages.#(role=="developer").content`)
		}
		firstUser = gjson.GetBytes(body, `messages.#(role=="user").content`)
	}
	return contentText(system) + "\n" + contentText(firstUser)
}

// contentText reads the text of message content: a string, or the text parts
// of a content array, which OpenAI, Anthropic and Gemini all name text.
func contentText(content gjson.Result) string {
	if !content.IsArray() {
		return content.String()
	}
	var text strings.Builder
	for _, part := range content.Array() {
		text.WriteString(part.Get("text").String())
	}
	return text.String()
}

// maxUsageResponseBytes bounds the usage response read into memory.
const maxUsageResponseBytes = 64 << 10

// FetchUsage reads how much of each allowance window apiKey has used. Upstream
// refusals return a *common.Message for the web console; other errors are
// transport or payload failures for the server log.
func FetchUsage(ctx context.Context, client *http.Client, baseURL string, apiKey string, now time.Time) (*Usage, error) {
	// The usage route lives on the origin of the inference base URL, so a
	// regional host or proxy keeps working.
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid OpenCode Go base URL: %q", baseURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.Scheme+"://"+parsed.Host+"/zen/go/v1/usage", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "new-api/"+common.Version)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, common.NewMessage("OpenCode Go rejected the API key")
	case http.StatusForbidden:
		// OpenCode answers 403 for a valid key without a Go subscription.
		return nil, common.NewMessage("This API key has no OpenCode Go subscription")
	default:
		return nil, common.NewMessage("Upstream returned status {{status}}", map[string]any{"status": response.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUsageResponseBytes))
	if err != nil {
		return nil, err
	}
	return decodeUsage(body, now)
}

// UsageWindow is one allowance window, named like the Codex usage windows the
// web console already renders.
type UsageWindow struct {
	Status             string  `json:"status,omitempty"`
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at,omitempty"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds,omitempty"`
}

// Usage is how much of each allowance window an OpenCode Go key has used. Each
// window allows a share of the plan's monthly dollar allowance.
type Usage struct {
	FiveHour     *UsageWindow `json:"five_hour,omitempty"`
	Weekly       *UsageWindow `json:"weekly,omitempty"`
	Monthly      *UsageWindow `json:"monthly,omitempty"`
	LimitReached bool         `json:"limit_reached"`
}

// decodeUsage reads the GET /zen/go/v1/usage response:
//
//	{"usage":{"rolling":{"status":"ok","percent":12,"resetsAt":"…"},"weekly":{…},"monthly":{…}}}
//
// rolling is the 5-hour window. A window at 0% carries a placeholder resetsAt,
// which is dropped.
func decodeUsage(body []byte, now time.Time) (*Usage, error) {
	var payload struct {
		Usage map[string]struct {
			Status   string          `json:"status"`
			Percent  json.Number     `json:"percent"`
			ResetsAt json.RawMessage `json:"resetsAt"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid OpenCode Go usage response: %w", err)
	}

	usage := &Usage{}
	windows := []struct {
		name    string
		seconds int64
		target  **UsageWindow
	}{
		{name: "rolling", seconds: 5 * 60 * 60, target: &usage.FiveHour},
		{name: "weekly", seconds: 7 * 24 * 60 * 60, target: &usage.Weekly},
		{name: "monthly", seconds: 30 * 24 * 60 * 60, target: &usage.Monthly},
	}
	for _, window := range windows {
		reported, ok := payload.Usage[window.name]
		if !ok {
			continue
		}
		percent, err := reported.Percent.Float64()
		if err != nil {
			continue
		}
		decoded := &UsageWindow{Status: reported.Status, UsedPercent: percent, LimitWindowSeconds: window.seconds}
		var resetsAt string
		if percent > 0 && common.Unmarshal(reported.ResetsAt, &resetsAt) == nil {
			if resetAt, err := time.Parse(time.RFC3339Nano, resetsAt); err == nil && resetAt.After(now) {
				decoded.ResetAt = resetAt.Unix()
				decoded.ResetAfterSeconds = int64(resetAt.Sub(now).Seconds())
			}
		}
		*window.target = decoded
		if percent >= 100 || reported.Status == "rate-limited" {
			usage.LimitReached = true
		}
	}
	if usage.FiveHour == nil && usage.Weekly == nil && usage.Monthly == nil {
		return nil, errors.New("OpenCode Go usage response has no usage windows")
	}
	return usage, nil
}
