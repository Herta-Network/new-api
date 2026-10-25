package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	videoSharedRouter := router.Group("/v1")
	videoSharedRouter.Use(middleware.RouteTag("relay"))
	videoSharedRouter.Use(middleware.TokenAuth())
	videoSharedRouter.Use(middleware.SystemPerformanceCheck())

	//	@Summary		提交视频生成任务
	//	@Description	异步任务：返回任务 ID，之后用 GET /v1/video/generations/{task_id} 查询。各渠道读取的字段不同。
	//	@Tags			视频
	//	@Param			body	body		common.TaskSubmitReq	true	"视频生成请求，model 必填"
	//	@Success		200		{object}	controller.taskSubmissionResponse
	//	@Failure		default	{object}	dto.TaskError
	//	@Security		TokenAuth
	//	@Router			/v1/video/generations [post]
	videoSharedRouter.POST(
		"/video/generations",
		middleware.PinTaskPluginEndpoint(),
		middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),
		middleware.PrepareTaskPluginEndpoint(),
		middleware.Distribute(),
		func(c *gin.Context) {
			controller.RelayTaskPluginEndpoint(c, controller.RelayTask)
		},
	)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		//	@Summary		查询视频生成任务
		//	@Description	data 是任务记录；Gemini 和 Vertex 渠道的任务会先向上游查询最新状态，data 改为 task_id、status、url、format 等字段。
		//	@Tags			视频
		//	@Param			task_id	path		string	true	"任务 ID"
		//	@Success		200		{object}	object{code=string,message=string,data=dto.TaskDto}
		//	@Failure		default	{object}	dto.TaskError
		//	@Security		TokenAuth
		//	@Router			/v1/video/generations/{task_id} [get]
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)

		//	@Summary		混剪视频
		//	@Description	基于已完成的视频任务生成新视频，使用原任务的模型和渠道。
		//	@Tags			视频
		//	@Param			video_id	path		string	true	"原视频的任务 ID"
		//	@Param			body		body		object	true	"混剪参数，如 prompt，交给原任务的渠道"
		//	@Success		200			{object}	controller.taskSubmissionResponse
		//	@Failure		default		{object}	dto.TaskError
		//	@Security		TokenAuth
		//	@Router			/v1/videos/{video_id}/remix [post]
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
}
