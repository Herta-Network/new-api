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
	// KindLayout wraps every email: Subject, Content.
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
// console lists them.
var Editable = []Kind{
	{Name: common.EmailKindVerification, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindPasswordReset, Vars: []string{"Link", "Minutes"}, Required: []string{"Link"}},
	{Name: common.EmailKindBindingNew, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindBindingCurrent, Vars: []string{"Code", "Minutes"}, Required: []string{"Code"}},
	{Name: common.EmailKindSecurityNotice, Vars: []string{"Event"}, Required: []string{"Event"}},
	{Name: common.EmailKindQuotaLow, Vars: []string{"Title", "Quota", "Link"}},
	{Name: KindLayout, Vars: []string{"Subject", "Content"}, Required: []string{"Content"}},
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
)

// Builtin returns the built-in template of kind in lang, a supported
// backend language, built from the translated texts the emails always had.
func Builtin(kind string, lang string) Template {
	// Each variable translates to its own placeholder, so the texts become
	// template source.
	vars := map[string]any{}
	for _, name := range []string{"SystemName", "Code", "Minutes", "Link", "Event", "Quota", "Content"} {
		vars[name] = "{{." + name + "}}"
	}
	vars["Prompt"] = "{{.Title}}"
	text := func(key string) string { return i18n.Translate(lang, key, vars) }
	paragraphs := func(keys ...string) string {
		var body strings.Builder
		for _, key := range keys {
			body.WriteString("<p>" + text(key) + "</p>\n")
		}
		return body.String()
	}
	switch kind {
	case common.EmailKindVerification:
		return Template{text(i18n.MsgEmailVerificationSubject), paragraphs(i18n.MsgEmailVerificationGreeting, i18n.MsgEmailVerificationCode, i18n.MsgEmailVerificationValidity)}
	case common.EmailKindPasswordReset:
		return Template{text(i18n.MsgEmailPasswordResetSubject), paragraphs(i18n.MsgEmailPasswordResetGreeting, i18n.MsgEmailPasswordResetLink, i18n.MsgEmailPasswordResetLinkFallback, i18n.MsgEmailPasswordResetValidity)}
	case common.EmailKindBindingNew:
		return Template{text(i18n.MsgEmailBindingSubject), paragraphs(i18n.MsgEmailBindingNewIntro, i18n.MsgEmailBindingCode, i18n.MsgEmailBindingNewValidity)}
	case common.EmailKindBindingCurrent:
		return Template{text(i18n.MsgEmailBindingSubject), paragraphs(i18n.MsgEmailBindingOldIntro, i18n.MsgEmailBindingCode, i18n.MsgEmailBindingOldValidity)}
	case common.EmailKindSecurityNotice:
		return Template{text(i18n.MsgEmailSecurityNoticeSubject), paragraphs(i18n.MsgEmailSecurityNoticeChanged, i18n.MsgEmailSecurityNoticeAdvice)}
	case common.EmailKindQuotaLow:
		return Template{"{{.Title}}", paragraphs(i18n.MsgNotifyQuotaLowEmail)}
	case common.EmailKindNotice:
		return Template{"{{.Title}}", "<p>{{.Content}}</p>\n"}
	case KindTest:
		return Template{text(i18n.MsgEmailTestSubject), paragraphs(i18n.MsgEmailTestContent)}
	case KindLayout:
		return Template{Body: strings.ReplaceAll(layout, "{{footer}}", text(i18n.MsgEmailLayoutFooter))}
	}
	return Template{}
}

// layout is the built-in layout; {{footer}} takes the translated footer.
const layout = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background-color:#f4f4f5;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f4f4f5;">
<tr>
<td align="center" style="padding:32px 16px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background-color:#ffffff;border:1px solid #e4e4e7;border-radius:8px;">
<tr>
<td style="padding:20px 32px;border-bottom:1px solid #e4e4e7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:18px;font-weight:600;color:#18181b;">{{.SystemName}}</td>
</tr>
<tr>
<td style="padding:24px 32px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:14px;line-height:1.6;color:#3f3f46;">
{{.Content}}
</td>
</tr>
</table>
<p style="max-width:560px;margin:16px auto 0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:12px;line-height:1.5;color:#a1a1aa;">{{footer}}</p>
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
// strings, which are escaped, and htmltemplate.HTML for Content. A variable
// of the kind that data lacks is empty.
func Execute(kind Kind, tmpl Template, data map[string]any) (subject string, body string, err error) {
	values := map[string]any{"SystemName": ""}
	for _, name := range kind.Vars {
		values[name] = ""
	}
	for name, value := range data {
		values[name] = value
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

// PlainTextHTML turns plain text into markup that keeps its line breaks.
func PlainTextHTML(text string) htmltemplate.HTML {
	return htmltemplate.HTML(strings.ReplaceAll(htmltemplate.HTMLEscapeString(text), "\n", "<br>\n"))
}
