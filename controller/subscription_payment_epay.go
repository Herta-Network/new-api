package controller

import (
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type SubscriptionEpayPayRequest struct {
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
}

func SubscriptionRequestEpay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req SubscriptionEpayPayRequest
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
	rate := operation_setting.USDExchangeRate
	if plan.Currency != "USD" || math.IsNaN(plan.PriceAmount) || math.IsInf(plan.PriceAmount, 0) || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		common.ApiErrorT(c, "Invalid payment configuration")
		return
	}
	money, err := payment.RoundCNY(decimal.NewFromFloat(plan.PriceAmount).Mul(decimal.NewFromFloat(rate)))
	if err != nil {
		common.ApiErrorT(c, "Plan amount is too low")
		return
	}
	result, err := service.StartPayment(c.Request.Context(), service.PaymentCheckout{Method: req.PaymentMethod, UserID: c.GetInt("id"), Money: money, Plan: plan})
	if err != nil {
		common.ApiErrorT(c, "Failed to start payment")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": result.Params, "url": result.URL})
}
