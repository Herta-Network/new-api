package common

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
)

// OpenCode Go serves each model on one endpoint. These families follow the
// protocol OpenCode's own client picks; every other model is served on Chat
// Completions.
const (
	openCodeGoResponsesModels = "re:(?i)^(gpt-|grok-|muse-spark-)"
	openCodeGoMessagesModels  = "re:(?i)^(claude-|qwen|minimax-|union-alpha$)"
	openCodeGoGeminiModels    = "re:(?i)^gemini-"
	// OpenCode serves Gemini at /v1/models, not the /v1beta/models clients call.
	openCodeGoGeminiPath = "/v1/models/{model}:generateContent"
)

// GetAdvancedCustomPreset returns fresh defaults.
func GetAdvancedCustomPreset(channelType int) *dto.AdvancedCustomConfig {
	if channelType == constant.ChannelTypeOpenCodeGo {
		return openCodeGoAdvancedCustomPreset()
	}
	if channelType != constant.ChannelTypeVLLM && channelType != constant.ChannelTypeSGLang {
		return nil
	}
	config := &dto.AdvancedCustomConfig{}
	for _, path := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings", "/v1/messages", dto.AdvancedCustomModelListPath} {
		config.Routes = append(config.Routes, dto.AdvancedCustomRoute{
			IncomingPath: path, UpstreamPath: path, Converter: "none",
			Auth: &dto.AdvancedCustomRouteAuth{Type: dto.AdvancedCustomAuthTypeHeader, Name: "Authorization", Value: "Bearer {api_key}"},
		})
	}
	if channelType == constant.ChannelTypeSGLang {
		for _, path := range []string{"/v1/rerank", "/rerank"} {
			config.Routes = append(config.Routes, dto.AdvancedCustomRoute{
				IncomingPath: path, UpstreamPath: "/v1/rerank", Converter: dto.AdvancedCustomConverterSGLangRerank,
			})
		}
	}
	return config
}

// openCodeGoAdvancedCustomPreset sends each model to the endpoint OpenCode Go
// serves it on and converts the client protocol where a converter exists.
// Responses and Gemini requests for Messages models, and Messages requests for
// Gemini models, have no converter and reach the Chat Completions catch-all.
func openCodeGoAdvancedCustomPreset() *dto.AdvancedCustomConfig {
	const geminiIncomingPath = "/v1beta/models/{model}:generateContent"
	routes := []dto.AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterOpenAIChatToOpenAIResponses, Models: []string{openCodeGoResponsesModels}},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/messages", Converter: relayconvert.ConverterOpenAIChatToClaudeMessages, Models: []string{openCodeGoMessagesModels}},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: openCodeGoGeminiPath, Converter: relayconvert.ConverterOpenAIChatToGeminiContent, Models: []string{openCodeGoGeminiModels}},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterNone},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterNone, Models: []string{openCodeGoResponsesModels}},
		{IncomingPath: "/v1/responses", UpstreamPath: openCodeGoGeminiPath, Converter: relayconvert.ConverterOpenAIResponsesToGemini, Models: []string{openCodeGoGeminiModels}},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat},
		{IncomingPath: "/v1/messages", UpstreamPath: "/v1/messages", Converter: relayconvert.ConverterNone, Models: []string{openCodeGoMessagesModels}},
		{IncomingPath: "/v1/messages", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterClaudeMessagesToOpenAIResponses, Models: []string{openCodeGoResponsesModels}},
		{IncomingPath: "/v1/messages", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterClaudeMessagesToOpenAIChat},
		{IncomingPath: geminiIncomingPath, UpstreamPath: openCodeGoGeminiPath, Converter: relayconvert.ConverterNone, Models: []string{openCodeGoGeminiModels}},
		{IncomingPath: geminiIncomingPath, UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterGeminiContentToOpenAIResponses, Models: []string{openCodeGoResponsesModels}},
		{IncomingPath: geminiIncomingPath, UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterGeminiContentToOpenAIChat},
		{IncomingPath: dto.AdvancedCustomModelListPath, UpstreamPath: dto.AdvancedCustomModelListPath, Converter: relayconvert.ConverterNone},
	}
	for i := range routes {
		routes[i].Auth = &dto.AdvancedCustomRouteAuth{Type: dto.AdvancedCustomAuthTypeHeader, Name: "Authorization", Value: "Bearer {api_key}"}
	}
	return &dto.AdvancedCustomConfig{Routes: routes}
}
