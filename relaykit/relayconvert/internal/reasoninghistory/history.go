// Package reasoninghistory carries provider-native reasoning across a Claude
// Messages ↔ OpenAI Responses conversion. The original blocks or items are
// sealed by the host's convmeta.ReasoningHistorySealer into the other
// protocol's opaque field, and restored verbatim when the client sends them
// back, so the upstream sees exactly the reasoning it produced.
package reasoninghistory

import (
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// A token states which upstream protocol its items belong to, so a token
// minted for one direction is never replayed into the other.
const (
	// KindClaude items are Claude thinking and redacted_thinking blocks,
	// carried to a Responses client in encrypted_content.
	KindClaude = "claude"
	// KindResponses items are Responses reasoning items, carried to a
	// Messages client in a thinking signature.
	KindResponses = "responses"
)

const packetVersion = 1

type packet struct {
	Version int               `json:"v"`
	Kind    string            `json:"kind"`
	Items   []json.RawMessage `json:"items"`
}

// SealerOf returns the host sealer, or nil when continuation is disabled.
func SealerOf(info convmeta.Meta) convmeta.ReasoningHistorySealer {
	if info == nil {
		return nil
	}
	return convmeta.OptionsOf(info).ReasoningHistory
}

// ResponsesClientSealerOf returns the host sealer for reasoning handed to a
// Responses client, or nil when the client did not ask for
// reasoning.encrypted_content: a token is larger than the thinking it carries,
// and a client that cannot replay it gains nothing from it.
func ResponsesClientSealerOf(info convmeta.Meta) convmeta.ReasoningHistorySealer {
	if info == nil || !convmeta.OptionsOf(info).ResponsesIncludesEncryptedReasoning {
		return nil
	}
	return SealerOf(info)
}

// Seal returns a token carrying items, or "" when there is nothing to carry,
// no sealer, or sealing failed; the reasoning is then dropped as before.
func Seal(sealer convmeta.ReasoningHistorySealer, kind string, items []json.RawMessage) string {
	if sealer == nil || len(items) == 0 {
		return ""
	}
	plaintext, err := kitutil.Marshal(packet{Version: packetVersion, Kind: kind, Items: items})
	if err != nil {
		kitutil.LogSystemError(fmt.Sprintf("encode reasoning history: %v", err))
		return ""
	}
	token, err := sealer.Seal(plaintext)
	if err != nil {
		kitutil.LogSystemError(fmt.Sprintf("seal reasoning history: %v", err))
		return ""
	}
	return token
}

// Open returns the items of a token of the given kind sealed for the current
// route. Anything else, including the provider's own signatures and
// encrypted content, reports false.
func Open(sealer convmeta.ReasoningHistorySealer, kind string, token string) ([]json.RawMessage, bool) {
	if sealer == nil || token == "" || !sealer.Owns(token) {
		return nil, false
	}
	plaintext, ok := sealer.Open(token)
	if !ok {
		return nil, false
	}
	var decoded packet
	if err := kitutil.Unmarshal(plaintext, &decoded); err != nil {
		return nil, false
	}
	if decoded.Version != packetVersion || decoded.Kind != kind || len(decoded.Items) == 0 {
		return nil, false
	}
	return decoded.Items, true
}

// ClaudeBlock is the replayable form of a Claude thinking or redacted_thinking
// block: only the fields Claude verifies, so nothing else rides along.
func ClaudeBlock(block *dto.ClaudeMediaMessage) (json.RawMessage, bool) {
	if block == nil {
		return nil, false
	}
	var value any
	switch block.Type {
	case "thinking":
		thinking := ""
		if block.Thinking != nil {
			thinking = *block.Thinking
		}
		value = struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature,omitempty"`
		}{block.Type, thinking, block.Signature}
	case "redacted_thinking":
		if block.Data == "" {
			return nil, false
		}
		value = struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{block.Type, block.Data}
	default:
		return nil, false
	}
	encoded, err := kitutil.Marshal(value)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// ResponsesItem is the replayable form of a Responses reasoning output item,
// kept as the upstream returned it, ID included, the way stateless clients
// such as Codex replay their history. It is produced only for items that
// carry encrypted content, which is what a stateless replay depends on.
func ResponsesItem(output *dto.ResponsesOutput) (json.RawMessage, bool) {
	if output == nil || output.Type != "reasoning" || output.EncryptedContent == "" {
		return nil, false
	}
	summary := output.Summary
	if summary == nil {
		summary = []dto.ResponsesReasoningSummaryPart{}
	}
	// Reasoning text parts share the summary part's shape.
	var content []dto.ResponsesReasoningSummaryPart
	for _, part := range output.Content {
		if part.Type == "reasoning_text" {
			content = append(content, dto.ResponsesReasoningSummaryPart{Type: part.Type, Text: part.Text})
		}
	}
	encoded, err := kitutil.Marshal(struct {
		Type             string                              `json:"type"`
		ID               string                              `json:"id,omitempty"`
		Summary          []dto.ResponsesReasoningSummaryPart `json:"summary"`
		Content          []dto.ResponsesReasoningSummaryPart `json:"content,omitempty"`
		EncryptedContent string                              `json:"encrypted_content"`
	}{output.Type, output.ID, summary, content, output.EncryptedContent})
	if err != nil {
		return nil, false
	}
	return encoded, true
}
