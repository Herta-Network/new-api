package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type emailBindRequest struct {
	Email     string `json:"email"`
	FlowToken string `json:"flow_token"`
	NewCode   string `json:"new_code"`
	OldCode   string `json:"old_code"`
}

// bindingNotificationResult reports whether the security notification email
// about a binding change failed to send.
type bindingNotificationResult struct {
	NotificationWarning bool `json:"notification_warning"`
}

// @Summary		开始绑定邮箱
// @Description	向新邮箱发送验证码；账户已有邮箱且二次验证没有使用两步验证或 Passkey 时，也向原邮箱发送（old_email_required 为 true）。需要 scope 为 account.binding.bind 的 X-Security-Proof，其 context 为 {provider: "email", email}。
// @Tags			第三方登录
// @Param			body	body		emailBindRequest	true	"要绑定的邮箱 email"
// @Success		200		{object}	common.ApiResponse{data=service.EmailBindingData}
// @Security		UserAuth && SecurityProof
// @Router			/api/oauth/email/bind/start [post]
func EmailBindStart(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded, notificationFailed := false, false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.binding_start", map[string]any{"provider": "email", "success": succeeded, "notification_failed": notificationFailed})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		writeSecurityOperationError(c, service.ErrVerificationContextInvalid)
		return
	}
	email, err := service.ValidateAccountEmail(request.Email)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	context, err := common.Marshal(service.AccountBindingContext{Provider: "email", Email: email})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	authorization := middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context})
	if authorization == nil {
		return
	}
	data, err := service.StartEmailBinding(identity, authorization, email, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded, notificationFailed = true, data.NotificationWarning
	common.ApiSuccess(c, data)
}

// @Summary		重发邮箱绑定验证码
// @Description	未到 resend_at 时返回 429 EMAIL_BINDING_RESEND_WAIT。
// @Tags			第三方登录
// @Param			body	body		emailBindRequest	true	"绑定流程的 flow_token"
// @Success		200		{object}	common.ApiResponse{data=service.EmailBindingData}
// @Failure		429		{object}	common.ApiResponse
// @Security		UserAuth
// @Router			/api/oauth/email/bind/resend [post]
func EmailBindResend(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded := false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.email_binding_resend", map[string]any{"success": succeeded})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	data, err := service.ResendAccountEmailBinding(identity, request.FlowToken, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	common.ApiSuccess(c, data)
}

// @Summary		完成绑定邮箱
// @Description	提交新邮箱的验证码 new_code，需要验证原邮箱时还要 old_code。
// @Tags			第三方登录
// @Param			body	body		emailBindRequest	true	"绑定流程的 flow_token 和验证码"
// @Success		200		{object}	common.ApiResponse{data=bindingNotificationResult}
// @Security		UserAuth
// @Router			/api/oauth/email/bind [post]
func EmailBind(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded, notificationFailed := false, false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.binding_bind", map[string]any{"provider": "email", "success": succeeded, "notification_failed": notificationFailed})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	state, err := service.FinishEmailBinding(identity, request.FlowToken, request.NewCode, request.OldCode)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	lang := service.AccountMailLanguage(c, identity.UserID)
	notificationFailed = service.NotifyAccountSecurityChange(lang, state.CurrentEmail, i18n.MsgSecurityEventEmailChanged) != nil
	if err := service.NotifyAccountSecurityChange(lang, state.Email, i18n.MsgSecurityEventEmailConfirmed); err != nil {
		notificationFailed = true
	}
	if err := model.PublishUserAuthCache(identity.UserID); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiSuccess(c, bindingNotificationResult{NotificationWarning: notificationFailed})
}
