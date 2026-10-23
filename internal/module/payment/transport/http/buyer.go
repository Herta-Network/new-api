// Package handler serves the payment module's HTTP API under /api/payment:
// gateway callbacks, the buyer's orders, and the administrator's orders,
// unmatched payments and payment methods.
package handler

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// paymentAPIError answers a failed payment request: web console messages
// as they are, an unknown order as not found, anything else as a generic
// failure whose cause stays in the server log.
func paymentAPIError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, payment.ErrOrderNotFound):
		common.ApiErrorT(c, "Order not found")
	case errors.Is(err, payment.ErrOrderNotPending):
		common.ApiErrorT(c, "This order can no longer be paid. Create a new order.")
	default:
		logger.LogError(c.Request.Context(), common.LogText("payment request failed: %v", err))
		common.ApiErrorT(c, "The payment request failed. Try again later.")
	}
}

// GetPaymentInfo returns what the wallet page needs to offer a top-up.
func GetPaymentInfo(c *gin.Context) {
	settings := operation_setting.GetPaymentSetting()
	common.ApiSuccess(c, gin.H{
		"currency":                         operation_setting.PaymentCurrency(),
		"min_topup":                        settings.MinTopup,
		"amount_options":                   settings.AmountOptions,
		"amount_discount":                  settings.AmountDiscount,
		"payment_compliance_confirmed":     operation_setting.IsPaymentComplianceConfirmed(),
		"payment_compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
	})
}

// GetBuyerPaymentMethods lists the methods the purpose in the query can be
// paid with.
func GetBuyerPaymentMethods(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiSuccess(c, []payment.MethodOption{})
		return
	}
	methods, err := payment.BuyerMethods(c.Request.Context(), c.Query("purpose"))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, methods)
}

func decodePurchase(c *gin.Context) (payment.PurchaseRequest, bool) {
	var req payment.PurchaseRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return req, false
	}
	return req, true
}

// QuotePayment prices a purchase without creating an order.
func QuotePayment(c *gin.Context) {
	req, ok := decodePurchase(c)
	if !ok {
		return
	}
	quote, err := payment.QuotePurchase(c.Request.Context(), c.GetInt("id"), req)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, quote)
}

// CreatePaymentOrder creates an order and starts its payment.
func CreatePaymentOrder(c *gin.Context) {
	req, ok := decodePurchase(c)
	if !ok {
		return
	}
	order, checkout, err := payment.CreateOrder(c.Request.Context(), c.GetInt("id"), req, c.ClientIP())
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"order": order, "checkout": checkout})
}

// CheckoutPaymentOrder starts the payment of a pending order again.
func CheckoutPaymentOrder(c *gin.Context) {
	checkout, err := payment.ResumeCheckout(c.Request.Context(), c.GetInt("id"), c.Param("no"))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, checkout)
}

// SyncPaymentOrder asks the gateway whether an order was paid and settles
// it if so.
func SyncPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	if err == nil {
		order, err = payment.SyncOrder(c.Request.Context(), order, c.ClientIP())
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

// CloseUserPaymentOrder gives up a pending order of the user.
func CloseUserPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	if err == nil {
		order, err = payment.CloseOrder(c.Request.Context(), order, payment.CloseByOwner)
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

// ListUserPaymentOrders lists the user's orders, newest first.
func ListUserPaymentOrders(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	orders, total, err := payment.ListOrders(payment.OrderFilter{UserId: c.GetInt("id")}, pageInfo)
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(orders)
	common.ApiSuccess(c, pageInfo)
}

// GetUserPaymentOrder returns one of the user's orders.
func GetUserPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}
