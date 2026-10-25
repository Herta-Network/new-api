// Package handler serves the admin operations overview under
// /api/admin/console.
package handler

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/console"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// consoleNow is the clock of the admin console statistics; its location is
// the zone of their day, month and hour buckets.
var consoleNow = time.Now

// GetConsoleOverview reports today's usage, registrations, revenue, channel
// counts and the payments that need an operator.
//
//	@Summary		获取今日运营概览
//	@Description	金额以对应货币的 1/100 为单位；revenue 按当前汇率折算为站点货币，无汇率的币种单列在 other_revenue。
//	@Tags			运营概览
//	@Success		200	{object}	common.ApiResponse{data=console.Overview}
//	@Security		AdminAuth
//	@Router			/api/admin/console/overview [get]
func GetConsoleOverview(c *gin.Context) {
	overview, err := console.GetOverview(c.Request.Context(), consoleNow(), operation_setting.PaymentCurrency())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

// GetConsoleRevenue reports the revenue of today, this month and all time.
//
//	@Summary		获取收入统计
//	@Description	今日按小时、本月按天、全部按月统计；金额以站点货币的 1/100 为单位，其他币种的订单单列在 other_currencies。
//	@Tags			运营概览
//	@Success		200	{object}	common.ApiResponse{data=console.Revenue}
//	@Security		AdminAuth
//	@Router			/api/admin/console/revenue [get]
func GetConsoleRevenue(c *gin.Context) {
	revenue, err := console.GetRevenue(consoleNow(), operation_setting.PaymentCurrency())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, revenue)
}

// GetConsoleUsers reports registrations and paying users of today, this
// month and all time.
//
//	@Summary	获取用户统计
//	@Tags		运营概览
//	@Success	200	{object}	common.ApiResponse{data=console.Users}
//	@Security	AdminAuth
//	@Router		/api/admin/console/users [get]
func GetConsoleUsers(c *gin.Context) {
	users, err := console.GetUsers(consoleNow())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, users)
}

// GetConsoleRankings ranks the models and users of today, or of yesterday
// with period=yesterday, by consumed quota.
//
//	@Summary	获取模型和用户排行
//	@Tags		运营概览
//	@Param		period	query		string	false	"today（默认）或 yesterday"
//	@Success	200		{object}	common.ApiResponse{data=console.Rankings}
//	@Security	AdminAuth
//	@Router		/api/admin/console/rankings [get]
func GetConsoleRankings(c *gin.Context) {
	period := c.DefaultQuery("period", console.RankingToday)
	if period != console.RankingToday && period != console.RankingYesterday {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rankings, err := console.GetRankings(consoleNow(), period)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rankings)
}
