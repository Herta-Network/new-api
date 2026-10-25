package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// @Summary		创建日志清理任务
// @Description	在后台删除 created_at 早于 target_timestamp 的使用日志；已有进行中的日志清理任务时返回该任务。
// @Tags			系统任务
// @Param			target_timestamp	query		int	true	"Unix 秒时间戳"
// @Success		200					{object}	common.ApiResponse{data=model.SystemTaskResponse}
// @Security		RootAuth
// @Router			/api/system-task/log-cleanup [post]
func CreateLogCleanupSystemTask(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}

	task, err := service.StartLogCleanupTask(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, task.ToResponse())
}

// @Summary		获取进行中的系统任务
// @Description	没有该类型进行中的任务时 data 为 null。
// @Tags			系统任务
// @Param			type	query		string	true	"任务类型：log_cleanup、channel_test、model_update、midjourney_poll、async_task_poll"
// @Success		200		{object}	common.ApiResponse{data=model.SystemTaskResponse}
// @Security		RootAuth
// @Router			/api/system-task/current [get]
func GetCurrentSystemTask(c *gin.Context) {
	taskType := c.Query("type")
	if taskType == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "type is required",
		})
		return
	}

	task, err := model.GetActiveSystemTask(taskType)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		common.ApiSuccess(c, nil)
		return
	}

	common.ApiSuccess(c, task.ToResponse())
}

type systemTaskListQuery struct {
	model.SystemTaskFilter
	Offset int `form:"offset" binding:"gte=0"`
	Limit  int `form:"limit"`
}

type systemTaskListResponse struct {
	Success bool                       `json:"success"`
	Message string                     `json:"message"`
	Data    []model.SystemTaskResponse `json:"data"`
	Total   int64                      `json:"total"`
}

// @Summary	列出系统任务
// @Tags		系统任务
// @Param		scope	query		string	false	"active（进行中）或 history（已结束）"
// @Param		type	query		string	false	"任务类型，如 log_cleanup"
// @Param		status	query		string	false	"pending、running、succeeded 或 failed"
// @Param		offset	query		int		false	"跳过的条数"
// @Param		limit	query		int		false	"条数，默认 20，最大 100"
// @Success	200		{object}	systemTaskListResponse
// @Failure	400		{object}	common.ApiResponse
// @Security	RootAuth
// @Router		/api/system-task/list [get]
func ListSystemTasks(c *gin.Context) {
	var query systemTaskListQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid system task filters"})
		return
	}

	tasks, total, err := model.ListSystemTasks(query.SystemTaskFilter, query.Offset, query.Limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	responses := make([]model.SystemTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToResponse())
	}

	c.JSON(http.StatusOK, systemTaskListResponse{
		Success: true,
		Message: "",
		Data:    responses,
		Total:   total,
	})
}

// @Summary		清理系统任务历史
// @Description	只删除已结束的任务，并保留每种类型最新的一条（调度器据此判断下次运行时间）。
// @Tags			系统任务
// @Param			type	query		string	false	"只删除此类型"
// @Param			status	query		string	false	"只删除此状态：succeeded 或 failed"
// @Success		200		{object}	common.ApiResponse{data=systemDeletedCount}
// @Failure		400		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/system-task/history [delete]
func DeleteSystemTaskHistory(c *gin.Context) {
	var filter model.SystemTaskFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid system task filters"})
		return
	}
	deleted, err := model.DeleteSystemTaskHistory(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, systemDeletedCount{DeletedCount: deleted})
}

// @Summary	获取系统任务
// @Tags		系统任务
// @Param		task_id	path		string	true	"任务 ID"
// @Success	200		{object}	common.ApiResponse{data=model.SystemTaskResponse}
// @Failure	404		{object}	common.ApiResponse
// @Security	RootAuth
// @Router		/api/system-task/{task_id} [get]
func GetSystemTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task id is required",
		})
		return
	}

	task, err := model.GetSystemTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "task not found",
		})
		return
	}

	common.ApiSuccess(c, task.ToResponse())
}
