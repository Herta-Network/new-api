package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type requestPolicy struct {
	Options map[string]string `json:"options"`
}

type requestPolicyResponse struct {
	Success bool          `json:"success"`
	Data    requestPolicy `json:"data"`
}

// @Summary		获取请求策略
// @Description	options 以设置项名为键，含敏感词、渠道自动禁用与重试、渠道测试和渠道亲和的设置项。
// @Tags			系统设置
// @Success		200	{object}	requestPolicyResponse
// @Security		RootAuth
// @Router			/api/option/request_policy [get]
func GetRequestPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, requestPolicyResponse{Success: true, Data: requestPolicy{Options: model.CurrentRequestPolicy().Options}})
}

type updateRequestPolicyRequest struct {
	Options map[string]string `json:"options"`
}

// @Summary		更新请求策略
// @Description	只更新 options 中给出的设置项；含非请求策略设置项、取值无效或保存失败时返回 400。成功时返回更新后的全部设置项。
// @Tags			系统设置
// @Param			body	body		updateRequestPolicyRequest	true	"要更新的设置项"
// @Success		200		{object}	requestPolicyResponse
// @Failure		400		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/option/request_policy [patch]
func UpdateRequestPolicy(c *gin.Context) {
	var request updateRequestPolicyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpdateRequestPolicyOptions(request.Options); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	GetRequestPolicy(c)
}
