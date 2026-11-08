// Package render keeps the email templates: the kinds of email, their
// built-in templates, the checks an administrator's template must pass and
// the rendering.
//
// A template is a subject and an HTML body in Go template syntax, limited
// to variables ({{.Code}}) and conditions ({{if .Link}}…{{else}}…{{end}}).
// The body is rendered with html/template, which escapes every variable for
// where it stands (text, attribute, URL), so a value can never add markup;
// the subject is plain text on one line. Only Content, the rendered email
// placed in the layout, is markup.
package render

import (
	htmltemplate "html/template"
	"maps"
	"slices"
	"strings"
	texttemplate "text/template"
	"text/template/parse"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
)

// Kinds that only this module sends.
const (
	// KindLayout wraps every email: Subject, Content, and what the layout
	// shows around it (see Editable).
	KindLayout = "layout"
	// KindTest is the test email an administrator sends.
	KindTest = "test"
)

// Kind is an email the site sends from a template.
type Kind struct {
	Name string `json:"kind"`
	// Vars are the variables the sender fills; SystemName is always there.
	Vars []string `json:"variables"`
	// Required are the variables the body must use.
	Required []string `json:"required"`
}

// Editable are the kinds administrators can replace, in the order the
// console lists them. A variable added to a kind later is one the sender
// may leave empty, so templates saved before keep working.
var Editable = []Kind{
	{Name: common.EmailKindVerification, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindPasswordReset, Vars: []string{"Link", "Minutes"}, Required: []string{"Link"}},
	{Name: common.EmailKindBindingNew, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindBindingCurrent, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindSecurityNotice, Vars: []string{"Event", "Time", "Link"}, Required: []string{"Event"}},
	{Name: common.EmailKindQuotaLow, Vars: []string{"Title", "Quota", "Link"}},
	{Name: common.EmailKindTopupReceipt, Vars: []string{"OrderNo", "Time", "Method", "Amount", "Credit", "Balance", "Link"}, Required: []string{"OrderNo"}},
	// Preheader is the preview text inboxes show after the subject and Tag
	// the email's label in the header; the others are the site's settings,
	// empty when they are not set.
	{Name: KindLayout, Vars: []string{"Subject", "Content", "Preheader", "Tag", "Logo", "SupportEmail", "CompanyName", "CompanyAddress", "TermsLink", "PrivacyLink"}, Required: []string{"Content"}},
}

// fixed are the kinds whose template is always the built-in one.
var fixed = []Kind{
	{Name: common.EmailKindNotice, Vars: []string{"Title", "Content"}},
	{Name: KindTest},
}

// Lookup returns the kind called name.
func Lookup(name string) (Kind, bool) {
	for _, kind := range slices.Concat(Editable, fixed) {
		if kind.Name == name {
			return kind, true
		}
	}
	return Kind{}, false
}

// IsEditable reports whether administrators can replace the template of
// the kind called name.
func IsEditable(name string) bool {
	return slices.ContainsFunc(Editable, func(kind Kind) bool { return kind.Name == name })
}

// Template is the source of an email. The layout has no subject.
type Template struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Limits of a template's source.
const (
	maxSubjectLength = 200
	maxBodyBytes     = 64 << 10
	// maxPreheaderLength bounds the preview text taken from a notice.
	maxPreheaderLength = 120
)

// Builtin returns the built-in template of kind in lang, a supported
// backend language, built from the translated texts of the email.
func Builtin(kind string, lang string) Template {
	// Each variable translates to its own placeholder, so the texts become
	// template source.
	vars := map[string]any{}
	for _, name := range []string{"SystemName", "Code", "Minutes", "Link", "Event", "Title", "Quota", "OrderNo", "Credit", "Balance", "SupportEmail"} {
		vars[name] = "{{." + name + "}}"
	}
	text := func(key string) string { return i18n.Translate(lang, key, vars) }
	switch kind {
	case common.EmailKindVerification:
		return Template{text(i18n.MsgEmailVerificationSubject), heading(text(i18n.MsgEmailVerificationTitle)) +
			paragraph(text(i18n.MsgEmailVerificationIntro)) +
			codeBox(text(i18n.MsgEmailCodeLabel)) +
			note(text(i18n.MsgEmailVerificationOutro))}
	case common.EmailKindPasswordReset:
		return Template{text(i18n.MsgEmailPasswordResetSubject), heading(text(i18n.MsgEmailPasswordResetTitle)) +
			paragraph(text(i18n.MsgEmailPasswordResetIntro)) +
			button(text(i18n.MsgEmailPasswordResetButton)) +
			linkFallback(text(i18n.MsgEmailLinkFallback)) +
			note(text(i18n.MsgEmailPasswordResetOutro))}
	case common.EmailKindBindingNew:
		return Template{text(i18n.MsgEmailBindingNewSubject), heading(text(i18n.MsgEmailBindingNewTitle)) +
			paragraph(text(i18n.MsgEmailBindingNewIntro)) +
			codeBox(text(i18n.MsgEmailCodeLabel)) +
			note(text(i18n.MsgEmailBindingNewOutro))}
	case common.EmailKindBindingCurrent:
		return Template{text(i18n.MsgEmailBindingCurrentSubject), heading(text(i18n.MsgEmailBindingCurrentTitle)) +
			paragraph(text(i18n.MsgEmailBindingCurrentIntro)) +
			codeBox(text(i18n.MsgEmailCodeLabel)) +
			note(text(i18n.MsgEmailBindingCurrentOutro))}
	case common.EmailKindSecurityNotice:
		return Template{text(i18n.MsgEmailSecurityNoticeSubject), heading(text(i18n.MsgEmailSecurityNoticeTitle)) +
			paragraph(text(i18n.MsgEmailSecurityNoticeIntro)) +
			rows(row(text(i18n.MsgEmailSecurityNoticeEvent), "{{.Event}}", valueStrong)+
				"{{if .Time}}"+row(text(i18n.MsgEmailSecurityNoticeTime), "{{.Time}}", 0)+"{{end}}") +
			"{{if .Link}}" + button(text(i18n.MsgEmailSecurityNoticeButton)) + "{{end}}" +
			note(text(i18n.MsgEmailSecurityNoticeOutro))}
	case common.EmailKindQuotaLow:
		return Template{"{{.Title}}", heading("{{.Title}}") +
			paragraph(text(i18n.MsgEmailQuotaLowIntro)) +
			amountBox(text(i18n.MsgEmailQuotaLowLabel), "{{.Quota}}") +
			"{{if .Link}}" + button(text(i18n.MsgEmailQuotaLowButton)) + "{{end}}" +
			note(text(i18n.MsgEmailQuotaLowOutro))}
	case common.EmailKindTopupReceipt:
		return Template{text(i18n.MsgEmailTopupReceiptSubject), heading(text(i18n.MsgEmailTopupReceiptTitle)) +
			paragraph(text(i18n.MsgEmailTopupReceiptIntro)) +
			rows(row(text(i18n.MsgEmailTopupReceiptOrderNo), "{{.OrderNo}}", valueMono)+
				"{{if .Time}}"+row(text(i18n.MsgEmailTopupReceiptTime), "{{.Time}}", 0)+"{{end}}"+
				"{{if .Method}}"+row(text(i18n.MsgEmailTopupReceiptMethod), "{{.Method}}", 0)+"{{end}}"+
				"{{if .Amount}}"+row(text(i18n.MsgEmailTopupReceiptAmount), "{{.Amount}}", valueMono)+"{{end}}"+
				"{{if .Credit}}"+row(text(i18n.MsgEmailTopupReceiptCredit), "+{{.Credit}}", valueMono|valueStrong|valueCredit)+"{{end}}"+
				"{{if .Balance}}"+row(text(i18n.MsgEmailTopupReceiptBalance), "{{.Balance}}", valueMono|valueStrong)+"{{end}}") +
			"{{if .Link}}" + button(text(i18n.MsgEmailTopupReceiptButton)) + "{{end}}" +
			note(text(i18n.MsgEmailTopupReceiptOutro))}
	case common.EmailKindNotice:
		return Template{"{{.Title}}", heading("{{.Title}}") + "{{.Content}}\n"}
	case KindTest:
		return Template{text(i18n.MsgEmailTestSubject), heading(text(i18n.MsgEmailTestTitle)) +
			paragraph(text(i18n.MsgEmailTestIntro))}
	case KindLayout:
		// Chinese runs a sentence on without a space.
		separator := " "
		if lang == i18n.LangZhCN || lang == i18n.LangZhTW {
			separator = ""
		}
		vars["SupportEmail"] = `<a href="mailto:{{.SupportEmail}}" style="color:#3F4C28;">{{.SupportEmail}}</a>`
		return Template{Body: strings.NewReplacer(
			"[lang]", lang,
			"[footer]", text(i18n.MsgEmailLayoutFooter),
			"[support]", separator+text(i18n.MsgEmailLayoutSupport),
			"[terms]", text(i18n.MsgEmailLayoutTerms),
			"[privacy]", text(i18n.MsgEmailLayoutPrivacy),
		).Replace(layout)}
	}
	return Template{}
}

// Frame returns what the layout shows of an email of kind in lang besides
// its content: the tag in the header and the preheader, the preview text
// inboxes show after the subject, written from data, the email's values.
// An administrator's template of the kind keeps them. A notice previews
// the start of its content.
func Frame(kind Kind, lang string, data map[string]any) (tag string, preheader string) {
	keys, ok := frames[kind.Name]
	if !ok {
		return "", ""
	}
	tag = i18n.Translate(lang, keys.tag)
	if kind.Name == common.EmailKindNotice {
		content, _ := data["Content"].(string)
		preheader = strings.Join(strings.Fields(content), " ")
		if runes := []rune(preheader); len(runes) > maxPreheaderLength {
			preheader = string(runes[:maxPreheaderLength]) + "…"
		}
		return tag, preheader
	}
	if keys.preheader == "" {
		return tag, ""
	}
	values := map[string]any{"SystemName": ""}
	for _, name := range kind.Vars {
		values[name] = ""
	}
	maps.Copy(values, data)
	return tag, strings.Join(strings.Fields(i18n.Translate(lang, keys.preheader, values)), " ")
}

// frames are the translation keys of each kind's tag and preheader.
var frames = map[string]struct{ tag, preheader string }{
	common.EmailKindVerification:   {i18n.MsgEmailVerificationTag, i18n.MsgEmailVerificationPreheader},
	common.EmailKindPasswordReset:  {i18n.MsgEmailPasswordResetTag, i18n.MsgEmailPasswordResetPreheader},
	common.EmailKindBindingNew:     {i18n.MsgEmailBindingNewTag, i18n.MsgEmailBindingNewPreheader},
	common.EmailKindBindingCurrent: {i18n.MsgEmailBindingCurrentTag, i18n.MsgEmailBindingCurrentPreheader},
	common.EmailKindSecurityNotice: {i18n.MsgEmailSecurityNoticeTag, i18n.MsgEmailSecurityNoticePreheader},
	common.EmailKindQuotaLow:       {i18n.MsgEmailQuotaLowTag, i18n.MsgEmailQuotaLowPreheader},
	common.EmailKindTopupReceipt:   {i18n.MsgEmailTopupReceiptTag, i18n.MsgEmailTopupReceiptPreheader},
	common.EmailKindNotice:         {i18n.MsgEmailNoticeTag, ""},
	KindTest:                       {i18n.MsgEmailTestTag, i18n.MsgEmailTestPreheader},
}

// The built-in emails are tables with inline styles, which every mail
// client renders, in the colors of the site's ledger style: paper, ink,
// fine rules and moss. They load no web font: the system fonts, with
// Georgia and Song as the serif fallbacks.
const (
	fontSans  = `-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei',Arial,sans-serif`
	fontSerif = `Georgia,'Songti SC','Noto Serif SC',SimSun,serif`
	fontMono  = `'SF Mono',Menlo,Consolas,'Courier New',monospace`
)

// heading is the title of an email.
func heading(text string) string {
	return `<h1 style="margin:0 0 16px;font-family:` + fontSerif + `;font-size:25px;line-height:1.35;font-weight:700;color:#1D1E1A;">` + text + "</h1>\n"
}

// paragraph is body text.
func paragraph(text string) string {
	return `<p style="margin:0 0 14px;font-family:` + fontSans + `;font-size:15px;line-height:1.75;color:#1D1E1A;">` + text + "</p>\n"
}

// note is the closing text of an email, quieter than its body.
func note(text string) string {
	return `<p style="margin:0 0 10px;font-family:` + fontSans + `;font-size:14px;line-height:1.7;color:#5C5D55;">` + text + "</p>\n"
}

// codeBox shows {{.Code}} large in a dashed box under label.
func codeBox(label string) string {
	return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 18px;">
<tr><td align="center" style="padding:20px 12px;background-color:#F4F2EC;border:1px dashed #C7C3B6;border-radius:4px;">
<div style="margin:0 0 6px;font-family:` + fontSans + `;font-size:12px;letter-spacing:0.12em;color:#6E6F66;">` + label + `</div>
<div style="padding-left:10px;font-family:` + fontMono + `;font-size:34px;line-height:1.2;font-weight:600;letter-spacing:10px;color:#1D1E1A;">{{.Code}}</div>
</td></tr>
</table>
`
}

// amountBox shows value, an amount, large between two rules.
func amountBox(label, value string) string {
	return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:4px 0 18px;border-top:1px solid #DDDACF;border-bottom:1px solid #DDDACF;">
<tr>
<td style="padding:16px 0;font-family:` + fontSans + `;font-size:13px;color:#6E6F66;">` + label + `</td>
<td align="right" style="padding:16px 0;font-family:` + fontSerif + `;font-size:30px;line-height:1.2;font-weight:600;color:#85621A;">` + value + `</td>
</tr>
</table>
`
}

// rows is a ledger of label and value rows.
func rows(content string) string {
	return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:4px 0 20px;border-top:1px dashed #C7C3B6;">
` + content + "</table>\n"
}

// How row writes a value.
const (
	valueMono = 1 << iota
	valueStrong
	// valueCredit is money coming in, in moss.
	valueCredit
)

// row is one row of rows.
func row(label, value string, style int) string {
	font, weight, color := fontSans, "400", "#1D1E1A"
	if style&valueMono != 0 {
		font = fontMono
	}
	if style&valueStrong != 0 {
		weight = "600"
	}
	if style&valueCredit != 0 {
		color = "#3F4C28"
	}
	return `<tr>
<td style="padding:10px 12px 10px 0;border-bottom:1px dotted #DDDACF;font-family:` + fontSans + `;font-size:14px;color:#5C5D55;white-space:nowrap;vertical-align:top;">` + label + `</td>
<td align="right" style="padding:10px 0;border-bottom:1px dotted #DDDACF;font-family:` + font + `;font-size:14px;font-weight:` + weight + `;color:` + color + `;">` + value + `</td>
</tr>
`
}

// button is a solid ink button that opens {{.Link}}.
func button(label string) string {
	return `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:6px 0 20px;">
<tr><td style="border-radius:6px;background-color:#1D1E1A;">
<a href="{{.Link}}" target="_blank" style="display:inline-block;padding:12px 24px;font-family:` + fontSans + `;font-size:15px;line-height:1.2;font-weight:600;color:#F4F2EC;text-decoration:none;border-radius:6px;">` + label + `</a>
</td></tr>
</table>
`
}

// linkFallback writes out {{.Link}} for a reader whose client does not
// open the button.
func linkFallback(label string) string {
	return `<p style="margin:0 0 18px;font-family:` + fontSans + `;font-size:13px;line-height:1.6;color:#5C5D55;">` + label + `<br>
<a href="{{.Link}}" target="_blank" style="font-family:` + fontMono + `;font-size:12.5px;color:#3F4C28;word-break:break-all;">{{.Link}}</a></p>
`
}

// layout is the built-in layout: the site's logo and name over an ink
// rule, the email on white paper, and the footer. [lang], [footer],
// [support], [terms] and [privacy] take the translated texts.
const layout = `<!DOCTYPE html>
<html lang="[lang]">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="x-apple-disable-message-reformatting">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background-color:#F4F2EC;">
{{if .Preheader}}<div style="display:none;max-height:0;max-width:0;overflow:hidden;opacity:0;mso-hide:all;font-size:1px;line-height:1px;color:#F4F2EC;">{{.Preheader}}</div>
{{end}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#F4F2EC;">
<tr>
<td align="center" style="padding:32px 16px 40px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
<tr>
<td style="padding:0 4px 14px;border-bottom:2px solid #1D1E1A;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
<tr>
{{if .Logo}}<td width="38" style="width:38px;vertical-align:middle;"><img src="{{.Logo}}" width="28" height="28" alt="" style="display:block;width:28px;height:28px;border:0;border-radius:6px;"></td>
{{end}}<td style="vertical-align:middle;font-family:` + fontSerif + `;font-size:19px;line-height:1.3;font-weight:700;color:#1D1E1A;">{{.SystemName}}</td>
{{if .Tag}}<td align="right" style="vertical-align:middle;font-family:` + fontSans + `;font-size:12px;letter-spacing:0.12em;color:#6E6F66;white-space:nowrap;">{{.Tag}}</td>
{{end}}</tr>
</table>
</td>
</tr>
<tr>
<td style="padding:32px 32px 30px;background-color:#FFFEFB;border:1px solid #DDDACF;border-top:0;font-family:` + fontSans + `;font-size:15px;line-height:1.75;color:#1D1E1A;">
{{.Content}}
</td>
</tr>
<tr>
<td style="padding:18px 4px 0;font-family:` + fontSans + `;font-size:12px;line-height:1.7;color:#6E6F66;">
[footer]{{if .SupportEmail}}[support]{{end}}
{{if .CompanyName}}<br>{{.CompanyName}}{{if .CompanyAddress}} · {{.CompanyAddress}}{{end}}{{else}}{{if .CompanyAddress}}<br>{{.CompanyAddress}}{{end}}{{end}}
{{if .TermsLink}}<br><a href="{{.TermsLink}}" target="_blank" style="color:#6E6F66;">[terms]</a>{{if .PrivacyLink}} · <a href="{{.PrivacyLink}}" target="_blank" style="color:#6E6F66;">[privacy]</a>{{end}}{{else}}{{if .PrivacyLink}}<br><a href="{{.PrivacyLink}}" target="_blank" style="color:#6E6F66;">[privacy]</a>{{end}}{{end}}
</td>
</tr>
</table>
</td>
</tr>
</table>
</body>
</html>
`

// Check returns the first problem of tmpl as the template of kind: syntax
// beyond variables and conditions, an unknown variable, a missing required
// variable, or a body that cannot be escaped.
func Check(kind Kind, tmpl Template) error {
	allowed := append([]string{"SystemName"}, kind.Vars...)
	if kind.Name != KindLayout {
		if strings.TrimSpace(tmpl.Subject) == "" || utf8.RuneCountInString(tmpl.Subject) > maxSubjectLength {
			return common.NewMessage("Email subject must be 1 to {{max}} characters", map[string]any{"max": maxSubjectLength})
		}
		subject, err := texttemplate.New("subject").Parse(tmpl.Subject)
		if err != nil {
			return common.NewMessage("Email subject template is invalid: {{reason}}", map[string]any{"reason": err.Error()})
		}
		if len(subject.Templates()) != 1 {
			return common.NewMessage("Email templates may only use variables and if blocks")
		}
		if _, err := usedVars(subject.Tree.Root, allowed); err != nil {
			return err
		}
	}
	if strings.TrimSpace(tmpl.Body) == "" || len(tmpl.Body) > maxBodyBytes {
		return common.NewMessage("Email body must be 1 to {{max}} bytes", map[string]any{"max": maxBodyBytes})
	}
	body, err := htmltemplate.New("body").Parse(tmpl.Body)
	if err != nil {
		return common.NewMessage("Email body template is invalid: {{reason}}", map[string]any{"reason": err.Error()})
	}
	if len(body.Templates()) != 1 {
		return common.NewMessage("Email templates may only use variables and if blocks")
	}
	used, err := usedVars(body.Tree.Root, allowed)
	if err != nil {
		return err
	}
	for _, name := range kind.Required {
		if !slices.Contains(used, name) {
			return common.NewMessage("Email body must use the variable {{name}}", map[string]any{"name": name})
		}
	}
	// html/template finds where a variable stands only when it runs.
	sample := map[string]any{}
	for _, name := range allowed {
		sample[name] = "x"
	}
	if _, _, err := Execute(kind, tmpl, sample); err != nil {
		return common.NewMessage("Email body template is invalid: {{reason}}", map[string]any{"reason": err.Error()})
	}
	return nil
}

// usedVars returns the variables the nodes of list use, failing on any
// action other than a variable or a condition on one, or on a variable not
// in allowed.
func usedVars(list *parse.ListNode, allowed []string) ([]string, error) {
	if list == nil {
		return nil, nil
	}
	var used []string
	for _, node := range list.Nodes {
		var pipe *parse.PipeNode
		var branches []*parse.ListNode
		switch typed := node.(type) {
		case *parse.TextNode, *parse.CommentNode:
			continue
		case *parse.ActionNode:
			pipe = typed.Pipe
		case *parse.IfNode:
			pipe, branches = typed.Pipe, []*parse.ListNode{typed.List, typed.ElseList}
		default:
			return nil, common.NewMessage("Email templates may only use variables and if blocks")
		}
		if pipe == nil || len(pipe.Decl) > 0 || len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 1 {
			return nil, common.NewMessage("Email templates may only use variables and if blocks")
		}
		field, ok := pipe.Cmds[0].Args[0].(*parse.FieldNode)
		if !ok || len(field.Ident) != 1 {
			return nil, common.NewMessage("Email templates may only use variables and if blocks")
		}
		if !slices.Contains(allowed, field.Ident[0]) {
			return nil, common.NewMessage("Unknown email template variable {{name}}", map[string]any{"name": field.Ident[0]})
		}
		used = append(used, field.Ident[0])
		for _, branch := range branches {
			names, err := usedVars(branch, allowed)
			if err != nil {
				return nil, err
			}
			used = append(used, names...)
		}
	}
	return used, nil
}

// Execute renders tmpl as the template of kind. data holds the variables:
// strings, which are escaped, and htmltemplate.HTML for the layout's
// Content; a notice's Content is plain text whose paragraphs and line
// breaks are kept. A variable of the kind that data lacks is empty.
func Execute(kind Kind, tmpl Template, data map[string]any) (subject string, body string, err error) {
	values := map[string]any{"SystemName": ""}
	for _, name := range kind.Vars {
		values[name] = ""
	}
	maps.Copy(values, data)
	if text, ok := values["Content"].(string); ok && kind.Name == common.EmailKindNotice {
		values["Content"] = plainTextHTML(text)
	}
	if kind.Name != KindLayout {
		subjectTemplate, err := texttemplate.New("subject").Option("missingkey=zero").Parse(tmpl.Subject)
		if err != nil {
			return "", "", err
		}
		var rendered strings.Builder
		if err := subjectTemplate.Execute(&rendered, values); err != nil {
			return "", "", err
		}
		// One line: a header holds no line breaks.
		subject = strings.Join(strings.Fields(rendered.String()), " ")
	}
	bodyTemplate, err := htmltemplate.New("body").Option("missingkey=zero").Parse(tmpl.Body)
	if err != nil {
		return "", "", err
	}
	var rendered strings.Builder
	if err := bodyTemplate.Execute(&rendered, values); err != nil {
		return "", "", err
	}
	return subject, rendered.String(), nil
}

// plainTextHTML turns plain text into paragraphs: a blank line starts a
// paragraph and a line break inside one stays a line break.
func plainTextHTML(text string) htmltemplate.HTML {
	var html strings.Builder
	for block := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		block = strings.Trim(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		html.WriteString(paragraph(strings.ReplaceAll(htmltemplate.HTMLEscapeString(block), "\n", "<br>\n")))
	}
	return htmltemplate.HTML(html.String())
}
