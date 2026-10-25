package router

import (
	"fmt"

	"github.com/QuantumNous/new-api/controller"
	conversationhttp "github.com/QuantumNous/new-api/internal/module/conversation/transport/http"
	"github.com/QuantumNous/new-api/middleware"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func SetTaskPluginProtocolRouter(router *gin.Engine) {
	for _, protocol := range pluginruntime.HostProtocols() {
		for _, operation := range protocol.Operations {
			for _, method := range operation.Methods {
				handlers, err := taskPluginProtocolHandlers(protocol.Name, operation.Name)
				if err != nil {
					panic(err)
				}
				router.Handle(method, operation.Path, handlers...)
			}
		}
	}
}

func taskPluginProtocolHandlers(protocol, operation string) ([]gin.HandlerFunc, error) {
	switch protocol + "." + operation {
	//	@Summary		创建 Response
	//	@Description	stream 为 true 时以 SSE 流式返回。模型由任务插件处理时还支持 background，之后用 GET /v1/responses/{response_id} 查询结果。
	//	@Tags			OpenAI
	//	@Produce		json,event-stream
	//	@Param			body	body		dto.OpenAIResponsesRequest	true	"Responses 请求，model 必填"
	//	@Success		200		{object}	dto.OpenAIResponsesResponse
	//	@Failure		default	{object}	controller.OpenAIErrorResponse
	//	@Security		TokenAuth
	//	@Router			/v1/responses [post]
	case "openai_responses.create":
		return []gin.HandlerFunc{
			middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(),
			middleware.ModelRequestRateLimit(), middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(),
			conversationhttp.ConversationSession(), middleware.Distribute(),
			func(c *gin.Context) {
				controller.RelayTaskPluginEndpoint(c, func(c *gin.Context) { controller.Relay(c, types.RelayFormatOpenAIResponses) })
			},
		}, nil
	//	@Summary	生成图像
	//	@Tags		图像
	//	@Param		body	body		dto.ImageRequest	true	"图像生成请求，model 和 prompt 必填"
	//	@Success	200		{object}	dto.ImageResponse
	//	@Failure	default	{object}	controller.OpenAIErrorResponse
	//	@Security	TokenAuth
	//	@Router		/v1/images/generations [post]

	//	@Summary		编辑图像
	//	@Description	也接受 JSON 请求体，字段同 ImageRequest，image 写图片 URL 或 base64。表单中其余字段由渠道读取。
	//	@Tags			图像
	//	@Accept			mpfd
	//	@Param			image		formData	file	true	"要编辑的图像，可传多张"
	//	@Param			mask		formData	file	false	"蒙版"
	//	@Param			prompt		formData	string	true	"编辑说明"
	//	@Param			model		formData	string	true	"模型名"
	//	@Param			n			formData	int		false	"生成张数，1 到 128，默认 1"
	//	@Param			size		formData	string	false	"尺寸"
	//	@Param			quality		formData	string	false	"质量"
	//	@Param			stream		formData	bool	false	"是否流式返回"
	//	@Param			watermark	formData	bool	false	"是否加水印，部分渠道支持"
	//	@Param			parameters	formData	string	false	"JSON 文本，作为 parameters 转发，部分渠道支持"
	//	@Success		200			{object}	dto.ImageResponse
	//	@Failure		default		{object}	controller.OpenAIErrorResponse
	//	@Security		TokenAuth
	//	@Router			/v1/images/edits [post]
	case "openai_image.generate", "openai_image.edit":
		return []gin.HandlerFunc{
			middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(),
			middleware.ModelRequestRateLimit(), middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(),
			func(c *gin.Context) {
				controller.RelayTaskPluginEndpoint(c, func(c *gin.Context) { controller.Relay(c, types.RelayFormatOpenAIImage) })
			},
		}, nil
	//	@Summary		创建视频
	//	@Description	异步任务。模型由任务插件处理时返回 OpenAI 视频对象；其他渠道返回任务提交结果（id、task_id、status 为 queued、model、created_at）。也接受 multipart 表单，input_reference 可传图片文件。
	//	@Tags			视频
	//	@Accept			json,mpfd
	//	@Param			body	body		common.TaskSubmitReq	true	"视频生成请求，model 必填"
	//	@Success		200		{object}	dto.OpenAIVideo
	//	@Failure		default	{object}	dto.TaskError
	//	@Security		TokenAuth
	//	@Router			/v1/videos [post]
	case "openai_video.create":
		return []gin.HandlerFunc{
			middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.SystemPerformanceCheck(),
			middleware.RouteTaskPluginEndpoint(), middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(),
			func(c *gin.Context) { controller.RelayTaskPluginEndpoint(c, controller.RelayTask) },
		}, nil
	case "openai_responses.retrieve":
		return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), controller.RetrieveTaskPluginResponse}, nil
	//	@Summary		查询视频
	//	@Description	以 OpenAI 视频对象返回任务状态；Gemini 和 Vertex 渠道的任务会先向上游查询最新状态。
	//	@Tags			视频
	//	@Param			task_id	path		string	true	"任务 ID"
	//	@Success		200		{object}	dto.OpenAIVideo
	//	@Failure		default	{object}	dto.TaskError
	//	@Security		TokenAuth
	//	@Router			/v1/videos/{task_id} [get]
	case "openai_video.retrieve":
		return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.Distribute(), controller.RelayTaskFetch}, nil
	case "openai_video.content":
		return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), controller.VideoProxy}, nil
	default:
		return nil, fmt.Errorf("host protocol registry operation %s.%s has no handler", protocol, operation)
	}
}
