package common

import (
	"bytes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// reasoningHistoryTokenPrefix marks a token this gateway issued. Its
	// version changes with the key derivation or the bound route fields.
	reasoningHistoryTokenPrefix = "narc1."
	// reasoningHistoryKeyInfo separates this key from the other keys derived
	// from CryptoSecret, and leads the bound route.
	reasoningHistoryKeyInfo = "new-api/reasoning-history/v1"
	// reasoningHistoryMaxTokenLength bounds what is decoded from a client.
	reasoningHistoryMaxTokenLength = 4 << 20
)

// reasoningHistorySealer seals the reasoning relaykit hands to a client when
// converting between Claude Messages and OpenAI Responses. A token opens only
// for the user, channel, upstream key, and upstream model that produced it;
// anything else is dropped from the history instead of failing the request.
// It reads the RelayInfo at each call, so a retry on another channel binds and
// checks the channel actually used.
type reasoningHistorySealer struct {
	info *RelayInfo
}

type reasoningHistoryKey struct {
	secret string
	aead   cipher.AEAD
}

var reasoningHistoryKeyCache atomic.Pointer[reasoningHistoryKey]

// reasoningHistoryAEAD derives the XChaCha20-Poly1305 key from CryptoSecret,
// which every instance of a deployment shares. Its 192-bit random nonces stay
// clear of collisions at any request volume. Changing CRYPTO_SECRET
// invalidates every outstanding token, which only drops their reasoning.
func reasoningHistoryAEAD(secret string) (cipher.AEAD, error) {
	if cached := reasoningHistoryKeyCache.Load(); cached != nil && cached.secret == secret {
		return cached.aead, nil
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, reasoningHistoryKeyInfo, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	reasoningHistoryKeyCache.Store(&reasoningHistoryKey{secret: secret, aead: aead})
	return aead, nil
}

// route is the AEAD associated data. It is never written into the token, so a
// token reveals no channel or account, and any difference fails to open:
// OpenAI encrypted reasoning decrypts only in its own organization, Claude
// signatures carry no promise across accounts, and another model may reject
// either. The user ID keeps one user's tokens out of another's requests.
func (s reasoningHistorySealer) route() ([]byte, bool) {
	info := s.info
	if info == nil || info.ChannelMeta == nil || info.ChannelId == 0 || info.UpstreamModelName == "" {
		return nil, false
	}
	keyHash := sha256.Sum256([]byte(info.ApiKey))
	route := make([]byte, 0, len(reasoningHistoryKeyInfo)+16+len(keyHash)+len(info.UpstreamModelName))
	route = append(route, reasoningHistoryKeyInfo...)
	route = binary.BigEndian.AppendUint64(route, uint64(info.UserId))
	route = binary.BigEndian.AppendUint64(route, uint64(info.ChannelId))
	route = append(route, keyHash[:]...)
	route = append(route, info.UpstreamModelName...)
	return route, true
}

func (s reasoningHistorySealer) Seal(plaintext []byte) (string, error) {
	route, ok := s.route()
	if !ok {
		return "", errors.New("reasoning history needs the channel and upstream model")
	}
	aead, err := reasoningHistoryAEAD(common.CryptoSecret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	_, _ = rand.Read(nonce)
	sealed := aead.Seal(nonce, nonce, plaintext, route)
	return reasoningHistoryTokenPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s reasoningHistorySealer) Open(token string) ([]byte, bool) {
	encoded, ok := strings.CutPrefix(token, reasoningHistoryTokenPrefix)
	if !ok || len(token) > reasoningHistoryMaxTokenLength {
		return nil, false
	}
	route, ok := s.route()
	if !ok {
		return nil, false
	}
	aead, err := reasoningHistoryAEAD(common.CryptoSecret)
	if err != nil {
		return nil, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, false
	}
	nonceSize := aead.NonceSize()
	plaintext, err := aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], route)
	if err != nil {
		if common.DebugEnabled {
			common.SysLog(common.LogText("channel #%d cannot use the reasoning history a client sent: it is sealed for another route or altered; dropping it", s.info.ChannelId))
		}
		return nil, false
	}
	return plaintext, true
}

func (s reasoningHistorySealer) Owns(value string) bool {
	return isReasoningHistoryToken(value)
}

func isReasoningHistoryToken(value string) bool {
	return strings.HasPrefix(value, reasoningHistoryTokenPrefix)
}

// responsesRequestIncludesEncryptedReasoning reports whether a Responses
// client asked for reasoning.encrypted_content, the field its reasoning
// history travels in.
func responsesRequestIncludesEncryptedReasoning(request any) bool {
	responsesRequest, ok := request.(*dto.OpenAIResponsesRequest)
	if !ok || responsesRequest == nil || len(responsesRequest.Include) == 0 {
		return false
	}
	var include []string
	if common.Unmarshal(responsesRequest.Include, &include) != nil {
		return false
	}
	return slices.Contains(include, "reasoning.encrypted_content")
}

// claudeContentHasReasoningHistory scans decoded content blocks for an issued
// token without converting them, so a request without one costs no re-encode.
func claudeContentHasReasoningHistory(content any) bool {
	switch blocks := content.(type) {
	case []dto.ClaudeMediaMessage:
		return slices.ContainsFunc(blocks, func(block dto.ClaudeMediaMessage) bool {
			return (block.Type == "thinking" && isReasoningHistoryToken(block.Signature)) ||
				(block.Type == "redacted_thinking" && isReasoningHistoryToken(block.Data))
		})
	case []any:
		return slices.ContainsFunc(blocks, func(raw any) bool {
			block, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			field := ""
			switch block["type"] {
			case "thinking":
				field = "signature"
			case "redacted_thinking":
				field = "data"
			default:
				return false
			}
			token, _ := block[field].(string)
			return isReasoningHistoryToken(token)
		})
	default:
		return false
	}
}

// RemoveClaudeReasoningHistory drops the thinking blocks of a Messages request
// that carry a token this gateway issued for a Responses upstream. A Claude
// upstream rejects such a signature, and the thinking signature repair would
// then also discard the native thinking that is still valid. Native
// signatures are kept.
func RemoveClaudeReasoningHistory(request *dto.ClaudeRequest) {
	if request == nil {
		return
	}
	messages := make([]dto.ClaudeMessage, 0, len(request.Messages))
	changed := false
	for _, message := range request.Messages {
		if message.Role != "assistant" || !claudeContentHasReasoningHistory(message.Content) {
			messages = append(messages, message)
			continue
		}
		blocks, err := message.ParseContent()
		if err != nil {
			messages = append(messages, message)
			continue
		}
		kept := make([]dto.ClaudeMediaMessage, 0, len(blocks))
		for _, block := range blocks {
			if (block.Type == "thinking" && isReasoningHistoryToken(block.Signature)) ||
				(block.Type == "redacted_thinking" && isReasoningHistoryToken(block.Data)) {
				continue
			}
			kept = append(kept, block)
		}
		if len(kept) == len(blocks) {
			messages = append(messages, message)
			continue
		}
		changed = true
		// Content of another kind always accompanies reasoning in a turn; a
		// turn of nothing but issued reasoning has nothing left to send.
		if len(kept) == 0 {
			continue
		}
		message.Content = kept
		messages = append(messages, message)
	}
	if changed {
		request.Messages = messages
	}
}

// RemoveResponsesReasoningHistory drops the reasoning input items of a
// Responses request that carry a token this gateway issued for a Claude
// upstream, which an OpenAI-compatible upstream cannot decrypt. This happens
// when a Responses client switches from a Claude model to an OpenAI one
// mid-conversation.
func RemoveResponsesReasoningHistory(request *dto.OpenAIResponsesRequest) error {
	// Most requests carry no issued token; skip decoding their input.
	if request == nil || !bytes.Contains(request.Input, []byte(reasoningHistoryTokenPrefix)) ||
		common.GetJsonType(request.Input) != "array" {
		return nil
	}
	var items []json.RawMessage
	if err := common.Unmarshal(request.Input, &items); err != nil {
		return err
	}
	kept := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var reasoning struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		}
		if common.Unmarshal(item, &reasoning) == nil && reasoning.Type == "reasoning" && isReasoningHistoryToken(reasoning.EncryptedContent) {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == len(items) {
		return nil
	}
	input, err := common.Marshal(kept)
	if err != nil {
		return err
	}
	request.Input = input
	return nil
}
