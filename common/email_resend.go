package common

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

var resendEmailClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func init() {
	RegisterEmailTransport(EmailProviderResend, EmailTransport{
		Options: []string{"ResendApiKey", "ResendFrom"},
		Validate: func(settings map[string]string) error {
			return ValidateResendConfiguration(settings["ResendApiKey"], settings["ResendFrom"])
		},
		Send: func(ctx context.Context, settings map[string]string, email OutgoingEmail) error {
			return sendResendEmail(ctx, settings["ResendApiKey"], settings["ResendFrom"], email)
		},
	})
}

// ValidateResendConfiguration is shared by provider activation and delivery.
// It checks local configuration only; saving settings never sends an email.
func ValidateResendConfiguration(apiKey, from string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n\x00") {
		return NewMessage("Resend API key is not configured or invalid")
	}
	if strings.ContainsAny(from, "\r\n\x00") {
		return NewMessage("Resend sender address is not configured or invalid")
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(from)); err != nil {
		return NewMessage("Resend sender address is not configured or invalid")
	}
	return nil
}

// API contract: https://resend.com/docs/api-reference/emails/send-email
// OWASP Authentication / Session Management / Forgot Password cheat sheets:
// do not expose credentials, verification codes or reset links in errors/logs.
// This transport does not change token expiry, single-use or session rules.
// Resend refuses a request it answers with 4xx (invalid key, unverified
// domain, rate or quota limit), so the email was not sent; after a 5xx or
// an unreadable success the email may have been sent.
func sendResendEmail(ctx context.Context, apiKey, from string, email OutgoingEmail) error {
	apiKey = strings.TrimSpace(apiKey)
	sender, _ := mail.ParseAddress(strings.TrimSpace(from))
	if sender.Name == "" {
		sender.Name = SystemName
	}
	payload, err := Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
	}{sender.String(), email.To, email.Subject, email.HTML})
	if err != nil {
		return EmailNotSent(NewMessage("Resend request failed"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return EmailNotSent(NewMessage("Resend request failed"))
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	// Resend rejects API requests without a User-Agent.
	req.Header.Set("User-Agent", "new-api")
	req, written := TraceEmailRequest(req)
	resp, err := resendEmailClient.Do(req)
	if err != nil {
		return &EmailDeliveryError{Err: NewMessage("Resend request failed"), Detail: fmt.Sprintf("error=%q", err), NotSent: !written()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The reason (unverified domain, invalid key, quota) is the Detail,
		// because the error may reach whoever asked for the email.
		var failure struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		_ = DecodeJson(io.LimitReader(resp.Body, 4096), &failure)
		return &EmailDeliveryError{
			Err:     NewMessage("Resend email delivery failed (HTTP {{status}})", map[string]any{"status": resp.StatusCode}),
			Detail:  fmt.Sprintf("status=%d name=%q message=%q", resp.StatusCode, failure.Name, failure.Message),
			NotSent: resp.StatusCode < 500,
		}
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := DecodeJson(io.LimitReader(resp.Body, 4096), &result); err != nil || strings.TrimSpace(result.ID) == "" {
		return &EmailDeliveryError{Err: NewMessage("Invalid Resend delivery response"), Detail: fmt.Sprintf("status=%d", resp.StatusCode)}
	}
	return nil
}
