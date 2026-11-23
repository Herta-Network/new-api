// The CreateResponse handling is adapted from AstrLink's
// core/internal/providerapi/kiro_sse.go (https://github.com/Calcium-Ion/AstrLink,
// Copyright 2026 AstrLink contributors, Apache License 2.0). new-api changed
// it to remove the kiro: fields from every response it relays, to count usage
// locally, and to fail a stream that ends before its terminal event.

package kiro

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/tidwall/gjson"
)

// responsesMeter counts a CreateResponse locally, since Kiro reports no token
// counts: the input estimated for the request, and the output from the text,
// reasoning and function calls of the response.
type responsesMeter struct {
	prompt int
	output func(text string) int
	// limit is the client's output cap, which CreateResponse cannot take,
	// zero for none; count measures streamed output against it.
	limit int
	count func(text string) int
}

// errResponseFailed is a response Kiro ended as failed.
var errResponseFailed = errors.New("Kiro response failed")

// responsesStream relays a CreateResponse event stream, which Kiro always
// sends: the events for a streaming client, without the kiro: fields that
// describe the gateway's account, or the final response for one that does
// not stream. Only response.completed or response.incomplete ends it; a
// stream that stops before either reads as an error.
type responsesStream struct {
	source    io.ReadCloser
	lines     *bufio.Scanner
	streaming bool
	meter     responsesMeter
	maxBytes  int
	output    *outputCollector
	pending   bytes.Reader
	end       error
	bytesRead int
	// completed reports a terminal event that ends the response as a success.
	completed bool
	// used is the output counted against meter.limit; truncated reports a
	// response the relay ended at that cap.
	used      int
	truncated bool
	// template is the last response object Kiro sent and sequence the last
	// event number, which a response the relay ends continues.
	template string
	sequence int64
}

func newResponsesStream(source io.ReadCloser, streaming bool, meter responsesMeter, maxBytes int) *responsesStream {
	s := &responsesStream{source: source, lines: bufio.NewScanner(source), streaming: streaming, meter: meter, maxBytes: maxBytes, output: &outputCollector{
		items:     map[int]map[string]json.RawMessage{},
		positions: map[string]int{}, calls: map[string]int{},
		parts: map[int]map[int]*outputPart{}, arguments: map[int]*strings.Builder{},
	}}
	s.lines.Buffer(make([]byte, 64<<10), maxFrameBytes)
	return s
}

func (s *responsesStream) Close() error { return s.source.Close() }

func (s *responsesStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.pending.Len() == 0 {
		if s.end != nil {
			return 0, s.end
		}
		raw, err := s.nextEvent()
		if err != nil {
			s.end = err
			return 0, err
		}
		var frame bytes.Buffer
		frame.WriteString("event: ")
		frame.WriteString(gjson.GetBytes(raw, "type").String())
		frame.WriteString("\ndata: ")
		// A newline in valid JSON is whitespace between tokens.
		frame.Write(bytes.ReplaceAll(raw, []byte("\n"), []byte(" ")))
		frame.WriteString("\n\n")
		s.pending.Reset(frame.Bytes())
	}
	return s.pending.Read(p)
}

// response reads the whole stream for a client that does not stream and
// returns the final response object.
func (s *responsesStream) response() ([]byte, error) {
	defer s.Close()
	for {
		raw, err := s.nextEvent()
		if err != nil {
			return nil, err
		}
		if s.end == io.EOF {
			return []byte(gjson.GetBytes(raw, "response").Raw), nil
		}
	}
}

// nextEvent reads the next event the client gets. Some Kiro responses omit the
// blank separator between single-line JSON events.
func (s *responsesStream) nextEvent() ([]byte, error) {
	if s.end != nil {
		return nil, s.end
	}
	var data []byte
	for s.lines.Scan() {
		line := s.lines.Bytes()
		s.bytesRead += len(line) + 1
		if s.maxBytes > 0 && s.bytesRead > s.maxBytes {
			return nil, errIncomplete
		}
		if len(line) == 0 && len(data) != 0 {
			return nil, errIncomplete
		}
		v, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		v = bytes.TrimSpace(v)
		if len(data) == 0 && bytes.Equal(v, []byte("[DONE]")) {
			return nil, errIncomplete
		}
		data = append(data, v...)
		if len(data) > maxFrameBytes {
			return nil, errIncomplete
		}
		if !gjson.ValidBytes(data) {
			data = append(data, '\n')
			continue
		}
		e := gjson.ParseBytes(data)
		kind := e.Get("type").String()
		if kind == "" || strings.ContainsAny(kind, "\r\n") {
			return nil, errIncomplete
		}
		var produced string
		switch kind {
		case "response.output_text.delta", "response.refusal.delta", "response.reasoning_text.delta", "response.reasoning.delta",
			"response.reasoning_summary_text.delta", "response.function_call_arguments.delta":
			produced = e.Get("delta").String()
		case "response.output_item.added":
			if e.Get("item.type").Str == "function_call" {
				produced = e.Get("item.name").Str
			}
		}
		if produced != "" && !s.fits(produced) {
			return s.truncate()
		}
		if response := e.Get("response"); response.IsObject() {
			s.template = response.Raw
		}
		if n := e.Get("sequence_number"); n.Type == gjson.Number {
			s.sequence = n.Int()
		}
		// Merge deltas once for both the final response and output counting.
		if err := s.output.observe(e); err != nil {
			return nil, err
		}
		switch kind {
		case "kiro:keep_alive":
			data = nil
			continue
		case "response.completed", "response.incomplete", "response.failed", "error":
			event, err := s.terminal(data, e, kind)
			if err != nil {
				return nil, err
			}
			s.end, s.completed = io.EOF, true
			return event, nil
		}
		if e.Get("response").IsObject() {
			return withoutKiroFields(data)
		}
		return data, nil
	}
	if err := s.lines.Err(); err != nil {
		return nil, errIncomplete
	}
	return nil, errIncomplete
}

// fits reports whether text keeps the output within the client's cap, and
// counts it if so.
func (s *responsesStream) fits(text string) bool {
	if s.meter.limit <= 0 {
		return true
	}
	n := 0
	if s.meter.count != nil {
		n = s.meter.count(text)
	}
	if s.used+n > s.meter.limit {
		return false
	}
	s.used += n
	return true
}

// truncate ends the response at the client's cap, which CreateResponse could
// not take: the upstream is closed and the response ends incomplete for
// max_output_tokens, with the output sent so far.
func (s *responsesStream) truncate() ([]byte, error) {
	s.truncated = true
	_ = s.source.Close()
	if err := s.output.mergeTerminal(gjson.Parse("[]")); err != nil {
		return nil, err
	}
	output, err := s.output.marshal(true)
	if err != nil {
		return nil, err
	}
	response := map[string]json.RawMessage{}
	if s.template != "" && common.Unmarshal([]byte(s.template), &response) != nil {
		return nil, errIncomplete
	}
	response["status"] = json.RawMessage(`"incomplete"`)
	response["incomplete_details"] = json.RawMessage(`{"reason":"max_output_tokens"}`)
	response["output"] = output
	usage := map[string]json.RawMessage{}
	if err := s.completeUsage(usage, output); err != nil {
		return nil, err
	}
	response["usage"] = marshalRaw(usage)
	event := map[string]any{"type": "response.incomplete", "response": response}
	if s.sequence > 0 {
		event["sequence_number"] = s.sequence + 1
	}
	raw, err := common.Marshal(event)
	if err != nil {
		return nil, err
	}
	s.end, s.completed = io.EOF, true
	return withoutKiroFields(raw)
}

func (s *responsesStream) terminal(data []byte, e gjson.Result, kind string) ([]byte, error) {
	r := e.Get("response")
	if kind == "response.failed" || kind == "error" || r.Get("status").String() == "failed" {
		return nil, errResponseFailed
	}
	if !r.IsObject() || (len(s.output.items) == 0 && !r.Get("output").IsArray() && !r.Get("usage").IsObject() && r.Get("status").Str == "") {
		return nil, errIncomplete
	}
	if len(s.output.items) != 0 || len(r.Get("output").Array()) > 0 {
		if err := s.output.mergeTerminal(r.Get("output")); err != nil {
			return nil, err
		}
	}
	output, err := s.output.marshal(kind == "response.incomplete" || r.Get("status").Str == "incomplete")
	if err != nil {
		return nil, err
	}
	var response map[string]json.RawMessage
	if err = common.Unmarshal([]byte(r.Raw), &response); err != nil {
		return nil, errIncomplete
	}
	if !s.streaming {
		response["output"] = output
	}
	usage := map[string]json.RawMessage{}
	if raw := response["usage"]; raw != nil && string(raw) != "null" {
		if common.Unmarshal(raw, &usage) != nil || usage == nil {
			return nil, errIncomplete
		}
	}
	if err = s.completeUsage(usage, output); err != nil {
		return nil, err
	}
	response["usage"] = marshalRaw(usage)
	var evt map[string]json.RawMessage
	if err = common.Unmarshal(data, &evt); err != nil {
		return nil, errIncomplete
	}
	evt["response"] = marshalRaw(response)
	raw, err := common.Marshal(evt)
	if err != nil {
		return nil, err
	}
	return withoutKiroFields(raw)
}

// completeUsage keeps the token counts Kiro reports and counts the missing
// ones locally: Kiro reports credits, not tokens.
func (s *responsesStream) completeUsage(usage map[string]json.RawMessage, output []byte) error {
	counts := map[string]int{}
	for _, key := range []string{"input_tokens", "output_tokens"} {
		if raw, ok := usage[key]; ok {
			n, err := strconv.ParseInt(string(raw), 10, 64)
			if err != nil || n < 0 || n > 1_000_000_000 {
				return errIncomplete
			}
			counts[key] = int(n)
			continue
		}
		if key == "input_tokens" {
			counts[key] = s.meter.prompt
			continue
		}
		var parts []string
		outputText(gjson.ParseBytes(output), &parts)
		if s.meter.output != nil {
			counts[key] = s.meter.output(strings.Join(parts, "\n"))
		}
	}
	if s.truncated {
		// Settled on what was sent, which the cap bounds.
		counts["output_tokens"] = min(counts["output_tokens"], s.meter.limit)
	}
	usage["input_tokens"] = marshalRaw(counts["input_tokens"])
	usage["output_tokens"] = marshalRaw(counts["output_tokens"])
	usage["total_tokens"] = marshalRaw(counts["input_tokens"] + counts["output_tokens"])
	return nil
}

// outputText collects the generated text of Responses output items: message
// text and refusals, reasoning, and function calls with their arguments.
func outputText(v gjson.Result, parts *[]string) {
	if v.Type == gjson.String {
		if v.Str != "" {
			*parts = append(*parts, v.Str)
		}
		return
	}
	if v.IsArray() {
		for _, item := range v.Array() {
			outputText(item, parts)
		}
		return
	}
	switch v.Get("type").Str {
	case "function_call":
		*parts = append(*parts, v.Get("name").Str, v.Get("arguments").Str)
		return
	case "reasoning":
		content := v.Get("content")
		if len(content.Array()) == 0 {
			content = v.Get("summary")
		}
		outputText(content, parts)
		return
	}
	for _, k := range []string{"text", "refusal", "content"} {
		if child := v.Get(k); child.Exists() {
			outputText(child, parts)
		}
	}
}

// withoutKiroFields removes from an event's response the kiro: metadata the
// gateway sent upstream and the credit and context figures Kiro adds to usage.
// They describe the gateway's own Kiro account, not the client's request.
func withoutKiroFields(data []byte) ([]byte, error) {
	var evt, response map[string]json.RawMessage
	if err := common.Unmarshal(data, &evt); err != nil {
		return nil, errIncomplete
	}
	if err := common.Unmarshal(evt["response"], &response); err != nil {
		return nil, errIncomplete
	}
	for _, field := range []string{"metadata", "usage"} {
		var values map[string]json.RawMessage
		if common.Unmarshal(response[field], &values) != nil || values == nil {
			continue
		}
		for key := range values {
			if strings.HasPrefix(key, "kiro:") {
				delete(values, key)
			}
		}
		response[field] = marshalRaw(values)
	}
	evt["response"] = marshalRaw(response)
	return common.Marshal(evt)
}

// outputCollector merges the output items of a stream. The terminal event may
// omit output already delivered by the stream, so native output items
// (including unknown fields) keep their original indices.
type outputCollector struct {
	arguments map[int]*strings.Builder
	items     map[int]map[string]json.RawMessage
	positions map[string]int
	calls     map[string]int
	nextIndex int
	order     []int
	parts     map[int]map[int]*outputPart
}

type outputPart struct {
	kind        string
	text        strings.Builder
	annotations json.RawMessage
}

func (c *outputCollector) observe(e gjson.Result) error {
	kind := e.Get("type").String()
	switch kind {
	case "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.reasoning_text.delta", "response.reasoning_text.done", "response.reasoning.delta", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done",
		"response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done":
	default:
		return nil
	}
	id := e.Get("item_id").String()
	if id == "" {
		id = e.Get("item.id").String()
	}
	index, seen := c.positions[id]
	if !seen {
		index, seen = c.calls[e.Get("item.call_id").Str]
	}
	if pos := e.Get("output_index"); pos.Exists() {
		index = int(pos.Int())
	} else if !seen && (kind == "response.output_item.added" || kind == "response.output_item.done") {
		index = c.nextIndex
	}
	if index < 0 || index > 1<<20 {
		return errIncomplete
	}
	c.nextIndex = max(c.nextIndex, index+1)
	if id != "" {
		c.positions[id] = index
	}
	if c.items[index] == nil && (kind == "response.function_call_arguments.delta" || kind == "response.function_call_arguments.done") {
		// A compact stream may send arguments before the terminal call item.
		c.items[index] = map[string]json.RawMessage{"type": json.RawMessage(`"function_call"`)}
		if id != "" {
			c.items[index]["id"] = marshalRaw(id)
		}
	}
	switch kind {
	case "response.output_item.added", "response.output_item.done":
		item := e.Get("item")
		if !item.IsObject() {
			return errIncomplete
		}
		record := c.items[index]
		if record == nil {
			record = map[string]json.RawMessage{}
			c.items[index] = record
		}
		if call := item.Get("call_id").Str; call != "" {
			c.calls[call] = index
		}
		if common.Unmarshal([]byte(item.Raw), &record) != nil {
			return errIncomplete
		}
	case "response.function_call_arguments.delta":
		if c.arguments[index] == nil {
			c.arguments[index] = new(strings.Builder)
		}
		c.arguments[index].WriteString(e.Get("delta").String())
	case "response.function_call_arguments.done":
		if args := e.Get("arguments"); args.Type == gjson.String && args.Str != "" {
			delete(c.arguments, index)
			c.items[index]["arguments"] = json.RawMessage(args.Raw)
		}
	default:
		reasoning := strings.HasPrefix(kind, "response.reasoning")
		summary := strings.HasPrefix(kind, "response.reasoning_summary_")
		if reasoning && c.items[index] == nil {
			c.items[index] = map[string]json.RawMessage{"type": json.RawMessage(`"reasoning"`)}
		}
		if c.items[index] == nil {
			c.items[index] = map[string]json.RawMessage{"type": json.RawMessage(`"message"`), "role": json.RawMessage(`"assistant"`)}
			if id != "" {
				c.items[index]["id"] = marshalRaw(id)
			}
		}
		partIndex := int(e.Get("content_index").Int())
		if summary {
			partIndex = int(e.Get("summary_index").Int())
		}
		if partIndex < 0 || partIndex > 1<<20 {
			return errIncomplete
		}
		if c.parts[index] == nil {
			c.parts[index] = map[int]*outputPart{}
		}
		// Reasoning summary and full-text parts can share index 0.
		if summary {
			partIndex += 1 << 21
		}
		part := c.parts[index][partIndex]
		if part == nil {
			part = &outputPart{kind: "output_text"}
			c.parts[index][partIndex] = part
		}
		if reasoning {
			part.kind = "reasoning_text"
			if summary {
				part.kind = "summary_text"
			}
		}
		if strings.HasPrefix(kind, "response.refusal.") {
			part.kind = "refusal"
		}
		if strings.HasSuffix(kind, ".delta") {
			part.text.WriteString(e.Get("delta").String())
			return nil
		}
		field := "text"
		if part.kind == "refusal" {
			field = "refusal"
		}
		if final := e.Get(field); final.Type == gjson.String {
			part.text.Reset()
			part.text.WriteString(final.String())
		}
		if annotations := e.Get("annotations"); annotations.IsArray() {
			part.annotations = json.RawMessage(annotations.Raw)
		}
	}
	return nil
}

// mergeTerminal merges the terminal output, which can be a subset of the
// items already streamed. Stable IDs are matched before array offsets, which
// shift when the service omits an item.
func (c *outputCollector) mergeTerminal(output gjson.Result) error {
	// Keep both the order of the streamed items and the relative order of
	// the terminal items. Pending terminal-only items precede their next
	// known neighbour, or follow the remaining stream if there is none.
	stream := slices.Sorted(maps.Keys(c.items))
	var pending []int
	for pos, item := range output.Array() {
		if !item.IsObject() {
			return errIncomplete
		}
		id, call := item.Get("id").Str, item.Get("call_id").Str
		index, known := c.positions[id]
		if !known && call != "" {
			index, known = c.calls[call]
		}
		if !known && len(stream) > 0 && pos == stream[0] {
			// Text deltas may carry only an index, without an item ID.
			prior := c.items[pos]
			known = gjson.ParseBytes(prior["type"]).Str == item.Get("type").Str &&
				(len(prior["id"]) == 0 || id == "") && (len(prior["call_id"]) == 0 || call == "")
			index = pos
		}
		if known {
			for len(stream) > 0 && stream[0] < index {
				c.order = append(c.order, stream[0])
				stream = stream[1:]
			}
			if len(stream) == 0 || stream[0] != index {
				return errIncomplete
			}
			stream = stream[1:]
			c.order = append(c.order, pending...)
			pending = pending[:0]
			c.order = append(c.order, index)
		} else {
			index = c.nextIndex
			c.nextIndex++
			pending = append(pending, index)
			if len(stream) > 0 && pos < stream[0] {
				c.order = append(c.order, pending...)
				pending = pending[:0]
			}
		}
		fields := c.items[index]
		if fields == nil {
			fields = make(map[string]json.RawMessage)
			c.items[index] = fields
		}
		for field, value := range item.Map() {
			switch field {
			case "content", "arguments", "encrypted_content", "summary":
				if len(fields[field]) != 0 && (value.Type == gjson.Null || value.Raw == `""` || value.IsArray() && len(value.Array()) == 0) {
					continue
				}
			}
			fields[field] = json.RawMessage(value.Raw)
		}
	}
	c.order = append(c.order, stream...)
	c.order = append(c.order, pending...)
	return nil
}

func (c *outputCollector) marshal(incomplete bool) ([]byte, error) {
	out := make([]map[string]json.RawMessage, 0, len(c.items))
	for _, index := range c.order {
		item := c.items[index]
		unfinished := incomplete && string(item["status"]) != `"completed"`
		if unfinished {
			item["status"] = json.RawMessage(`"incomplete"`)
		}
		if string(item["type"]) == `"function_call"` {
			args := gjson.ParseBytes(item["arguments"]).String()
			if delta := c.arguments[index]; args == "" && delta != nil {
				args = delta.String()
				item["arguments"] = marshalRaw(args)
			}
			if !unfinished && !gjson.Valid(args) {
				return nil, errIncomplete
			}
		}
		if len(c.parts[index]) > 0 {
			byField := map[string][]map[string]any{}
			for _, n := range slices.Sorted(maps.Keys(c.parts[index])) {
				part := c.parts[index][n]
				value := map[string]any{"type": part.kind}
				if part.kind == "refusal" {
					value["refusal"] = part.text.String()
				} else {
					value["text"] = part.text.String()
					if part.annotations != nil {
						value["annotations"] = part.annotations
					}
				}
				field := "content"
				if part.kind == "summary_text" {
					field = "summary"
				}
				byField[field] = append(byField[field], value)
			}
			for k, parts := range byField {
				if len(gjson.ParseBytes(item[k]).Array()) == 0 {
					item[k] = marshalRaw(parts)
				}
			}
		}
		out = append(out, item)
	}
	return common.Marshal(out)
}
