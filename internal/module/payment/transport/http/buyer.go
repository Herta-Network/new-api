// Package handler serves the payment module's HTTP API under /api/payment:
// gateway callbacks, the buyer's orders, and the administrator's orders,
// unmatched payments and payment methods.
package handler

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
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

type paymentInfo struct {
	// Currency is the site currency.
	Currency string `json:"currency"`
	// MinTopup is the smallest top-up, a decimal amount of the site currency.
	MinTopup string `json:"min_topup"`
	// AmountOptions are top-up amounts in whole units of the site currency.
	AmountOptions []int `json:"amount_options"`
	// AmountDiscount maps such an amount to the multiplier its price is
	// charged at.
	AmountDiscount map[int]float64 `json:"amount_discount"`
}

// GetPaymentInfo returns what the wallet page needs to offer a top-up.
//
//	@Summary	获取充值信息
//	@Tags		支付
//	@Success	200	{object}	common.ApiResponse{data=paymentInfo}
//	@Security	UserAuth
//	@Router		/api/payment/info [get]
func GetPaymentInfo(c *gin.Context) {
	settings := operation_setting.GetPaymentSetting()
	common.ApiSuccess(c, paymentInfo{
		Currency:       operation_setting.PaymentCurrency(),
		MinTopup:       settings.MinTopup,
		AmountOptions:  settings.AmountOptions,
		AmountDiscount: settings.AmountDiscount,
	})
}

// GetBuyerPaymentMethods lists the payment options the purpose in the
// query can be paid with.
//
//	@Summary	列出可用支付方式
//	@Tags		支付
//	@Param		purpose	query		string	true	"购买用途：topup（充值）或 subscription（订阅）"
//	@Success	200		{object}	common.ApiResponse{data=[]payment.OptionView}
//	@Security	UserAuth
//	@Router		/api/payment/methods [get]
func GetBuyerPaymentMethods(c *gin.Context) {
	options, err := payment.BuyerOptions(c.Request.Context(), c.Query("purpose"))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, options)
}

// answerBuyerOrder answers with the buyer's view of an order, or err.
func answerBuyerOrder(c *gin.Context, order *entity.PaymentOrder, err error) {
	var view *payment.BuyerOrder
	if err == nil {
		view, err = payment.BuyerOrderOf(order)
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

// buyerOf describes the request that starts a checkout.
func buyerOf(c *gin.Context) payment.Buyer {
	return payment.Buyer{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
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
//
//	@Summary		购买报价
//	@Description	返回的金额以站点货币的 1/100 为单位（1234 即 12.34）。
//	@Tags			支付
//	@Param			body	body		payment.PurchaseRequest	true	"purpose 为 topup 或 subscription；payment_option_id 为支付方式 ID，-1 为余额；充值时 amount 为站点货币金额，JSON 数字，最多两位小数；订阅时 plan_id 为套餐 ID"
//	@Success		200		{object}	common.ApiResponse{data=payment.Quote}
//	@Security		UserAuth
//	@Router			/api/payment/quote [post]
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

type createdPaymentOrder struct {
	Order    *payment.BuyerOrder `json:"order"`
	Checkout *payment.Checkout   `json:"checkout"`
}

// CreatePaymentOrder creates an order and starts its payment.
//
//	@Summary		创建支付订单
//	@Description	创建订单并开始支付：checkout.type 为 url（打开链接）、stripe（用 Stripe.js 确认）、qrcode（展示二维码）或 paid（余额或免费购买已完成）。按买家设备选择支付形式的 EPay 通道以请求的 User-Agent 区分手机与电脑。按用户限流。
//	@Tags			支付
//	@Param			body	body		payment.PurchaseRequest	true	"purpose 为 topup 或 subscription；payment_option_id 为支付方式 ID，-1 为余额；充值时 amount 为站点货币金额，JSON 数字，最多两位小数；订阅时 plan_id 为套餐 ID"
//	@Success		200		{object}	common.ApiResponse{data=createdPaymentOrder}
//	@Security		UserAuth
//	@Router			/api/payment/orders [post]
func CreatePaymentOrder(c *gin.Context) {
	req, ok := decodePurchase(c)
	if !ok {
		return
	}
	order, checkout, err := payment.CreateOrder(c.Request.Context(), c.GetInt("id"), req, buyerOf(c))
	var view *payment.BuyerOrder
	if err == nil {
		view, err = payment.BuyerOrderOf(order)
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, createdPaymentOrder{Order: view, Checkout: checkout})
}

// CheckoutPaymentOrder starts the payment of a pending order again.
//
//	@Summary		重新发起支付
//	@Description	支付形式按本次请求的 User-Agent 重新判断买家设备。按用户限流。
//	@Tags			支付
//	@Param			no	path		string	true	"订单号"
//	@Success		200	{object}	common.ApiResponse{data=payment.Checkout}
//	@Security		UserAuth
//	@Router			/api/payment/orders/{no}/checkout [post]
func CheckoutPaymentOrder(c *gin.Context) {
	checkout, err := payment.ResumeCheckout(c.Request.Context(), c.GetInt("id"), c.Param("no"), buyerOf(c))
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	common.ApiSuccess(c, checkout)
}

// SyncPaymentOrder asks the gateway whether an order was paid and settles
// it if so.
//
//	@Summary		同步订单支付状态
//	@Description	向网关查询订单是否已支付，已支付则入账。按用户限流，轮询时注意间隔。
//	@Tags			支付
//	@Param			no	path		string	true	"订单号"
//	@Success		200	{object}	common.ApiResponse{data=payment.BuyerOrder}
//	@Security		UserAuth
//	@Router			/api/payment/orders/{no}/sync [post]
func SyncPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	if err == nil {
		order, err = payment.SyncOrder(c.Request.Context(), order, c.ClientIP())
	}
	answerBuyerOrder(c, order, err)
}

// CloseUserPaymentOrder gives up a pending order of the user.
//
//	@Summary		关闭支付订单
//	@Description	网关确认已支付的订单改为入账；网关无法确认未支付时订单保持待支付并返回失败。按用户限流。
//	@Tags			支付
//	@Param			no	path		string	true	"订单号"
//	@Success		200	{object}	common.ApiResponse{data=payment.BuyerOrder}
//	@Security		UserAuth
//	@Router			/api/payment/orders/{no}/close [post]
func CloseUserPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	if err == nil {
		order, err = payment.CloseOrder(c.Request.Context(), order, payment.CloseByOwner)
	}
	answerBuyerOrder(c, order, err)
}

// ListUserPaymentOrders lists the user's orders, newest first.
//
//	@Summary	列出我的支付订单
//	@Tags		支付
//	@Param		p			query		int	false	"页码，从 1 开始"
//	@Param		page_size	query		int	false	"每页条数"
//	@Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]payment.BuyerOrder}}
//	@Security	UserAuth
//	@Router		/api/payment/orders [get]
func ListUserPaymentOrders(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	orders, total, err := payment.ListOrders(payment.OrderFilter{UserId: c.GetInt("id")}, pageInfo)
	var views []*payment.BuyerOrder
	if err == nil {
		views, err = payment.BuyerOrders(orders)
	}
	if err != nil {
		paymentAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(views)
	common.ApiSuccess(c, pageInfo)
}

// GetUserPaymentOrder returns one of the user's orders.
//
//	@Summary	获取支付订单
//	@Tags		支付
//	@Param		no	path		string	true	"订单号"
//	@Success	200	{object}	common.ApiResponse{data=payment.BuyerOrder}
//	@Security	UserAuth
//	@Router		/api/payment/orders/{no} [get]
func GetUserPaymentOrder(c *gin.Context) {
	order, err := payment.UserOrder(c.GetInt("id"), c.Param("no"))
	answerBuyerOrder(c, order, err)
}
