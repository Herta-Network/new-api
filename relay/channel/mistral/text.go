package mistral

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// chatRequest is the body of Mistral's POST /v1/chat/completions, with its
// members in the order the Mistral SDK Vibe uses writes them (mistralai 3.5.0,
// ChatCompletionStreamRequest and ChatCompletionRequest). Mistral refuses
// members it does not know, so nothing else is sent. The types below follow
// the SDK's models member by member, and DoRequest finishes the body as the
// SDK's JSON encoder writes it (pythonJSON).
type chatRequest struct {
	Model             string          `json:"model"`
	Messages          []chatMessage   `json:"messages"`
	Temperature       *sdkFloat       `json:"temperature,omitempty"`
	TopP              *sdkFloat       `json:"top_p,omitempty"`
	MaxTokens         *uint           `json:"max_tokens,omitempty"`
	Stream            bool            `json:"stream"`
	Stop              any             `json:"stop,omitempty"`
	RandomSeed        json.RawMessage `json:"random_seed,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	ResponseFormat    *responseFormat `json:"response_format,omitempty"`
	Tools             []chatTool      `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	PresencePenalty   *sdkFloat       `json:"presence_penalty,omitempty"`
	FrequencyPenalty  *sdkFloat       `json:"frequency_penalty,omitempty"`
	MinTokens         *uint           `json:"min_tokens,omitempty"`
	RepetitionPenalty any             `json:"repetition_penalty,omitempty"`
	TopK              *int            `json:"top_k,omitempty"`
	LogProbs          *bool           `json:"logprobs,omitempty"`
	TopLogProbs       *int            `json:"top_logprobs,omitempty"`
	N                 *int            `json:"n,omitempty"`
	Prediction        any             `json:"prediction,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort   string          `json:"reasoning_effort,omitempty"`
	PromptMode        json.RawMessage `json:"prompt_mode,omitempty"`
	Guardrails        json.RawMessage `json:"guardrails,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	ServiceTier       json.RawMessage `json:"service_tier,omitempty"`
	SafePrompt        json.RawMessage `json:"safe_prompt,omitempty"`
}

// chatMessage is one Mistral message. Content is a string, a list of chunks,
// or null for an assistant turn that only calls tools.
type chatMessage struct {
	Role       string
	Content    any
	ToolCalls  []toolCall
	Prefix     bool
	ToolCallID string
	Name       string
}

// MarshalJSON writes a message's members in the order the SDK's model of its
// role has them: content before the role but on an assistant turn, which
// always carries prefix.
func (m chatMessage) MarshalJSON() ([]byte, error) {
	switch m.Role {
	case "assistant":
		return common.Marshal(struct {
			Role      string     `json:"role"`
			Content   any        `json:"content"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
			Prefix    bool       `json:"prefix"`
		}{m.Role, m.Content, m.ToolCalls, m.Prefix})
	case "tool":
		return common.Marshal(struct {
			Content    any    `json:"content"`
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id,omitempty"`
			Name       string `json:"name,omitempty"`
		}{m.Content, m.Role, m.ToolCallID, m.Name})
	}
	return common.Marshal(struct {
		Content any    `json:"content"`
		Role    string `json:"role"`
	}{m.Content, m.Role})
}

// toolCall is a call of an assistant turn. Index is the SDK's default, 0:
// Vibe sets none, so every call goes out with it.
type toolCall struct {
	Function toolCallFunction `json:"function"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Index    int              `json:"index"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Function chatFunction `json:"function"`
	Type     string       `json:"type"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Parameters  json.RawMessage `json:"parameters"`
	Description string          `json:"description,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// toolChoice names the one function the model must call.
type toolChoice struct {
	Function functionName `json:"function"`
	Type     string       `json:"type"`
}

type functionName struct {
	Name string `json:"name"`
}

type responseFormat struct {
	Type       string      `json:"type,omitempty"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name        string          `json:"name"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Description *string         `json:"description,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type prediction struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// The chunks of a message's content.
type (
	textChunk struct {
		Text string `json:"text"`
		Type string `json:"type"`
	}
	imageURLChunk struct {
		ImageURL imageURL `json:"image_url"`
		Type     string   `json:"type"`
	}
	imageURL struct {
		URL    string `json:"url"`
		Detail string `json:"detail,omitempty"`
	}
	audioChunk struct {
		InputAudio string `json:"input_audio"`
		Type       string `json:"type"`
	}
	fileChunk struct {
		FileID string `json:"file_id"`
		Type   string `json:"type"`
	}
	documentURLChunk struct {
		DocumentURL  string `json:"document_url"`
		Type         string `json:"type"`
		DocumentName string `json:"document_name,omitempty"`
	}
	thinkChunk struct {
		Thinking  []any   `json:"thinking"`
		Type      string  `json:"type"`
		Signature *string `json:"signature,omitempty"`
		Closed    *bool   `json:"closed,omitempty"`
	}
	referenceChunk struct {
		ReferenceIDs json.RawMessage `json:"reference_ids"`
		Type         string          `json:"type"`
	}
)

// chatMembers are the members of a chat request that reach Mistral, under
// their own name or Mistral's.
var chatMembers = map[string]bool{
	"model": true, "messages": true, "stream": true, "temperature": true, "top_p": true,
	"max_tokens": true, "max_completion_tokens": true, "stop": true, "seed": true,
	"metadata": true, "response_format": true, "tools": true, "tool_choice": true, "functions": true,
	"function_call": true, "presence_penalty": true, "frequency_penalty": true, "min_tokens": true,
	"repetition_penalty": true, "top_k": true, "logprobs": true, "top_logprobs": true, "n": true,
	"prediction": true, "parallel_tool_calls": true, "reasoning_effort": true,
	"prompt_cache_key": true, "service_tier": true,
	// The OpenAI adaptor already rendered nested reasoning into reasoning_effort.
	"reasoning": true,
}

// ignoredMembers are left out without a word because they never change the
// answer: Mistral reports usage on every stream, stores nothing, and has no
// end-user identifiers or prompt cache options.
var ignoredMembers = map[string]bool{
	"stream_options": true, "user": true, "store": true, "safety_identifier": true,
	"prompt_cache_options": true, "prompt_cache_retention": true,
}

// lossReport collects what a request loses on its way to Mistral.
type lossReport []types.ConversionDiagnostic

func (r *lossReport) add(code, path, message string, severity types.ConversionDiagnosticSeverity) {
	*r = append(*r, types.ConversionDiagnostic{
		Code: code, Path: path, Message: message, Severity: severity,
		From: types.RelayFormatOpenAI, To: types.RelayFormat(ChannelName),
	})
}

// clientError is a request Mistral cannot serve whatever the channel.
func clientError(format string, args ...any) error {
	return types.NewErrorWithStatusCode(fmt.Errorf(format, args...), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// convertChatRequest builds the Mistral body of a chat request the OpenAI
// adaptor has normalized, the way Vibe writes its own requests: temperature
// 1.0 and Vibe's reasoning effort for its default models when the client
// set none, the effort fitted to the levels the model takes, parallel tool
// calls on, earlier reasoning replayed as thinking chunks, images as objects
// and at most eight of them. Members Mistral lacks are dropped and reported,
// as are adjusted efforts and older images; the channel's tool loss policy
// decides whether a loss refuses the request.
func convertChatRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (*chatRequest, error) {
	var loss lossReport
	model := request.Model
	out := &chatRequest{
		Model:             model,
		Temperature:       sdkFloatOf(request.Temperature),
		TopP:              sdkFloatOf(request.TopP),
		Stream:            request.Stream != nil && *request.Stream,
		Stop:              request.Stop,
		Metadata:          request.Metadata,
		PresencePenalty:   sdkFloatOf(request.PresencePenalty),
		FrequencyPenalty:  sdkFloatOf(request.FrequencyPenalty),
		MinTokens:         request.MinTokens,
		TopK:              request.TopK,
		LogProbs:          request.LogProbs,
		TopLogProbs:       request.TopLogProbs,
		N:                 request.N,
		ParallelToolCalls: request.ParallelTooCalls,
		PromptCacheKey:    request.PromptCacheKey,
		ServiceTier:       request.ServiceTier,
	}
	if penalty := gjson.ParseBytes(request.RepetitionPenalty); penalty.Type == gjson.Number {
		out.RepetitionPenalty = sdkFloat(penalty.Float())
	} else if len(request.RepetitionPenalty) > 0 {
		out.RepetitionPenalty = request.RepetitionPenalty
	}
	// The SDK writes a JSON schema format's members in its own order, and a
	// prediction with its type, which defaults to content.
	if format := request.ResponseFormat; format != nil {
		out.ResponseFormat = &responseFormat{Type: format.Type}
		if schema := gjson.ParseBytes(format.JsonSchema); schema.IsObject() {
			out.ResponseFormat.JSONSchema = &jsonSchema{Name: schema.Get("name").Str, Schema: json.RawMessage(schema.Get("schema").Raw)}
			if description := schema.Get("description"); description.Type == gjson.String {
				out.ResponseFormat.JSONSchema.Description = &description.Str
			}
			if strict := schema.Get("strict"); strict.IsBool() {
				value := strict.Bool()
				out.ResponseFormat.JSONSchema.Strict = &value
			}
		}
	}
	if predicted := gjson.ParseBytes(request.Prediction); predicted.IsObject() && (!predicted.Get("content").Exists() || predicted.Get("content").Type == gjson.String) {
		out.Prediction = prediction{Type: cmp.Or(predicted.Get("type").Str, "content"), Content: predicted.Get("content").Str}
	} else if len(request.Prediction) > 0 {
		out.Prediction = request.Prediction
	}
	// Members of Mistral's own chat API come from the body of a client that
	// speaks it, such as Vibe or the Mistral SDK pointed at the gateway; the
	// gateway's shared request type does not carry them, so no other channel
	// sends them.
	if info.RelayFormat == types.RelayFormatOpenAI {
		native := clientMembers(c, "random_seed", "safe_prompt", "prompt_mode", "guardrails")
		out.RandomSeed = native["random_seed"]
		out.SafePrompt = native["safe_prompt"]
		out.PromptMode = native["prompt_mode"]
		out.Guardrails = native["guardrails"]
	}
	// n multiplies the answer while the gateway reserves quota for the prompt
	// alone, so it stays within what Mistral serves.
	if out.N != nil && (*out.N < 1 || *out.N > maxChoices) {
		return nil, clientError("Mistral does not support the requested n %d: it returns 1 to %d choices", *out.N, maxChoices)
	}
	if out.Temperature == nil {
		temperature := sdkFloat(defaultTemperature)
		out.Temperature = &temperature
	}
	if request.MaxTokens != nil || request.MaxCompletionTokens != nil {
		maxTokens := request.GetMaxTokens()
		out.MaxTokens = &maxTokens
	}
	if len(out.RandomSeed) == 0 && request.Seed != nil {
		seed := *request.Seed
		if seed != math.Trunc(seed) || math.Abs(seed) > 1<<53 {
			return nil, clientError("Mistral does not support the requested seed %v: it takes an integer", seed)
		}
		out.RandomSeed = json.RawMessage(fmt.Sprintf("%d", int64(seed)))
	}

	effort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	if effort == "" {
		effort = defaultReasoningEffort[model]
	}
	support, known := modelReasoning[model]
	out.ReasoningEffort = fitReasoningEffort(effort, support, known)
	if out.ReasoningEffort != effort {
		loss.add("reasoning_effort_adjusted", "reasoning_effort",
			fmt.Sprintf("%s takes reasoning_effort %q in place of %q", model, out.ReasoningEffort, effort), types.ConversionDiagnosticWarning)
	}
	info.SetReasoningEffort(out.ReasoningEffort)
	// Earlier reasoning goes back to a model known to reason or, for a model
	// Vibe does not know, when the request asks it to reason.
	replayReasoning := len(support.levels) > 0 || !known && out.ReasoningEffort != "" && out.ReasoningEffort != "none"

	out.Messages = convertMessages(request.Messages, replayReasoning, &loss)
	keepRecentImages(out.Messages, &loss)
	convertTools(request, out, &loss)
	if out.ParallelToolCalls == nil && len(out.Tools) > 0 {
		parallel := true
		out.ParallelToolCalls = &parallel
	}

	raw, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	gjson.ParseBytes(raw).ForEach(func(key, _ gjson.Result) bool {
		switch {
		case chatMembers[key.Str] || ignoredMembers[key.Str]:
		case key.Str == "web_search_options":
			// A hosted web search is a tool the model loses.
			loss.add("hosted_web_search_omitted", key.Str, "Mistral's chat completions have no hosted web search; it was left out", types.ConversionDiagnosticError)
		default:
			loss.add("unsupported_field", key.Str, "Mistral has no "+key.Str+" option; it was left out", types.ConversionDiagnosticWarning)
		}
		return true
	})
	info.RecordConversionDiagnostics(c, loss)
	if err := types.RejectConversionLoss(info.ConvOptions().EffectiveToolLossPolicy(), loss); err != nil {
		return nil, err
	}
	return out, nil
}

// convertMessages writes chat messages as Mistral messages, as Vibe does:
// developer messages as system ones, assistant turns that only call tools
// with null content, tool results with the name of the function they answer.
func convertMessages(messages []dto.Message, replayReasoning bool, loss *lossReport) []chatMessage {
	out := make([]chatMessage, 0, len(messages))
	functionNames := make(map[string]string)
	// unanswered lists the calls of the last assistant turn without a
	// result yet, so a result that names no call answers the next of them.
	var unanswered []string
	for i, message := range messages {
		path := fmt.Sprintf("messages.%d.content", i)
		converted := chatMessage{Role: message.Role}
		content := convertContent(message.Content, path, loss)
		switch message.Role {
		case "developer":
			converted.Role = "system"
		case "assistant":
			// Only an assistant message can be the prefix of the answer.
			converted.Prefix = message.Prefix != nil && *message.Prefix
			unanswered = nil
			for j, call := range message.ParseToolCalls() {
				// Mistral takes the client's ids as they are; a call without
				// one gets an id from its place in the conversation, which a
				// turn sent again repeats.
				id := call.ID
				if id == "" {
					id = derivedToolCallID(i, j)
				}
				unanswered = append(unanswered, id)
				functionNames[id] = call.Function.Name
				arguments := call.Function.Arguments
				if arguments == "" {
					arguments = "{}"
				}
				converted.ToolCalls = append(converted.ToolCalls, toolCall{
					Function: toolCallFunction{Name: call.Function.Name, Arguments: arguments}, ID: id, Type: "function",
				})
			}
			reasoning := ""
			if message.ReasoningContent != nil {
				reasoning = *message.ReasoningContent
			} else if message.Reasoning != nil {
				reasoning = *message.Reasoning
			}
			if replayReasoning && reasoning != "" {
				chunks := []any{thinkChunk{Thinking: []any{textChunk{Text: reasoning, Type: "text"}}, Type: "thinking"}}
				switch content := content.(type) {
				case string:
					if content != "" {
						chunks = append(chunks, textChunk{Text: content, Type: "text"})
					}
				case []any:
					chunks = append(chunks, content...)
				}
				content = chunks
			}
			if text, ok := content.(string); ok && text == "" {
				content = nil
			}
		case "tool":
			converted.ToolCallID = message.ToolCallId
			if converted.ToolCallID == "" && len(unanswered) > 0 {
				converted.ToolCallID = unanswered[0]
			}
			unanswered = slices.DeleteFunc(unanswered, func(id string) bool { return id == converted.ToolCallID })
			converted.Name = functionNames[converted.ToolCallID]
			if message.Name != nil && *message.Name != "" {
				converted.Name = *message.Name
			}
		}
		if content == nil && converted.Role != "assistant" {
			content = ""
		}
		converted.Content = content
		out = append(out, converted)
	}
	return out
}

// convertContent writes OpenAI message content as Mistral content: text
// alone as one string, its parts joined by a blank line as Vibe joins them;
// otherwise a list of chunks, with images as image_url objects, audio as its
// base64 data, files as Mistral file or document chunks, and Mistral's own
// chunks (thinking, document_url, reference) with the members the SDK has.
func convertContent(content any, path string, loss *lossReport) any {
	switch content := content.(type) {
	case nil:
		return nil
	case string:
		return content
	}
	raw, err := common.Marshal(content)
	if err != nil {
		return content
	}
	parts := gjson.ParseBytes(raw)
	if !parts.IsArray() {
		return json.RawMessage(raw)
	}
	var chunks []any
	var texts []string
	textOnly := true
	for _, part := range parts.Array() {
		kind := part.Get("type").Str
		switch kind {
		case dto.ContentTypeText:
			text := part.Get("text").Str
			texts = append(texts, text)
			chunks = append(chunks, textChunk{Text: text, Type: "text"})
			continue
		case dto.ContentTypeImageURL:
			image := part.Get("image_url")
			url := image.Get("url").Str
			if image.Type == gjson.String {
				url = image.Str
			}
			chunks = append(chunks, imageURLChunk{ImageURL: imageURL{URL: url, Detail: image.Get("detail").Str}, Type: "image_url"})
		case dto.ContentTypeInputAudio:
			audio := part.Get("input_audio")
			data := audio.Get("data").Str
			if audio.Type == gjson.String {
				data = audio.Str
			}
			chunks = append(chunks, audioChunk{InputAudio: data, Type: "input_audio"})
		case dto.ContentTypeFile:
			file := part.Get("file")
			switch fileID := cmp.Or(file.Get("file_id").Str, part.Get("file_id").Str); {
			case fileID != "":
				chunks = append(chunks, fileChunk{FileID: fileID, Type: "file"})
			case file.Get("file_data").Str != "":
				chunks = append(chunks, documentURLChunk{DocumentURL: file.Get("file_data").Str, Type: "document_url", DocumentName: file.Get("filename").Str})
			default:
				loss.add("unsupported_media_source", path, "a file part without file_id or file_data was left out", types.ConversionDiagnosticWarning)
				continue
			}
		case "document_url":
			chunks = append(chunks, documentURLChunk{DocumentURL: part.Get("document_url").Str, Type: kind, DocumentName: part.Get("document_name").Str})
		case "reference":
			chunks = append(chunks, referenceChunk{ReferenceIDs: json.RawMessage(cmp.Or(part.Get("reference_ids").Raw, "[]")), Type: kind})
		case "thinking":
			// Mistral's reasoning of an earlier turn: text and reference
			// chunks, with the signature and closed flag it came with.
			thinking := thinkChunk{Thinking: []any{}, Type: kind}
			for _, item := range part.Get("thinking").Array() {
				if item.Get("type").Str == "reference" {
					thinking.Thinking = append(thinking.Thinking, referenceChunk{ReferenceIDs: json.RawMessage(cmp.Or(item.Get("reference_ids").Raw, "[]")), Type: "reference"})
					continue
				}
				text := item.Get("text").Str
				if item.Type == gjson.String {
					text = item.Str
				}
				thinking.Thinking = append(thinking.Thinking, textChunk{Text: text, Type: "text"})
			}
			if signature := part.Get("signature"); signature.Type == gjson.String {
				thinking.Signature = &signature.Str
			}
			if closed := part.Get("closed"); closed.IsBool() {
				value := closed.Bool()
				thinking.Closed = &value
			}
			chunks = append(chunks, thinking)
		default:
			loss.add("unsupported_media_type", path, "Mistral takes no "+kind+" content; the part was left out", types.ConversionDiagnosticWarning)
			continue
		}
		textOnly = false
	}
	if textOnly {
		return strings.Join(texts, "\n\n")
	}
	return chunks
}

// keepRecentImages keeps the most recent images of a request and replaces
// older ones with a note, as Vibe does: Mistral refuses a request with more
// images than the model takes, eight on some models.
func keepRecentImages(messages []chatMessage, loss *lossReport) {
	kept := 0
	for i := len(messages) - 1; i >= 0; i-- {
		chunks, ok := messages[i].Content.([]any)
		if !ok {
			continue
		}
		for j := len(chunks) - 1; j >= 0; j-- {
			if _, image := chunks[j].(imageURLChunk); !image {
				continue
			}
			if kept < maxRequestImages {
				kept++
				continue
			}
			chunks[j] = textChunk{Text: droppedImageText, Type: "text"}
			loss.add("image_limit", fmt.Sprintf("messages.%d.content.%d", i, j), droppedImageText, types.ConversionDiagnosticWarning)
		}
	}
}

// convertTools writes function tools and the tool choice, taking the legacy
// functions and function_call members when the request has no tools. Tools
// of other kinds and other choices have no Mistral form and are left out.
func convertTools(request *dto.GeneralOpenAIRequest, out *chatRequest, loss *lossReport) {
	tools := request.Tools
	choice := request.ToolChoice
	if len(tools) == 0 && len(request.Functions) > 0 {
		var functions []dto.FunctionRequest
		if err := common.Unmarshal(request.Functions, &functions); err == nil {
			for _, function := range functions {
				tools = append(tools, dto.ToolCallRequest{Type: "function", Function: function})
			}
		}
		if choice == nil && len(request.FunctionCall) > 0 {
			call := gjson.ParseBytes(request.FunctionCall)
			if call.Type == gjson.String {
				choice = call.Str
			} else if name := call.Get("name").Str; name != "" {
				choice = map[string]any{"type": "function", "function": map[string]any{"name": name}}
			}
		}
	}
	for i, tool := range tools {
		if (tool.Type != "" && tool.Type != "function") || len(tool.Native) > 0 || len(tool.Custom) > 0 {
			loss.add("unsupported_tool", fmt.Sprintf("tools.%d", i), "Mistral takes function tools only; a "+tool.Type+" tool was left out", types.ConversionDiagnosticError)
			continue
		}
		parameters := tool.Function.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Tools = append(out.Tools, chatTool{Function: chatFunction{
			Name: tool.Function.Name, Parameters: parameters, Description: tool.Function.Description, Strict: tool.Function.Strict,
		}, Type: "function"})
	}
	if choice == nil {
		return
	}
	raw, err := common.Marshal(choice)
	if err != nil {
		return
	}
	parsed := gjson.ParseBytes(raw)
	switch {
	case parsed.Type == gjson.String:
		out.ToolChoice = parsed.Str
	case parsed.Get("type").Str == "function" && parsed.Get("function.name").Str != "":
		out.ToolChoice = toolChoice{Function: functionName{Name: parsed.Get("function.name").Str}, Type: "function"}
	default:
		loss.add("unsupported_tool_choice", "tool_choice", "Mistral takes auto, none, any, required or one function as the tool choice; it was left out", types.ConversionDiagnosticError)
	}
}

// derivedToolCallID is the id of the j-th call of the i-th message when the
// client gave it none: nine letters and digits, as Mistral writes its own.
func derivedToolCallID(i, j int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	sum := sha256.Sum256(fmt.Appendf(nil, "tool call %d.%d", i, j))
	derived := make([]byte, 9)
	for k := range derived {
		derived[k] = alphabet[int(sum[k])%len(alphabet)]
	}
	return string(derived)
}

// fitReasoningEffort fits a reasoning effort to what the model takes, as
// Vibe fits its own requests (completion_profile/reasoning.py): minimal is
// low; a level the model lacks becomes the nearest one below it, else its
// lowest; none becomes its lowest level on a model that always reasons. A
// model that does not reason gets no effort, and a model Mistral was not
// seen to serve gets the effort as written, but none only if it is known to
// reason.
func fitReasoningEffort(effort string, support reasoningSupport, known bool) string {
	if effort == "minimal" {
		effort = "low"
	}
	switch {
	case effort == "":
		return ""
	case !known:
		if effort == "none" {
			return ""
		}
		return effort
	case len(support.levels) == 0:
		return ""
	case effort == "none":
		if support.canTurnOff {
			return "none"
		}
		return support.levels[0]
	case slices.Contains(support.levels, effort):
		return effort
	}
	rank := slices.Index(standardEfforts, effort)
	if rank < 0 {
		return effort
	}
	fitted := support.levels[0]
	for _, level := range support.levels {
		if slices.Index(standardEfforts, level) <= rank {
			fitted = level
		}
	}
	return fitted
}

// clientMembers reads members of the client's own request body that the
// gateway's request types leave out.
func clientMembers(c *gin.Context, names ...string) map[string]json.RawMessage {
	members := make(map[string]json.RawMessage)
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return members
	}
	body, err := storage.Bytes()
	if err != nil {
		return members
	}
	for _, name := range names {
		if value := gjson.GetBytes(body, name); value.Exists() && value.Type != gjson.Null {
			members[name] = json.RawMessage(value.Raw)
		}
	}
	return members
}
