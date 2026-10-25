package handler

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

type paymentNoteRequest struct {
	Note string `json:"note"`
}

// decodePaymentNote reads the note an administrator must give for an
// action that has no gateway evidence.
func decodePaymentNote(c *gin.Context) (string, bool) {
	var req paymentNoteRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return "", false
	}
	note := strings.TrimSpace(req.Note)
	if note == "" || len(note) > 500 {
		common.ApiErrorT(c, "Enter a note of at most 500 characters")
		return "", false
	}
	return note, true
}

// AdminListPaymentOrders lists every order, newest first, filtered by the
// query.
//
//	@Summary	列出支付订单
//	@Tags		支付管理
//	@Param		p			query		int		false	"页码，从 1 开始"
//	@Param		page_size	query		int		false	"每页条数"
//	@Param		user_id		query		int		false	"用户 ID"
//	@Param		status		query		int		false	"状态：1 待支付、2 已支付、3 已关闭、5 已完成"
//	@Param		purpose		query		string	false	"用途：topup 或 subscription"
//	@Param		platform	query		string	false	"平台：epay、stripe、cryptomus 或 balance"
//	@Param		order_no	query		string	false	"订单号"
//	@Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]entity.PaymentOrder}}
//	@Security	AdminAuth
//	@Router		/api/payment/admin/orders [get]
func AdminListPaymentOrders(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId, _ := strconv.Atoi(c.Query("user_id"))
	status, _ := strconv.Atoi(c.Query("status"))
	orders, total, err := payment.ListOrders(payment.OrderFilter{
		UserId: userId, Status: status, Purpose: c.Query("purpose"), Platform: c.Query("platform"), OrderNo: strings.TrimSpace(c.Query("order_no")),
	}, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(orders)
	common.ApiSuccess(c, pageInfo)
}

// AdminListPaymentOrderInvoices lists the invoices of an order in the order
// they were issued.
//
//	@Summary	列出订单的发票
//	@Tags		支付管理
//	@Param		no	path		string	true	"订单号"
//	@Success	200	{object}	common.ApiResponse{data=[]entity.PaymentInvoice}
//	@Security	AdminAuth
//	@Router		/api/payment/admin/orders/{no}/invoices [get]
func AdminListPaymentOrderInvoices(c *gin.Context) {
	invoices, err := payment.OrderInvoices(c.Param("no"))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, invoices)
}

// AdminReconcilePaymentOrder asks the gateways whether an invoice of an
// order was paid and settles the order if so.
//
//	@Summary	对账支付订单
//	@Tags		支付管理
//	@Param		no	path		string	true	"订单号"
//	@Success	200	{object}	common.ApiResponse{data=entity.PaymentOrder}
//	@Security	AdminAuth
//	@Router		/api/payment/admin/orders/{no}/reconcile [post]
func AdminReconcilePaymentOrder(c *gin.Context) {
	order, err := payment.GetOrder(c.Param("no"))
	if err == nil {
		order, err = payment.SyncOrder(c.Request.Context(), order, c.ClientIP())
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

// AdminClosePaymentOrder closes a pending order through its gateway.
//
//	@Summary		关闭支付订单
//	@Description	网关确认已支付的订单改为入账；网关无法确认未支付时订单保持待支付并返回失败。
//	@Tags			支付管理
//	@Param			no	path		string	true	"订单号"
//	@Success		200	{object}	common.ApiResponse{data=entity.PaymentOrder}
//	@Security		AdminAuth
//	@Router			/api/payment/admin/orders/{no}/close [post]
func AdminClosePaymentOrder(c *gin.Context) {
	order, err := payment.GetOrder(c.Param("no"))
	if err == nil {
		order, err = payment.CloseOrder(c.Request.Context(), order, payment.CloseByAdmin)
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

// AdminFulfilPaymentOrder retries the fulfilment of a paid order.
//
//	@Summary	重试订单履约
//	@Tags		支付管理
//	@Param		no	path		string	true	"订单号"
//	@Success	200	{object}	common.ApiResponse{data=entity.PaymentOrder}
//	@Security	AdminAuth
//	@Router		/api/payment/admin/orders/{no}/fulfil [post]
func AdminFulfilPaymentOrder(c *gin.Context) {
	order, err := payment.FulfilOrder(c.Param("no"), c.ClientIP())
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

// AdminCompletePaymentOrder marks an order paid without gateway evidence;
// the administrator's note is kept in the audit log.
//
//	@Summary		手动完成支付订单
//	@Description	不经网关确认把订单记为已支付并履约，备注记入审计日志。
//	@Tags			支付管理
//	@Param			no		path		string				true	"订单号"
//	@Param			body	body		paymentNoteRequest	true	"备注，去除首尾空白后不能为空，最多 500 字节"
//	@Success		200		{object}	common.ApiResponse{data=entity.PaymentOrder}
//	@Security		AdminAuth
//	@Router			/api/payment/admin/orders/{no}/complete [post]
func AdminCompletePaymentOrder(c *gin.Context) {
	note, ok := decodePaymentNote(c)
	if !ok {
		return
	}
	order, err := payment.CompleteOrderManually(c.Param("no"), c.ClientIP())
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	recordAudit(c, "payment.order_complete", map[string]any{"order_no": order.OrderNo, "user_id": order.UserId, "note": note})
	common.ApiSuccess(c, order)
}

// AdminListPaymentUnmatched lists the payments that could not settle their
// order; resolved=true or false filters them.
//
//	@Summary		列出无法匹配的支付
//	@Description	网关确认收款但无法结算订单的支付，如重复支付、金额不符或履约失败。金额以 currency 的 1/100 为单位。
//	@Tags			支付管理
//	@Param			p			query		int		false	"页码，从 1 开始"
//	@Param			page_size	query		int		false	"每页条数"
//	@Param			resolved	query		bool	false	"true 只看已处理，false 只看未处理，不传则全部"
//	@Success		200			{object}	common.ApiResponse{data=common.PageInfo{items=[]entity.PaymentUnmatched}}
//	@Security		AdminAuth
//	@Router			/api/payment/admin/unmatched [get]
func AdminListPaymentUnmatched(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	var resolved *bool
	if value, err := strconv.ParseBool(c.Query("resolved")); err == nil {
		resolved = &value
	}
	records, total, err := payment.ListUnmatched(resolved, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(records)
	common.ApiSuccess(c, pageInfo)
}

// AdminResolvePaymentUnmatched marks an unmatched payment handled.
//
//	@Summary		处理无法匹配的支付
//	@Description	只做标记，不退款也不入账；备注记入审计日志。
//	@Tags			支付管理
//	@Param			id		path		int					true	"记录 ID"
//	@Param			body	body		paymentNoteRequest	true	"备注，去除首尾空白后不能为空，最多 500 字节"
//	@Success		200		{object}	common.ApiResponse
//	@Security		AdminAuth
//	@Router			/api/payment/admin/unmatched/{id}/resolve [post]
func AdminResolvePaymentUnmatched(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	note, ok := decodePaymentNote(c)
	if !ok {
		return
	}
	resolved, err := payment.ResolveUnmatched(id, c.GetInt("id"), note)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !resolved {
		common.ApiErrorT(c, "The record does not exist or is already resolved")
		return
	}
	recordAudit(c, "payment.unmatched_resolve", map[string]any{"id": id, "note": note})
	common.ApiSuccess(c, nil)
}

type paymentPlatformList struct {
	Platforms []payment.PlatformInfo `json:"platforms"`
	// Currencies are the currencies a site may price in.
	Currencies                   []string `json:"currencies"`
	ExchangeRateAPIKeyConfigured bool     `json:"exchange_rate_api_key_configured"`
}

// GetPaymentPlatforms describes the gateway platforms and site currencies
// for the payment settings.
//
//	@Summary	列出支付平台
//	@Tags		支付设置
//	@Success	200	{object}	common.ApiResponse{data=paymentPlatformList}
//	@Security	RootAuth
//	@Router		/api/payment/settings/platforms [get]
func GetPaymentPlatforms(c *gin.Context) {
	common.ApiSuccess(c, paymentPlatformList{
		Platforms:                    payment.Platforms(),
		Currencies:                   operation_setting.PaymentCurrencies,
		ExchangeRateAPIKeyConfigured: strings.TrimSpace(operation_setting.GetPaymentSetting().ExchangeRateAPIKey) != "",
	})
}

// AdminListPaymentOptions lists the payment options with the number of
// their channels.
//
//	@Summary	列出支付方式
//	@Tags		支付设置
//	@Success	200	{object}	common.ApiResponse{data=[]payment.OptionDetail}
//	@Security	RootAuth
//	@Router		/api/payment/settings/options [get]
func AdminListPaymentOptions(c *gin.Context) {
	options, err := payment.AdminOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, options)
}

func decodePaymentOption(c *gin.Context) (payment.OptionInput, bool) {
	var in payment.OptionInput
	if err := common.DecodeJson(c.Request.Body, &in); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return in, false
	}
	return in, true
}

// @Summary	创建支付方式
// @Tags		支付设置
// @Param		body	body		payment.OptionInput	true	"fee_mode：0 不收手续费、1 按比例、2 固定、3 比例加固定；fee_rate_bp 为基点（0–10000）；fee_fixed 以站点货币的 1/100 为单位"
// @Success	200		{object}	common.ApiResponse{data=entity.PaymentOption}
// @Security	RootAuth
// @Router		/api/payment/settings/options [post]
func AdminCreatePaymentOption(c *gin.Context) {
	in, ok := decodePaymentOption(c)
	if !ok {
		return
	}
	option, err := payment.CreateOption(in)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	recordAudit(c, "payment.option_create", map[string]any{"id": option.Id, "name": option.Name})
	common.ApiSuccess(c, option)
}

// @Summary		更新支付方式
// @Description	余额支付方式（ID -1）的手续费总是 0。
// @Tags			支付设置
// @Param			id		path		int					true	"支付方式 ID"
// @Param			body	body		payment.OptionInput	true	"fee_mode：0 不收手续费、1 按比例、2 固定、3 比例加固定；fee_rate_bp 为基点（0–10000）；fee_fixed 以站点货币的 1/100 为单位"
// @Success		200		{object}	common.ApiResponse{data=entity.PaymentOption}
// @Security		RootAuth
// @Router			/api/payment/settings/options/{id} [put]
func AdminUpdatePaymentOption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	in, ok := decodePaymentOption(c)
	if !ok {
		return
	}
	option, err := payment.UpdateOption(id, in)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	recordAudit(c, "payment.option_update", map[string]any{"id": option.Id, "name": option.Name})
	common.ApiSuccess(c, option)
}

// @Summary		删除支付方式
// @Description	余额支付方式和仍有渠道的支付方式不能删除。
// @Tags			支付设置
// @Param			id	path		int	true	"支付方式 ID"
// @Success		200	{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/payment/settings/options/{id} [delete]
func AdminDeletePaymentOption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := payment.DeleteOption(id); err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminListPaymentMethods lists the payment channels with their secrets
// masked.
//
//	@Summary		列出支付渠道
//	@Description	config 中的密钥字段为空字符串，secrets_set 表示各密钥是否已配置。
//	@Tags			支付设置
//	@Success		200	{object}	common.ApiResponse{data=[]payment.MethodDetail}
//	@Security		RootAuth
//	@Router			/api/payment/settings/methods [get]
func AdminListPaymentMethods(c *gin.Context) {
	methods, err := payment.AdminMethods()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, methods)
}

func decodePaymentMethod(c *gin.Context) (payment.MethodInput, bool) {
	var in payment.MethodInput
	if err := common.DecodeJson(c.Request.Body, &in); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return in, false
	}
	return in, true
}

// @Summary	创建支付渠道
// @Tags		支付设置
// @Param		body	body		payment.MethodInput	true	"min_amount、max_amount 以站点货币的 1/100 为单位，0 表示不限；config 的字段见支付平台列表，密钥字段留空则保留原值"
// @Success	200		{object}	common.ApiResponse{data=payment.MethodDetail}
// @Security	RootAuth
// @Router		/api/payment/settings/methods [post]
func AdminCreatePaymentMethod(c *gin.Context) {
	in, ok := decodePaymentMethod(c)
	if !ok {
		return
	}
	method, err := payment.CreateMethod(c.Request.Context(), in)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	recordAudit(c, "payment.method_create", map[string]any{"id": method.Id, "name": method.Name, "platform": method.Platform})
	common.ApiSuccess(c, method)
}

// @Summary		更新支付渠道
// @Description	platform 须留空或与原值相同；有待支付订单时不能修改 config 和 domain；余额渠道不能修改。
// @Tags			支付设置
// @Param			id		path		int					true	"支付渠道 ID"
// @Param			body	body		payment.MethodInput	true	"min_amount、max_amount 以站点货币的 1/100 为单位，0 表示不限；config 的字段见支付平台列表，密钥字段留空则保留原值"
// @Success		200		{object}	common.ApiResponse{data=payment.MethodDetail}
// @Security		RootAuth
// @Router			/api/payment/settings/methods/{id} [put]
func AdminUpdatePaymentMethod(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	in, ok := decodePaymentMethod(c)
	if !ok {
		return
	}
	method, err := payment.UpdateMethod(c.Request.Context(), id, in)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	recordAudit(c, "payment.method_update", map[string]any{"id": method.Id, "name": method.Name, "platform": method.Platform})
	common.ApiSuccess(c, method)
}

// @Summary		删除支付渠道
// @Description	余额渠道和有待支付订单的渠道不能删除；删除 Stripe 渠道时同时删除其 Webhook。
// @Tags			支付设置
// @Param			id	path		int	true	"支付渠道 ID"
// @Success		200	{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/payment/settings/methods/{id} [delete]
func AdminDeletePaymentMethod(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := payment.DeleteMethod(c.Request.Context(), id); err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminSyncPaymentWebhook points a Stripe method's webhook at its current
// callback URL.
//
//	@Summary	同步 Stripe Webhook
//	@Tags		支付设置
//	@Param		id	path		int	true	"支付渠道 ID"
//	@Success	200	{object}	common.ApiResponse{data=payment.MethodDetail}
//	@Security	RootAuth
//	@Router		/api/payment/settings/methods/{id}/webhook/sync [post]
func AdminSyncPaymentWebhook(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	method, err := payment.SyncStripeWebhook(c.Request.Context(), id)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, method)
}
