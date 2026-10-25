package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type perfMetricsSummaryResponse struct {
	Success bool                         `json:"success"`
	Data    perfmetrics.SummaryAllResult `json:"data"`
}

// @Summary		获取模型性能概览
// @Description	定价模块公开时登录可选，要求登录或已关闭时需要登录。
// @Tags			系统
// @Param			hours			query		int			false	"统计最近多少小时，默认 24，最大 720"
// @Param			recent_model	query		[]string	false	"要返回近 7 天成功率序列的模型，可重复，最多 50 个"	collectionFormat(multi)
// @Success		200				{object}	perfMetricsSummaryResponse
// @Failure		400				{object}	common.ApiResponse
// @Failure		500				{object}	common.ApiResponse
// @Router			/api/perf-metrics/summary [get]
func GetPerfMetricsSummary(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	recentModels := c.QueryArray("recent_model")
	if len(recentModels) > perfmetrics.MaxRecentModels {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	activeGroups := append(lo.Keys(ratio_setting.GetGroupRatioCopy()), "auto")
	result, err := perfmetrics.QuerySummaryAll(hours, activeGroups, recentModels...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, perfMetricsSummaryResponse{
		Success: true,
		Data:    result,
	})
}

type perfMetricsResponse struct {
	Success bool                    `json:"success"`
	Data    perfmetrics.QueryResult `json:"data"`
}

// @Summary		获取模型性能指标
// @Description	定价模块公开时登录可选，要求登录或已关闭时需要登录。
// @Tags			系统
// @Param			model	query		string	true	"模型名"
// @Param			group	query		string	false	"只看此分组"
// @Param			hours	query		int		false	"统计最近多少小时，默认 24，最大 720"
// @Success		200		{object}	perfMetricsResponse
// @Failure		400		{object}	common.ApiResponse
// @Failure		500		{object}	common.ApiResponse
// @Router			/api/perf-metrics [get]
func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	result, err := perfmetrics.Query(perfmetrics.QueryParams{
		Model:         modelName,
		Group:         c.Query("group"),
		Hours:         hours,
		AllowedGroups: append(lo.Keys(ratio_setting.GetGroupRatioCopy()), "auto"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, perfMetricsResponse{
		Success: true,
		Data:    result,
	})
}
