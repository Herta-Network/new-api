package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetPrefillGroups 获取预填组列表，可通过 ?type=xxx 过滤
//
//	@Summary	列出预填组
//	@Tags		分组
//	@Param		type	query		string	false	"按类型过滤，如 model、tag、endpoint"
//	@Success	200		{object}	common.ApiResponse{data=[]model.PrefillGroup}
//	@Security	AdminAuth
//	@Router		/api/prefill_group/ [get]
func GetPrefillGroups(c *gin.Context) {
	groupType := c.Query("type")
	groups, err := model.GetAllPrefillGroups(groupType)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, groups)
}

// CreatePrefillGroup 创建新的预填组
//
//	@Summary	创建预填组
//	@Tags		分组
//	@Param		body	body		model.PrefillGroup	true	"预填组；name 和 type 必填，名称不能重复"
//	@Success	200		{object}	common.ApiResponse{data=model.PrefillGroup}
//	@Security	AdminAuth
//	@Router		/api/prefill_group/ [post]
func CreatePrefillGroup(c *gin.Context) {
	var g model.PrefillGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		common.ApiError(c, err)
		return
	}
	if g.Name == "" || g.Type == "" {
		common.ApiErrorT(c, "Group name and type cannot be empty")
		return
	}
	// 创建前检查名称
	if dup, err := model.IsPrefillGroupNameDuplicated(0, g.Name); err != nil {
		common.ApiError(c, err)
		return
	} else if dup {
		common.ApiErrorT(c, "Group name already exists")
		return
	}

	if err := g.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &g)
}

// UpdatePrefillGroup 更新预填组
//
//	@Summary	更新预填组
//	@Tags		分组
//	@Param		body	body		model.PrefillGroup	true	"预填组；按 id 更新"
//	@Success	200		{object}	common.ApiResponse{data=model.PrefillGroup}
//	@Security	AdminAuth
//	@Router		/api/prefill_group/ [put]
func UpdatePrefillGroup(c *gin.Context) {
	var g model.PrefillGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		common.ApiError(c, err)
		return
	}
	if g.Id == 0 {
		common.ApiErrorT(c, "Missing group ID")
		return
	}
	// 名称冲突检查
	if dup, err := model.IsPrefillGroupNameDuplicated(g.Id, g.Name); err != nil {
		common.ApiError(c, err)
		return
	} else if dup {
		common.ApiErrorT(c, "Group name already exists")
		return
	}

	if err := g.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &g)
}

// DeletePrefillGroup 删除预填组
//
//	@Summary	删除预填组
//	@Tags		分组
//	@Param		id	path		int	true	"预填组 ID"
//	@Success	200	{object}	common.ApiResponse
//	@Security	AdminAuth
//	@Router		/api/prefill_group/{id} [delete]
func DeletePrefillGroup(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeletePrefillGroupByID(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
