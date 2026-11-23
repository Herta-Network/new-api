package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type ChannelAffinityKeySource struct {
	Type string `json:"type"` // context_int, context_string, request_header, gjson, conversation_session
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
}

// ChannelAffinityRule is a seed source for request routing: a request it
// matches is routed by the first non-empty value of its key sources, and gets
// its parameter override template. See service.ChannelRouter.
type ChannelAffinityRule struct {
	Name             string                     `json:"name"`
	ModelRegex       []string                   `json:"model_regex"`
	PathRegex        []string                   `json:"path_regex"`
	UserAgentInclude []string                   `json:"user_agent_include,omitempty"`
	KeySources       []ChannelAffinityKeySource `json:"key_sources"`

	ValueRegex string `json:"value_regex"`

	ParamOverrideTemplate map[string]any `json:"param_override_template,omitempty"`

	SkipRetryOnFailure bool `json:"skip_retry_on_failure"`
	// "inherit" uses the global default; off/prefer/strict override it.
	// Empty preserves the legacy SkipRetryOnFailure behavior. off supplies no
	// seed; prefer moves to the next candidate after a failure; strict, for a
	// request that carries earlier turns, tries the seed's first choice only.
	SessionMode string `json:"session_mode,omitempty"`
}

// ChannelAffinitySetting configures the routing seed: which value keeps the
// turns of one conversation on the same channel and key, so upstream prompt
// caches and reasoning stay usable. Routing keeps no state; the seed decides.
type ChannelAffinitySetting struct {
	Enabled bool `json:"enabled"`
	// Default for rules with SessionMode "inherit". Empty defaults to "prefer".
	SessionMode string `json:"session_mode"`
	// DeriveFromPrompt seeds requests without an explicit key from their
	// system prompt and tools, or from their first user message.
	DeriveFromPrompt bool `json:"derive_from_prompt"`
	// TemplateSpread spreads the conversations of one user that share a
	// system prompt and tools over up to this many buckets; 1 keeps them
	// together and 0 seeds by the first user message instead.
	TemplateSpread int                   `json:"template_spread"`
	Rules          []ChannelAffinityRule `json:"rules"`
}

// LegacyChannelAffinityOptions are option names of the stateful affinity
// cache that routing no longer reads. Stored values are accepted and ignored.
var LegacyChannelAffinityOptions = []string{"switch_on_success", "keep_on_channel_disabled", "max_entries", "default_ttl_seconds"}

// Keep Codex CLI passthrough aligned with upstream. Codex uses lower-case
// header names, while HTTP matching here is case-insensitive.
// Request session/thread headers:
// https://github.com/openai/codex/commit/7c7b4861d88960f7e3bd5b7f30f8351be666dd84
// Responses metadata headers/client_metadata:
// https://github.com/openai/codex/commit/14df0e8833aad0d6d78287954b61ffac67af936c
// x-codex-turn-state response/request round trip:
// https://github.com/openai/codex/commit/ebdd8795e924a8149b616e46ca2ed7848c207a4b
var codexCliPassThroughHeaders = []string{
	"Originator",
	"Session_id",
	"Thread_id",
	"Session-Id",
	"Thread-Id",
	"X-Client-Request-Id",
	"User-Agent",
	"X-Codex-Beta-Features",
	"X-Codex-Turn-State",
	"X-Codex-Turn-Metadata",
	"X-Codex-Window-Id",
	"X-Codex-Parent-Thread-Id",
	//"X-Codex-Installation-Id",
	"X-OpenAI-Subagent",
	"X-OpenAI-Memgen-Request",
	//"X-OAI-Attestation",
	"X-ResponsesAPI-Include-Timing-Metrics",
	"X-OpenAI-Internal-Codex-Responses-Lite",
}

var claudeCliPassThroughHeaders = []string{
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"User-Agent",
	"X-App",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Anthropic-Version",
}

func buildPassHeaderTemplate(headers []string) map[string]any {
	clonedHeaders := make([]string, 0, len(headers))
	clonedHeaders = append(clonedHeaders, headers...)
	return map[string]any{
		"operations": []map[string]any{
			{
				"mode":        "pass_headers",
				"value":       clonedHeaders,
				"keep_origin": true,
			},
		},
	}
}

func buildCodexPassHeaderTemplate() map[string]any {
	requestHeaders := make([]string, 0, len(codexCliPassThroughHeaders))
	requestHeaders = append(requestHeaders, codexCliPassThroughHeaders...)
	return map[string]any{
		"operations": []map[string]any{
			{
				"mode":        "pass_headers",
				"value":       requestHeaders,
				"keep_origin": true,
			},
		},
	}
}

var channelAffinitySetting = ChannelAffinitySetting{
	Enabled:          true,
	DeriveFromPrompt: true,
	TemplateSpread:   1,
	Rules: []ChannelAffinityRule{
		{
			Name:       "codex cli trace",
			ModelRegex: []string{"^gpt-.*$"},
			PathRegex:  []string{"/v1/responses"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
			},
			ValueRegex:            "",
			ParamOverrideTemplate: buildCodexPassHeaderTemplate(),
			SkipRetryOnFailure:    true,
			UserAgentInclude:      nil,
		},
		{
			Name:       "claude cli trace",
			ModelRegex: []string{"^claude-.*$"},
			PathRegex:  []string{"/v1/messages"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			ValueRegex:            "",
			ParamOverrideTemplate: buildPassHeaderTemplate(claudeCliPassThroughHeaders),
			SkipRetryOnFailure:    true,
			UserAgentInclude:      nil,
		},
	},
}

func init() {
	config.GlobalConfig.Register("channel_affinity_setting", &channelAffinitySetting)
}

func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return &channelAffinitySetting
}
