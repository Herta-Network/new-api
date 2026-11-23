// The GenerateAssistantResponse decoding is adapted from AstrLink's
// core/internal/providerapi/kiro_stream.go (https://github.com/Calcium-Ion/AstrLink,
// Copyright 2026 AstrLink contributors, Apache License 2.0). new-api changed
// it to emit Anthropic Messages events on its own Read, to count usage
// locally, and to end a response without Kiro's stop signal with an error.

package kiro

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// maxFrameBytes bounds one event-stream frame; Kiro's events are far smaller.
const maxFrameBytes = 16 << 20

// errIncomplete is a response that ended without Kiro's stop signal or broke
// its protocol. It is never settled as a success.
var errIncomplete = errors.New("Kiro response stream was incomplete or invalid")

// garMeter counts a GenerateAssistantResponse locally, since Kiro reports no
// token counts: the input estimated for the request, and the output from its
// text, reasoning and tool calls.
type garMeter struct {
	prompt int
	output func(text string, toolUses int) int
	// limit is the client's output cap where Kiro cannot enforce it, zero for
	// none; count measures streamed output against it.
	limit int
	count func(text string) int
	// stops are the client's stop sequences, which Kiro has none of: the
	// response ends before the first one in its text.
	stops []string
	// oneTool ends the response after its first tool call, for a client that
	// disabled parallel tool use.
	oneTool bool
}

type garBlock struct {
	kind, id, name, data string
	signature            strings.Builder
	text                 strings.Builder
	input                strings.Builder
	closed               bool
}

// garStream turns a GenerateAssistantResponse event stream into an Anthropic
// Messages response: SSE events for a streaming client, or one message.
type garStream struct {
	source    io.ReadCloser
	model     string
	streaming bool
	meter     garMeter
	// maxBytes bounds a response read whole; zero for a stream.
	maxBytes int

	id         string
	pending    []byte
	blocks     []*garBlock
	toolIndex  map[string]int
	lastTool   string
	textIndex  int
	started    bool
	done       bool
	terminal   bool
	output     bool
	stopReason string
	usage      map[string]int
	bytes      int
	failure    error
	// used is the output counted against meter.limit; truncated reports a
	// response the relay ended at that cap.
	used      int
	truncated bool
	// held is text of the open text block not sent yet because it may begin
	// a stop sequence; stopSequence is the one the response ended at.
	held         string
	stopSequence string
}

func newGARStream(source io.ReadCloser, model string, streaming bool, meter garMeter, maxBytes int) *garStream {
	return &garStream{source: source, model: model, streaming: streaming, meter: meter, maxBytes: maxBytes, id: "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""), textIndex: -1, toolIndex: map[string]int{}}
}

func (r *garStream) Close() error { return r.source.Close() }

// Read serves the Messages events of a streaming client. A response that
// fails or ends early ends with an Anthropic error event, never a stop, so
// the relay fails the request instead of settling it.
func (r *garStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.failure != nil {
			return 0, io.EOF
		}
		if r.done {
			return 0, io.EOF
		}
		if err := r.next(); err != nil {
			r.failure = err
			r.emit("error", map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": err.Error()}})
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// message reads the whole response for a client that does not stream.
func (r *garStream) message() ([]byte, error) {
	defer r.Close()
	for !r.done {
		if err := r.next(); err != nil {
			return nil, err
		}
	}
	content := make([]any, 0, len(r.blocks))
	for _, block := range r.blocks {
		switch block.kind {
		case "tool_use":
			// A call the cap cut off has no usable input.
			if completeToolInput(block) != nil {
				continue
			}
			content = append(content, map[string]any{"type": "tool_use", "id": block.id, "name": block.name, "input": json.RawMessage(block.input.String())})
		case "thinking":
			b := map[string]any{"type": "thinking", "thinking": block.text.String()}
			if block.signature.Len() > 0 {
				b["signature"] = block.signature.String()
			}
			content = append(content, b)
		case "redacted_thinking":
			content = append(content, map[string]any{"type": block.kind, "data": block.data})
		default:
			content = append(content, map[string]any{"type": "text", "text": block.text.String()})
		}
	}
	return common.Marshal(map[string]any{"id": r.id, "type": "message", "role": "assistant", "model": r.model, "content": content, "stop_reason": r.stopReason, "stop_sequence": r.matchedStop(), "usage": r.usage})
}

// matchedStop is the stop sequence the response ended at, or null.
func (r *garStream) matchedStop() any {
	if r.stopReason != "stop_sequence" {
		return nil
	}
	return r.stopSequence
}

func (r *garStream) emit(event string, value any) {
	if !r.streaming {
		return
	}
	raw, err := common.Marshal(value)
	if err != nil {
		return
	}
	r.pending = append(r.pending, "event: "+event+"\ndata: "...)
	r.pending = append(r.pending, raw...)
	r.pending = append(r.pending, '\n', '\n')
}

func (r *garStream) start() {
	if r.started {
		return
	}
	r.started = true
	r.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": r.id, "type": "message", "role": "assistant", "model": r.model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": r.meter.prompt, "output_tokens": 0},
	}})
}

func (r *garStream) next() error {
	limit := maxFrameBytes
	if r.maxBytes > 0 {
		limit = min(limit, r.maxBytes-r.bytes)
	}
	headers, payload, size, err := readFrame(r.source, limit)
	if err == io.EOF {
		return r.finish()
	}
	if err != nil {
		return errIncomplete
	}
	r.bytes += size
	if kind := headers[":message-type"]; kind == "exception" || kind == "error" {
		return fmt.Errorf("Kiro stream failed: %s", cleanHeader(headers[":exception-type"]+headers[":error-code"]))
	}
	event := gjson.ParseBytes(payload)
	if !gjson.ValidBytes(payload) || !event.IsObject() {
		return errIncomplete
	}
	switch headers[":event-type"] {
	case "metadataEvent":
		if reason := event.Get("stopReason"); reason.Exists() {
			return r.setStop(reason.String())
		}
	case "error", "validationError", "serviceUnavailableError":
		return fmt.Errorf("Kiro stream failed: %s", cleanHeader(headers[":event-type"]))
	case "assistantResponseEvent":
		return r.text("text", event.Get("content").String())
	case "reasoningContentEvent":
		if text := event.Get("text").String(); text != "" {
			if err := r.text("thinking", text); err != nil || r.done {
				return err
			}
		}
		if sig := event.Get("signature").String(); sig != "" {
			r.signature(sig)
		}
		if data := event.Get("redactedContent").String(); data != "" {
			return r.redacted(data)
		}
	case "toolUseEvent":
		return r.tool(event)
	case "meteringEvent":
		// Some versions send a final metering frame in place of stopReason.
		if event.Get("unit").Str == "credit" && event.Get("usage").Type == gjson.Number {
			r.terminal = true
		}
	}
	return nil
}

// cleanHeader keeps an exception name printable and short.
func cleanHeader(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 33 || r > 126 {
			return -1
		}
		return r
	}, name)
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		return "exception"
	}
	return name
}

func (r *garStream) setStop(reason string) error {
	switch reason {
	case "MAX_TOKENS", "max_tokens", "maxTokens", "length":
		r.stopReason = "max_tokens"
	case "TOOL_USE", "tool_use", "toolUse":
		r.stopReason = "tool_use"
	case "END_TURN", "end_turn":
		r.stopReason = "end_turn"
	case "CONTENT_FILTERED":
		r.stopReason = "refusal"
	case "PAUSE_TURN":
		r.stopReason = "pause_turn"
	case "MODEL_CONTEXT_WINDOW_EXCEEDED":
		r.stopReason = "model_context_window_exceeded"
	default:
		return fmt.Errorf("Kiro stopped with %s", cleanHeader(reason))
	}
	r.terminal = true
	return nil
}

// fits reports whether text, and extra tokens, keep the output within the
// client's cap, and counts them if so.
func (r *garStream) fits(text string, extra int) bool {
	if r.meter.limit <= 0 {
		return true
	}
	n := extra
	if r.meter.count != nil {
		n += r.meter.count(text)
	}
	if r.used+n > r.meter.limit {
		return false
	}
	r.used += n
	return true
}

// truncate ends the response at the client's cap, which Kiro could not
// enforce: the upstream is closed and the response stops for max_tokens.
func (r *garStream) truncate() error {
	r.truncated, r.terminal, r.stopReason = true, true, "max_tokens"
	_ = r.source.Close()
	return r.finish()
}

// end ends the response before Kiro does, for a reason Kiro could not act on
// (a stop sequence, a single tool call): the upstream is closed and the
// response stops as Anthropic's would.
func (r *garStream) end(reason string) error {
	r.terminal, r.stopReason = true, reason
	_ = r.source.Close()
	return r.finish()
}

func (r *garStream) text(kind, text string) error {
	if text == "" {
		return nil
	}
	if !r.fits(text, 0) {
		return r.truncate()
	}
	if kind == "text" && len(r.meter.stops) > 0 {
		// Text that may begin a stop sequence waits for the next delta; a
		// sequence only ends the text block it is in.
		pending := r.held + text
		r.held = ""
		at, stop := -1, ""
		for _, sequence := range r.meter.stops {
			if i := strings.Index(pending, sequence); i >= 0 && (at < 0 || i < at) {
				at, stop = i, sequence
			}
		}
		if at >= 0 {
			if err := r.send(kind, pending[:at]); err != nil {
				return err
			}
			r.stopSequence = stop
			return r.end("stop_sequence")
		}
		keep := 0
		for _, sequence := range r.meter.stops {
			for n := min(len(sequence)-1, len(pending)); n > keep; n-- {
				if strings.HasSuffix(pending, sequence[:n]) {
					keep = n
					break
				}
			}
		}
		held := pending[len(pending)-keep:]
		text = pending[:len(pending)-keep]
		if held != "" {
			// Held text belongs to the open text block, which sends it when
			// it ends without the sequence.
			r.openText(kind)
		}
		r.held = held
	}
	return r.send(kind, text)
}

// send adds text to the open text or thinking block, opening one as needed.
func (r *garStream) send(kind, text string) error {
	if text == "" {
		return nil
	}
	if r.textIndex >= 0 && r.blocks[r.textIndex].signature.Len() > 0 {
		r.closeText()
	}
	r.openText(kind)
	r.blocks[r.textIndex].text.WriteString(text)
	delta := map[string]string{"type": kind + "_delta"}
	if kind == "thinking" {
		delta["thinking"] = text
	} else {
		delta["text"] = text
	}
	r.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": r.textIndex, "delta": delta})
	return nil
}

func (r *garStream) openText(kind string) {
	r.start()
	r.output = true
	if r.textIndex < 0 || r.blocks[r.textIndex].kind != kind || r.blocks[r.textIndex].closed {
		r.closeText()
		r.textIndex = len(r.blocks)
		r.blocks = append(r.blocks, &garBlock{kind: kind})
		block := map[string]any{"type": kind}
		if kind == "thinking" {
			block["thinking"] = ""
		} else {
			block["text"] = ""
		}
		r.emit("content_block_start", map[string]any{"type": "content_block_start", "index": r.textIndex, "content_block": block})
	}
}

func (r *garStream) signature(sig string) {
	r.openText("thinking")
	r.blocks[r.textIndex].signature.WriteString(sig)
	r.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": r.textIndex, "delta": map[string]string{"type": "signature_delta", "signature": sig}})
}

func (r *garStream) redacted(data string) error {
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return errIncomplete
	}
	r.start()
	r.closeText()
	index := len(r.blocks)
	r.output = true
	r.blocks = append(r.blocks, &garBlock{kind: "redacted_thinking", data: data})
	r.emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]string{"type": "redacted_thinking", "data": data}})
	r.closeBlock(index)
	return nil
}

func (r *garStream) closeBlock(index int) {
	if r.blocks[index].closed {
		return
	}
	r.blocks[index].closed = true
	r.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (r *garStream) closeText() {
	if r.textIndex >= 0 {
		r.flushHeld()
		r.closeBlock(r.textIndex)
		r.textIndex = -1
	}
}

// flushHeld sends the text held for a stop sequence once its block ends
// without one.
func (r *garStream) flushHeld() {
	held := r.held
	r.held = ""
	if held == "" || r.textIndex < 0 || r.blocks[r.textIndex].kind != "text" {
		return
	}
	r.blocks[r.textIndex].text.WriteString(held)
	r.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": r.textIndex, "delta": map[string]string{"type": "text_delta", "text": held}})
}

func (r *garStream) tool(event gjson.Result) error {
	id, name := event.Get("toolUseId").String(), event.Get("name").String()
	input := event.Get("input")
	fragment := ""
	switch {
	case input.Type == gjson.String:
		fragment = input.String()
	case input.IsObject():
		fragment = input.Raw
	case input.Exists() && input.Type != gjson.Null:
		return errIncomplete
	}
	if id == "" {
		id = r.lastTool
	}
	index, exists := r.toolIndex[id]
	if !exists {
		if id == "" || name == "" {
			return errIncomplete
		}
		if r.meter.oneTool && len(r.toolIndex) > 0 {
			return r.end("tool_use")
		}
		if !r.fits(name, claudeToolUseTokens) {
			return r.truncate()
		}
		r.start()
		r.closeText()
		r.output = true
		index = len(r.blocks)
		r.toolIndex[id] = index
		r.blocks = append(r.blocks, &garBlock{kind: "tool_use", id: id, name: name})
		r.emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}})
	}
	block := r.blocks[index]
	if block.closed || (name != "" && name != block.name) {
		return errIncomplete
	}
	r.lastTool = id
	if fragment != "" {
		if !r.fits(fragment, 0) {
			return r.truncate()
		}
		block.input.WriteString(fragment)
		r.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]string{"type": "input_json_delta", "partial_json": fragment}})
	}
	if event.Get("stop").Bool() {
		if err := completeToolInput(block); err != nil {
			return err
		}
		r.closeBlock(index)
		if r.meter.oneTool {
			return r.end("tool_use")
		}
	}
	return nil
}

func completeToolInput(block *garBlock) error {
	input := block.input.String()
	if input == "" {
		block.input.WriteString("{}")
		return nil
	}
	if !gjson.Valid(input) || !gjson.Parse(input).IsObject() {
		return errIncomplete
	}
	return nil
}

// finish ends a response whose event stream closed. Only a stop signal makes
// it complete; one without output is complete only when Kiro said why.
func (r *garStream) finish() error {
	if !r.terminal || (!r.output && (r.stopReason == "" || r.stopReason == "end_turn" || r.stopReason == "tool_use")) {
		return errIncomplete
	}
	r.start()
	r.flushHeld()
	var text []string
	for i, block := range r.blocks {
		switch block.kind {
		case "tool_use":
			if err := completeToolInput(block); err != nil && !r.truncated {
				return err
			}
			text = append(text, block.name, block.input.String())
		case "text", "thinking":
			text = append(text, block.text.String())
		}
		r.closeBlock(i)
	}
	if r.stopReason == "" {
		r.stopReason = "end_turn"
		if len(r.toolIndex) > 0 {
			r.stopReason = "tool_use"
		}
	}
	output := 0
	if r.meter.output != nil {
		output = r.meter.output(strings.Join(text, "\n"), len(r.toolIndex))
	}
	if r.truncated {
		// Settled on what was sent, which the cap bounds.
		output = min(output, r.meter.limit)
	}
	r.usage = map[string]int{"input_tokens": r.meter.prompt, "output_tokens": output}
	r.emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": r.stopReason, "stop_sequence": r.matchedStop()}, "usage": r.usage})
	r.emit("message_stop", map[string]string{"type": "message_stop"})
	r.done = true
	return nil
}

// readFrame reads one AWS event-stream message: its string headers, payload
// and size. Both CRCs are checked.
func readFrame(reader io.Reader, maxFrame int) (map[string]string, []byte, int, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(reader, prelude[:]); err != nil {
		if err == io.EOF {
			return nil, nil, 0, io.EOF
		}
		return nil, nil, 0, err
	}
	total, headerSize := binary.BigEndian.Uint32(prelude[:4]), binary.BigEndian.Uint32(prelude[4:8])
	if total < 16 || int64(total) > int64(maxFrame) || headerSize > total-16 || binary.BigEndian.Uint32(prelude[8:]) != crc32.ChecksumIEEE(prelude[:8]) {
		return nil, nil, 0, errIncomplete
	}
	frame := make([]byte, int(total)-12)
	if _, err := io.ReadFull(reader, frame); err != nil {
		return nil, nil, 0, errIncomplete
	}
	crc := crc32.NewIEEE()
	_, _ = crc.Write(prelude[:])
	_, _ = crc.Write(frame[:len(frame)-4])
	if binary.BigEndian.Uint32(frame[len(frame)-4:]) != crc.Sum32() {
		return nil, nil, 0, errIncomplete
	}
	headers := map[string]string{}
	for rest := frame[:headerSize]; len(rest) > 0; {
		nameSize := int(rest[0])
		rest = rest[1:]
		if nameSize == 0 || len(rest) < nameSize+1 {
			return nil, nil, 0, errIncomplete
		}
		name, kind := string(rest[:nameSize]), rest[nameSize]
		rest = rest[nameSize+1:]
		size := 0
		switch kind {
		case 0, 1:
		case 2:
			size = 1
		case 3:
			size = 2
		case 4:
			size = 4
		case 5, 8:
			size = 8
		case 9:
			size = 16
		case 6, 7:
			if len(rest) < 2 {
				return nil, nil, 0, errIncomplete
			}
			size = int(binary.BigEndian.Uint16(rest[:2]))
			rest = rest[2:]
		default:
			return nil, nil, 0, errIncomplete
		}
		if len(rest) < size {
			return nil, nil, 0, errIncomplete
		}
		if kind == 7 {
			headers[name] = string(rest[:size])
		}
		rest = rest[size:]
	}
	return headers, bytes.Clone(frame[headerSize : len(frame)-4]), int(total), nil
}
