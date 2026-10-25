package router

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	conversationhttp "github.com/QuantumNous/new-api/internal/module/conversation/transport/http"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	router.Use(middleware.StatsMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.RouteTag("relay"))
	modelsRouter.Use(middleware.TokenAuth())
	{
		//	@Summary		列出模型
		//	@Description	列出调用者的令牌可用的模型。按请求头选择格式：带 x-api-key 和 anthropic-version 时返回 Anthropic 格式（anthropicModelListResponse：data、first_id、has_more、last_id）；带 x-goog-api-key 或查询参数 key 时返回 Gemini 格式（geminiModelListResponse，同 GET /v1beta/models）；否则返回 OpenAI 格式。
		//	@Tags			模型列表
		//	@Success		200	{object}	controller.openAIModelListResponse
		//	@Security		TokenAuth
		//	@Security		AnthropicApiKey
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1/models [get]
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.ListModels(c, constant.ChannelTypeAnthropic)
			case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "": // 单独的适配
				controller.ListModels(c, constant.ChannelTypeGemini)
			default:
				controller.ListModels(c, constant.ChannelTypeOpenAI)
			}
		})

		//	@Summary		查询模型
		//	@Description	带 x-api-key 和 anthropic-version 时返回 Anthropic 格式（AnthropicModel），否则返回 OpenAI 格式。只查各渠道适配器内置的模型清单；不在清单里的模型仍返回 HTTP 200，响应体是 OpenAIErrorResponse，code 为 model_not_found。
		//	@Tags			模型列表
		//	@Param			model	path		string	true	"模型名"
		//	@Success		200		{object}	dto.OpenAIModels
		//	@Security		TokenAuth
		//	@Security		AnthropicApiKey
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1/models/{model} [get]
		modelsRouter.GET("/:model", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.RetrieveModel(c, constant.ChannelTypeAnthropic)
			default:
				controller.RetrieveModel(c, constant.ChannelTypeOpenAI)
			}
		})
	}

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.RouteTag("relay"))
	geminiRouter.Use(middleware.TokenAuth())
	{
		//	@Summary		列出 Gemini 模型
		//	@Description	以 Gemini 格式列出调用者的令牌可用的模型，不分页。
		//	@Tags			模型列表
		//	@Success		200	{object}	controller.geminiModelListResponse
		//	@Security		TokenAuth
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1beta/models [get]
		geminiRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.RouteTag("relay"))
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	{
		//	@Summary		列出模型（OpenAI 格式）
		//	@Description	Gemini 的 OpenAI 兼容路径，以 OpenAI 格式列出调用者的令牌可用的模型。
		//	@Tags			模型列表
		//	@Success		200	{object}	controller.openAIModelListResponse
		//	@Security		TokenAuth
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1beta/openai/models [get]
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.RouteTag("relay"))
	playgroundRouter.Use(middleware.SystemPerformanceCheck())
	playgroundRouter.Use(middleware.UserAuth(), middleware.Distribute())
	{
		playgroundRouter.POST("/chat/completions", controller.Playground)
	}
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.RouteTag("relay"))
	relayV1Router.Use(middleware.SystemPerformanceCheck())
	relayV1Router.Use(middleware.TokenAuth())
	{
		// Responses WebSocket route. Channel selection happens after the first
		// response.create event; each event runs the ordinary request limiter.
		relayV1Router.GET("/responses", controller.ResponsesWebSocket)
	}
	relayV1Router.Use(middleware.ModelRequestRateLimit())
	{
		// WebSocket 路由（统一到 Relay）
		wsRouter := relayV1Router.Group("")
		wsRouter.Use(middleware.Distribute())

		//	@Summary		实时语音会话
		//	@Description	升级为 WebSocket 连接，按 OpenAI Realtime 协议双向转发事件并按用量计费。浏览器无法设置请求头时，可在 Sec-WebSocket-Protocol 里写 openai-insecure-api-key.<API 密钥> 代替 Authorization。
		//	@Tags			OpenAI
		//	@Param			model	query	string	true	"模型名"
		//	@Success		101		"切换到 WebSocket 协议"
		//	@Security		TokenAuth
		//	@Router			/v1/realtime [get]
		wsRouter.GET("/realtime", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		//http router
		httpRouter := relayV1Router.Group("")
		httpRouter.Use(conversationhttp.ConversationSession())
		httpRouter.Use(middleware.Distribute())

		// claude related routes
		// TODO: /messages/count_tokens is disabled. The current controller.CountClaudeTokens
		// httpRouter.POST("/messages/count_tokens", controller.CountClaudeTokens)

		//	@Summary		创建 Claude 消息
		//	@Description	stream 为 true 时以 SSE 流式返回 Claude 事件。
		//	@Tags			Claude
		//	@Produce		json,event-stream
		//	@Param			body	body		dto.ClaudeRequest	true	"Claude Messages 请求"
		//	@Success		200		{object}	dto.ClaudeResponse
		//	@Failure		default	{object}	controller.ClaudeErrorResponse
		//	@Security		TokenAuth
		//	@Security		AnthropicApiKey
		//	@Router			/v1/messages [post]
		httpRouter.POST("/messages", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatClaude)
		})

		// chat related routes

		//	@Summary		文本补全
		//	@Description	stream 为 true 时以 SSE 流式返回。
		//	@Tags			OpenAI
		//	@Produce		json,event-stream
		//	@Param			body	body		dto.GeneralOpenAIRequest	true	"补全请求，model 和 prompt 必填"
		//	@Success		200		{object}	object						"OpenAI completions 格式的补全结果"
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/completions [post]
		httpRouter.POST("/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		//	@Summary		对话补全
		//	@Description	stream 为 true 时以 SSE 流式返回 chat.completion.chunk。
		//	@Tags			OpenAI
		//	@Produce		json,event-stream
		//	@Param			body	body		dto.GeneralOpenAIRequest	true	"对话请求，model 必填"
		//	@Success		200		{object}	dto.OpenAITextResponse
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/chat/completions [post]
		httpRouter.POST("/chat/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// response related routes

		//	@Summary	压缩 Responses 上下文
		//	@Tags		OpenAI
		//	@Param		body	body		dto.OpenAIResponsesCompactionRequest	true	"压缩请求，model 必填"
		//	@Success	200		{object}	dto.OpenAIResponsesCompactionResponse
		//	@Failure	default	{object}	controller.OpenAIErrorResponse
		//	@Security	TokenAuth
		//	@Router		/v1/responses/compact [post]
		httpRouter.POST("/responses/compact", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponsesCompaction)
		})

		// alpha search related routes (Codex standalone web search)

		//	@Summary		联网搜索
		//	@Description	Codex 的独立联网搜索接口。请求体原样转发给上游（模型映射时只改写 model），响应原样返回；按一次 web_search_preview 工具调用计费。只有 Sub2API、New API、Codex 和高级自定义渠道支持。
		//	@Tags			搜索
		//	@Param			body	body		dto.AlphaSearchRequest	true	"搜索请求，model 必填，其余字段原样转发"
		//	@Success		200		{object}	object					"上游的搜索结果"
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/alpha/search [post]
		httpRouter.POST("/alpha/search", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAlphaSearch)
		})

		// DeepSeek Search and Fetch API routes

		//	@Summary		DeepSeek 搜索
		//	@Description	请求体原样转发给上游，响应原样返回；按模型 deepseek-search 的按次价格计费，请求体里的 model 不影响计费。只有 DeepSeek、New API、Sub2API 和高级自定义渠道支持。
		//	@Tags			搜索
		//	@Param			body	body		dto.DeepSeekToolsRequest	true	"DeepSeek Search API 请求体，原样转发"
		//	@Success		200		{object}	object						"上游的搜索结果"
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/tools/search [post]
		httpRouter.POST("/tools/search", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatDeepSeekTools)
		})

		//	@Summary		DeepSeek 网页抓取
		//	@Description	请求体原样转发给上游，响应原样返回；按模型 deepseek-fetch 的按次价格计费，请求体里的 model 不影响计费。只有 DeepSeek、New API、Sub2API 和高级自定义渠道支持。
		//	@Tags			搜索
		//	@Param			body	body		dto.DeepSeekToolsRequest	true	"DeepSeek Fetch API 请求体，原样转发"
		//	@Success		200		{object}	object						"上游的抓取结果"
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/tools/fetch [post]
		httpRouter.POST("/tools/fetch", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatDeepSeekTools)
		})

		// The legacy Edits API rewrites input by an instruction; it is a
		// text request.

		//	@Summary		文本编辑（旧接口）
		//	@Description	OpenAI 已停用的 Edits 接口：按 instruction 修改 input，按文本请求转发和计费。
		//	@Tags			OpenAI
		//	@Param			body	body		dto.GeneralOpenAIRequest	true	"model 和 instruction 必填，input 是要修改的文本"
		//	@Success		200		{object}	dto.OpenAITextResponse
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/edits [post]
		httpRouter.POST("/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// image related routes. /images/generations and /images/edits are
		// host protocol endpoints (openai_image) registered by
		// SetTaskPluginProtocolRouter; unclaimed models fall back to Relay.

		// embedding related routes

		//	@Summary	创建嵌入
		//	@Tags		嵌入与重排
		//	@Param		body	body		dto.EmbeddingRequest	true	"嵌入请求，model 必填"
		//	@Success	200		{object}	dto.OpenAIEmbeddingResponse
		//	@Failure	default	{object}	controller.OpenAIErrorResponse
		//	@Security	TokenAuth
		//	@Router		/v1/embeddings [post]
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatEmbedding)
		})

		// audio related routes

		//	@Summary		语音转文字
		//	@Description	multipart 表单，除下列字段外的 OpenAI 字段（如 language、prompt、temperature）原样转发。response_format 为 text、srt、vtt 时返回纯文本。
		//	@Tags			音频
		//	@Accept			mpfd
		//	@Produce		json,plain
		//	@Param			file			formData	file				true	"音频文件"
		//	@Param			model			formData	string				true	"模型名"
		//	@Param			response_format	formData	string				false	"json（默认）、text、srt、verbose_json、vtt"
		//	@Success		200				{object}	dto.AudioResponse	"转写结果"
		//	@Failure		default			{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/audio/transcriptions [post]
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		//	@Summary		语音翻译为英文
		//	@Description	multipart 表单，除下列字段外的 OpenAI 字段（如 language、prompt、temperature）原样转发。response_format 为 text、srt、vtt 时返回纯文本。
		//	@Tags			音频
		//	@Accept			mpfd
		//	@Produce		json,plain
		//	@Param			file			formData	file				true	"音频文件"
		//	@Param			model			formData	string				true	"模型名"
		//	@Param			response_format	formData	string				false	"json（默认）、text、srt、verbose_json、vtt"
		//	@Success		200				{object}	dto.AudioResponse	"翻译结果"
		//	@Failure		default			{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/audio/translations [post]
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		//	@Summary	文字转语音
		//	@Tags		音频
		//	@Produce	octet-stream
		//	@Param		body	body		dto.AudioRequest	true	"语音合成请求，model 必填"
		//	@Success	200		{file}		file				"音频内容，格式由 response_format 决定"
		//	@Failure	default	{object}	controller.OpenAIErrorResponse
		//	@Security	TokenAuth
		//	@Router		/v1/audio/speech [post]
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		// rerank related routes

		//	@Summary	重排文档
		//	@Tags		嵌入与重排
		//	@Param		body	body		dto.RerankRequest	true	"重排请求，query 和 documents 必填"
		//	@Success	200		{object}	dto.RerankResponse
		//	@Failure	default	{object}	controller.OpenAIErrorResponse
		//	@Security	TokenAuth
		//	@Router		/v1/rerank [post]
		httpRouter.POST("/rerank", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatRerank)
		})

		// gemini relay routes

		//	@Summary		Gemini 嵌入（旧路径）
		//	@Description	按 Gemini embedContent 格式处理，请求体没有 model 时用路径里的模型。注意：请求先按 generateContent 校验，请求体还须带非空的 contents。
		//	@Tags			Gemini
		//	@Param			model	path		string						true	"模型名"
		//	@Param			body	body		dto.GeminiEmbeddingRequest	true	"嵌入请求"
		//	@Success		200		{object}	dto.GeminiEmbeddingResponse
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/engines/{model}/embeddings [post]
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})

		//	@Summary		调用 Gemini 模型
		//	@Description	Gemini 原生接口的 v1 路径。streamGenerateContent 或 alt=sse 时以 SSE 流式返回。
		//	@Tags			Gemini
		//	@Produce		json,event-stream
		//	@Param			path	path		string					true	"模型和方法，如 gemini-2.5-flash:generateContent，方法还有 streamGenerateContent、embedContent、batchEmbedContents"
		//	@Param			alt		query		string					false	"sse 表示以 SSE 流式返回"
		//	@Param			body	body		dto.GeminiChatRequest	true	"generateContent 请求；embedContent 和 batchEmbedContents 的请求体分别是 GeminiEmbeddingRequest 和 GeminiBatchEmbeddingRequest"
		//	@Success		200		{object}	dto.GeminiChatResponse
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Security		AnthropicApiKey
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1/models/{path} [post]
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})

		// other relay routes

		//	@Summary	内容审核
		//	@Tags		OpenAI
		//	@Param		body	body		dto.GeneralOpenAIRequest	true	"审核请求，input 为待审核内容；model 可省略"
		//	@Success	200		{object}	object						"上游的审核结果"
		//	@Failure	default	{object}	controller.OpenAIErrorResponse
		//	@Security	TokenAuth
		//	@Router		/v1/moderations [post]
		httpRouter.POST("/moderations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// not implemented

		//	@Summary		未实现的 OpenAI 接口
		//	@Description	固定返回 501，code 为 api_not_implemented。请求仍先经过渠道分发，请求体没有 model 时先返回 400。
		//	@Tags			未实现
		//	@Failure		501	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/images/variations [post]
		//	@Router			/v1/files [get]
		//	@Router			/v1/files [post]
		//	@Router			/v1/fine-tunes [post]
		//	@Router			/v1/fine-tunes [get]
		httpRouter.POST("/images/variations", controller.RelayNotImplemented)
		httpRouter.GET("/files", controller.RelayNotImplemented)
		httpRouter.POST("/files", controller.RelayNotImplemented)

		//	@Summary		未实现的 OpenAI 接口
		//	@Description	固定返回 501，code 为 api_not_implemented。请求仍先经过渠道分发，请求体没有 model 时先返回 400。
		//	@Tags			未实现
		//	@Param			id	path		string	true	"文件或微调任务 ID"
		//	@Failure		501	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Router			/v1/files/{id} [delete]
		//	@Router			/v1/files/{id} [get]
		//	@Router			/v1/files/{id}/content [get]
		//	@Router			/v1/fine-tunes/{id} [get]
		//	@Router			/v1/fine-tunes/{id}/cancel [post]
		//	@Router			/v1/fine-tunes/{id}/events [get]
		httpRouter.DELETE("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id/content", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes/:id/cancel", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id/events", controller.RelayNotImplemented)

		//	@Summary		删除模型（未实现）
		//	@Description	固定返回 501，code 为 api_not_implemented。请求仍先按路径里的模型经过渠道分发，没有可用渠道时返回分发错误。
		//	@Tags			未实现
		//	@Param			model	path		string	true	"模型名"
		//	@Failure		501		{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Security		AnthropicApiKey
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1/models/{model} [delete]
		httpRouter.DELETE("/models/:model", controller.RelayNotImplemented)
	}

	relayMjRouter := router.Group("/mj")
	relayMjRouter.Use(middleware.RouteTag("relay"))
	relayMjRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjRouter)

	relayMjModeRouter := router.Group("/:mode/mj")
	relayMjModeRouter.Use(middleware.RouteTag("relay"))
	relayMjModeRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjModeRouter)
	//relayMjRouter.Use()

	relayGeminiRouter := router.Group("/v1beta")
	// :countTokens is not implemented. Answer it like an unregistered route
	// before auth/channel selection instead of silently relaying it as
	// generateContent (#7283).
	relayGeminiRouter.Use(func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, ":countTokens") {
			controller.RelayNotFound(c)
			c.Abort()
		}
	})
	relayGeminiRouter.Use(middleware.RouteTag("relay"))
	relayGeminiRouter.Use(middleware.SystemPerformanceCheck())
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(conversationhttp.ConversationSession())
	relayGeminiRouter.Use(middleware.Distribute())
	{
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		//	@Summary		调用 Gemini 模型
		//	@Description	Gemini 原生接口。streamGenerateContent 或 alt=sse 时以 SSE 流式返回；countTokens 未实现，返回 404。
		//	@Tags			Gemini
		//	@Produce		json,event-stream
		//	@Param			path	path		string					true	"模型和方法，如 gemini-2.5-flash:generateContent，方法还有 streamGenerateContent、embedContent、batchEmbedContents"
		//	@Param			alt		query		string					false	"sse 表示以 SSE 流式返回"
		//	@Param			body	body		dto.GeminiChatRequest	true	"generateContent 请求；embedContent 和 batchEmbedContents 的请求体分别是 GeminiEmbeddingRequest 和 GeminiBatchEmbeddingRequest"
		//	@Success		200		{object}	dto.GeminiChatResponse
		//	@Failure		default	{object}	controller.OpenAIErrorResponse
		//	@Security		TokenAuth
		//	@Security		GoogleApiKey
		//	@Security		GoogleApiKeyQuery
		//	@Router			/v1beta/models/{path} [post]
		relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
	}
}

// registerMjRouterGroup registers the Midjourney routes under /mj and under
// /:mode/mj, so each route has one annotation block per prefix.
func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	//	@Summary		获取任务图片
	//	@Description	不需要鉴权：网关按任务 ID 代为拉取任务图片，开启图片转发时任务的 imageUrl 指向这里。失败时返回 {"error": "原因"}。
	//	@Tags			Midjourney
	//	@Produce		octet-stream
	//	@Param			id	path	string	true	"Midjourney 任务 ID"
	//	@Success		200	{file}	file	"图片内容，Content-Type 取上游"
	//	@Router			/mj/image/{id} [get]

	//	@Summary		获取任务图片
	//	@Description	不需要鉴权：网关按任务 ID 代为拉取任务图片，开启图片转发时任务的 imageUrl 指向这里。失败时返回 {"error": "原因"}。
	//	@Tags			Midjourney
	//	@Produce		octet-stream
	//	@Param			mode	path	string	true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
	//	@Param			id		path	string	true	"Midjourney 任务 ID"
	//	@Success		200		{file}	file	"图片内容，Content-Type 取上游"
	//	@Router			/{mode}/mj/image/{id} [get]
	relayMjRouter.GET("/image/:id", relay.RelayMidjourneyImage)
	relayMjRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		//	@Summary		执行按钮动作
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"customId 和 taskId 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/action [post]

		//	@Summary		执行按钮动作
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"customId 和 taskId 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/action [post]
		relayMjRouter.POST("/submit/action", controller.RelayMidjourney)

		//	@Summary		提交 Shorten 任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/shorten [post]

		//	@Summary		提交 Shorten 任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/shorten [post]
		relayMjRouter.POST("/submit/shorten", controller.RelayMidjourney)

		//	@Summary		提交 Modal 任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId 必填，可带 prompt、maskBase64"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/modal [post]

		//	@Summary		提交 Modal 任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId 必填，可带 prompt、maskBase64"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/modal [post]
		relayMjRouter.POST("/submit/modal", controller.RelayMidjourney)

		//	@Summary		提交绘图任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt 必填，可带 base64Array 垫图"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/imagine [post]

		//	@Summary		提交绘图任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt 必填，可带 base64Array 垫图"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/imagine [post]
		relayMjRouter.POST("/submit/imagine", controller.RelayMidjourney)

		//	@Summary		提交变换任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId、action（如 UPSCALE、VARIATION、REROLL）和 index 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/change [post]

		//	@Summary		提交变换任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId、action（如 UPSCALE、VARIATION、REROLL）和 index 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/change [post]
		relayMjRouter.POST("/submit/change", controller.RelayMidjourney)

		//	@Summary		提交简易变换任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"content 必填，格式为“任务 ID 动作”，如 1320098173412546 U2"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/simple-change [post]

		//	@Summary		提交简易变换任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"content 必填，格式为“任务 ID 动作”，如 1320098173412546 U2"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/simple-change [post]
		relayMjRouter.POST("/submit/simple-change", controller.RelayMidjourney)

		//	@Summary		提交图生文任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"图片放在 base64 字段，原样转发"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/describe [post]

		//	@Summary		提交图生文任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"图片放在 base64 字段，原样转发"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/describe [post]
		relayMjRouter.POST("/submit/describe", controller.RelayMidjourney)

		//	@Summary		提交混图任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"base64Array 为要混合的图片"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/blend [post]

		//	@Summary		提交混图任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"base64Array 为要混合的图片"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/blend [post]
		relayMjRouter.POST("/submit/blend", controller.RelayMidjourney)

		//	@Summary		提交编辑任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt、maskBase64 等字段原样转发"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/edits [post]

		//	@Summary		提交编辑任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"prompt、maskBase64 等字段原样转发"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/edits [post]
		relayMjRouter.POST("/submit/edits", controller.RelayMidjourney)

		//	@Summary		提交视频任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/video [post]

		//	@Summary		提交视频任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest	true	"taskId 必填"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/video [post]
		relayMjRouter.POST("/submit/video", controller.RelayMidjourney)
		//relayMjRouter.POST("/notify", controller.RelayMidjourney)

		//	@Summary		查询任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只能查询自己的任务。
		//	@Tags			Midjourney
		//	@Param			id		path		string	true	"Midjourney 任务 ID"
		//	@Success		200		{object}	dto.MidjourneyDto
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/task/{id}/fetch [get]

		//	@Summary		查询任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只能查询自己的任务。
		//	@Tags			Midjourney
		//	@Param			mode	path		string	true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			id		path		string	true	"Midjourney 任务 ID"
		//	@Success		200		{object}	dto.MidjourneyDto
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/task/{id}/fetch [get]
		relayMjRouter.GET("/task/:id/fetch", controller.RelayMidjourney)

		//	@Summary		查询图片 seed
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只能查询自己的任务，由任务原来的渠道回答。
		//	@Tags			Midjourney
		//	@Param			id		path		string					true	"Midjourney 任务 ID"
		//	@Success		200		{object}	dto.MidjourneyResponse	"result 是 seed"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/task/{id}/image-seed [get]

		//	@Summary		查询图片 seed
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只能查询自己的任务，由任务原来的渠道回答。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			id		path		string					true	"Midjourney 任务 ID"
		//	@Success		200		{object}	dto.MidjourneyResponse	"result 是 seed"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/task/{id}/image-seed [get]
		relayMjRouter.GET("/task/:id/image-seed", controller.RelayMidjourney)

		//	@Summary		批量查询任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只返回自己的任务。
		//	@Tags			Midjourney
		//	@Param			body	body		object{ids=[]string}	true	"要查询的任务 ID"
		//	@Success		200		{array}		dto.MidjourneyDto
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/task/list-by-condition [post]

		//	@Summary		批量查询任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。只返回自己的任务。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		object{ids=[]string}	true	"要查询的任务 ID"
		//	@Success		200		{array}		dto.MidjourneyDto
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/task/list-by-condition [post]
		relayMjRouter.POST("/task/list-by-condition", controller.RelayMidjourney)

		//	@Summary		提交换脸任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.SwapFaceRequest		true	"源图和目标图"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/insight-face/swap [post]

		//	@Summary		提交换脸任务
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string					true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.SwapFaceRequest		true	"源图和目标图"
		//	@Success		200		{object}	dto.MidjourneyResponse	"midjourney-proxy 的提交结果：code 1 表示成功（21 任务已存在、22 排队中也改写为 1），result 是任务 ID"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/insight-face/swap [post]
		relayMjRouter.POST("/insight-face/swap", controller.RelayMidjourney)

		//	@Summary		上传图片到 Discord
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			body	body		dto.MidjourneyRequest			true	"base64Array 为要上传的图片"
		//	@Success		200		{object}	dto.MidjourneyUploadResponse	"result 是图片链接"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/mj/submit/upload-discord-images [post]

		//	@Summary		上传图片到 Discord
		//	@Description	兼容 midjourney-proxy 接口。API 密钥也可以放在 mj-api-secret 请求头（Authorization 为空或为 midjourney-proxy 时生效）。
		//	@Tags			Midjourney
		//	@Param			mode	path		string							true	"模式前缀，如 mj-fast、mj-relax、mj-turbo；以 mj- 开头的前缀在转发上游前去掉，其他前缀原样转发"
		//	@Param			body	body		dto.MidjourneyRequest			true	"base64Array 为要上传的图片"
		//	@Success		200		{object}	dto.MidjourneyUploadResponse	"result 是图片链接"
		//	@Failure		default	{object}	controller.MidjourneyErrorResponse
		//	@Security		TokenAuth
		//	@Router			/{mode}/mj/submit/upload-discord-images [post]
		relayMjRouter.POST("/submit/upload-discord-images", controller.RelayMidjourney)
	}
}
