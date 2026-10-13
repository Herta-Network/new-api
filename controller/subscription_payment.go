package controller

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type SubscriptionPayRequest struct {
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
}

func SubscriptionRequestPayment(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req SubscriptionPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorT(c, "Subscription plan is not enabled")
		return
	}
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(c.GetInt("id"), plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorT(c, "Purchase limit for this plan has been reached")
			return
		}
	}
	method, version, _, err := service.LoadPaymentConfiguration(req.PaymentMethod)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	currency := "USD"
	rate := 1.0
	if method.Provider == payment.Epay {
		currency = "CNY"
		rate = operation_setting.USDExchangeRate
	}
	if plan.Currency != "USD" || math.IsNaN(plan.PriceAmount) || math.IsInf(plan.PriceAmount, 0) || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		common.ApiErrorT(c, "Invalid payment configuration")
		return
	}
	money, err := payment.RoundMoney(decimal.NewFromFloat(plan.PriceAmount).Mul(decimal.NewFromFloat(rate)), currency)
	if err != nil {
		common.ApiErrorT(c, "Plan amount is too low")
		return
	}
	input := service.PaymentCheckout{Method: req.PaymentMethod, VersionID: version.ID, UserID: c.GetInt("id"), Money: money, Plan: plan}
	input.Money, err = service.QuotePayment(c.Request.Context(), input)
	if err != nil {
		common.ApiErrorT(c, "Failed to start payment")
		return
	}
	result, err := service.StartPayment(c.Request.Context(), input)
	if err != nil {
		common.ApiErrorT(c, "Failed to start payment")
		return
	}
	paymentResult(c, result)
}
