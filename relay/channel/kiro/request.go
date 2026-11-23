package kiro

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// fieldError is a request option or content Kiro cannot honor.
type fieldError struct{ field string }

func (e fieldError) Error() string { return "Kiro does not support the requested " + e.field }

// conversationID names the conversation of a request for Kiro's metadata.
// Kiro keeps no conversation state, so it only has to stay stable across the
// turns of one conversation.
func conversationID(scope, anchor string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+"\x00"+anchor)).String()
}

func marshalRaw(v any) json.RawMessage {
	raw, _ := common.Marshal(v)
	return raw
}

// createResponse builds the CreateResponse body of an OpenAI Responses
// request, given as its top-level members.
func createResponse(fields map[string]json.RawMessage, profile, scope string, model Model) (map[string]any, error) {
	result := map[string]any{"origin": "AI_EDITOR", "stream": true, "profileArn": profile}
	meta := map[string]json.RawMessage{}
	instructions := ""
	for key, v := range fields {
		switch key {
		case "tools":
			for _, t := range gjson.ParseBytes(v).Array() {
				if t.Get("type").Str != "function" {
					return nil, fieldError{"tool type"}
				}
			}
			result[key] = v
		case "model", "user", "safety_identifier":
			result[key] = v
		case "reasoning":
			reasoning, err := responsesReasoning(v, model)
			if err != nil {
				return nil, err
			}
			result[key] = reasoning
		case "instructions":
			text := gjson.ParseBytes(v)
			if text.Type != gjson.String {
				return nil, fieldError{key}
			}
			instructions = text.Str
		case "input":
			input := gjson.ParseBytes(v)
			if input.Type == gjson.String {
				result[key] = []any{map[string]string{"role": "user", "content": input.Str}}
				continue
			}
			if !input.IsArray() {
				return nil, fieldError{"input"}
			}
			var items []any
			for _, item := range input.Array() {
				if !item.IsObject() || item.Get("type").Str == "item_reference" {
					return nil, fieldError{"stored input references"}
				}
				switch item.Get("type").Str {
				case "", "message":
					if content := item.Get("content"); content.IsArray() {
						for _, part := range content.Array() {
							if _, err := responsesPart(part, model); err != nil {
								return nil, err
							}
						}
					}
				case "function_call_output":
					// Kiro reads a function's output as text, images and
					// files included, so they follow in a user message.
					output := item.Get("output")
					if !output.IsArray() {
						break
					}
					var text []string
					var media []any
					for _, part := range output.Array() {
						isMedia, err := responsesPart(part, model)
						if err != nil {
							return nil, err
						}
						if isMedia {
							media = append(media, json.RawMessage(part.Raw))
							text = append(text, "[The "+strings.TrimPrefix(part.Get("type").Str, "input_")+" this call returned follows in the next message.]")
						} else {
							text = append(text, part.Get("text").Str)
						}
					}
					var rewritten map[string]json.RawMessage
					if err := common.UnmarshalJsonStr(item.Raw, &rewritten); err != nil {
						return nil, fieldError{"input"}
					}
					rewritten["output"] = marshalRaw(strings.Join(text, "\n"))
					items = append(items, rewritten)
					if media != nil {
						items = append(items, map[string]any{"role": "user", "content": media})
					}
					continue
				}
				items = append(items, json.RawMessage(item.Raw))
			}
			result[key] = items
		case "metadata":
			if common.Unmarshal(v, &meta) != nil || meta == nil {
				return nil, fieldError{"metadata"}
			}
		case "store", "background":
			if string(v) != "false" {
				return nil, fieldError{key}
			}
			if key == "store" {
				result[key] = false
			}
		case "tool_choice":
			if string(v) != `"auto"` {
				return nil, fieldError{key}
			}
		case "parallel_tool_calls":
			if string(v) != "true" {
				return nil, fieldError{key}
			}
		case "include":
			options := gjson.ParseBytes(v)
			if !options.IsArray() {
				return nil, fieldError{key}
			}
			for _, option := range options.Array() {
				if option.Str != "reasoning.encrypted_content" {
					return nil, fieldError{key}
				}
			}
		// Kiro caches prompt prefixes on its own, so a cache key has nothing to
		// steer; it has no sampling controls and no per-request output cap on
		// this operation. Usage is always reported, whatever stream_options
		// asks, and client_metadata describes the client to the gateway.
		case "stream", "stream_options", "prompt_cache_key", "temperature", "top_p", "max_output_tokens", "client_metadata":
		default:
			return nil, fieldError{key}
		}
	}
	anchor := gjson.ParseBytes(fields["input"])
	if anchor.IsArray() {
		anchor = anchor.Get("0")
	}
	seed := anchor.Raw
	if id := meta["session_id"]; id != nil {
		seed = string(id)
	}
	for k := range meta {
		if strings.HasPrefix(k, "kiro:") {
			return nil, fieldError{"reserved metadata"}
		}
	}
	meta["kiro:conversation_id"] = marshalRaw(conversationID(scope, seed))
	meta["kiro:agent_continuation_id"] = marshalRaw(uuid.NewString())
	meta["kiro:agent_mode"] = json.RawMessage(`"vibe"`)
	result["metadata"] = meta
	items, leading, err := systemInput(result["input"])
	if err != nil {
		return nil, err
	}
	system := strings.Join(slices.DeleteFunc(append([]string{instructions}, leading...), func(text string) bool { return text == "" }), "\n\n")
	if system != "" && model.SystemPrompt {
		result["instructions"] = system
	} else if system != "" {
		if items, err = foldIntoFirstUserInput(items, system); err != nil {
			return nil, err
		}
	}
	if items != nil {
		result["input"] = items
	}
	return result, nil
}

// responsesPart checks one content part of a Responses input and reports
// whether it is an image or a file. Kiro takes images and PDF files as data
// URLs, as the Kiro IDE sends them, on a model that reads images.
func responsesPart(part gjson.Result, model Model) (bool, error) {
	switch kind := part.Get("type").Str; kind {
	case "input_text", "output_text", "text", "refusal":
		return false, nil
	case "input_image":
		media, _, _ := strings.Cut(strings.TrimPrefix(part.Get("image_url").Str, "data:"), ";base64,")
		format, isImage := strings.CutPrefix(media, "image/")
		if !strings.HasPrefix(part.Get("image_url").Str, "data:") || !isImage || !slices.Contains(imageFormats, format) {
			return false, fieldError{"input_image: Kiro takes JPEG, PNG, GIF and WebP images as base64 data URLs"}
		}
	case "input_file":
		if !strings.HasPrefix(part.Get("file_data").Str, "data:application/pdf;base64,") {
			return false, fieldError{"input_file: Kiro takes PDF files as base64 file_data"}
		}
	default:
		return false, fieldError{"input content " + strconv.Quote(kind)}
	}
	if !model.ReadsImages() {
		return false, fieldError{"image or PDF input: the model " + model.ID + " reads text only"}
	}
	return true, nil
}

// responsesReasoning fits an OpenAI reasoning summary request to the model's
// request schema. A Claude model takes the summary as thinking.display, which
// needs a thinking type and knows only "summarized" and "omitted"; OpenAI's auto,
// concise and detailed all ask for a summary. A model without a display option
// has no summary to give, and Kiro refuses the field, so it is dropped.
func responsesReasoning(raw json.RawMessage, model Model) (json.RawMessage, error) {
	var reasoning map[string]json.RawMessage
	if common.Unmarshal(raw, &reasoning) != nil || reasoning == nil {
		return nil, fieldError{"reasoning"}
	}
	summary, ok := reasoning["summary"]
	if !ok {
		return raw, nil
	}
	delete(reasoning, "summary")
	thinking := gjson.GetBytes(model.Schema, "properties.thinking.properties")
	display := gjson.ParseBytes(summary).Str
	if !enumHas(thinking.Get("display.enum"), display) {
		display = "summarized"
	}
	if string(summary) != "null" && enumHas(thinking.Get("display.enum"), display) && (reasoning["type"] != nil || enumHas(thinking.Get("type.enum"), "adaptive")) {
		if reasoning["type"] == nil {
			reasoning["type"] = json.RawMessage(`"adaptive"`)
		}
		reasoning["summary"] = marshalRaw(display)
	}
	return common.Marshal(reasoning)
}

// fitSchemaObject fits thinking or output_config to the model's request
// schema, which allows no other members. Anthropic's manual thinking
// ({"type":"enabled","budget_tokens":N}) becomes adaptive thinking, the only
// kind Kiro's Claude models offer, and "disabled" is left out where the model
// cannot turn thinking off. A value outside the schema's options is dropped so
// the model default applies. A model without the object cannot honor it.
func fitSchemaObject(name string, raw json.RawMessage, schema gjson.Result) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if !schema.IsObject() || common.Unmarshal(raw, &value) != nil || value == nil {
		return nil, fieldError{name}
	}
	properties := schema.Get("properties")
	if name == "thinking" {
		types := properties.Get("type.enum")
		switch kind := gjson.ParseBytes(value["type"]).Str; {
		case enumHas(types, kind):
		case kind == "enabled" && enumHas(types, "adaptive"):
			value["type"] = json.RawMessage(`"adaptive"`)
		case kind == "disabled":
			return nil, nil
		default:
			return nil, fieldError{name}
		}
	}
	for key, member := range value {
		allowed := properties.Get(gjson.Escape(key))
		if !allowed.IsObject() || allowed.Get("enum").Exists() && !enumHas(allowed.Get("enum"), gjson.ParseBytes(member).Str) {
			delete(value, key)
		}
	}
	if len(value) == 0 {
		return nil, nil
	}
	return common.Marshal(value)
}

func enumHas(enum gjson.Result, value string) bool {
	for _, allowed := range enum.Array() {
		if allowed.Str == value {
			return true
		}
	}
	return false
}

// systemInput takes the developer and system messages out of a Responses
// input, since Kiro accepts neither role as an input item. The leading ones
// come back as system prompt text; a later one stays in place as a user
// message.
func systemInput(input any) ([]map[string]json.RawMessage, []string, error) {
	if input == nil {
		return nil, nil, nil
	}
	raw, err := common.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	var items []map[string]json.RawMessage
	if common.Unmarshal(raw, &items) != nil {
		return nil, nil, fieldError{"input"}
	}
	var system []string
	kept := items[:0]
	for _, item := range items {
		role := gjson.ParseBytes(item["role"]).Str
		switch {
		case role != "developer" && role != "system":
			kept = append(kept, item)
		case len(kept) > 0:
			item["role"] = json.RawMessage(`"user"`)
			kept = append(kept, item)
		default:
			text, err := inputText(gjson.ParseBytes(item["content"]))
			if err != nil {
				return nil, nil, err
			}
			system = append(system, text)
		}
	}
	return kept, system, nil
}

// inputText is the text of a message whose content is a string or text parts.
func inputText(content gjson.Result) (string, error) {
	if content.Type == gjson.String {
		return content.Str, nil
	}
	if !content.IsArray() {
		return "", fieldError{"developer message content"}
	}
	var parts []string
	for _, part := range content.Array() {
		if kind := part.Get("type").Str; kind != "input_text" && kind != "text" || part.Get("text").Type != gjson.String {
			return "", fieldError{"developer message content"}
		}
		parts = append(parts, part.Get("text").Str)
	}
	return strings.Join(parts, "\n"), nil
}

// foldedSystem carries a system prompt the profile cannot send (it lacks
// system_field_injection) at the start of the first user message, which keeps
// it in the conversation's stable prefix. It goes in as plain text: wrapped in
// a tag that claims system authority, models treat it as an embedded prompt
// injection and reason about it on every turn.
func foldedSystem(system, content string) string {
	if content == "" {
		return system
	}
	return system + "\n\n" + content
}

// foldIntoFirstUserInput puts the system prompt at the start of the first user
// message of a Responses input, or in a user message of its own before the
// input when it has none.
func foldIntoFirstUserInput(items []map[string]json.RawMessage, system string) ([]map[string]json.RawMessage, error) {
	for _, item := range items {
		if gjson.ParseBytes(item["role"]).Str != "user" || !slices.Contains([]string{"", "message"}, gjson.ParseBytes(item["type"]).Str) {
			continue
		}
		content := gjson.ParseBytes(item["content"])
		switch {
		case content.Type == gjson.String:
			item["content"] = marshalRaw(foldedSystem(system, content.Str))
		case content.IsArray():
			parts := []any{map[string]string{"type": "input_text", "text": foldedSystem(system, "")}}
			for _, part := range content.Array() {
				parts = append(parts, json.RawMessage(part.Raw))
			}
			item["content"] = marshalRaw(parts)
		default:
			return nil, fieldError{"input"}
		}
		return items, nil
	}
	lead := map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": marshalRaw(foldedSystem(system, ""))}
	return append([]map[string]json.RawMessage{lead}, items...), nil
}

func textBlocks(value gjson.Result) (string, error) {
	if value.Type == gjson.String {
		return value.Str, nil
	}
	if !value.IsArray() {
		return "", fieldError{"text content"}
	}
	var parts []string
	for _, b := range value.Array() {
		if b.Get("type").Str != "text" || b.Get("text").Type != gjson.String {
			return "", fieldError{"text block"}
		}
		parts = append(parts, b.Get("text").Str)
	}
	return strings.Join(parts, "\n"), nil
}

type turn struct {
	Content   string           `json:"content"`
	Model     string           `json:"modelId,omitempty"`
	Origin    string           `json:"origin,omitempty"`
	Images    []any            `json:"images,omitempty"`
	Documents []map[string]any `json:"documents,omitempty"`
	Context   *userContext     `json:"userInputMessageContext,omitempty"`
	Tools     []map[string]any `json:"toolUses,omitempty"`
	Reasoning any              `json:"reasoningContent,omitempty"`

	// toolChanges are the tool_addition and tool_removal blocks of a system
	// message, applied in order to the tools the request declares.
	toolChanges []gjson.Result
}

type userContext struct {
	Tools   []any `json:"tools,omitempty"`
	Results []any `json:"toolResults,omitempty"`
}

// imageFormats are the image types Anthropic and Kiro both take.
var imageFormats = []string{"jpeg", "png", "gif", "webp"}

// attachMedia puts an Anthropic image or document block on the user turn, as
// one of its images or documents, and returns the text that stands for it
// instead: a plain-text document's text. Kiro reads a PDF document's pages
// as images, so a model reads PDFs only where it reads images.
func (t *turn) attachMedia(b gjson.Result) (text string, attached bool, err error) {
	source := b.Get("source")
	kind, sourceType := b.Get("type").Str, source.Get("type").Str
	switch {
	case sourceType == "url":
		return "", false, fieldError{kind + " URL source: Kiro takes the data inline, so send it as base64"}
	case sourceType == "file":
		return "", false, fieldError{kind + " file source: Kiro cannot read Anthropic Files API uploads"}
	case kind == "document" && sourceType == "text":
		if source.Get("data").Type != gjson.String {
			return "", false, fieldError{"document"}
		}
		return strings.Join(slices.DeleteFunc([]string{b.Get("title").Str, b.Get("context").Str, source.Get("data").Str}, func(s string) bool { return s == "" }), "\n\n"), false, nil
	case sourceType != "base64" || source.Get("data").Str == "":
		return "", false, fieldError{kind + " source " + strconv.Quote(sourceType)}
	case kind == "document":
		if source.Get("media_type").Str != "application/pdf" {
			return "", false, fieldError{"document media type: Kiro takes PDF and plain-text documents"}
		}
		// Named in request order once the history is built.
		t.Documents = append(t.Documents, map[string]any{"format": "pdf", "source": map[string]string{"bytes": source.Get("data").Str}})
		return "", true, nil
	}
	format, _ := strings.CutPrefix(source.Get("media_type").Str, "image/")
	if !slices.Contains(imageFormats, format) {
		return "", false, fieldError{"image media type: Kiro takes JPEG, PNG, GIF and WebP images"}
	}
	t.Images = append(t.Images, map[string]any{"format": format, "source": map[string]string{"bytes": source.Get("data").Str}})
	return "", true, nil
}

// toolResultText is the content of a tool result. Kiro ignores images and
// documents inside a tool result, so they go on the turn that carries the
// result, and the result says where they are.
func (t *turn) toolResultText(content gjson.Result) (string, error) {
	if content.Type == gjson.String {
		return content.Str, nil
	}
	if !content.IsArray() {
		return "", fieldError{"tool_result content"}
	}
	var parts []string
	for _, b := range content.Array() {
		switch b.Get("type").Str {
		case "text":
			if b.Get("text").Type != gjson.String {
				return "", fieldError{"text block"}
			}
			parts = append(parts, b.Get("text").Str)
		case "image", "document":
			text, attached, err := t.attachMedia(b)
			if err != nil {
				return "", err
			}
			if attached {
				text = "[The " + b.Get("type").Str + " this result returned is attached to this message.]"
			}
			parts = append(parts, text)
		default:
			return "", fieldError{"tool_result content block " + strconv.Quote(b.Get("type").Str)}
		}
	}
	return strings.Join(parts, "\n"), nil
}

// messageTurn converts one Messages message. A system message, which GAR has
// no turn for, comes back as a user turn holding its text.
func messageTurn(m gjson.Result, model string) (string, *turn, error) {
	role, content := m.Get("role").Str, m.Get("content")
	t := &turn{}
	kind := "assistantResponseMessage"
	switch role {
	case "user", "system":
		kind = "userInputMessage"
		t.Model = model
		t.Origin = "AI_EDITOR"
	case "assistant":
	default:
		return "", nil, fieldError{"role"}
	}
	if content.Type == gjson.String {
		t.Content = content.Str
		return kind, t, nil
	}
	var parts []string
	for _, b := range content.Array() {
		switch b.Get("type").Str {
		case "text":
			if b.Get("text").Type != gjson.String {
				return "", nil, fieldError{"text"}
			}
			parts = append(parts, b.Get("text").Str)
		case "tool_addition", "tool_removal":
			// Mid-conversation tool changes: a definition, or a reference to a
			// tool of the request's tools.
			tool := b.Get("tool")
			valid := tool.Get("type").Str == "tool_reference" && tool.Get("name").Str != "" ||
				tool.Get("type").Str == "tool_definition" && b.Get("type").Str == "tool_addition" && tool.Get("definition").IsObject()
			if role != "system" || !valid {
				return "", nil, fieldError{"tool change"}
			}
			t.toolChanges = append(t.toolChanges, b)
		case "tool_use":
			if role != "assistant" || b.Get("id").Str == "" || b.Get("name").Str == "" || !b.Get("input").IsObject() {
				return "", nil, fieldError{"tool_use"}
			}
			t.Tools = append(t.Tools, map[string]any{"toolUseId": b.Get("id").Str, "name": b.Get("name").Str, "input": json.RawMessage(b.Get("input").Raw)})
		case "image", "document":
			if role != "user" {
				return "", nil, fieldError{b.Get("type").Str + " in a " + role + " message"}
			}
			text, _, err := t.attachMedia(b)
			if err != nil {
				return "", nil, err
			}
			if text != "" {
				parts = append(parts, text)
			}
		case "tool_result":
			if role != "user" || b.Get("tool_use_id").Str == "" {
				return "", nil, fieldError{"tool_result"}
			}
			text := ""
			// Anthropic lets a tool result omit its content.
			if content := b.Get("content"); content.Exists() && content.Type != gjson.Null {
				var err error
				if text, err = t.toolResultText(content); err != nil {
					return "", nil, err
				}
			}
			status := "success"
			if b.Get("is_error").Bool() {
				status = "error"
			}
			if t.Context == nil {
				t.Context = &userContext{}
			}
			t.Context.Results = append(t.Context.Results, map[string]any{"toolUseId": b.Get("tool_use_id").Str, "status": status, "content": []any{map[string]string{"text": text}}})
		case "thinking", "redacted_thinking":
			if role != "assistant" {
				return "", nil, fieldError{"thinking history"}
			}
			// Kiro replays one reasoning block per assistant turn. The first
			// is kept: it was produced on the conversation as it stands before
			// this message, so its signature still verifies. Later ones (a
			// response that thought again between tool calls) were produced
			// on content Kiro cannot replay around them; leaving a block out
			// is valid and only drops that reasoning.
			if t.Reasoning != nil {
				continue
			}
			if b.Get("type").Str == "thinking" {
				t.Reasoning = map[string]any{"reasoningText": map[string]string{"text": b.Get("thinking").Str, "signature": b.Get("signature").Str}}
			} else {
				t.Reasoning = map[string]string{"redactedContent": b.Get("data").Str}
			}
		default:
			return "", nil, fieldError{"message content"}
		}
	}
	t.Content = strings.Join(parts, "\n")
	return kind, t, nil
}

// generateAssistantResponse builds the GenerateAssistantResponse body of an
// Anthropic Messages request, given as its top-level members.
func generateAssistantResponse(fields map[string]json.RawMessage, profile, scope string, model Model) (map[string]any, error) {
	result := map[string]any{"profileArn": profile, "agentMode": "vibe"}
	amrf := map[string]any{}
	system := ""
	outputConfig := map[string]json.RawMessage{}
	withoutTools := false
	for k, raw := range fields {
		switch k {
		// Kiro caches prompt prefixes on its own, whatever the cache markers say,
		// and has no request metadata; cache_control is dropped wherever it appears.
		case "model", "stream", "messages", "tools", "metadata", "cache_control":
		// Kiro has no context editing (its 5.5 models take a million tokens),
		// and the edits are not carried out here either. Claude Code asks for
		// clear_thinking_20251015 with keep "all", which clears nothing, and,
		// after an idle gap, for clear_tool_uses_20250919. Clearing earlier
		// turns from the history sent to Kiro would change it where they start,
		// losing Kiro's prefix cache from there on every later turn, while the
		// input billed is estimated from the request as the client sent it.
		// The response reports no applied_edits, which reads as nothing
		// cleared; Claude Code reads them only to track tool-use clearing.
		case "context_management":
		case "system":
			v, err := textBlocks(gjson.ParseBytes(raw))
			if err != nil {
				return nil, err
			}
			system = v
		case "output_config":
			// Applied after the messages, whose system messages may change it.
			if common.Unmarshal(raw, &outputConfig) != nil || outputConfig == nil {
				return nil, fieldError{k}
			}
		case "tool_choice":
			// GenerateAssistantResponse has no tool choice. With "auto" the
			// model chooses, as it does on Kiro, and the relay ends the
			// response after the first call when parallel calls are disabled.
			// "none" declares no tools. Nothing makes the model call a tool,
			// and Anthropic itself refuses forced choices on Claude 5.5.
			switch kind := gjson.ParseBytes(raw).Get("type").Str; kind {
			case "auto":
			case "none":
				withoutTools = true
			case "any", "tool":
				return nil, fieldError{"tool_choice " + strconv.Quote(kind) + `: Kiro cannot force a tool call; use "auto" and name the tool in the prompt`}
			default:
				return nil, fieldError{k}
			}
		case "stop_sequences":
			// Kiro has no stop sequences; the relay ends the response at the
			// first one in its text.
			sequences := gjson.ParseBytes(raw)
			if !sequences.IsArray() {
				return nil, fieldError{k}
			}
			for _, sequence := range sequences.Array() {
				if sequence.Type != gjson.String || sequence.Str == "" {
					return nil, fieldError{k}
				}
			}
		case "max_tokens":
			// A model that takes no cap runs to its ceiling.
			if model.TakesOutputLimit() {
				requested, err := strconv.ParseInt(string(raw), 10, 64)
				if err != nil || requested <= 0 {
					return nil, fieldError{k}
				}
				amrf[k] = model.OutputLimit(requested)
			}
		case "temperature", "top_p", "top_k":
			// Kiro has no sampling controls unless a model's schema lists one.
			if model.TakesOption(k) {
				amrf[k] = raw
			}
		case "thinking":
			fitted, err := fitSchemaObject(k, raw, gjson.GetBytes(model.Schema, "properties."+k))
			if err != nil {
				return nil, err
			}
			if fitted != nil {
				amrf[k] = fitted
			}
		default:
			return nil, fieldError{k}
		}
	}
	msgs := gjson.ParseBytes(fields["messages"]).Array()
	history := []map[string]*turn{}
	var toolChanges []gjson.Result
	// Kiro has one turn per speaker. Preserve the order and tools of consecutive
	// messages. System messages follow the rule of Responses developer
	// messages: the leading ones join the system prompt, and a later one is
	// user text where it stands, which joins the turn of the user message
	// before or after it, so tool results still answer the turn that called.
	for _, m := range msgs {
		kind, t, err := messageTurn(m, model.ID)
		if err != nil {
			return nil, err
		}
		if m.Get("role").Str == "system" {
			toolChanges = append(toolChanges, t.toolChanges...)
			// Per-message effort (beta) holds from its message on, so the last
			// one is the effort of the turn Kiro generates.
			if config := m.Get("output_config"); config.Exists() && config.Type != gjson.Null {
				if !config.IsObject() || common.UnmarshalJsonStr(config.Raw, &outputConfig) != nil {
					return nil, fieldError{"output_config"}
				}
			}
			switch {
			case t.Content == "":
				continue
			case len(history) == 0 && system != "":
				system += "\n\n" + t.Content
				continue
			case len(history) == 0:
				system = t.Content
				continue
			}
		}
		if len(history) > 0 {
			if previous := history[len(history)-1][kind]; previous != nil {
				if previous.Content != "" && t.Content != "" {
					previous.Content += "\n\n"
				}
				previous.Content += t.Content
				previous.Tools = append(previous.Tools, t.Tools...)
				previous.Images = append(previous.Images, t.Images...)
				previous.Documents = append(previous.Documents, t.Documents...)
				// The first reasoning block of the turn stays, as within a message.
				if previous.Reasoning == nil {
					previous.Reasoning = t.Reasoning
				}
				if t.Context != nil {
					if previous.Context == nil {
						previous.Context = &userContext{}
					}
					previous.Context.Results = append(previous.Context.Results, t.Context.Results...)
				}
				continue
			}
		}
		history = append(history, map[string]*turn{kind: t})
	}
	if len(history) == 0 {
		return nil, errors.New("Kiro requires message history")
	}
	current := history[len(history)-1]["userInputMessage"]
	if current == nil {
		return nil, fieldError{"assistant prefill"}
	}
	// Documents are named in request order; the name is all Kiro asks.
	documents, toolHistory := 0, false
	for _, entry := range history {
		for _, t := range entry {
			if (len(t.Images) > 0 || len(t.Documents) > 0) && !model.ReadsImages() {
				return nil, fieldError{"image or PDF input: the model " + model.ID + " reads text only"}
			}
			for _, document := range t.Documents {
				documents++
				document["name"] = "document-" + strconv.Itoa(documents)
			}
			toolHistory = toolHistory || len(t.Tools) > 0 || t.Context != nil && len(t.Context.Results) > 0
		}
	}
	history = history[:len(history)-1]
	if system != "" && model.SystemPrompt {
		result["systemPrompt"] = system
	} else if system != "" {
		first := current
		for _, entry := range history {
			if user := entry["userInputMessage"]; user != nil {
				first = user
				break
			}
		}
		first.Content = foldedSystem(system, first.Content)
	}
	if len(outputConfig) > 0 {
		fitted, err := fitSchemaObject("output_config", marshalRaw(outputConfig), gjson.GetBytes(model.Schema, "properties.output_config"))
		if err != nil {
			return nil, err
		}
		if fitted != nil {
			amrf["output_config"] = fitted
		}
	}
	tools := gjson.ParseBytes(fields["tools"]).Array()
	declared := slices.Clone(tools)
	for _, change := range toolChanges {
		tool := change.Get("tool")
		definition := tool.Get("definition")
		name := cmp.Or(definition.Get("name").Str, tool.Get("name").Str)
		named := func(t gjson.Result) bool { return t.Get("name").Str == name }
		at := slices.IndexFunc(declared, named)
		switch {
		case change.Get("type").Str == "tool_removal":
			if at >= 0 {
				declared = slices.Delete(declared, at, at+1)
			}
		case definition.Exists() && at >= 0:
			declared[at] = definition
		case definition.Exists():
			declared = append(declared, definition)
		case at < 0:
			// A reference names one of the request's tools, which Kiro is told
			// about from the start, deferred or not, unless one was removed.
			from := slices.IndexFunc(tools, named)
			if from < 0 {
				return nil, fieldError{"tool reference"}
			}
			declared = append(declared, tools[from])
		}
	}
	if withoutTools {
		if toolHistory && model.NeedsToolDeclarations() {
			return nil, fieldError{`tool_choice "none" for ` + model.ID + `: Kiro needs this model's tools declared once the conversation has tool calls`}
		}
		declared = nil
	}
	for _, tool := range declared {
		if tool.Get("name").Str == "" || !tool.Get("input_schema").IsObject() {
			if kind := tool.Get("type").Str; kind != "" && kind != "custom" {
				// A server tool runs on Anthropic's side; Kiro has none of them.
				return nil, fieldError{"server tool " + strconv.Quote(kind) + ": Kiro runs only tools the client declares with an input_schema and executes itself"}
			}
			return nil, fieldError{"tool declaration"}
		}
		if current.Context == nil {
			current.Context = &userContext{}
		}
		current.Context.Tools = append(current.Context.Tools, map[string]any{"toolSpecification": map[string]any{"name": tool.Get("name").Str, "description": tool.Get("description").Str, "inputSchema": map[string]any{"json": json.RawMessage(tool.Get("input_schema").Raw)}}})
	}
	result["conversationState"] = map[string]any{"chatTriggerType": "MANUAL", "agentTaskType": "vibe", "agentContinuationId": uuid.NewString(), "conversationId": conversationID(scope, msgs[0].Raw), "currentMessage": map[string]any{"userInputMessage": current}, "history": history}
	if len(amrf) > 0 {
		result["additionalModelRequestFields"] = amrf
	}
	return result, nil
}
