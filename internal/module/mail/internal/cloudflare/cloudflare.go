// Package cloudflare sends email through the Email Sending REST API of
// Cloudflare Email Service:
// https://developers.cloudflare.com/email-service/api/send-emails/rest-api/
//
// The sender's domain must be onboarded to Email Sending on the account,
// and the API token needs the "Email Sending: Edit" permission.
package cloudflare

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// APIBase is the Cloudflare API.
const APIBase = "https://api.cloudflare.com/client/v4"

// errorAuthUpstream is the error code of a request Cloudflare refused
// because its authentication service was unavailable.
const errorAuthUpstream = 10100

// address is a sender with a display name.
type address struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

type sendRequest struct {
	From    any      `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

type sendResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result *struct {
		Delivered            []string `json:"delivered"`
		Queued               []string `json:"queued"`
		PermanentBounces     []string `json:"permanent_bounces"`
		SuppressedRecipients []string `json:"suppressed_recipients"`
	} `json:"result"`
}

// Send asks Cloudflare to deliver email from sender. A failure is an
// *common.EmailDeliveryError: Cloudflare sent nothing when the request
// never fully reached it, when it answered 4xx, when its authentication
// service was unavailable, or when every recipient bounced or is
// suppressed; after another 5xx or an unreadable success the email may
// have been sent.
func Send(ctx context.Context, client *http.Client, base, accountID, token string, sender *mail.Address, email common.OutgoingEmail) error {
	request := sendRequest{From: sender.Address, To: email.To, Subject: email.Subject, HTML: email.HTML}
	if sender.Name != "" {
		request.From = address{Address: sender.Address, Name: sender.Name}
	}
	payload, err := common.Marshal(request)
	if err != nil {
		return common.EmailNotSent(common.NewMessage("Cloudflare request failed"))
	}
	endpoint := base + "/accounts/" + url.PathEscape(accountID) + "/email/sending/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return common.EmailNotSent(common.NewMessage("Cloudflare request failed"))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "new-api")
	req, written := common.TraceEmailRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return &common.EmailDeliveryError{Err: common.NewMessage("Cloudflare request failed"), Detail: fmt.Sprintf("error=%q", err), NotSent: !written()}
	}
	defer resp.Body.Close()

	var result sendResponse
	decodeErr := common.DecodeJson(io.LimitReader(resp.Body, 64<<10), &result)
	var reasons []string
	refusedAuth := false
	for _, failure := range result.Errors {
		reasons = append(reasons, fmt.Sprintf("%d %s", failure.Code, failure.Message))
		refusedAuth = refusedAuth || failure.Code == errorAuthUpstream
	}
	detail := fmt.Sprintf("status=%d errors=%q", resp.StatusCode, reasons)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &common.EmailDeliveryError{
			Err:     common.NewMessage("Cloudflare email delivery failed (HTTP {{status}})", map[string]any{"status": resp.StatusCode}),
			Detail:  detail,
			NotSent: resp.StatusCode < 500 || refusedAuth,
		}
	}
	if decodeErr != nil || !result.Success || result.Result == nil {
		return &common.EmailDeliveryError{Err: common.NewMessage("Invalid Cloudflare delivery response"), Detail: detail}
	}
	accepted := len(result.Result.Delivered) + len(result.Result.Queued)
	if len(result.Result.PermanentBounces)+len(result.Result.SuppressedRecipients) > 0 {
		return &common.EmailDeliveryError{
			Err:     common.NewMessage("Cloudflare could not deliver the email to its recipient"),
			Detail:  fmt.Sprintf("status=%d permanent_bounces=%q suppressed_recipients=%q", resp.StatusCode, result.Result.PermanentBounces, result.Result.SuppressedRecipients),
			NotSent: accepted == 0,
		}
	}
	if accepted == 0 {
		return &common.EmailDeliveryError{Err: common.NewMessage("Invalid Cloudflare delivery response"), Detail: detail}
	}
	return nil
}

// ValidateSettings checks the Cloudflare settings without sending: an
// account ID of 32 hexadecimal digits, a token, and a sender address,
// either bare or "Name <address>".
func ValidateSettings(accountID, token, from string) error {
	if len(accountID) != 32 || strings.Trim(strings.ToLower(accountID), "0123456789abcdef") != "" {
		return common.NewMessage("Cloudflare account ID is not configured or invalid")
	}
	if token == "" || strings.ContainsFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return common.NewMessage("Cloudflare API token is not configured or invalid")
	}
	if _, err := ParseSender(from); err != nil {
		return err
	}
	return nil
}

// ParseSender reads the sender setting. Line breaks are refused, also in a
// name decoded from RFC 2047 words.
func ParseSender(from string) (*mail.Address, error) {
	if strings.ContainsAny(from, "\r\n\x00") {
		return nil, common.NewMessage("Cloudflare sender address is not configured or invalid")
	}
	sender, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil || strings.ContainsAny(sender.Name, "\r\n\x00") {
		return nil, common.NewMessage("Cloudflare sender address is not configured or invalid")
	}
	return sender, nil
}
