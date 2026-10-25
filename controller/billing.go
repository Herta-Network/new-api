package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// @Summary		查询额度
// @Description	设置 general_setting.billing_api_uses_token_quota 开启（默认）时返回调用令牌的额度，否则返回用户的额度。金额是站点货币金额，额度按 tokens 显示时是原始额度；无限额度令牌固定为 100000000。查询失败时仍返回 HTTP 200，响应体是 OpenAIErrorResponse。
// @Tags			账单
// @Success		200	{object}	OpenAISubscriptionResponse
// @Security		TokenAuth
// @Router			/dashboard/billing/subscription [get]
// @Router			/v1/dashboard/billing/subscription [get]
func GetSubscription(c *gin.Context) {
	var remainQuota int
	var usedQuota int
	var err error
	var token *model.Token
	var expiredTime int64
	if operation_setting.GetGeneralSetting().BillingAPIUsesTokenQuota {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err == nil {
			expiredTime = token.ExpiredTime
			remainQuota = token.RemainQuota
			usedQuota = token.UsedQuota
		}
	} else {
		userId := c.GetInt("id")
		remainQuota, err = model.GetUserQuota(userId, false)
		if err == nil {
			usedQuota, err = model.GetUserUsedQuota(userId)
		}
	}
	if expiredTime <= 0 {
		expiredTime = 0
	}
	if err != nil {
		openAIError := types.OpenAIError{
			Message: err.Error(),
			Type:    "upstream_error",
		}
		c.JSON(200, OpenAIErrorResponse{Error: openAIError})
		return
	}
	amount := billingAPIAmount(remainQuota + usedQuota)
	if token != nil && token.UnlimitedQuota {
		amount = 100000000
	}
	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       amount,
		HardLimitUSD:       amount,
		SystemHardLimitUSD: amount,
		AccessUntil:        expiredTime,
	}
	c.JSON(200, subscription)
	return
}

// @Summary		查询用量
// @Description	设置 general_setting.billing_api_uses_token_quota 开启（默认）时返回调用令牌的已用额度，否则返回用户的已用额度。total_usage 是站点货币金额乘以 100，额度按 tokens 显示时是原始额度乘以 100；不按日期筛选。查询失败时仍返回 HTTP 200，响应体是 OpenAIErrorResponse。
// @Tags			账单
// @Success		200	{object}	OpenAIUsageResponse
// @Security		TokenAuth
// @Router			/dashboard/billing/usage [get]
// @Router			/v1/dashboard/billing/usage [get]
func GetUsage(c *gin.Context) {
	var quota int
	var err error
	var token *model.Token
	if operation_setting.GetGeneralSetting().BillingAPIUsesTokenQuota {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err == nil {
			quota = token.UsedQuota
		}
	} else {
		userId := c.GetInt("id")
		quota, err = model.GetUserUsedQuota(userId)
	}
	if err != nil {
		openAIError := types.OpenAIError{
			Message: err.Error(),
			Type:    "new_api_error",
		}
		c.JSON(200, OpenAIErrorResponse{Error: openAIError})
		return
	}
	amount := billingAPIAmount(quota)
	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: amount * 100,
	}
	c.JSON(200, usage)
	return
}

// billingAPIAmount is how the OpenAI-compatible billing endpoints report
// quota: raw quota in the tokens display mode, otherwise the amount users
// see in the site currency (USD while the site currency has no rate).
func billingAPIAmount(quota int) float64 {
	if operation_setting.IsTokensDisplay() {
		return float64(quota)
	}
	amount, _ := operation_setting.QuotaDisplayAmount(int64(quota))
	return amount.InexactFloat64()
}
