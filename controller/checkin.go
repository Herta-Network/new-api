package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// checkinStatusResponse is the body of GetCheckinStatus, which carries no
// message.
type checkinStatusResponse struct {
	Success bool          `json:"success"`
	Data    checkinStatus `json:"data"`
}

type checkinStatus struct {
	Enabled  bool `json:"enabled"`
	MinQuota int  `json:"min_quota"`
	MaxQuota int  `json:"max_quota"`
	// Stats are total_quota, total_checkins, checkin_count (this month),
	// checked_in_today and records (this month's check-ins).
	Stats map[string]any `json:"stats"`
}

// GetCheckinStatus 获取用户签到状态和历史记录
//
//	@Summary	获取签到状态
//	@Tags		签到
//	@Param		month	query		string	false	"月份，格式 2006-01，默认当月"
//	@Success	200		{object}	checkinStatusResponse
//	@Security	UserAuth
//	@Router		/api/user/checkin [get]
func GetCheckinStatus(c *gin.Context) {
	setting := operation_setting.GetCheckinSetting()
	if !setting.Enabled {
		common.ApiErrorT(c, "Check-in feature is not enabled")
		return
	}
	userId := c.GetInt("id")
	// 获取月份参数，默认为当前月份
	month := c.DefaultQuery("month", time.Now().Format("2006-01"))

	stats, err := model.GetUserCheckinStats(userId, month)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, checkinStatusResponse{
		Success: true,
		Data: checkinStatus{
			Enabled:  setting.Enabled,
			MinQuota: setting.MinQuota,
			MaxQuota: setting.MaxQuota,
			Stats:    stats,
		},
	})
}

type checkinResult struct {
	QuotaAwarded int    `json:"quota_awarded"`
	CheckinDate  string `json:"checkin_date"`
}

// DoCheckin 执行用户签到
//
//	@Summary	签到
//	@Tags		签到
//	@Param		turnstile	query		string	false	"Turnstile 令牌，开启 Turnstile 时必填"
//	@Success	200			{object}	common.ApiResponse{data=checkinResult}
//	@Security	UserAuth
//	@Router		/api/user/checkin [post]
func DoCheckin(c *gin.Context) {
	setting := operation_setting.GetCheckinSetting()
	if !setting.Enabled {
		common.ApiErrorT(c, "Check-in feature is not enabled")
		return
	}

	userId := c.GetInt("id")

	checkin, err := model.UserCheckin(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLog(userId, model.LogTypeSystem, common.NewMessage("Daily check-in, received {{quota}}", map[string]any{"quota": logger.FormatQuota(checkin.QuotaAwarded)}))
	common.ApiSuccessT(c, "Check-in successful", checkinResult{
		QuotaAwarded: checkin.QuotaAwarded,
		CheckinDate:  checkin.CheckinDate,
	})
}
