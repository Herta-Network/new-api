package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// logFilterFromQuery reads the log search shared by the log list and the log
// export. Username and channel are read only for administrators.
func logFilterFromQuery(c *gin.Context, admin bool) model.LogFilter {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	filter := model.LogFilter{
		Type:              logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		TokenName:         c.Query("token_name"),
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
	}
	if admin {
		filter.Username = c.Query("username")
		filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	}
	return filter
}

// limitLogTimeRange returns the start of a log or dashboard search kept within
// the longest log time range. A missing start reaches back the whole range, and
// time after now does not count toward it.
func limitLogTimeRange(startTimestamp int64, endTimestamp int64) (int64, error) {
	maxRange := operation_setting.GetLogSetting().MaxRangeSeconds()
	end := common.GetTimestamp()
	if endTimestamp != 0 {
		end = min(end, endTimestamp)
	}
	if startTimestamp == 0 {
		return end - maxRange, nil
	}
	if end-startTimestamp > maxRange {
		return 0, common.NewMessage("The time range cannot exceed {{days}} days", map[string]any{"days": maxRange / (24 * 60 * 60)})
	}
	return startTimestamp, nil
}

// @Summary	列出全部日志
// @Tags		日志
// @Param		p					query		int		false	"页码，从 1 开始"
// @Param		page_size			query		int		false	"每页条数，最多 100"
// @Param		type				query		int		false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
// @Param		start_timestamp		query		int		false	"起始时间，Unix 秒"
// @Param		end_timestamp		query		int		false	"结束时间，Unix 秒"
// @Param		model_name			query		string	false	"模型名，可用 % 通配"
// @Param		token_name			query		string	false	"令牌名"
// @Param		group				query		string	false	"分组"
// @Param		request_id			query		string	false	"请求 ID"
// @Param		upstream_request_id	query		string	false	"上游请求 ID"
// @Param		username			query		string	false	"用户名，可用 % 通配"
// @Param		channel				query		int		false	"渠道 ID"
// @Success	200					{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Log}}
// @Security	AdminAuth
// @Router		/api/log/ [get]
func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logs, total, capped, err := model.GetAllLogs(logFilterFromQuery(c, true), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		model.FormatAdminLogs(logs)
	} else {
		model.FormatRootLogs(logs)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.TotalCapped = capped
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// @Summary		列出我的日志
// @Description	普通用户查询的时间范围不能超过日志最长查询范围，未传 start_timestamp 时从该范围的起点开始。
// @Tags			日志
// @Param			p					query		int		false	"页码，从 1 开始"
// @Param			page_size			query		int		false	"每页条数，最多 100"
// @Param			type				query		int		false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
// @Param			start_timestamp		query		int		false	"起始时间，Unix 秒"
// @Param			end_timestamp		query		int		false	"结束时间，Unix 秒"
// @Param			model_name			query		string	false	"模型名，可用 % 通配"
// @Param			token_name			query		string	false	"令牌名"
// @Param			group				query		string	false	"分组"
// @Param			request_id			query		string	false	"请求 ID"
// @Param			upstream_request_id	query		string	false	"上游请求 ID"
// @Success		200					{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Log}}
// @Security		UserAuth
// @Router			/api/log/self [get]
func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := logFilterFromQuery(c, false)
	if c.GetInt("role") < common.RoleAdminUser {
		startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		filter.StartTimestamp = startTimestamp
	}
	logs, total, capped, err := model.GetUserLogs(c.GetInt("id"), filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.TotalCapped = capped
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// ExportAllLogs streams every user's logs matching the search.
//
//	@Summary		导出全部日志
//	@Description	开始输出前的失败照常返回 JSON。每个用户同一时间只能进行一个导出，时间范围不能超过日志最长查询范围。
//	@Tags			日志
//	@Produce		json,application/x-ndjson
//	@Param			type				query		int			false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
//	@Param			start_timestamp		query		int			false	"起始时间，Unix 秒"
//	@Param			end_timestamp		query		int			false	"结束时间，Unix 秒"
//	@Param			model_name			query		string		false	"模型名，可用 % 通配"
//	@Param			token_name			query		string		false	"令牌名"
//	@Param			group				query		string		false	"分组"
//	@Param			request_id			query		string		false	"请求 ID"
//	@Param			upstream_request_id	query		string		false	"上游请求 ID"
//	@Param			username			query		string		false	"用户名，可用 % 通配"
//	@Param			channel				query		int			false	"渠道 ID"
//	@Success		200					{object}	model.Log	"NDJSON：每行一条日志，从新到旧；最后一行是结果 common.ApiResponse，成功时 data.count 为导出条数"
//	@Security		AdminAuth
//	@Router			/api/log/export [get]
func ExportAllLogs(c *gin.Context) {
	exportLogs(c, true)
}

// ExportUserLogs streams the caller's own logs matching the search.
//
//	@Summary		导出我的日志
//	@Description	普通用户需要管理员开启用户导出。开始输出前的失败照常返回 JSON。每个用户同一时间只能进行一个导出，时间范围不能超过日志最长查询范围。
//	@Tags			日志
//	@Produce		json,application/x-ndjson
//	@Param			type				query		int			false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
//	@Param			start_timestamp		query		int			false	"起始时间，Unix 秒"
//	@Param			end_timestamp		query		int			false	"结束时间，Unix 秒"
//	@Param			model_name			query		string		false	"模型名，可用 % 通配"
//	@Param			token_name			query		string		false	"令牌名"
//	@Param			group				query		string		false	"分组"
//	@Param			request_id			query		string		false	"请求 ID"
//	@Param			upstream_request_id	query		string		false	"上游请求 ID"
//	@Success		200					{object}	model.Log	"NDJSON：每行一条日志，从新到旧；最后一行是结果 common.ApiResponse，成功时 data.count 为导出条数"
//	@Security		UserAuth
//	@Router			/api/log/self/export [get]
func ExportUserLogs(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser && !operation_setting.GetLogSetting().UserExportEnabled {
		common.ApiErrorT(c, "Log export is disabled")
		return
	}
	exportLogs(c, false)
}

// logExportResult is the data of the last line of a log export.
type logExportResult struct {
	Count int `json:"count"`
}

// exportLogs writes one JSON log per line, newest first, and ends with an API
// response object: success with the log count, or the failure. Each user runs
// one export at a time, and every export stays within the longest log time
// range.
func exportLogs(c *gin.Context, all bool) {
	filter := logFilterFromQuery(c, all)
	startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter.StartTimestamp = startTimestamp

	userId := c.GetInt("id")
	release, ok, err := service.AcquireLogExportLock(userId)
	if err != nil {
		common.SysError(common.LogText("failed to lock log export: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	if !ok {
		common.ApiErrorT(c, "Another log export is running. Try again after it finishes.")
		return
	}
	defer release()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	count := 0
	writeLog := func(log *model.Log) error {
		line, err := common.Marshal(log)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(line, '\n')); err != nil {
			return err
		}
		count++
		return nil
	}
	ctx := c.Request.Context()
	if all {
		err = model.StreamAllLogs(ctx, filter, c.GetInt("role"), writeLog)
	} else {
		err = model.StreamUserLogs(ctx, userId, filter, writeLog)
	}
	if ctx.Err() != nil {
		return
	}
	// The status is already sent, so the result object becomes the last line.
	if err != nil {
		common.SysError(common.LogText("failed to export logs: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	common.ApiSuccess(c, logExportResult{Count: count})
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
//
//	@Summary		搜索全部日志
//	@Description	已废弃，总是返回失败。
//	@Tags			日志
//	@Deprecated
//	@Success	200	{object}	common.ApiResponse
//	@Security	AdminAuth
//	@Router		/api/log/search [get]
func SearchAllLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
//
//	@Summary		搜索我的日志
//	@Description	已废弃，总是返回失败。
//	@Tags			日志
//	@Deprecated
//	@Success	200	{object}	common.ApiResponse
//	@Security	UserAuth
//	@Router		/api/log/self/search [get]
func SearchUserLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

// @Summary		查询 API 密钥的日志
// @Description	用 API 密钥查询它最近的日志（最多 1000 条），允许跨域。
// @Tags			日志
// @Success		200	{object}	common.ApiResponse{data=[]model.Log}
// @Security		TokenAuth
// @Router			/api/log/token [get]
func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		common.ApiErrorI18n(c, i18n.MsgTokenInvalid)
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	common.ApiSuccess(c, logs)
}

// @Summary	统计全部日志用量
// @Tags		日志
// @Param		type			query		int		false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
// @Param		start_timestamp	query		int		false	"起始时间，Unix 秒"
// @Param		end_timestamp	query		int		false	"结束时间，Unix 秒"
// @Param		model_name		query		string	false	"模型名，可用 % 通配"
// @Param		token_name		query		string	false	"令牌名"
// @Param		channel			query		int		false	"渠道 ID"
// @Param		group			query		string	false	"分组"
// @Param		username		query		string	false	"用户名，可用 % 通配"
// @Success	200				{object}	common.ApiResponse{data=model.Stat}
// @Security	AdminAuth
// @Router		/api/log/stat [get]
func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	common.ApiSuccess(c, stat)
	return
}

// @Summary		统计我的日志用量
// @Description	普通用户查询的时间范围不能超过日志最长查询范围，未传 start_timestamp 时从该范围的起点开始。
// @Tags			日志
// @Param			type			query		int		false	"日志类型：0 全部，1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录"
// @Param			start_timestamp	query		int		false	"起始时间，Unix 秒"
// @Param			end_timestamp	query		int		false	"结束时间，Unix 秒"
// @Param			model_name		query		string	false	"模型名，可用 % 通配"
// @Param			token_name		query		string	false	"令牌名"
// @Param			channel			query		int		false	"渠道 ID"
// @Param			group			query		string	false	"分组"
// @Success		200				{object}	common.ApiResponse{data=model.Stat}
// @Security		UserAuth
// @Router			/api/log/self/stat [get]
func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if c.GetInt("role") < common.RoleAdminUser {
		var err error
		if startTimestamp, err = limitLogTimeRange(startTimestamp, endTimestamp); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	quotaNum, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	common.ApiSuccess(c, quotaNum)
	return
}
