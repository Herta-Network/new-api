package mistral

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeChoices rewrites a Mistral chat completion, or one chunk of its
// stream under member "delta", into the shape the gateway's OpenAI handlers
// read. A reasoning model answers with content as a list of chunks, text and
// thinking ones, which become content and reasoning_content as Vibe reads
// them (adapters/mistral.py); model_length becomes length. In a stream, the
// finish reason error is Mistral aborting the answer: it is taken out and
// aborted reports it, so the stream ends there as one the upstream cut short.
func normalizeChoices(body []byte, member string) (normalized []byte, aborted bool, err error) {
	choices := gjson.GetBytes(body, "choices")
	if !choices.IsArray() {
		return body, false, nil
	}
	for i, choice := range choices.Array() {
		prefix := fmt.Sprintf("choices.%d.", i)
		if content := choice.Get(member + ".content"); content.IsArray() {
			var text, reasoning strings.Builder
			for _, chunk := range content.Array() {
				switch chunk.Get("type").Str {
				case "text":
					text.WriteString(chunk.Get("text").Str)
				case "thinking":
					for _, part := range chunk.Get("thinking").Array() {
						if part.Type == gjson.String {
							reasoning.WriteString(part.Str)
						} else {
							reasoning.WriteString(part.Get("text").Str)
						}
					}
				}
			}
			if text.Len() > 0 || member == "message" {
				body, err = sjson.SetBytes(body, prefix+member+".content", text.String())
			} else {
				body, err = sjson.DeleteBytes(body, prefix+member+".content")
			}
			if err != nil {
				return nil, false, err
			}
			if reasoning.Len() > 0 {
				if body, err = sjson.SetBytes(body, prefix+member+".reasoning_content", reasoning.String()); err != nil {
					return nil, false, err
				}
			}
		}
		switch choice.Get("finish_reason").Str {
		case "model_length":
			body, err = sjson.SetBytes(body, prefix+"finish_reason", "length")
		case "error":
			if member == "delta" {
				aborted = true
				body, err = sjson.DeleteBytes(body, prefix+"finish_reason")
			}
		}
		if err != nil {
			return nil, false, err
		}
	}
	return body, aborted, nil
}

// streamNormalizer passes a Mistral event stream through normalizeChoices,
// one data line at a time, and ends it after a chunk that aborted.
//
// With several choices, Mistral finishes each in its own chunk. The gateway
// takes the first finish reason for the end of the stream, so the reasons are
// held back until every choice finished and then sent together: a choice
// aborted after another finished still ends the stream as cut short.
type streamNormalizer struct {
	upstream io.ReadCloser
	reader   *bufio.Reader
	choices  int
	finished map[int]string
	pending  []byte
	err      error
}

func newStreamNormalizer(upstream io.ReadCloser, choices int) *streamNormalizer {
	return &streamNormalizer{upstream: upstream, reader: bufio.NewReader(upstream), choices: choices, finished: make(map[int]string)}
}

func (s *streamNormalizer) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		line, err := s.reader.ReadBytes('\n')
		s.err = err
		payload, isData := bytes.CutPrefix(bytes.TrimRight(line, "\r\n"), []byte("data:"))
		payload = bytes.TrimSpace(payload)
		if !isData || len(payload) == 0 || payload[0] != '{' {
			s.pending = line
			continue
		}
		normalized, aborted, normalizeErr := normalizeChoices(payload, "delta")
		if normalizeErr != nil {
			s.pending = line
			continue
		}
		var finishChunk []byte
		if s.choices > 1 {
			for i, choice := range gjson.GetBytes(normalized, "choices").Array() {
				reason := choice.Get("finish_reason")
				if reason.Type != gjson.String || reason.Str == "" {
					continue
				}
				s.finished[int(choice.Get("index").Int())] = reason.Str
				if normalized, normalizeErr = sjson.DeleteBytes(normalized, fmt.Sprintf("choices.%d.finish_reason", i)); normalizeErr != nil {
					return 0, normalizeErr
				}
			}
			if !aborted && len(s.finished) >= s.choices {
				var choices []string
				for index := range s.choices {
					choices = append(choices, fmt.Sprintf(`{"index":%d,"delta":{},"finish_reason":%q}`, index, s.finished[index]))
				}
				finishChunk = fmt.Appendf(nil, `{"id":%s,"object":%s,"created":%s,"model":%s,"choices":[%s]}`,
					jsonOrNull(normalized, "id"), jsonOrNull(normalized, "object"), jsonOrNull(normalized, "created"), jsonOrNull(normalized, "model"), strings.Join(choices, ","))
			}
		}
		// Mistral reports the usage on the chunk that finishes the answer.
		// It goes out as a chunk of its own after it, as OpenAI sends it, so
		// a client that asked for no usage still gets that chunk's tool
		// calls and finish reason.
		usage := gjson.GetBytes(normalized, "usage")
		var usageChunk []byte
		if usage.IsObject() && len(gjson.GetBytes(normalized, "choices").Array()) > 0 {
			usageChunk = fmt.Appendf(nil, `{"id":%s,"object":%s,"created":%s,"model":%s,"choices":[],"usage":%s}`,
				jsonOrNull(normalized, "id"), jsonOrNull(normalized, "object"), jsonOrNull(normalized, "created"), jsonOrNull(normalized, "model"), usage.Raw)
			if stripped, err := sjson.DeleteBytes(normalized, "usage"); err == nil {
				normalized = stripped
			} else {
				usageChunk = nil
			}
		}
		s.pending = append(append([]byte("data: "), normalized...), '\n')
		for _, chunk := range [][]byte{finishChunk, usageChunk} {
			if chunk != nil {
				s.pending = append(append(append(s.pending, "\ndata: "...), chunk...), '\n')
			}
		}
		if aborted {
			s.err = io.EOF
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// jsonOrNull is the raw JSON of a member, or null when it is missing.
func jsonOrNull(body []byte, path string) string {
	if value := gjson.GetBytes(body, path); value.Exists() {
		return value.Raw
	}
	return "null"
}

func (s *streamNormalizer) Close() error {
	return s.upstream.Close()
}
