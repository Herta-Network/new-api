package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func parseFlowQuotaTimeRange(c *gin.Context) (int64, int64, bool) {
	startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "invalid time range")
		return 0, 0, false
	}
	return startTimestamp, endTimestamp, true
}

// @Summary	获取全站用量数据
// @Tags		数据统计
// @Param		start_timestamp	query		int		false	"起始时间，Unix 秒"
// @Param		end_timestamp	query		int		false	"结束时间，Unix 秒"
// @Param		username		query		string	false	"只看此用户"
// @Success	200				{object}	common.ApiResponse{data=[]model.QuotaData}
// @Security	AdminAuth
// @Router		/api/data/ [get]
func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
	return
}

// @Summary	按用户汇总用量数据
// @Tags		数据统计
// @Param		start_timestamp	query		int	false	"起始时间，Unix 秒"
// @Param		end_timestamp	query		int	false	"结束时间，Unix 秒"
// @Success	200				{object}	common.ApiResponse{data=[]model.QuotaData}
// @Security	AdminAuth
// @Router		/api/data/users [get]
func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	dates, err := model.GetQuotaDataGroupByUser(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
}

// @Summary		获取我的用量数据
// @Description	时间范围不能超过日志最长查询范围，未传 start_timestamp 时从该范围的起点开始。
// @Tags			数据统计
// @Param			start_timestamp	query		int	false	"起始时间，Unix 秒"
// @Param			end_timestamp	query		int	false	"结束时间，Unix 秒"
// @Success		200				{object}	common.ApiResponse{data=[]model.QuotaData}
// @Security		UserAuth
// @Router			/api/data/self [get]
func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	startTimestamp, err := limitLogTimeRange(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
	return
}

// @Summary		获取全站用量流向
// @Description	按分组和模型汇总；管理员还按用户和渠道拆分，超级管理员再按令牌和节点拆分。
// @Tags			数据统计
// @Param			start_timestamp	query		int		true	"起始时间，Unix 秒"
// @Param			end_timestamp	query		int		true	"结束时间，Unix 秒"
// @Param			username		query		string	false	"只看此用户"
// @Success		200				{object}	common.ApiResponse{data=[]model.FlowQuotaData}
// @Security		AdminAuth
// @Router			/api/data/flow [get]
func GetAllFlowQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, username, 0, c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
	return
}

// @Summary		获取我的用量流向
// @Description	按令牌、分组和模型汇总；时间范围不能超过日志最长查询范围。
// @Tags			数据统计
// @Param			start_timestamp	query		int	true	"起始时间，Unix 秒"
// @Param			end_timestamp	query		int	true	"结束时间，Unix 秒"
// @Success		200				{object}	common.ApiResponse{data=[]model.FlowQuotaData}
// @Security		UserAuth
// @Router			/api/data/flow/self [get]
func GetUserFlowQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	if _, err := limitLogTimeRange(startTimestamp, endTimestamp); err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, "", userId, common.RoleCommonUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
	return
}
