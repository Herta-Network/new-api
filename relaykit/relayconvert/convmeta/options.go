package convmeta

import "github.com/QuantumNous/new-api/relaykit/types"

// Options is the per-request snapshot of host configuration that converters
// consult. The host fills it from its settings system when constructing the
// Meta (see relaycommon.RelayInfo.ConvOptions); relaykit users fill it
// directly. Zero value = every adaptation disabled, no defaults applied.
type Options struct {
	Claude ClaudeOptions
	Gemini GeminiOptions

	// ToolLossPolicy controls whether a cross-protocol conversion may omit or
	// approximate built-in-tool semantics. The zero value uses the allow
	// policy: conversion succeeds and every loss is returned as a diagnostic.
	// safe/strict rejection is request-phase opt-in only; response and stream
	// conversion never reject regardless of this field.
	ToolLossPolicy types.ConversionLossPolicy

	// OpenRouterDialect marks the upstream as OpenRouter's OpenAI-compatible
	// surface, which accepts extra fields (reasoning config, cache_control on
	// system parts) that converters emit only for that dialect. The host sets
	// it from the channel type.
	OpenRouterDialect bool

	// WebSearch encodes hosted web search for the upstream dialect. Nil, or a
	// nil result, keeps the default: web_search_options on Chat and the native
	// hosted tool on Responses, Claude, and Gemini. The host sets it from the
	// channel adaptor's Init.
	WebSearch WebSearchEncoder

	// PreserveThinkingSuffix reports models whose -thinking/-nothinking/effort
	// suffix must be kept on the outgoing model name (host blacklist lookup).
	// Nil means "never preserve".
	PreserveThinkingSuffix func(modelName string) bool

	// PreserveEffortTail reports real model IDs whose names already end in an
	// effort-like token (for example qwen-max). Nil means "never preserve".
	PreserveEffortTail func(modelName string) bool

	// ReasoningHistory seals the provider-native reasoning a client has to
	// carry back when Claude Messages and OpenAI Responses are converted into
	// each other: Claude thinking blocks with their signatures, and Responses
	// reasoning items with their encrypted content. Nil disables continuation,
	// and such reasoning is dropped from the history as before.
	ReasoningHistory ReasoningHistorySealer
	// ResponsesIncludesEncryptedReasoning reports that a Responses client
	// asked for reasoning.encrypted_content. Reasoning sealed for a Responses
	// client travels in that field, so it is sealed only when asked for; a
	// Messages client always gets it in the thinking signature it replays.
	ResponsesIncludesEncryptedReasoning bool
}

// ReasoningHistorySealer protects reasoning that relaykit hands to a client in
// the other protocol's opaque field: a Claude thinking signature or redacted
// thinking data, or a Responses encrypted_content. The host owns the key and
// decides what a token is bound to (user, channel, upstream account, model);
// relaykit decides what is sealed and where it travels.
type ReasoningHistorySealer interface {
	// Seal encrypts and authenticates plaintext for the current route.
	Seal(plaintext []byte) (string, error)
	// Open returns the plaintext of a token this sealer issued for the
	// current route. It reports false for a token that is foreign, altered,
	// or bound to another route; converters then drop what it carried.
	Open(token string) ([]byte, bool)
	// Owns reports whether value has the form of a token this sealer issues,
	// without opening it, so a native path can remove tokens its upstream
	// cannot read.
	Owns(value string) bool
}

type ClaudeOptions struct {
	// ThinkingAdapterEnabled is the host thinking-suffix adapter toggle. The
	// host passes it to reasoning.ParseClaudeModelSuffix, so -thinking,
	// -nothinking, -thinking-<budget>, and effort tails such as -high on
	// claude-* names become reasoning intent only while it is on; when off the
	// model name is sent verbatim. Explicit @ modifiers are unaffected. Suffix
	// parsing itself is the host entry layer's job (standalone users call
	// Parse* themselves).
	ThinkingAdapterEnabled bool
	// ThinkingAdapterBudgetTokensPercentage sizes thinking budget_tokens as a
	// fraction of max_tokens when the adapter fires.
	ThinkingAdapterBudgetTokensPercentage float64
	// DefaultMaxTokens returns the max_tokens to inject when the source
	// request carries none. The Claude Messages API requires max_tokens
	// (omitting it is a 400), so when this hook is nil and no other path
	// supplies a value, OpenAI→Claude request conversion fails with an
	// explicit error instead of emitting a request the upstream is
	// guaranteed to reject. The new-api host always provides this hook;
	// standalone relaykit users must supply one or guarantee max_tokens on
	// every request.
	DefaultMaxTokens func(modelName string) int
	// WebSearchToolVersion selects the Claude hosted web-search tool version
	// emitted by cross-protocol conversion. Empty keeps the compatibility
	// baseline web_search_20250305.
	WebSearchToolVersion string
}

type GeminiOptions struct {
	// ThinkingAdapterEnabled is the host thinking-suffix adapter toggle. The
	// host and gemini_chat request conversion pass it to
	// reasoning.ParseGeminiModelSuffix, so -thinking, -nothinking,
	// -thinking-<budget>, and effort tails such as -high on gemini-* names
	// become reasoning intent only while it is on; when off the model name is
	// sent verbatim. Explicit @ modifiers are unaffected.
	ThinkingAdapterEnabled bool
	// ThinkingAdapterBudgetTokensPercentage sizes thinkingBudget as a fraction
	// of maxOutputTokens when the adapter fires.
	ThinkingAdapterBudgetTokensPercentage float64
	// FunctionCallThoughtSignatureEnabled attaches thoughtSignature bypass
	// values to function-call parts.
	FunctionCallThoughtSignatureEnabled bool
	// SupportsImagine reports whether the model supports image generation
	// (switches response modalities). Nil means "never".
	SupportsImagine func(modelName string) bool
	// SupportsJSONSchema reports whether the upstream accepts JSON Schema in
	// parametersJsonSchema and responseJsonSchema for the model. A schema that
	// Gemini's typed Schema cannot hold without loss is then sent there
	// unchanged; otherwise it is cleaned to the typed Schema and the loss is
	// reported as a diagnostic. Nil means "never".
	SupportsJSONSchema func(modelName string) bool
	// SafetySetting returns the harm threshold for a category. Nil or empty
	// return means no safetySettings are attached.
	SafetySetting func(category string) string
}

func (o *ClaudeOptions) DefaultMaxTokensFor(modelName string) (int, bool) {
	if o == nil || o.DefaultMaxTokens == nil {
		return 0, false
	}
	return o.DefaultMaxTokens(modelName), true
}

func (o *GeminiOptions) SupportsImagineModel(modelName string) bool {
	return o != nil && o.SupportsImagine != nil && o.SupportsImagine(modelName)
}

func (o *GeminiOptions) SupportsJSONSchemaModel(modelName string) bool {
	return o != nil && o.SupportsJSONSchema != nil && o.SupportsJSONSchema(modelName)
}

func (o *GeminiOptions) SafetySettingFor(category string) string {
	if o == nil || o.SafetySetting == nil {
		return ""
	}
	return o.SafetySetting(category)
}

func (o *Options) ShouldPreserveThinkingSuffix(modelName string) bool {
	return o != nil && o.PreserveThinkingSuffix != nil && o.PreserveThinkingSuffix(modelName)
}

func (o *Options) ShouldPreserveEffortTail(modelName string) bool {
	return o != nil && o.PreserveEffortTail != nil && o.PreserveEffortTail(modelName)
}

func (o *Options) EffectiveToolLossPolicy() types.ConversionLossPolicy {
	if o == nil || o.ToolLossPolicy == "" {
		return types.ConversionLossPolicyAllow
	}
	return o.ToolLossPolicy
}
