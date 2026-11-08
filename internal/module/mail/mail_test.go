package mail

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/module/mail/internal/render"
	"github.com/QuantumNous/new-api/internal/module/mail/internal/store"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccountID = "0123456789abcdef0123456789abcdef"

// useEmailOptions replaces the saved options with options until the test
// ends.
func useEmailOptions(t *testing.T, options map[string]string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	saved := common.OptionMap
	common.OptionMap = options
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = saved
		common.OptionMapRWMutex.Unlock()
	})
}

// useCloudflare points the Cloudflare API at handler and makes Cloudflare
// the primary method, with SMTP, which is not configured, as the backup.
func useCloudflare(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	saved := cloudflareAPIBase
	cloudflareAPIBase = server.URL
	t.Cleanup(func() { cloudflareAPIBase = saved })
	savedServer, savedAccount := common.SMTPServer, common.SMTPAccount
	common.SMTPServer, common.SMTPAccount = "", ""
	t.Cleanup(func() { common.SMTPServer, common.SMTPAccount = savedServer, savedAccount })
	useEmailOptions(t, map[string]string{
		"EmailProvider":           common.EmailProviderCloudflare,
		"EmailBackupProvider":     common.EmailProviderSMTP,
		CloudflareAccountIDOption: testAccountID,
		CloudflareTokenOption:     "cf-test-token",
		CloudflareFromOption:      `"Site Mail" <noreply@example.com>`,
	})
}

func TestCloudflareSendsThroughTheEmailSendingAPI(t *testing.T) {
	var request struct {
		From    map[string]string `json:"from"`
		To      []string          `json:"to"`
		Subject string            `json:"subject"`
		HTML    string            `json:"html"`
	}
	useCloudflare(t, func(writer http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/accounts/"+testAccountID+"/email/sending/send", r.URL.Path)
		assert.Equal(t, "Bearer cf-test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, common.DecodeJson(r.Body, &request))
		_, _ = writer.Write([]byte(`{"success":true,"errors":[],"messages":[],"result":{"delivered":["one@example.com"],"permanent_bounces":[],"queued":[]}}`))
	})

	require.NoError(t, common.SendEmail("Hello", "one@example.com", "<p>hi</p>"))
	assert.Equal(t, map[string]string{"address": "noreply@example.com", "name": "Site Mail"}, request.From)
	assert.Equal(t, []string{"one@example.com"}, request.To)
	assert.Equal(t, "Hello", request.Subject)
	assert.Equal(t, "<p>hi</p>", request.HTML)
}

func TestCloudflareFailureReportsWhetherTheEmailWasSent(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		sent    bool
		notSent bool
	}{
		{"token lacks permission", http.StatusForbidden, `{"success":false,"errors":[{"code":10102,"message":"email.sending.error.authentication.forbidden"}]}`, false, true},
		{"throttled", http.StatusTooManyRequests, `{"success":false,"errors":[{"code":10004,"message":"email.sending.error.throttled"}]}`, false, true},
		{"authentication service unavailable", http.StatusServiceUnavailable, `{"success":false,"errors":[{"code":10100,"message":"email.sending.error.authentication.upstream"}]}`, false, true},
		{"internal error", http.StatusInternalServerError, `{"success":false,"errors":[{"code":10002,"message":"email.sending.error.internal_server"}]}`, false, false},
		{"recipient bounced", http.StatusOK, `{"success":true,"result":{"delivered":[],"queued":[],"permanent_bounces":["one@example.com"]}}`, false, true},
		{"recipient suppressed", http.StatusOK, `{"success":true,"result":{"delivered":[],"queued":[],"permanent_bounces":[],"suppressed_recipients":["one@example.com"]}}`, false, true},
		{"unreadable success", http.StatusOK, `not json`, false, false},
		{"queued", http.StatusOK, `{"success":true,"result":{"delivered":[],"queued":["one@example.com"],"permanent_bounces":[]}}`, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCloudflare(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			})

			err := common.SendEmailThrough(common.EmailProviderCloudflare, "Hello", "one@example.com", "<p>123456</p>")

			if test.sent {
				require.NoError(t, err)
				return
			}
			var failure *common.EmailDeliveryError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, test.notSent, failure.NotSent)
			assert.NotContains(t, failure.Error(), "cf-test-token")
			assert.NotContains(t, failure.Detail, "cf-test-token")
		})
	}
}

func TestCloudflareSettingsMustBeCompleteToSend(t *testing.T) {
	tests := []struct{ name, accountID, token, from, message string }{
		{"account ID not hexadecimal", "../../accounts/other/xxxxxxxxxxxxxxxxx", "cf-test-token", "noreply@example.com", "Cloudflare account ID is not configured or invalid"},
		{"token with a line break", testAccountID, "cf-test\r\nX: y", "noreply@example.com", "Cloudflare API token is not configured or invalid"},
		{"missing token", testAccountID, " ", "noreply@example.com", "Cloudflare API token is not configured or invalid"},
		{"sender with a header", testAccountID, "cf-test-token", "noreply@example.com\r\nBcc: x@example.com", "Cloudflare sender address is not configured or invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useEmailOptions(t, map[string]string{
				"EmailProvider":           common.EmailProviderSMTP,
				CloudflareAccountIDOption: test.accountID,
				CloudflareTokenOption:     test.token,
				CloudflareFromOption:      test.from,
			})
			require.EqualError(t, common.ValidateEmailOption("EmailProvider", common.EmailProviderCloudflare), test.message)
			var failure *common.EmailDeliveryError
			require.ErrorAs(t, common.SendEmailThrough(common.EmailProviderCloudflare, "Hello", "one@example.com", "<p>hi</p>"), &failure)
			assert.True(t, failure.NotSent, "invalid settings never reach Cloudflare")
		})
	}
}

// Every built-in template, the fixed ones too, passes the checks an
// administrator's template must pass, every text it uses is translated, and
// every email has a tag; all but a notice also have a preheader.
func TestBuiltinTemplatesPassTheTemplateChecks(t *testing.T) {
	kinds := slices.Concat(render.Editable, []render.Kind{mustKind(t, common.EmailKindNotice), mustKind(t, render.KindTest)})
	for _, kind := range kinds {
		for _, lang := range i18n.SupportedLanguages() {
			builtin := render.Builtin(kind.Name, lang)
			require.NotEmpty(t, builtin.Body, "%s %s", kind.Name, lang)
			assert.NoError(t, render.Check(kind, builtin), "%s %s", kind.Name, lang)
			assert.NotRegexp(t, `email\.[a-z_]+`, builtin.Subject+builtin.Body, "%s %s: a text is missing from the locale", kind.Name, lang)
			if kind.Name == render.KindLayout {
				continue
			}
			tag, preheader := render.Frame(kind, lang, sampleData(kind.Name, lang))
			assert.NotEmpty(t, tag, "%s %s", kind.Name, lang)
			if kind.Name != common.EmailKindNotice {
				assert.NotEmpty(t, preheader, "%s %s", kind.Name, lang)
				assert.NotContains(t, preheader, "no value", "%s %s", kind.Name, lang)
			}
		}
	}
}

func mustKind(t *testing.T, name string) render.Kind {
	t.Helper()
	kind, ok := render.Lookup(name)
	require.True(t, ok, name)
	return kind
}

func TestTemplateChecksAllowOnlyVariablesAndConditions(t *testing.T) {
	verification, _ := render.Lookup(common.EmailKindVerification)
	layout, _ := render.Lookup(render.KindLayout)
	tests := []struct {
		name    string
		kind    render.Kind
		tmpl    render.Template
		message string
	}{
		{"variables and conditions", verification, render.Template{Subject: "{{.SystemName}} code", Body: `<p>{{if .Code}}<b>{{.Code}}</b>{{else}}-{{end}} {{.Minutes}}</p>`}, ""},
		{"defined template", verification, render.Template{Subject: "Code", Body: `{{define "x"}}{{.Code}}{{end}}{{.Code}}`}, "Email templates may only use variables and if blocks"},
		{"range", verification, render.Template{Subject: "Code", Body: `{{range .Code}}{{.}}{{end}}{{.Code}}`}, "Email templates may only use variables and if blocks"},
		{"function call", verification, render.Template{Subject: "Code", Body: `{{printf "%s" .Code}}`}, "Email templates may only use variables and if blocks"},
		{"unknown variable", verification, render.Template{Subject: "Code", Body: `{{.Code}} {{.Password}}`}, "Unknown email template variable {{name}}"},
		{"unknown variable in the subject", verification, render.Template{Subject: "{{.Link}}", Body: `{{.Code}}`}, "Unknown email template variable {{name}}"},
		{"code missing", verification, render.Template{Subject: "Code", Body: `<p>Hello</p>`}, "Email body must use the variable {{name}}"},
		{"empty subject", verification, render.Template{Subject: " ", Body: `{{.Code}}`}, "Email subject must be 1 to {{max}} characters"},
		{"syntax error", verification, render.Template{Subject: "Code", Body: `{{.Code`}, "Email body template is invalid: {{reason}}"},
		{"layout without content", layout, render.Template{Body: `<html>{{.SystemName}}</html>`}, "Email body must use the variable {{name}}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := render.Check(test.kind, test.tmpl)
			if test.message == "" {
				require.NoError(t, err)
				return
			}
			var message *common.Message
			require.ErrorAs(t, err, &message)
			assert.Equal(t, test.message, message.Key)
		})
	}
}

// useSiteSettings sets what the layout shows around every email until the
// test ends. The legal settings stay empty: the footer links to the
// console's terms and privacy pages without them.
func useSiteSettings(t *testing.T, name, logo string, email system_setting.EmailSetting, frontendAddress string) {
	t.Helper()
	savedName, savedLogo := common.SystemName, common.Logo
	savedEmail, savedLegal := *system_setting.GetEmailSetting(), *system_setting.GetLegalSettings()
	savedServer, savedFrontend := system_setting.ServerAddress, system_setting.FrontendAddress
	common.SystemName, common.Logo = name, logo
	*system_setting.GetEmailSetting(), *system_setting.GetLegalSettings() = email, system_setting.LegalSettings{}
	system_setting.ServerAddress, system_setting.FrontendAddress = "https://api.example.com", frontendAddress
	t.Cleanup(func() {
		common.SystemName, common.Logo = savedName, savedLogo
		*system_setting.GetEmailSetting(), *system_setting.GetLegalSettings() = savedEmail, savedLegal
		system_setting.ServerAddress, system_setting.FrontendAddress = savedServer, savedFrontend
	})
}

// No value a sender or the site's settings give can add markup to an
// email, in any kind, in its preheader or in the layout around it; a link
// takes no script URL and a subject stays on one line.
func TestTemplateEmailsEscapeTheirValues(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useEmailOptions(t, map[string]string{})
	const hostile = `<i>"x"&'y'</i>`
	useSiteSettings(t, "Site"+hostile, "javascript:alert(1)", system_setting.EmailSetting{
		CompanyName: hostile, CompanyAddress: hostile, SupportEmail: hostile,
	}, "https://www.example.com")

	for _, name := range append(TestKinds()[1:], common.EmailKindNotice) {
		kind := mustKind(t, name)
		vars := map[string]string{}
		for _, variable := range kind.Vars {
			vars[variable] = hostile + "\nline"
		}
		for _, lang := range i18n.SupportedLanguages() {
			subject, html, err := renderEmail(lang, common.TemplateEmail{Kind: name, Vars: vars})
			require.NoError(t, err, "%s %s", name, lang)
			assert.NotContains(t, html, "<i>", "%s %s", name, lang)
			assert.Contains(t, html, "&lt;i&gt;", "%s %s", name, lang)
			assert.NotContains(t, subject, "\n", "%s %s", name, lang)
			assert.NotContains(t, html, "<img", "%s %s: a script URL is no logo", name, lang)
		}
	}

	subject, html, err := renderEmail(i18n.LangEn, common.TemplateEmail{Kind: common.EmailKindPasswordReset, Vars: map[string]string{"Link": "javascript:alert(1)", "Minutes": "<10>"}})
	require.NoError(t, err)
	assert.Equal(t, "Reset your Site"+hostile+" password", subject, "the subject is plain text")
	assert.Contains(t, html, `<a href="#ZgotmplZ"`, "a link takes no script URL")
	assert.Contains(t, html, "expires in &lt;10&gt; minutes")

	subject, html, err = renderEmail(i18n.LangEn, common.TemplateEmail{Kind: common.EmailKindNotice, Vars: map[string]string{
		"Title":   "Channel\r\nBcc: x@example.com",
		"Content": "Reason: <img src=x onerror=alert(1)>\nsecond line\n\nnext paragraph",
	}})
	require.NoError(t, err)
	assert.Equal(t, "Channel Bcc: x@example.com", subject, "a subject stays on one line")
	assert.Contains(t, html, "Reason: &lt;img src=x onerror=alert(1)&gt;<br>\nsecond line</p>")
	assert.Contains(t, html, ">next paragraph</p>", "a blank line starts a paragraph")
	assert.Contains(t, html, "Reason: &lt;img src=x onerror=alert(1)&gt; second line next paragraph</div>", "a notice previews its content")
}

// The footer shows the operator and the support address only when they are
// set, the links to the console's terms and privacy policy whenever the
// Frontend Address is set, and the header the logo only when an email can
// show it.
func TestLayoutShowsTheSiteSettingsThatAreSet(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useEmailOptions(t, map[string]string{})
	email := common.TemplateEmail{Kind: common.EmailKindVerification, Vars: map[string]string{"Code": "654321", "Minutes": "10"}}

	// Without a Frontend Address there is no console: the server address
	// would answer /terms with a 404.
	useSiteSettings(t, "Mioriq AI", "", system_setting.EmailSetting{}, "")
	_, html, err := renderEmail(i18n.LangZhCN, email)
	require.NoError(t, err)
	assert.Contains(t, html, `<html lang="zh-CN">`)
	assert.Contains(t, html, ">你的验证码是 654321，10 分钟内有效</div>", "the preheader leads the body")
	assert.Contains(t, html, ">验证码</td>", "the tag")
	assert.Contains(t, html, "此邮件由 Mioriq AI 自动发送，请勿直接回复。\n\n\n</td>", "nothing that is not set")
	for _, absent := range []string{"<img", "mailto:", "/terms", "/privacy"} {
		assert.NotContains(t, html, absent)
	}

	useSiteSettings(t, "Mioriq AI", "/logo.png", system_setting.EmailSetting{
		CompanyName: "Herta Network LLC", CompanyAddress: "30 N Gould St Ste N, Sheridan, WY 82801, USA", SupportEmail: "support@example.com",
	}, "https://www.example.com/")
	_, html, err = renderEmail(i18n.LangZhCN, email)
	require.NoError(t, err)
	assert.Contains(t, html, `<img src="https://www.example.com/logo.png"`, "a logo path is on the console")
	assert.Contains(t, html, `请勿直接回复。需要帮助请写信到 <a href="mailto:support@example.com" style="color:#3F4C28;">support@example.com</a>`)
	assert.Contains(t, html, "<br>Herta Network LLC · 30 N Gould St Ste N, Sheridan, WY 82801, USA")
	assert.Contains(t, html, `<br><a href="https://www.example.com/terms" target="_blank" style="color:#6E6F66;">服务条款</a> · <a href="https://www.example.com/privacy" target="_blank" style="color:#6E6F66;">隐私政策</a>`)

	useSiteSettings(t, "Mioriq AI", "https://cdn.example.com/logo.SVG?v=2", system_setting.EmailSetting{CompanyAddress: "Sheridan, WY"},
		"https://www.example.com")
	_, html, err = renderEmail(i18n.LangEn, email)
	require.NoError(t, err)
	assert.NotContains(t, html, "<img", "mail clients do not show SVG")
	assert.Contains(t, html, "Please do not reply.\n<br>Sheridan, WY\n<br><a href=\"https://www.example.com/terms\" target=\"_blank\" style=\"color:#6E6F66;\">Terms of Service</a> · <a href=\"https://www.example.com/privacy\"")
}

func TestAdministratorTemplatesApplyPerLanguage(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useEmailOptions(t, map[string]string{})
	email := common.TemplateEmail{Kind: common.EmailKindVerification, Vars: map[string]string{"Code": "654321", "Minutes": "10"}}

	require.NoError(t, SaveTemplate(common.EmailKindVerification, i18n.LangEn, Template{Subject: "Your code", Body: `<p class="custom">{{.Code}}</p>`}))
	require.NoError(t, SaveTemplate(render.KindLayout, i18n.LangEn, Template{Subject: "ignored", Body: `<div class="layout">{{.Content}}</div>`}))

	subject, html, err := renderEmail(i18n.LangEn, email)
	require.NoError(t, err)
	assert.Equal(t, "Your code", subject)
	assert.Equal(t, `<div class="layout"><p class="custom">654321</p></div>`, html)

	_, html, err = renderEmail(i18n.LangZhCN, email)
	require.NoError(t, err)
	assert.Contains(t, html, ">验证你的邮箱</h1>", "another language keeps its built-in template")
	assert.Contains(t, html, ">654321</div>")

	// A saved template that no longer renders falls back to the built-in one.
	require.NoError(t, store.SaveTemplate(model.DB, common.EmailKindVerification, i18n.LangEn, "Your code", `<a href="{{.Code}}`))
	_, html, err = renderEmail(i18n.LangEn, email)
	require.NoError(t, err)
	assert.Contains(t, html, ">Verify your email</h1>")
	assert.Contains(t, html, ">654321</div>")

	// A security notice saved before it had Time and Link still applies;
	// the sender's new values are simply not shown.
	require.NoError(t, SaveTemplate(common.EmailKindSecurityNotice, i18n.LangEn, Template{Subject: "Notice", Body: `<p>{{.Event}}</p>`}))
	_, html, err = renderEmail(i18n.LangEn, common.TemplateEmail{Kind: common.EmailKindSecurityNotice, Vars: map[string]string{
		"Event": "Password updated", "Time": "Nov 7, 2026, 17:20 (UTC+8)", "Link": "https://www.example.com/user/settings/security",
	}})
	require.NoError(t, err)
	assert.Equal(t, `<div class="layout"><p>Password updated</p></div>`, html)
	require.NoError(t, ResetTemplate(common.EmailKindSecurityNotice, i18n.LangEn))

	require.NoError(t, ResetTemplate(common.EmailKindVerification, i18n.LangEn))
	require.NoError(t, ResetTemplate(render.KindLayout, i18n.LangEn))
	templates, err := Templates()
	require.NoError(t, err)
	for _, kind := range templates {
		for _, entry := range kind.Languages {
			assert.False(t, entry.Customized, "%s %s", kind.Name, entry.Language)
		}
	}

	assert.EqualError(t, SaveTemplate(common.EmailKindNotice, i18n.LangEn, Template{Subject: "x", Body: "{{.Content}}"}), "Unknown email template")
	assert.EqualError(t, SaveTemplate(common.EmailKindVerification, "fr", Template{Subject: "x", Body: "{{.Code}}"}), "Unsupported email template language")
}

func TestPreviewRendersADraftWithSampleValues(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useEmailOptions(t, map[string]string{})

	preview, err := PreviewTemplate(render.KindLayout, i18n.LangEn, Template{Body: `<main>{{.Content}}</main>`})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(preview.HTML, "<main>"))
	assert.Contains(t, preview.HTML, ">123456</div>", "a layout previews around the sample verification email")

	_, err = PreviewTemplate(common.EmailKindVerification, i18n.LangEn, Template{Subject: "Code", Body: `<p>no code</p>`})
	require.Error(t, err)
	templates, err := store.AllTemplates(model.DB)
	require.NoError(t, err)
	assert.Empty(t, templates, "a preview saves nothing")
}

func TestTestEmailUsesOnlyTheChosenMethod(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useCloudflare(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"success":false,"errors":[{"code":10102,"message":"email.sending.error.authentication.forbidden"}]}`))
	})

	err := SendTestEmail(common.EmailProviderCloudflare, render.KindTest, i18n.LangEn, "admin@example.com")

	var failure *common.EmailDeliveryError
	require.ErrorAs(t, err, &failure)
	assert.Contains(t, failure.Detail, "10102 email.sending.error.authentication.forbidden", "the backup method does not take over a test")
	assert.EqualError(t, SendTestEmail(common.EmailProviderCloudflare, render.KindLayout, i18n.LangEn, "admin@example.com"), "Unknown email template")
}
