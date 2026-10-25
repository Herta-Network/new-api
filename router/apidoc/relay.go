package apidoc

//	@title			new-api AI 调用接口
//	@version		1.0
//	@description	兼容 OpenAI、Claude、Gemini、Midjourney 等协议的 AI 调用接口。请求体和响应体沿用各协议的格式，new-api 按模型选择渠道转发，并按用量计费。
//	@description
//	@description	鉴权：API 密钥（sk- 开头）放在 Authorization: Bearer 里；Claude 客户端也可以用 x-api-key，Gemini 客户端也可以用 x-goog-api-key 或查询参数 key。
//	@description
//	@description	错误：HTTP 状态码表示失败原因，响应体是 {"error": {...}}（Claude 协议是 {"type": "error", "error": {...}}）。流式响应已经开始后发生的错误写在事件流里。
//	@BasePath		/
//	@accept			json
//	@produce		json

//	@securityDefinitions.apikey	TokenAuth
//	@in							header
//	@name						Authorization
//	@description				API 密钥：Bearer sk-<密钥>。

//	@securityDefinitions.apikey	AnthropicApiKey
//	@in							header
//	@name						x-api-key
//	@description				Claude 客户端的 API 密钥写法，只用于 /v1/messages 和 /v1/models。

//	@securityDefinitions.apikey	GoogleApiKey
//	@in							header
//	@name						x-goog-api-key
//	@description				Gemini 客户端的 API 密钥写法，只用于 Gemini 路径和 /v1/models。

//	@securityDefinitions.apikey	GoogleApiKeyQuery
//	@in							query
//	@name						key
//	@description				Gemini 客户端的 API 密钥写法，只用于 Gemini 路径和 /v1/models。

//	@securityDefinitions.apikey	UserAuth
//	@in							header
//	@name						Authorization
//	@description				控制台登录用户：Bearer <访问令牌>，只用于操练场。

//	@tag.name			模型列表
//	@tag.name			OpenAI
//	@tag.description	对话、补全、Responses、实时语音和内容审核
//	@tag.name			Claude
//	@tag.name			Gemini
//	@tag.name			嵌入与重排
//	@tag.name			图像
//	@tag.name			音频
//	@tag.name			搜索
//	@tag.description	联网搜索和网页抓取工具
//	@tag.name			视频
//	@tag.name			异步任务
//	@tag.description	任务插件提交的异步任务及其产物
//	@tag.name			Midjourney
//	@tag.name			操练场
//	@tag.name			账单
//	@tag.description	兼容 OpenAI 旧版的额度和用量查询
//	@tag.name			未实现
//	@tag.description	保留的 OpenAI 路径，固定返回 501
