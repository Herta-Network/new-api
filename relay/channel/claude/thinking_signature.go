package claude

// The thinking signature repair is adapted from AstrLink
// (https://github.com/Calcium-Ion/AstrLink,
// core/internal/ingress/thinking_signature.go), licensed under the Apache
// License, Version 2.0. It was modified to run on the new-api relay types and
// JSON wrappers, and to preserve untouched JSON values as raw bytes.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

// thinkingSignatureErrorLimit bounds the upstream error inspected for a repair.
const thinkingSignatureErrorLimit = 64 * 1024

// DoRequestWithThinkingSignatureRepair sends a Messages request. When the
// upstream rejects a thinking signature carried in the conversation history,
// it removes the assistant thinking blocks and the settings that depend on
// them, then retries once on the same channel. Every other response is
// returned as received for the normal error handling and channel retry.
func DoRequestWithThinkingSignatureRepair(a channel.Adaptor, c *gin.Context, info *relaycommon.RelayInfo, body common.ReplayableBody) (any, error) {
	resp, err := a.DoRequest(c, info, body)
	if err != nil || !supportsThinkingSignatureRepair(info) {
		return resp, err
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil || httpResp.StatusCode != http.StatusBadRequest {
		return resp, nil
	}
	// Inspect only a bounded error, and keep every byte for the normal error
	// handling when no repair is selected.
	upstreamBody := httpResp.Body
	errorBody, readErr := io.ReadAll(io.LimitReader(upstreamBody, thinkingSignatureErrorLimit+1))
	httpResp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(errorBody), upstreamBody), upstreamBody}
	if readErr != nil || len(errorBody) > thinkingSignatureErrorLimit || !isThinkingSignatureError(errorBody) ||
		c.Request.Context().Err() != nil {
		return httpResp, nil
	}
	sentReader, err := body.NewReader()
	if err != nil {
		return httpResp, nil
	}
	sent, err := io.ReadAll(sentReader)
	_ = sentReader.Close()
	if err != nil {
		return httpResp, nil
	}
	repaired, changed := rectifyThinkingSignature(sent)
	if !changed {
		return httpResp, nil
	}
	service.CloseResponseBodyGracefully(httpResp)
	logger.LogWarn(c, common.LogText("channel #%d rejected a thinking signature in the conversation history; retrying once without thinking blocks", info.ChannelId))
	return a.DoRequest(c, info, bytes.NewReader(repaired))
}

// supportsThinkingSignatureRepair requires a Messages request sent to a
// Messages upstream for a Claude model. Model mapping must be resolved first:
// Anthropic-compatible providers such as DeepSeek and Kimi require thinking to
// be passed back unchanged, so other models are excluded even when they are
// served through the Messages API. A pass-through body is never rewritten.
func supportsThinkingSignatureRepair(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	if info.RelayFormat != types.RelayFormatClaude || info.GetFinalRequestRelayFormat() != types.RelayFormatClaude {
		return false
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return false
	}
	model := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(info.UpstreamModelName)), "anthropic/")
	return slices.ContainsFunc([]string{"claude-", "opus-", "sonnet-", "haiku-"}, func(prefix string) bool {
		return strings.HasPrefix(model, prefix)
	})
}

func isThinkingSignatureError(body []byte) bool {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if common.Unmarshal(body, &envelope) != nil {
		return false
	}
	message := envelope.Error.Message
	if message == "" {
		message = envelope.Message
	}
	message = strings.ToLower(message)
	thinking := strings.Contains(message, "thinking")
	// A bare authentication/request signature error is unrelated. Some
	// validators identify the history block only through its JSON field path.
	if strings.Contains(message, "signature") && (thinking || (strings.Contains(message, "messages.") && strings.Contains(message, ".signature"))) {
		return true
	}
	return thinking && ((strings.Contains(message, "expected") && strings.Contains(message, "found")) ||
		strings.Contains(message, "cannot be modified") || strings.Contains(message, "thinking block must contain"))
}

type thinkingRepairTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// rectifyThinkingSignature repairs only the assistant history and the
// thinking-dependent settings. Tool calls, results, user content and unknown
// fields keep their raw bytes, so large integers in tool inputs or metadata
// are not rounded.
func rectifyThinkingSignature(body []byte) ([]byte, bool) {
	var document map[string]json.RawMessage
	if common.Unmarshal(body, &document) != nil || document == nil {
		return nil, false
	}
	var messages []json.RawMessage
	if common.Unmarshal(document["messages"], &messages) != nil || len(messages) == 0 {
		return nil, false
	}
	changed := false
	messagesChanged := false
	for i, entry := range messages {
		var message map[string]json.RawMessage
		if common.Unmarshal(entry, &message) != nil {
			continue
		}
		var role string
		if common.Unmarshal(message["role"], &role) != nil || role != "assistant" {
			continue
		}
		var content []json.RawMessage
		if common.Unmarshal(message["content"], &content) != nil {
			continue
		}
		repaired := make([]any, 0, len(content))
		modified := false
		for _, block := range content {
			var header struct {
				Type     string `json:"type"`
				Thinking any    `json:"thinking"`
			}
			if common.Unmarshal(block, &header) == nil {
				switch header.Type {
				case "thinking":
					modified = true
					if text, _ := header.Thinking.(string); strings.TrimSpace(text) != "" {
						repaired = append(repaired, thinkingRepairTextBlock{Type: "text", Text: text})
					}
					continue
				case "redacted_thinking":
					modified = true
					continue
				}
			}
			repaired = append(repaired, block)
		}
		if !modified {
			continue
		}
		if len(repaired) == 0 {
			repaired = append(repaired, thinkingRepairTextBlock{Type: "text", Text: "(thinking omitted)"})
		}
		encodedContent, err := common.Marshal(repaired)
		if err != nil {
			return nil, false
		}
		message["content"] = encodedContent
		encodedMessage, err := common.Marshal(message)
		if err != nil {
			return nil, false
		}
		messages[i] = encodedMessage
		messagesChanged = true
	}
	if messagesChanged {
		encodedMessages, err := common.Marshal(messages)
		if err != nil {
			return nil, false
		}
		document["messages"] = encodedMessages
		changed = true
	}
	if _, exists := document["thinking"]; exists {
		delete(document, "thinking")
		changed = true
	}
	if !changed {
		return nil, false
	}
	var management map[string]json.RawMessage
	if common.Unmarshal(document["context_management"], &management) == nil && management != nil {
		var edits []json.RawMessage
		if common.Unmarshal(management["edits"], &edits) == nil {
			kept := make([]json.RawMessage, 0, len(edits))
			for _, edit := range edits {
				var header struct {
					Type string `json:"type"`
				}
				_ = common.Unmarshal(edit, &header)
				if !strings.HasPrefix(header.Type, "clear_thinking_") {
					kept = append(kept, edit)
				}
			}
			if len(kept) != len(edits) {
				if len(kept) == 0 {
					delete(management, "edits")
				} else {
					encodedEdits, err := common.Marshal(kept)
					if err != nil {
						return nil, false
					}
					management["edits"] = encodedEdits
				}
				if len(management) == 0 {
					delete(document, "context_management")
				} else {
					encodedManagement, err := common.Marshal(management)
					if err != nil {
						return nil, false
					}
					document["context_management"] = encodedManagement
				}
			}
		}
	}
	result, err := common.Marshal(document)
	return result, err == nil
}
