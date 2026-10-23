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

// AdminReconcilePaymentOrder asks the gateway whether an order was paid and
// settles it if so.
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

// GetPaymentPlatforms describes the gateway platforms and site currencies
// for the payment settings.
func GetPaymentPlatforms(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"platforms":                        payment.Platforms(),
		"currencies":                       operation_setting.PaymentCurrencies,
		"exchange_rate_api_key_configured": strings.TrimSpace(operation_setting.GetPaymentSetting().ExchangeRateAPIKey) != "",
	})
}

// AdminListPaymentMethods lists the payment methods with their secrets
// masked.
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
