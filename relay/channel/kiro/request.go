package kiro

import (
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
			for _, item := range input.Array() {
				if !item.IsObject() || item.Get("type").Str == "item_reference" {
					return nil, fieldError{"stored input references"}
				}
				if kind := item.Get("type").Str; kind != "" && kind != "message" {
					continue
				}
				content := item.Get("content")
				if !content.IsArray() {
					continue
				}
				for _, part := range content.Array() {
					if kind := part.Get("type").Str; kind != "input_text" && kind != "output_text" && kind != "text" && kind != "refusal" {
						return nil, fieldError{"non-text input"}
					}
				}
			}
			result[key] = v
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
	Context   *userContext     `json:"userInputMessageContext,omitempty"`
	Tools     []map[string]any `json:"toolUses,omitempty"`
	Reasoning any              `json:"reasoningContent,omitempty"`
}

type userContext struct {
	Tools   []any `json:"tools,omitempty"`
	Results []any `json:"toolResults,omitempty"`
}

func messageTurn(m gjson.Result, model string) (string, *turn, error) {
	role, content := m.Get("role").Str, m.Get("content")
	t := &turn{}
	kind := "assistantResponseMessage"
	if role == "user" {
		kind = "userInputMessage"
		t.Model = model
		t.Origin = "AI_EDITOR"
	} else if role != "assistant" {
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
		case "tool_use":
			if role != "assistant" || b.Get("id").Str == "" || b.Get("name").Str == "" || !b.Get("input").IsObject() {
				return "", nil, fieldError{"tool_use"}
			}
			t.Tools = append(t.Tools, map[string]any{"toolUseId": b.Get("id").Str, "name": b.Get("name").Str, "input": json.RawMessage(b.Get("input").Raw)})
		case "tool_result":
			if role != "user" || b.Get("tool_use_id").Str == "" {
				return "", nil, fieldError{"tool_result"}
			}
			text := ""
			// Anthropic lets a tool result omit its content.
			if content := b.Get("content"); content.Exists() && content.Type != gjson.Null {
				var err error
				if text, err = textBlocks(content); err != nil {
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
			if role != "assistant" || t.Reasoning != nil {
				return "", nil, fieldError{"thinking history"}
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
	for k, raw := range fields {
		switch k {
		// Kiro caches prompt prefixes on its own, whatever the cache markers say,
		// and has no request metadata; cache_control is dropped wherever it appears.
		case "model", "stream", "messages", "tools", "metadata", "cache_control":
		case "system":
			v, err := textBlocks(gjson.ParseBytes(raw))
			if err != nil {
				return nil, err
			}
			if model.SystemPrompt {
				result["systemPrompt"] = v
			} else {
				system = v
			}
		case "tool_choice":
			if v := gjson.ParseBytes(raw); v.Get("type").Str != "auto" || v.Get("disable_parallel_tool_use").Bool() {
				return nil, fieldError{k}
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
		case "thinking", "output_config":
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
	if len(msgs) == 0 {
		return nil, errors.New("Kiro requires message history")
	}
	history := []map[string]*turn{}
	// Kiro has one turn per speaker. Preserve the order and tools of consecutive messages.
	for _, m := range msgs {
		kind, t, err := messageTurn(m, model.ID)
		if err != nil {
			return nil, err
		}
		if len(history) > 0 {
			if previous := history[len(history)-1][kind]; previous != nil {
				if previous.Content != "" && t.Content != "" {
					previous.Content += "\n\n"
				}
				previous.Content += t.Content
				previous.Tools = append(previous.Tools, t.Tools...)
				if t.Reasoning != nil {
					if previous.Reasoning != nil {
						return nil, fieldError{"multiple reasoning blocks in one turn"}
					}
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
	current := history[len(history)-1]["userInputMessage"]
	if current == nil {
		return nil, fieldError{"assistant prefill"}
	}
	history = history[:len(history)-1]
	if system != "" {
		first := current
		for _, entry := range history {
			if user := entry["userInputMessage"]; user != nil {
				first = user
				break
			}
		}
		first.Content = foldedSystem(system, first.Content)
	}
	for _, tool := range gjson.ParseBytes(fields["tools"]).Array() {
		if tool.Get("name").Str == "" || !tool.Get("input_schema").IsObject() {
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
