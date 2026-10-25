package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

type PaymentComplianceRequest struct {
	Confirmed bool `json:"confirmed"`
}

func requirePaymentCompliance(c *gin.Context) bool {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return false
	}
	return true
}

type paymentComplianceConfirmation struct {
	Confirmed    bool   `json:"confirmed"`
	TermsVersion string `json:"terms_version"`
	ConfirmedAt  int64  `json:"confirmed_at"`
	ConfirmedBy  int    `json:"confirmed_by"`
}

// @Summary		确认支付合规条款
// @Description	确认后开放支付、兑换码、订阅和邀请奖励功能。须用登录得到的访问令牌，用个人访问令牌（PAT）调用返回 403。
// @Tags			系统设置
// @Param			body	body		PaymentComplianceRequest	true	"confirmed 须为 true"
// @Success		200		{object}	common.ApiResponse{data=paymentComplianceConfirmation}
// @Failure		403		{object}	common.ApiResponse	"用个人访问令牌调用"
// @Security		RootAuth
// @Router			/api/option/payment_compliance [post]
func ConfirmPaymentCompliance(c *gin.Context) {
	if c.GetBool("use_access_token") {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "This operation requires dashboard session authentication. API access token is not allowed.",
		})
		return
	}

	var req PaymentComplianceRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !req.Confirmed {
		common.ApiErrorT(c, "Please confirm the compliance statement")
		return
	}

	now := time.Now().Unix()
	userId := c.GetInt("id")
	clientIP := c.ClientIP()

	updates := map[string]string{
		"payment_setting.compliance_confirmed":     "true",
		"payment_setting.compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
		"payment_setting.compliance_confirmed_at":  strconv.FormatInt(now, 10),
		"payment_setting.compliance_confirmed_by":  strconv.Itoa(userId),
		"payment_setting.compliance_confirmed_ip":  clientIP,
	}

	// One transaction, so a failure never leaves part of the confirmation.
	if err := model.UpdateOptionsBulk(updates); err != nil {
		common.ApiError(c, err)
		return
	}

	logger.LogInfo(c.Request.Context(), common.LogText(
		"payment compliance confirmed user_id=%d ip=%s terms_version=%s confirmed_at=%d",
		userId,
		clientIP,
		operation_setting.CurrentComplianceTermsVersion,
		now,
	))

	common.ApiSuccess(c, paymentComplianceConfirmation{
		Confirmed:    true,
		TermsVersion: operation_setting.CurrentComplianceTermsVersion,
		ConfirmedAt:  now,
		ConfirmedBy:  userId,
	})
}
