package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetSelfStatement returns the current user's statement of a month.
//
//	@Summary		获取我的对账单
//	@Description	按北京时间（Asia/Shanghai）分天，列出所选月份 1 日到今天（过去的月份到月末）的每一天：调用的请求数、tokens 和消耗额度，当天的余额流水，当天净变动，以及日终余额。
//	@Description
//	@Description	调用消耗来自小时用量汇总（开启数据看板和消费日志时才有，最多晚一个汇总间隔），异步任务的退款计为负的消耗；流水是调用以外的每一次余额变动，额度带符号。日终余额来自每天零点后的快照，服务没有记录到的日子为 null；今天的余额是当前余额。额度单位与 quota 相同。
//	@Tags			个人
//	@Param			month	query		string	false	"月份，格式 2006-01，默认北京时间的当月"
//	@Success		200		{object}	common.ApiResponse{data=model.WalletStatement}
//	@Security		UserAuth
//	@Router			/api/user/statement [get]
func GetSelfStatement(c *gin.Context) {
	writeWalletStatement(c, c.GetInt("id"))
}

// GetUserStatement returns a user's statement of a month, as the user reads
// it, for support to reconcile it.
//
//	@Summary		获取用户的对账单
//	@Description	与用户自己看到的对账单（GET /api/user/statement）相同，供客服对账。只能查看角色低于自己的用户，超级管理员不受此限。
//	@Tags			用户管理
//	@Param			id		path		int		true	"用户 ID"
//	@Param			month	query		string	false	"月份，格式 2006-01，默认北京时间的当月"
//	@Success		200		{object}	common.ApiResponse{data=model.WalletStatement}
//	@Security		AdminAuth
//	@Router			/api/user/{id}/statement [get]
func GetUserStatement(c *gin.Context) {
	userId, ok := manageableUserId(c)
	if !ok {
		return
	}
	writeWalletStatement(c, userId)
}

// GetUserWalletLedger lists a user's wallet ledger, with the administrator
// of each adjustment.
//
//	@Summary		列出用户的余额流水
//	@Description	除调用扣费外的每一次余额变动，新的在前，额度带符号。operator_id 是管理员调整的操作者 ID，其他类型为 0；用户自己的对账单不返回它。只能查看角色低于自己的用户，超级管理员不受此限。
//	@Tags			用户管理
//	@Param			id				path		int		true	"用户 ID"
//	@Param			p				query		int		false	"页码，从 1 开始"
//	@Param			page_size		query		int		false	"每页条数，最多 100"
//	@Param			type			query		string	false	"流水类型：topup、redemption、admin_adjustment、checkin、signup_bonus、invitee_bonus、aff_transfer、subscription_purchase"
//	@Param			start_timestamp	query		int		false	"起始时间，Unix 秒"
//	@Param			end_timestamp	query		int		false	"结束时间，Unix 秒"
//	@Success		200				{object}	common.ApiResponse{data=common.PageInfo{items=[]model.WalletLedgerEntry}}
//	@Security		AdminAuth
//	@Router			/api/user/{id}/wallet_ledger [get]
func GetUserWalletLedger(c *gin.Context) {
	userId, ok := manageableUserId(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	filter := model.WalletLedgerFilter{Type: c.Query("type")}
	filter.StartTimestamp, _ = strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	filter.EndTimestamp, _ = strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	entries, total, err := model.GetUserWalletLedger(userId, filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(entries)
	common.ApiSuccess(c, pageInfo)
}

// writeWalletStatement answers with the statement of userId for the month
// query, by default the current month in Beijing time.
func writeWalletStatement(c *gin.Context, userId int) {
	now := time.Now()
	month := now
	if value := c.Query("month"); value != "" {
		parsed, err := time.ParseInLocation("2006-01", value, model.WalletDayZone)
		if err != nil {
			common.ApiErrorT(c, "Month must be in YYYY-MM format")
			return
		}
		month = parsed
	}
	statement, err := model.GetWalletStatement(userId, month, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, statement)
}

// manageableUserId reads the user id of an administrator route and checks
// that the administrator may manage that user; otherwise it answers with the
// error and returns false.
func manageableUserId(c *gin.Context) (int, bool) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return 0, false
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return 0, false
	}
	return userId, true
}
