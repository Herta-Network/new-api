package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Email sending methods. SMTP is built in; the others are EmailTransports,
// Resend registered by this package and Cloudflare by the mail module.
const (
	EmailProviderSMTP       = "smtp"
	EmailProviderResend     = "resend"
	EmailProviderCloudflare = "cloudflare"
)

// EmailProviders are the sending methods an administrator can choose, as
// the primary method (option EmailProvider) or the backup method (option
// EmailBackupProvider).
var EmailProviders = []string{EmailProviderSMTP, EmailProviderResend, EmailProviderCloudflare}

// emailSendTimeout bounds one delivery through an API provider.
const emailSendTimeout = 20 * time.Second

// OutgoingEmail is a composed email on its way to a provider. To holds bare
// addresses, at most 50.
type OutgoingEmail struct {
	To      []string
	Subject string
	HTML    string
}

// EmailTransport is a sending method that is not SMTP.
type EmailTransport struct {
	// Options are the option keys that hold the method's settings.
	Options []string
	// Validate checks the settings, keyed by option, without sending.
	Validate func(settings map[string]string) error
	// Send hands one email to the provider. It returns an
	// *EmailDeliveryError, whose NotSent decides whether the backup method
	// may send the email again.
	Send func(ctx context.Context, settings map[string]string, email OutgoingEmail) error
}

// EmailDeliveryError is a failed delivery. Err is what the requester sees;
// Detail is the provider's reason, written to the server log and shown to
// an administrator who sends a test email, because it may name the account,
// its domains or its quota.
type EmailDeliveryError struct {
	Err    error
	Detail string
	// NotSent reports that the provider certainly did not accept the email:
	// the request never fully reached it, or it refused the request. Only
	// then may the backup method send the email, so a recipient never gets
	// it twice.
	NotSent bool
}

func (e *EmailDeliveryError) Error() string { return e.Err.Error() }

func (e *EmailDeliveryError) Unwrap() error { return e.Err }

// EmailNotSent wraps err, a failure before the email reached any provider.
func EmailNotSent(err error) *EmailDeliveryError {
	return &EmailDeliveryError{Err: err, Detail: err.Error(), NotSent: true}
}

var (
	emailTransportsMutex sync.RWMutex
	emailTransports      = map[string]EmailTransport{}
)

// RegisterEmailTransport makes a sending method available. Modules call it
// from their init functions.
func RegisterEmailTransport(provider string, transport EmailTransport) {
	emailTransportsMutex.Lock()
	defer emailTransportsMutex.Unlock()
	emailTransports[provider] = transport
}

func emailTransport(provider string) (EmailTransport, bool) {
	emailTransportsMutex.RLock()
	defer emailTransportsMutex.RUnlock()
	transport, ok := emailTransports[provider]
	return transport, ok
}

// EmailTransportOptions lists the option keys of every registered sending
// method, so they exist in OptionMap before an administrator sets them.
func EmailTransportOptions() []string {
	emailTransportsMutex.RLock()
	defer emailTransportsMutex.RUnlock()
	var keys []string
	for _, transport := range emailTransports {
		keys = append(keys, transport.Options...)
	}
	return keys
}

// IsEmailOption reports whether a change to key is checked by
// ValidateEmailOption.
func IsEmailOption(key string) bool {
	return key == "EmailProvider" || key == "EmailBackupProvider" || slices.Contains(EmailTransportOptions(), key)
}

// ValidateEmailOption checks a change of an email delivery option against
// the other saved options. The primary method must be a known method; the
// backup method is none or a known method other than the primary. While a
// method is primary or backup, its settings must stay complete; a method
// out of use keeps an incomplete draft until it is chosen. SMTP settings are
// not checked, as installations always kept them as they were. Saving
// settings never sends an email.
func ValidateEmailOption(key, value string) error {
	settings := map[string]string{}
	OptionMapRWMutex.RLock()
	settings["EmailProvider"] = OptionMap["EmailProvider"]
	settings["EmailBackupProvider"] = OptionMap["EmailBackupProvider"]
	for _, option := range EmailTransportOptions() {
		settings[option] = OptionMap[option]
	}
	OptionMapRWMutex.RUnlock()
	settings[key] = value

	switch key {
	case "EmailProvider":
		if !slices.Contains(EmailProviders, value) {
			return NewMessage("Email sending method must be SMTP, Resend or Cloudflare")
		}
		return validateEmailMethod(value, settings)
	case "EmailBackupProvider":
		if value == "" {
			return nil
		}
		if !slices.Contains(EmailProviders, value) {
			return NewMessage("Backup email sending method must be SMTP, Resend or Cloudflare")
		}
		// The primary may take the backup's place, so two methods swap with
		// the primary written first; until the backup changes too, the
		// methods are equal and there is no backup.
		if value == settings["EmailProvider"] {
			return NewMessage("Backup email sending method must differ from the primary method")
		}
		return validateEmailMethod(value, settings)
	}
	for _, provider := range []string{settings["EmailProvider"], settings["EmailBackupProvider"]} {
		if transport, ok := emailTransport(provider); ok && slices.Contains(transport.Options, key) {
			return transport.Validate(settings)
		}
	}
	return nil
}

// validateEmailMethod checks that provider can send with settings.
func validateEmailMethod(provider string, settings map[string]string) error {
	if provider == EmailProviderSMTP {
		return nil
	}
	transport, ok := emailTransport(provider)
	if !ok {
		return NewMessage("Email sending method is not available")
	}
	return transport.Validate(settings)
}

// parseEmailRecipients reads a receiver list separated by ";".
func parseEmailRecipients(receiver string) ([]string, error) {
	var to []string
	for address := range strings.SplitSeq(receiver, ";") {
		recipient, err := mail.ParseAddress(strings.TrimSpace(address))
		if err != nil || len(to) >= 50 {
			return nil, NewMessage("Invalid email address")
		}
		to = append(to, recipient.Address)
	}
	return to, nil
}

// SendEmail sends an HTML email through the primary sending method. When
// that method certainly did not send it, the backup method, if any, sends
// it instead; after a failure that leaves the email possibly sent, such as
// a timeout after the request was written, it is not sent again.
func SendEmail(subject string, receiver string, content string) error {
	OptionMapRWMutex.RLock()
	primary := OptionMap["EmailProvider"]
	backup := OptionMap["EmailBackupProvider"]
	OptionMapRWMutex.RUnlock()
	if primary == "" { // Installations without a selector send through SMTP.
		primary = EmailProviderSMTP
	}
	err := SendEmailThrough(primary, subject, receiver, content)
	var failure *EmailDeliveryError
	if err == nil || backup == "" || backup == primary || !errors.As(err, &failure) || !failure.NotSent {
		return err
	}
	SysLog(LogText("email sending method %s did not send the email, sending it through backup method %s", primary, backup))
	return SendEmailThrough(backup, subject, receiver, content)
}

// SendEmailThrough sends an HTML email through one sending method, without
// a backup. A failure is an *EmailDeliveryError; its Detail is logged here.
func SendEmailThrough(provider string, subject string, receiver string, content string) error {
	err := sendEmailThrough(provider, subject, receiver, content)
	var failure *EmailDeliveryError
	if errors.As(err, &failure) {
		SysError(LogText("email delivery through %s to %s failed: not_sent=%t %s", provider, receiver, failure.NotSent, failure.Detail))
	}
	return err
}

func sendEmailThrough(provider string, subject string, receiver string, content string) error {
	if provider == EmailProviderSMTP {
		return sendSMTPEmail(subject, receiver, content)
	}
	transport, ok := emailTransport(provider)
	if !ok {
		return EmailNotSent(NewMessage("Email sending method is not available"))
	}
	settings := map[string]string{}
	OptionMapRWMutex.RLock()
	for _, option := range transport.Options {
		settings[option] = OptionMap[option]
	}
	OptionMapRWMutex.RUnlock()
	if err := transport.Validate(settings); err != nil {
		return EmailNotSent(err)
	}
	to, err := parseEmailRecipients(receiver)
	if err != nil {
		return EmailNotSent(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
	defer cancel()
	err = transport.Send(ctx, settings, OutgoingEmail{To: to, Subject: subject, HTML: content})
	var failure *EmailDeliveryError
	if err != nil && !errors.As(err, &failure) {
		// A transport that does not say otherwise may have sent the email.
		return &EmailDeliveryError{Err: NewMessage("Email delivery failed"), Detail: err.Error()}
	}
	return err
}

// TraceEmailRequest returns req with a trace that records whether the whole
// request was written to the provider. A request that failed before it was
// fully written cannot have been processed, so its email was not sent.
func TraceEmailRequest(req *http.Request) (*http.Request, func() bool) {
	var written atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				written.Store(true)
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), written.Load
}

// Email kinds: the emails the kernel sends from templates, rendered by the
// renderer the mail module registers.
const (
	EmailKindVerification   = "verification"    // Code, Minutes
	EmailKindPasswordReset  = "password_reset"  // Link, Minutes
	EmailKindBindingNew     = "binding_new"     // Code, Minutes: to the address being linked
	EmailKindBindingCurrent = "binding_current" // Code, Minutes: to the address being replaced
	EmailKindSecurityNotice = "security_notice" // Event
	EmailKindQuotaLow       = "quota_low"       // Title, Quota, Link
	EmailKindNotice         = "notice"          // Title, Content: a plain-text notice
)

// TemplateEmail is an email of Kind to render from Vars, whose values are
// plain text the renderer escapes.
type TemplateEmail struct {
	Kind string
	Vars map[string]string
}

// EmailRenderer renders an email in lang, a backend language ("" for
// DEFAULT_LANGUAGE), into its subject and HTML body.
type EmailRenderer func(lang string, email TemplateEmail) (subject string, html string, err error)

var emailRenderer atomic.Pointer[EmailRenderer]

// RegisterEmailRenderer sets the renderer of template emails. The mail
// module registers it from its init function; kernel tests register a stub.
func RegisterEmailRenderer(renderer EmailRenderer) {
	emailRenderer.Store(&renderer)
}

// SendTemplateEmail renders email in lang and sends it like SendEmail.
func SendTemplateEmail(lang string, receiver string, email TemplateEmail) error {
	renderer := emailRenderer.Load()
	if renderer == nil {
		return EmailNotSent(NewMessage("Email templates are not available"))
	}
	subject, html, err := (*renderer)(lang, email)
	if err != nil {
		return EmailNotSent(err)
	}
	return SendEmail(subject, receiver, html)
}
