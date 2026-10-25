package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// SetTaskRouter registers the generic task-plugin API surface.
//
// Gin requires every route sharing a path position to use the same wildcard
// name, so the first segment is uniformly ":key"; it carries the plugin key
// on submit routes and the task id on read routes.
func SetTaskRouter(router *gin.Engine) {
	taskSubmitRouter := router.Group("/v1/tasks")
	taskSubmitRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	{
		//	@Summary	提交插件任务
		//	@Description	key 是任务插件的 key，请求体交给该插件处理，model 须是插件声明的模型或其别名。插件不存在或缺少 model 时返回 400，响应体是 {"error": {...}}。
		//	@Tags	异步任务
		//	@Param	key	path	string	true	"任务插件 key"
		//	@Param	body	body	object	true	"插件定义的请求体，须含 model"
		//	@Success	200	{object}	controller.taskSubmissionResponse
		//	@Failure	default	{object}	dto.TaskError
		//	@Security	TokenAuth
		//	@Router	/v1/tasks/{key} [post]
		taskSubmitRouter.POST("/:key", middleware.PrepareTaskPluginSubmit(), middleware.Distribute(), controller.RelayTask)
	}

	taskReadRouter := router.Group("/v1/tasks")
	taskReadRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	{
		taskReadRouter.GET("/:key", controller.GetTask)
		taskReadRouter.GET("/:key/artifacts", controller.GetTaskArtifacts)
	}

	taskContentRouter := router.Group("/v1/tasks")
	taskContentRouter.Use(
		middleware.RouteTag("relay"),
		middleware.TokenOrTaskArtifactAccessAuth("key", "artifact_key"),
	)
	{
		taskContentRouter.GET("/:key/artifacts/:artifact_key/content", controller.TaskArtifactContent)
		taskContentRouter.HEAD("/:key/artifacts/:artifact_key/content", controller.TaskArtifactContent)
	}
}
