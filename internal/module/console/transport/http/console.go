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
func GetConsoleOverview(c *gin.Context) {
	overview, err := console.GetOverview(c.Request.Context(), consoleNow(), operation_setting.PaymentCurrency())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

// GetConsoleRevenue reports the revenue of today, this month and all time.
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
