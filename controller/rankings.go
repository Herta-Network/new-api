package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type rankingsResponse struct {
	Success bool                      `json:"success"`
	Data    *service.RankingsResponse `json:"data"`
}

// @Summary		获取模型排行
// @Description	访问取决于顶部导航的排行模块设置：公开（登录可选）、需要登录，或关闭（返回 403）。
// @Tags			系统
// @Param			period	query		string	false	"统计周期：today、week、month、year，默认 week"
// @Success		200		{object}	rankingsResponse
// @Failure		400		{object}	common.ApiResponse
// @Router			/api/rankings [get]
func GetRankings(c *gin.Context) {
	result, err := service.GetRankingsSnapshot(c.DefaultQuery("period", "week"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, rankingsResponse{
		Success: true,
		Data:    result,
	})
}
