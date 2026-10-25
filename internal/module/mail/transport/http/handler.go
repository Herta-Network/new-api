// Package handler serves the root's email template and test email APIs.
package handler

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/module/mail"

	"github.com/gin-gonic/gin"
)

// ListEmailTemplates lists the editable emails with their templates in each
// backend language, the backend languages, and the emails a test can send.
func ListEmailTemplates(c *gin.Context) {
	templates, err := mail.Templates()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"templates":        templates,
		"languages":        i18n.SupportedLanguages(),
		"test_kinds":       mail.TestKinds(),
		"default_language": i18n.ResolveLang(""),
	})
}

func UpdateEmailTemplate(c *gin.Context) {
	var tmpl mail.Template
	if err := common.DecodeJson(c.Request.Body, &tmpl); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := mail.SaveTemplate(c.Param("kind"), c.Param("language"), tmpl); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func ResetEmailTemplate(c *gin.Context) {
	if err := mail.ResetTemplate(c.Param("kind"), c.Param("language")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// PreviewEmailTemplate renders a draft template with sample values. It
// changes nothing, so the audit log skips it.
func PreviewEmailTemplate(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
	var request struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		mail.Template
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	preview, err := mail.PreviewTemplate(request.Kind, request.Language, request.Template)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

// SendTestEmail sends a sample email through one sending method. The root
// sees the provider's reason for a failure, which other requesters never do.
func SendTestEmail(c *gin.Context) {
	var request struct {
		Provider string `json:"provider"`
		Kind     string `json:"kind"`
		Language string `json:"language"`
		To       string `json:"to"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.Var(request.To, "required,email"); err != nil {
		common.ApiErrorT(c, "Invalid email address")
		return
	}
	err := mail.SendTestEmail(request.Provider, request.Kind, request.Language, request.To)
	var failure *common.EmailDeliveryError
	if errors.As(err, &failure) {
		reason := failure.Detail
		if reason == "" {
			reason = failure.Error()
		}
		common.ApiErrorT(c, "Test email failed: {{reason}}", map[string]any{"reason": reason})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
