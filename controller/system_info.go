package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// @Summary	列出节点实例
// @Tags		系统信息
// @Success	200	{object}	common.ApiResponse{data=[]model.SystemInstanceResponse}
// @Security	RootAuth
// @Router		/api/system-info/instances [get]
func ListSystemInstances(c *gin.Context) {
	instances, err := model.ListSystemInstances()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	now := common.GetTimestamp()
	responses := make([]model.SystemInstanceResponse, 0, len(instances))
	for _, instance := range instances {
		responses = append(responses, instance.ToResponse(now))
	}

	common.ApiSuccess(c, responses)
}

// GetNodeSync answers whether each node runs the latest revision of every
// sync topic.
//
// @Summary		查看节点同步状态
// @Description	每个同步主题（设置、渠道等）的最新版本，以及每个节点是否已加载这个版本、加载时间和失败原因。
// @Tags			系统信息
// @Success		200	{object}	common.ApiResponse{data=service.NodeSyncStatus}
// @Security		RootAuth
// @Router			/api/system-info/sync [get]
func GetNodeSync(c *gin.Context) {
	status, err := service.GetNodeSyncStatus()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

type systemDeletedCount struct {
	DeletedCount int64 `json:"deleted_count"`
}

// @Summary		删除全部失联节点实例
// @Description	失联指超过 90 秒没有上报。
// @Tags			系统信息
// @Success		200	{object}	common.ApiResponse{data=systemDeletedCount}
// @Security		RootAuth
// @Router			/api/system-info/stale-instances [delete]
func DeleteStaleSystemInstances(c *gin.Context) {
	deletedCount, err := model.DeleteStaleSystemInstances(common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, systemDeletedCount{DeletedCount: deletedCount})
}

// @Summary		删除失联节点实例
// @Description	只删除超过 90 秒没有上报的实例；实例仍在线或不存在时返回 success=false。
// @Tags			系统信息
// @Param			node_name	path		string	true	"节点名"
// @Success		200			{object}	common.ApiResponse{data=systemDeletedCount}
// @Security		RootAuth
// @Router			/api/system-info/instances/{node_name} [delete]
func DeleteStaleSystemInstance(c *gin.Context) {
	nodeName := c.Param("node_name")
	if strings.TrimSpace(nodeName) == "" {
		common.ApiErrorMsg(c, "node name is required")
		return
	}

	deleted, err := model.DeleteStaleSystemInstance(nodeName, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !deleted {
		common.ApiErrorMsg(c, "instance is not stale or no longer exists")
		return
	}

	common.ApiSuccess(c, systemDeletedCount{DeletedCount: 1})
}
