// Package mail is the email module: the Cloudflare sending method and the
// email templates. Its init function registers both with the kernel, which
// sends every email through common.SendEmail and has template emails
// rendered here: an administrator's template of the reader's language, else
// the built-in one, placed in the layout.
package mail

import (
	"context"
	htmltemplate "html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/module/mail/entity"
	"github.com/QuantumNous/new-api/internal/module/mail/internal/cloudflare"
	"github.com/QuantumNous/new-api/internal/module/mail/internal/render"
	"github.com/QuantumNous/new-api/internal/module/mail/internal/store"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// The options that hold the Cloudflare settings. GetOptions never returns
// the token; it reports CloudflareEmailTokenConfigured instead.
const (
	CloudflareAccountIDOption = "CloudflareEmailAccountId"
	CloudflareTokenOption     = "CloudflareEmailApiToken"
	CloudflareFromOption      = "CloudflareEmailFrom"
)

// cloudflareTimeout bounds every Cloudflare request.
const cloudflareTimeout = 20 * time.Second

// cloudflareAPIBase is the Cloudflare API; tests point it at a local fake.
var cloudflareAPIBase = cloudflare.APIBase

// cloudflareHTTPClient carries the Cloudflare requests. It is built on
// first use, after the outbound HTTP client is initialized.
var cloudflareHTTPClient = sync.OnceValue(func() *http.Client {
	client := &http.Client{
		Timeout: cloudflareTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if base := service.GetHttpClient(); base != nil {
		client.Transport = base.Transport
	}
	return client
})

func init() {
	common.RegisterEmailRenderer(renderEmail)
	common.RegisterEmailTransport(common.EmailProviderCloudflare, common.EmailTransport{
		Options: []string{CloudflareAccountIDOption, CloudflareTokenOption, CloudflareFromOption},
		Validate: func(settings map[string]string) error {
			return cloudflare.ValidateSettings(strings.TrimSpace(settings[CloudflareAccountIDOption]), strings.TrimSpace(settings[CloudflareTokenOption]), settings[CloudflareFromOption])
		},
		Send: sendCloudflareEmail,
	})
}

func sendCloudflareEmail(ctx context.Context, settings map[string]string, email common.OutgoingEmail) error {
	sender, err := cloudflare.ParseSender(settings[CloudflareFromOption])
	if err != nil {
		return common.EmailNotSent(err)
	}
	if sender.Name == "" {
		sender.Name = common.SystemName
	}
	accountID := strings.TrimSpace(settings[CloudflareAccountIDOption])
	token := strings.TrimSpace(settings[CloudflareTokenOption])
	return cloudflare.Send(ctx, cloudflareHTTPClient(), cloudflareAPIBase, accountID, token, sender, email)
}

// renderEmail is the kernel's common.EmailRenderer.
func renderEmail(lang string, email common.TemplateEmail) (string, string, error) {
	kind, ok := render.Lookup(email.Kind)
	if !ok || kind.Name == render.KindLayout || kind.Name == render.KindTest {
		return "", "", common.NewMessage("Unknown email template")
	}
	data := map[string]any{}
	for name, value := range email.Vars {
		data[name] = value
	}
	return compose(kind, i18n.ResolveLang(lang), data, nil)
}

// compose renders kind in lang from data and places it in the layout,
// with the email's tag and preheader and what the site's settings show
// around every email. The administrator's templates of lang apply, else the
// built-in ones; drafts stand in for the templates of their kinds in a
// preview.
func compose(kind render.Kind, lang string, data map[string]any, drafts map[string]render.Template) (string, string, error) {
	data["SystemName"] = common.SystemName
	stored, err := store.Templates(model.DB, lang, kind.Name, render.KindLayout)
	if err != nil {
		// The built-in templates still send sign-in and recovery emails.
		common.SysError(common.LogText("failed to load email templates, using the built-in ones: %v", err))
	}
	layoutKind, _ := render.Lookup(render.KindLayout)
	subject, body, err := execute(kind, lang, stored, drafts, data)
	if err != nil {
		return "", "", err
	}
	tag, preheader := render.Frame(kind, lang, data)
	settings := system_setting.GetEmailSetting()
	layoutData := map[string]any{
		"SystemName":     common.SystemName,
		"Subject":        subject,
		"Content":        htmltemplate.HTML(body),
		"Preheader":      preheader,
		"Tag":            tag,
		"Logo":           emailLogo(),
		"SupportEmail":   strings.TrimSpace(settings.SupportEmail),
		"CompanyName":    strings.TrimSpace(settings.CompanyName),
		"CompanyAddress": strings.TrimSpace(settings.CompanyAddress),
	}
	// The console shows the terms and the privacy policy the legal settings
	// hold at /terms and /privacy; an empty one has no page to link to.
	legal := system_setting.GetLegalSettings()
	if strings.TrimSpace(legal.UserAgreement) != "" {
		layoutData["TermsLink"] = service.ConsoleURL("/terms")
	}
	if strings.TrimSpace(legal.PrivacyPolicy) != "" {
		layoutData["PrivacyLink"] = service.ConsoleURL("/privacy")
	}
	_, html, err := execute(layoutKind, lang, stored, drafts, layoutData)
	if err != nil {
		return "", "", err
	}
	return subject, html, nil
}

// emailLogo is the site's logo as an email shows it: the Logo setting when
// it is an http or https image other than SVG, which Gmail and other
// clients do not display; a path on its own is on the console. Otherwise
// the layout shows the site's name alone.
func emailLogo() string {
	logo := strings.TrimSpace(common.Logo)
	if strings.HasPrefix(logo, "/") && !strings.HasPrefix(logo, "//") {
		logo = service.ConsoleURL(logo)
	}
	parsed, err := url.Parse(logo)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".svg") {
		return ""
	}
	return parsed.String()
}

// execute renders kind with its draft, else the administrator's template,
// else the built-in one. A saved template that no longer renders falls back
// to the built-in one, so the email still goes out.
func execute(kind render.Kind, lang string, stored map[string]entity.EmailTemplate, drafts map[string]render.Template, data map[string]any) (string, string, error) {
	if draft, ok := drafts[kind.Name]; ok {
		return render.Execute(kind, draft, data)
	}
	if row, ok := stored[kind.Name]; ok && render.IsEditable(kind.Name) {
		subject, body, err := render.Execute(kind, render.Template{Subject: row.Subject, Body: row.Body}, data)
		if err == nil {
			return subject, body, nil
		}
		common.SysError(common.LogText("email template %s in %s failed to render, using the built-in one: %v", kind.Name, lang, err))
	}
	return render.Execute(kind, render.Builtin(kind.Name, lang), data)
}

// Template is the source of an email: a subject and an HTML body.
type Template = render.Template

// LanguageTemplate is the template of an email in one backend language.
type LanguageTemplate struct {
	Language   string   `json:"language"`
	Template   Template `json:"template"`
	Builtin    Template `json:"builtin"`
	Customized bool     `json:"customized"`
	UpdatedAt  int64    `json:"updated_at"`
}

// TemplateKind is an email administrators can edit: its variables and its
// template in each backend language.
type TemplateKind struct {
	render.Kind
	Languages []LanguageTemplate `json:"languages"`
}

// Templates lists the emails administrators can edit with their templates.
func Templates() ([]TemplateKind, error) {
	rows, err := store.AllTemplates(model.DB)
	if err != nil {
		return nil, err
	}
	kinds := make([]TemplateKind, 0, len(render.Editable))
	for _, kind := range render.Editable {
		item := TemplateKind{Kind: kind}
		for _, lang := range i18n.SupportedLanguages() {
			builtin := render.Builtin(kind.Name, lang)
			entry := LanguageTemplate{Language: lang, Template: builtin, Builtin: builtin}
			for _, row := range rows {
				if row.Kind == kind.Name && row.Language == lang {
					entry.Template = Template{Subject: row.Subject, Body: row.Body}
					entry.Customized, entry.UpdatedAt = true, row.UpdatedAt
				}
			}
			item.Languages = append(item.Languages, entry)
		}
		kinds = append(kinds, item)
	}
	return kinds, nil
}

// editableKind returns the editable kind called name in lang, a backend
// language.
func editableKind(name, lang string) (render.Kind, error) {
	kind, ok := render.Lookup(name)
	if !ok || !render.IsEditable(name) {
		return kind, common.NewMessage("Unknown email template")
	}
	if !slices.Contains(i18n.SupportedLanguages(), lang) {
		return kind, common.NewMessage("Unsupported email template language")
	}
	return kind, nil
}

// SaveTemplate replaces the built-in template of kind in lang after
// checking it. The layout has no subject.
func SaveTemplate(kindName, lang string, tmpl Template) error {
	kind, err := editableKind(kindName, lang)
	if err != nil {
		return err
	}
	if kind.Name == render.KindLayout {
		tmpl.Subject = ""
	}
	if err := render.Check(kind, tmpl); err != nil {
		return err
	}
	return store.SaveTemplate(model.DB, kind.Name, lang, tmpl.Subject, tmpl.Body)
}

// ResetTemplate restores the built-in template of kind in lang.
func ResetTemplate(kindName, lang string) error {
	kind, err := editableKind(kindName, lang)
	if err != nil {
		return err
	}
	return store.DeleteTemplate(model.DB, kind.Name, lang)
}

// Preview is a rendered email.
type Preview struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// PreviewTemplate renders tmpl, a draft of kind in lang, with sample values.
// A draft layout wraps the sample verification email.
func PreviewTemplate(kindName, lang string, tmpl Template) (Preview, error) {
	kind, err := editableKind(kindName, lang)
	if err != nil {
		return Preview{}, err
	}
	if kind.Name == render.KindLayout {
		tmpl.Subject = ""
	}
	if err := render.Check(kind, tmpl); err != nil {
		return Preview{}, err
	}
	shown := kind
	if kind.Name == render.KindLayout {
		shown, _ = render.Lookup(common.EmailKindVerification)
	}
	subject, html, err := compose(shown, lang, sampleData(shown.Name, lang), map[string]render.Template{kind.Name: tmpl})
	return Preview{Subject: subject, HTML: html}, err
}

// sampleData are the values of a preview or test email of kind in lang;
// no real code or link is ever shown.
func sampleData(kind, lang string) map[string]any {
	minutes := common.VerificationValidMinutes
	if kind == common.EmailKindBindingNew || kind == common.EmailKindBindingCurrent {
		minutes = int(model.EmailBindingTTL / time.Minute)
	}
	link := service.ConsoleURL("/user/wallet")
	switch kind {
	case common.EmailKindPasswordReset:
		link = service.ConsoleURL("/user/reset?" + url.Values{"email": {"user@example.com"}, "token": {"sample-token"}}.Encode())
	case common.EmailKindSecurityNotice:
		link = service.ConsoleURL(service.AccountSecurityPath)
	case common.EmailKindTopupReceipt:
		link = service.ConsoleURL(service.WalletOrdersPath)
	}
	symbol := operation_setting.CurrencySymbol(operation_setting.PaymentCurrency())
	return map[string]any{
		"Code":    "123456",
		"Minutes": strconv.Itoa(minutes),
		"Link":    link,
		"Event":   i18n.Translate(lang, i18n.MsgSecurityEventPasswordUpdated),
		"Time":    i18n.FormatTime(lang, time.Now()),
		"Title":   i18n.Translate(lang, i18n.MsgNotifyQuotaLowTitle),
		"Quota":   logger.FormatQuotaToCents(int(common.QuotaPerUnit)),
		"OrderNo": "P20261107080200SAMPLE0001",
		"Method":  "Stripe",
		"Amount":  symbol + "50.00",
		"Credit":  symbol + "50.00",
		"Balance": symbol + "178.40",
	}
}

// TestKinds are the emails an administrator can send as a test: the test
// email and every editable email but the layout, which wraps them all.
func TestKinds() []string {
	kinds := []string{render.KindTest}
	for _, kind := range render.Editable {
		if kind.Name != render.KindLayout {
			kinds = append(kinds, kind.Name)
		}
	}
	return kinds
}

// SendTestEmail sends a sample of kind in lang to receiver through provider
// alone, with the saved settings and templates. A failure is an
// *common.EmailDeliveryError whose Detail is the provider's reason.
func SendTestEmail(provider, kindName, lang, receiver string) error {
	if !slices.Contains(common.EmailProviders, provider) {
		return common.NewMessage("Email sending method must be SMTP, Resend or Cloudflare")
	}
	if !slices.Contains(TestKinds(), kindName) {
		return common.NewMessage("Unknown email template")
	}
	if !slices.Contains(i18n.SupportedLanguages(), lang) {
		return common.NewMessage("Unsupported email template language")
	}
	kind, _ := render.Lookup(kindName)
	subject, html, err := compose(kind, lang, sampleData(kind.Name, lang), nil)
	if err != nil {
		return err
	}
	return common.SendEmailThrough(provider, subject, receiver, html)
}
