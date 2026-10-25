package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// @Summary	获取渠道亲和缓存统计
// @Tags		系统设置
// @Success	200	{object}	common.ApiResponse{data=service.ChannelAffinityCacheStats}
// @Security	RootAuth
// @Router		/api/option/channel_affinity_cache [get]
func GetChannelAffinityCacheStats(c *gin.Context) {
	stats := service.GetChannelAffinityCacheStats()
	common.ApiSuccess(c, stats)
}

type channelAffinityCacheCleared struct {
	Deleted int `json:"deleted"`
}

// @Summary		清除渠道亲和缓存
// @Description	all=true 时清除全部，否则清除 rule_name 规则的缓存；data.deleted 是清除的条数。
// @Tags			系统设置
// @Param			all			query		string	false	"为 true 时清除全部"
// @Param			rule_name	query		string	false	"规则名，all 不为 true 时必填"
// @Success		200			{object}	common.ApiResponse{data=channelAffinityCacheCleared}
// @Failure		400			{object}	common.ApiResponse	"缺少 rule_name、规则不存在或规则未开启 include_rule_name"
// @Security		RootAuth
// @Router			/api/option/channel_affinity_cache [delete]
func ClearChannelAffinityCache(c *gin.Context) {
	all := strings.TrimSpace(c.Query("all"))
	ruleName := strings.TrimSpace(c.Query("rule_name"))

	if all == "true" {
		deleted := service.ClearChannelAffinityCacheAll()
		common.ApiSuccess(c, channelAffinityCacheCleared{
			Deleted: deleted,
		})
		return
	}

	if ruleName == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Missing parameter rule_name, or use all=true to clear everything"))
		return
	}

	deleted, err := service.ClearChannelAffinityCacheByRuleName(ruleName)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}

	common.ApiSuccess(c, channelAffinityCacheCleared{
		Deleted: deleted,
	})
}

// @Summary		获取渠道亲和用量统计
// @Description	返回一条渠道亲和规则下某个亲和键在统计窗口内的命中和令牌用量。
// @Tags			日志
// @Param			rule_name	query		string	true	"规则名"
// @Param			using_group	query		string	false	"使用的分组"
// @Param			key_fp		query		string	true	"亲和键指纹"
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
