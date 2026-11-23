package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// A routing seed keeps the turns of one conversation on the same channel and
// key, so the upstream prompt cache of that account and the reasoning it
// signed stay usable. The seed comes from the first of: a session rule's key,
// a session header, the request's own cache key, the digest of its system
// prompt and tools, or the digest of its first user message. It is scoped to
// the user and the requested model, and never leaves the gateway. The design
// follows the cache-aware routing of Herta Network's Nexus relay.

const (
	routeSeedDomain = "new-api:route-seed:v1"
	// routeTemplateMinBytes is the size a system prompt and tools need before
	// they seed routing; a short or generic preamble says less about the
	// conversation than its first user message.
	routeTemplateMinBytes = 1024
	// routeSectionMaxBytes bounds each part of the request that is hashed.
	routeSectionMaxBytes = 256 << 10
	// routeInspectMaxBodyBytes bounds the body scanned for prompts and earlier
	// turns. Larger bodies are mostly inline media.
	routeInspectMaxBodyBytes = 16 << 20
)

// Seed sources as routing logs name them; rule and header sources add the
// rule or header name.
const (
	routeSourceRandom    = "random"
	routeSourceTemplate  = "template"
	routeSourceFirstUser = "first_user"
)

// routeHistory tells whether a request continues a conversation: it carries
// earlier assistant output, or refers to a stored response.
type routeHistory string

const (
	routeHistoryNone    routeHistory = "none"
	routeHistoryPresent routeHistory = "present"
	// routeHistoryUnknown is a body too large, or not JSON, to look into.
	routeHistoryUnknown routeHistory = "unknown"
)

type routeSeed struct {
	digest [32]byte
	source string
}

func (seed routeSeed) random() bool { return seed.source == routeSourceRandom }

// fingerprint names the seed in logs and cache statistics without revealing
// the value it came from.
func (seed routeSeed) fingerprint() string {
	if seed.random() {
		return ""
	}
	return hex.EncodeToString(seed.digest[:4])
}

// newRandomRouteSeed seeds a request nothing identifies. Ranking candidates by
// a random seed is a weighted random draw, and failing over to the next rank
// keeps drawing from the remaining weight.
func newRandomRouteSeed() routeSeed {
	seed := routeSeed{source: routeSourceRandom}
	_, _ = rand.Read(seed.digest[:])
	return seed
}

func scopedRouteSeed(userID int, modelName, source string, parts ...[]byte) routeSeed {
	hash := sha256.New()
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(userID))
	for _, part := range [][]byte{[]byte(routeSeedDomain), id[:], []byte(modelName), []byte(source)} {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	for _, part := range parts {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	seed := routeSeed{source: source}
	hash.Sum(seed.digest[:0])
	return seed
}

// routeRule is the session rule a request matched.
type routeRule struct {
	name       string
	mode       string
	modeSource string
	template   map[string]any
}

// routeRequest is what routing reads from one request.
type routeRequest struct {
	seed    routeSeed
	history routeHistory
	rule    *routeRule
}

// inspectRouteRequest reads the routing seed of the request on c, whether it
// carries earlier turns, and the session rule it matched. body, when not nil,
// is read instead of the request's body storage: a Responses WebSocket create
// is not the connection's HTTP request.
func inspectRouteRequest(c *gin.Context, modelName string, body []byte) routeRequest {
	request := routeRequest{seed: newRandomRouteSeed(), history: routeHistoryNone}
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled || c == nil || c.Request == nil {
		return request
	}
	userID := c.GetInt("id")
	path := c.Request.URL.Path
	seeded := false
	if rule, value, found := matchChannelAffinityRule(c, setting, modelName, path); found {
		mode, modeSource := EffectiveSessionMode(setting, rule)
		request.rule = &routeRule{name: rule.Name, mode: mode, modeSource: modeSource, template: cloneStringAnyMap(rule.ParamOverrideTemplate)}
		if mode != "off" {
			request.seed = scopedRouteSeed(userID, modelName, "rule:"+rule.Name, []byte(value))
			seeded = true
		}
	}
	if !seeded {
		for _, name := range constant.ConversationSessionHeaders {
			if value := strings.TrimSpace(c.Request.Header.Get(name)); value != "" {
				request.seed = scopedRouteSeed(userID, modelName, "header:"+name, []byte(value))
				seeded = true
				break
			}
		}
	}
	// Only a strict rule asks whether the request carries earlier turns.
	needHistory := request.rule != nil && request.rule.mode == "strict"
	if seeded && !needHistory {
		return request
	}
	protocol := routeProtocolOf(path)
	if protocol == routeProtocolNone {
		request.history = routeHistoryUnknown
		return request
	}
	shape := readRouteShape(c, body, protocol, !seeded && setting.DeriveFromPrompt, needHistory)
	request.history = shape.history
	if seeded {
		return request
	}
	switch {
	case shape.explicitValue != "":
		request.seed = scopedRouteSeed(userID, modelName, shape.explicitSource, []byte(shape.explicitValue))
	case setting.DeriveFromPrompt && setting.TemplateSpread > 0 && len(shape.template) >= routeTemplateMinBytes:
		parts := [][]byte{shape.template}
		if setting.TemplateSpread > 1 {
			// The first user message never changes within a conversation, so
			// each conversation keeps its bucket.
			var bucket [8]byte
			if len(shape.firstUser) > 0 {
				sum := sha256.Sum256(shape.firstUser)
				binary.BigEndian.PutUint64(bucket[:], binary.BigEndian.Uint64(sum[:8])%uint64(setting.TemplateSpread))
			}
			parts = append(parts, bucket[:])
		}
		request.seed = scopedRouteSeed(userID, modelName, routeSourceTemplate, parts...)
	case setting.DeriveFromPrompt && len(shape.firstUser) > 0:
		request.seed = scopedRouteSeed(userID, modelName, routeSourceFirstUser, shape.firstUser)
	}
	return request
}

type routeProtocol int

const (
	routeProtocolNone routeProtocol = iota
	routeProtocolChat
	routeProtocolResponses
	routeProtocolMessages
	routeProtocolGemini
)

func routeProtocolOf(path string) routeProtocol {
	switch {
	case strings.Contains(path, "/chat/completions"):
		return routeProtocolChat
	case strings.HasPrefix(path, "/v1/responses"):
		return routeProtocolResponses
	case strings.HasPrefix(path, "/v1/messages"):
		return routeProtocolMessages
	case strings.Contains(path, ":generateContent"), strings.Contains(path, ":streamGenerateContent"):
		return routeProtocolGemini
	}
	return routeProtocolNone
}

// routeShape is what routing reads from a request body.
type routeShape struct {
	explicitSource string
	explicitValue  string
	history        routeHistory
	template       []byte
	firstUser      []byte
}

// explicitRoutePath is the body field where a client names its conversation
// for prompt caching.
func explicitRoutePath(protocol routeProtocol) string {
	switch protocol {
	case routeProtocolChat, routeProtocolResponses:
		return "prompt_cache_key"
	case routeProtocolMessages:
		return "metadata.user_id"
	}
	return ""
}

// readRouteShape reads the explicit cache key, the earlier turns and, with
// derive, the prompt digests from the request body. It reads the top-level
// members without copying or decoding the body, and walks the messages only
// for their roles and text. With needHistory, a body that is not valid JSON
// leaves the history unknown.
func readRouteShape(c *gin.Context, body []byte, protocol routeProtocol, derive, needHistory bool) routeShape {
	shape := routeShape{history: routeHistoryUnknown}
	explicitPath := explicitRoutePath(protocol)
	if body == nil {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return shape
		}
		if storage.Size() > routeInspectMaxBodyBytes {
			// A plain member path is read by streaming, without loading the body.
			if explicitPath != "" {
				if values, err := common.ReadBodyJSONFields(storage, explicitPath); err == nil {
					shape.setExplicit(explicitPath, values[0])
				}
			}
			return shape
		}
		if body, err = storage.Bytes(); err != nil {
			return shape
		}
	}
	if len(body) > routeInspectMaxBodyBytes {
		if explicitPath != "" {
			shape.setExplicit(explicitPath, ojson.GetManyBytesNoCopy(nil, body, explicitPath)[0])
		}
		return shape
	}
	if needHistory && !ojson.ValidBytes(body) {
		return shape
	}
	var system, firstUser []byte
	foundUser := false
	// walk reads the role of each message, the text of the system ones, and
	// the first user message; history is an earlier turn followed by more.
	walk := func(messages []ojson.Result, roleOf func(ojson.Result) string, earlier func(ojson.Result, string) bool, textOf func(ojson.Result) ojson.Result) {
		shape.history = routeHistoryNone
		for i, message := range messages {
			role := roleOf(message)
			if i < len(messages)-1 && earlier(message, role) {
				shape.history = routeHistoryPresent
			}
			if !derive {
				continue
			}
			switch {
			case role == "system" || role == "developer":
				system = appendRouteText(system, textOf(message))
			case role == "user" && !foundUser:
				foundUser = true
				firstUser = appendRouteText(nil, textOf(message))
			}
		}
	}
	role := func(message ojson.Result) string { return message.Get("role").String() }
	content := func(message ojson.Result) ojson.Result { return message.Get("content") }
	var tools ojson.Result
	switch protocol {
	case routeProtocolChat:
		values := ojson.GetManyBytesNoCopy(nil, body, explicitPath, "messages", "tools")
		shape.setExplicit(explicitPath, values[0])
		walk(values[1].Array(), role, func(_ ojson.Result, role string) bool { return role == "assistant" }, content)
		tools = values[2]
	case routeProtocolMessages:
		values := ojson.GetManyBytesNoCopy(nil, body, explicitPath, "messages", "system", "tools")
		shape.setExplicit(explicitPath, values[0])
		system = appendRouteText(nil, values[2])
		walk(values[1].Array(), role, func(_ ojson.Result, role string) bool { return role == "assistant" }, content)
		tools = values[3]
	case routeProtocolResponses:
		values := ojson.GetManyBytesNoCopy(nil, body, explicitPath, "previous_response_id", "input", "instructions", "tools")
		shape.setExplicit(explicitPath, values[0])
		system = appendRouteText(nil, values[3])
		input := values[2]
		if input.Type == ojson.String {
			shape.history = routeHistoryNone
			firstUser, foundUser = appendRouteText(nil, input), true
		} else {
			walk(input.Array(), role, func(item ojson.Result, role string) bool {
				return role == "assistant" || isResponsesHistoryItem(item.Get("type").String())
			}, content)
		}
		if previous := values[1]; previous.Type == ojson.String && strings.TrimSpace(previous.String()) != "" {
			shape.history = routeHistoryPresent
		}
		tools = values[4]
	case routeProtocolGemini:
		values := ojson.GetManyBytesNoCopy(nil, body, "contents", "systemInstruction", "system_instruction", "tools")
		system = appendRouteText(appendRouteText(nil, values[1].Get("parts")), values[2].Get("parts"))
		walk(values[0].Array(), role, func(_ ojson.Result, role string) bool { return role == "model" }, func(message ojson.Result) ojson.Result {
			return message.Get("parts")
		})
		tools = values[3]
	}
	if derive {
		shape.template = routeTemplate(system, tools)
		shape.firstUser = routeCap(firstUser)
	}
	return shape
}

func (shape *routeShape) setExplicit(source string, value ojson.Result) {
	if value.Type != ojson.String {
		return
	}
	if text := strings.TrimSpace(value.String()); text != "" {
		shape.explicitSource, shape.explicitValue = source, text
	}
}

// isResponsesHistoryItem reports a Responses input item that only an earlier
// turn produces: model output such as reasoning or a tool call, a tool
// result, or a reference to a stored item.
func isResponsesHistoryItem(itemType string) bool {
	switch itemType {
	case "reasoning", "item_reference", "compaction":
		return true
	}
	return strings.HasSuffix(itemType, "_call") || strings.HasSuffix(itemType, "_call_output") || strings.HasPrefix(itemType, "mcp_")
}

// routeTemplate joins the text of the system prompt with the tool
// definitions.
func routeTemplate(system []byte, tools ojson.Result) []byte {
	template := routeCap(system)
	if definitions := routeTools(tools); len(definitions) > 0 {
		template = append(template, 0)
		template = append(template, routeCap(definitions)...)
	}
	return template
}

// routeTools is the tool definitions as sent, which one client repeats byte
// for byte on every turn, without the cache_control member of each tool,
// which clients add and move between turns.
func routeTools(tools ojson.Result) []byte {
	var definitions []byte
	for _, tool := range tools.Array() {
		if !tool.IsObject() {
			definitions = append(append(definitions, tool.Raw...), '\n')
			continue
		}
		tool.ForEach(func(key, value ojson.Result) bool {
			if key.String() != "cache_control" {
				definitions = append(append(append(append(definitions, key.Raw...), ':'), value.Raw...), ',')
			}
			return true
		})
		definitions = append(definitions, '\n')
	}
	return definitions
}

// appendRouteText appends the text a message content carries: a string, or
// the text of each part of an array. Other parts, such as images, and markers
// such as cache_control, which clients move between turns, are left out.
func appendRouteText(dst []byte, content ojson.Result) []byte {
	switch {
	case content.Type == ojson.String:
		return append(append(dst, content.String()...), '\n')
	case content.IsArray():
		for _, part := range content.Array() {
			if part.Type == ojson.String {
				dst = append(append(dst, part.String()...), '\n')
			} else if text := part.Get("text"); text.Type == ojson.String {
				dst = append(append(dst, text.String()...), '\n')
			}
		}
	}
	return dst
}

func routeCap(section []byte) []byte {
	if len(section) > routeSectionMaxBytes {
		return section[:routeSectionMaxBytes]
	}
	return section
}
