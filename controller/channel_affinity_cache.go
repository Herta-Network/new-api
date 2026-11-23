package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// @Summary		获取路由种子缓存命中统计
// @Description	返回一个路由种子在最近一小时内的上游缓存命中和令牌用量；参数取自日志 admin_info.routing。
// @Tags			日志
// @Param			rule_name	query		string	true	"种子来源（routing.seed.source）"
// @Param			using_group	query		string	false	"请求实际使用的分组"
// @Param			key_fp		query		string	true	"种子指纹（routing.seed.fp）"
// @Success		200			{object}	common.ApiResponse{data=service.ChannelAffinityUsageCacheStats}
// @Failure		400			{object}	common.ApiResponse	"缺少 rule_name 或 key_fp"
// @Security		AdminAuth
// @Router			/api/log/channel_affinity_usage_cache [get]
func GetChannelAffinityUsageCacheStats(c *gin.Context) {
	ruleName := strings.TrimSpace(c.Query("rule_name"))
	usingGroup := strings.TrimSpace(c.Query("using_group"))
	keyFp := strings.TrimSpace(c.Query("key_fp"))

	if ruleName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "missing param: rule_name",
		})
		return
	}
	if keyFp == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "missing param: key_fp",
		})
		return
	}

	stats := service.GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFp)
	common.ApiSuccess(c, stats)
}
