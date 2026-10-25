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

type emailTemplateList struct {
	Templates []mail.TemplateKind `json:"templates"`
	// Languages are the backend languages; DefaultLanguage is the one a
	// reader who states none gets.
	Languages       []string `json:"languages"`
	DefaultLanguage string   `json:"default_language"`
	// TestKinds are the emails a test can send.
	TestKinds []string `json:"test_kinds"`
}

// ListEmailTemplates lists the editable emails with their templates in each
// backend language, the backend languages, and the emails a test can send.
//
//	@Summary	列出邮件模板
//	@Tags		邮件
//	@Success	200	{object}	common.ApiResponse{data=emailTemplateList}
//	@Security	RootAuth
//	@Router		/api/email/templates [get]
func ListEmailTemplates(c *gin.Context) {
	templates, err := mail.Templates()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, emailTemplateList{
		Templates:       templates,
		Languages:       i18n.SupportedLanguages(),
		DefaultLanguage: i18n.ResolveLang(""),
		TestKinds:       mail.TestKinds(),
	})
}

// UpdateEmailTemplate saves the template of an email in one language.
//
//	@Summary	保存邮件模板
//	@Tags		邮件
//	@Param		kind		path		string			true	"邮件种类，即 templates[].kind"
//	@Param		language	path		string			true	"后端语言，如 zh-CN"
//	@Param		body		body		mail.Template	true	"模板"
//	@Success	200			{object}	common.ApiResponse
//	@Security	RootAuth
//	@Router		/api/email/templates/{kind}/{language} [put]
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

// ResetEmailTemplate drops the saved template of an email in one language,
// so the built-in one applies again.
//
//	@Summary	恢复内置邮件模板
//	@Tags		邮件
//	@Param		kind		path		string	true	"邮件种类，即 templates[].kind"
//	@Param		language	path		string	true	"后端语言，如 zh-CN"
//	@Success	200			{object}	common.ApiResponse
//	@Security	RootAuth
//	@Router		/api/email/templates/{kind}/{language} [delete]
func ResetEmailTemplate(c *gin.Context) {
	if err := mail.ResetTemplate(c.Param("kind"), c.Param("language")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

type previewEmailTemplateRequest struct {
	Kind     string `json:"kind"`
	Language string `json:"language"`
	mail.Template
}

// PreviewEmailTemplate renders a draft template with sample values. It
// changes nothing, so the audit log skips it.
//
//	@Summary	预览邮件模板
//	@Tags		邮件
//	@Param		body	body		previewEmailTemplateRequest	true	"邮件种类、语言和模板草稿"
//	@Success	200		{object}	common.ApiResponse{data=mail.Preview}
//	@Security	RootAuth
//	@Router		/api/email/templates/preview [post]
func PreviewEmailTemplate(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
	var request previewEmailTemplateRequest
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

type sendTestEmailRequest struct {
	// Provider is the sending method to test: smtp, resend or cloudflare.
	Provider string `json:"provider"`
	// Kind is one of the test kinds ListEmailTemplates returns.
	Kind     string `json:"kind"`
	Language string `json:"language"`
	To       string `json:"to"`
}

// SendTestEmail sends a sample email through one sending method. The root
// sees the provider's reason for a failure, which other requesters never do.
//
//	@Summary	发送测试邮件
//	@Tags		邮件
//	@Param		body	body		sendTestEmailRequest	true	"发送方式、邮件种类、语言和收件人"
//	@Success	200		{object}	common.ApiResponse
//	@Security	RootAuth
//	@Router		/api/email/test [post]
func SendTestEmail(c *gin.Context) {
	var request sendTestEmailRequest
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
