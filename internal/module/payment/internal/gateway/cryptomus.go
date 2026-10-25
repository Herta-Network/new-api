// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/cryptomus.go
//   - internal/module/billing/internal/payment/cryptomus/cryptomus.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the protocol client and the gateway are one type; the
// invoice lifetime is the remaining payment window of the order (clamped to
// what Cryptomus accepts) instead of a fixed window; the return URL is also
// sent as url_success; the notice currency is upper-cased; the unused
// not-found classification is dropped; JSON goes through the common
// wrappers.

package gateway

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// cryptomusAPI is the production Cryptomus API. Only tests point a registry
// elsewhere (see WithCryptomusBaseURL).
const cryptomusAPI = "https://api.cryptomus.com"

// Invoice lifetimes Cryptomus accepts, in seconds.
const (
	cryptomusMinLifetime = 300
	cryptomusMaxLifetime = 43200
)

// Payment statuses documented by Cryptomus. paid_over means the payer sent
// more than the invoice amount; the invoice itself is still settled in full.
// wrong_amount is a final state in which the payer sent less than the
// invoice amount, and locked is a final state in which received funds were
// frozen by the AML program: both hold money without covering the order.
const (
	cryptomusPaid               = "paid"
	cryptomusPaidOver           = "paid_over"
	cryptomusWrongAmount        = "wrong_amount"
	cryptomusWrongAmountWaiting = "wrong_amount_waiting"
	cryptomusProcess            = "process"
	cryptomusConfirmCheck       = "confirm_check"
	cryptomusCheck              = "check"
	cryptomusFail               = "fail"
	cryptomusCancel             = "cancel"
	cryptomusSystemFail         = "system_fail"
	cryptomusRefundProcess      = "refund_process"
	cryptomusRefundFail         = "refund_fail"
	cryptomusRefundPaid         = "refund_paid"
	cryptomusLocked             = "locked"
)

func cryptomusPaidStatus(status string) bool {
	return status == cryptomusPaid || status == cryptomusPaidOver
}

// CryptomusConfig is the stored configuration of a Cryptomus payment method.
type CryptomusConfig struct {
	MerchantID string `json:"merchant_id"`
	APIKey     string `json:"api_key"`
}

type cryptomusGateway struct {
	config     CryptomusConfig
	baseURL    string
	httpClient *http.Client
}

func openCryptomus(r *Registry, method Method) (Gateway, error) {
	var config CryptomusConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, fmt.Errorf("decode Cryptomus configuration: %w", err)
	}
	if config.MerchantID == "" || config.APIKey == "" {
		return nil, errors.New("incomplete Cryptomus configuration")
	}
	return &cryptomusGateway{config: config, baseURL: r.cryptomusBaseURL, httpClient: r.httpClient}, nil
}

// normalizeCryptomus trims the credentials and requires both.
func normalizeCryptomus(raw []byte) (string, error) {
	var config CryptomusConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("decode Cryptomus configuration: %w", err)
	}
	config.MerchantID = strings.TrimSpace(config.MerchantID)
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.MerchantID == "" || config.APIKey == "" {
		return "", errors.New("the Cryptomus merchant ID and API key are required")
	}
	content, err := common.Marshal(config)
	return string(content), err
}

func (g *cryptomusGateway) Platform() string { return PlatformCryptomus }

func (g *cryptomusGateway) ChargeCurrency() string { return "" }

// NeedsNotifyURL is true only for a new invoice; a claimed invoice already
// carries its callback.
func (g *cryptomusGateway) NeedsNotifyURL(o *Order) bool { return o.TradeNo == "" }

func (g *cryptomusGateway) ServerCheckout() bool { return true }

// cryptomusInvoice is the gateway's view of a payment. The info endpoint
// reports the settlement state in payment_status while the create endpoint
// uses status; state accepts either.
type cryptomusInvoice struct {
	UUID          string `json:"uuid"`
	OrderNo       string `json:"order_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	PaymentAmount string `json:"payment_amount"`
	URL           string `json:"url"`
	Status        string `json:"status"`
	PaymentStatus string `json:"payment_status"`
	IsFinal       bool   `json:"is_final"`
}

// state returns the invoice's settlement state; status wins when both
// fields are present.
func (i *cryptomusInvoice) state() string {
	if i.Status != "" {
		return i.Status
	}
	return i.PaymentStatus
}

// StartPayment returns the hosted checkout of the order's only invoice. The
// claimed invoice keeps every checkout retry on the same URL, so a callback
// can never reference an invoice the order does not know about.
func (g *cryptomusGateway) StartPayment(ctx context.Context, c Checkout) (*Action, error) {
	if c.Order.TradeNo != "" {
		invoice, err := g.getInvoice(ctx, c.Order.TradeNo, "")
		if err != nil {
			return nil, err
		}
		return g.checkoutAction(invoice, c.Order)
	}
	if c.Order.OrderNo == "" || c.Charge.Amount <= 0 || c.Charge.Currency == "" {
		return nil, errors.New("invalid Cryptomus charge")
	}
	// The invoice must not outlive the order's payment window by more than
	// Cryptomus' minimum lifetime: a payment made after the order closed
	// could no longer settle it.
	lifetime := int64(time.Until(c.Order.ExpiresAt).Seconds())
	lifetime = min(max(lifetime, cryptomusMinLifetime), cryptomusMaxLifetime)
	request := map[string]any{
		"amount":   FormatAmount(c.Charge.Amount),
		"currency": strings.ToUpper(c.Charge.Currency),
		"order_id": c.Order.OrderNo,
		"lifetime": lifetime,
	}
	if c.NotifyURL != "" {
		request["url_callback"] = c.NotifyURL
	}
	if c.ReturnURL != "" {
		request["url_return"] = c.ReturnURL
		request["url_success"] = c.ReturnURL
	}
	var invoice cryptomusInvoice
	if err := g.post(ctx, "/v1/payment", request, &invoice); err != nil {
		// The gateway enforces one invoice per order number. A previous
		// checkout may have created it and crashed before claiming it, so
		// recover that invoice instead of failing.
		existing, queryErr := g.getInvoice(ctx, "", c.Order.OrderNo)
		if queryErr != nil {
			return nil, fmt.Errorf("create Cryptomus invoice: %w", err)
		}
		invoice = *existing
	}
	if invoice.UUID == "" {
		return nil, errors.New("the Cryptomus invoice has no uuid")
	}
	winner, err := c.Claim(ctx, invoice.UUID)
	if err != nil {
		return nil, err
	}
	if winner != invoice.UUID {
		// A concurrent checkout claimed the order's invoice first; ours is
		// never referenced again and simply expires at the gateway.
		existing, err := g.getInvoice(ctx, winner, "")
		if err != nil {
			return nil, err
		}
		invoice = *existing
	}
	return g.checkoutAction(&invoice, c.Order)
}

// checkoutAction guards a reused invoice against drift: the checkout URL
// handed to the payer must belong to this order and charge exactly the
// recorded payment expectation.
func (g *cryptomusGateway) checkoutAction(invoice *cryptomusInvoice, o *Order) (*Action, error) {
	if invoice.OrderNo != o.OrderNo {
		return nil, errors.New("the Cryptomus invoice belongs to another order")
	}
	amount, err := parseCryptomusMoney(invoice.Amount)
	if err != nil || amount != o.PaymentAmount || !strings.EqualFold(invoice.Currency, o.PaymentCurrency) {
		return nil, errors.New("the Cryptomus invoice does not match the payment expectation")
	}
	if invoice.URL == "" {
		return nil, errors.New("the Cryptomus invoice has no checkout URL")
	}
	return &Action{Type: ActionURL, URL: invoice.URL}, nil
}

// Reconcile confirms that no money was collected before the order closes.
// Invoices cannot be cancelled through the API, but they expire on their own
// after their lifetime and report a final state. A paid invoice is settled
// instead; active, underpaid, AML-frozen and refund invoices stay pending
// for reconciliation or manual resolution. Nobody's close request can
// invalidate the invoice or forfeit received funds.
func (g *cryptomusGateway) Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error) {
	o := req.Order
	// Checkout records its payment expectation before creating an invoice,
	// so every close verdict requires that checkout to be unchanged when the
	// close commits: a "never started" snapshot must not close an invoice
	// that is being created.
	if o.PaymentCurrency == "" && o.TradeNo == "" {
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	invoice, err := g.matchingInvoice(ctx, o)
	if err != nil {
		// Even an explicit payment-not-found answer cannot close a started
		// checkout: invoice creation may still be in flight after a timeout,
		// before its UUID was stored locally.
		return Reconciliation{}, fmt.Errorf("cannot safely expire Cryptomus order %s: %w: %w", o.OrderNo, err, ErrUnconfirmed)
	}
	if cryptomusPaidStatus(invoice.state()) {
		return Reconciliation{TradeNo: invoice.UUID}, nil
	}
	// is_final alone does not mean unpaid (e.g. refund_fail). Only an
	// explicitly cancelled invoice with zero received funds is safe to close.
	paidAmount, amountErr := parseCryptomusMoney(invoice.PaymentAmount)
	if invoice.IsFinal && invoice.state() == cryptomusCancel && amountErr == nil && paidAmount == 0 {
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	return Reconciliation{}, fmt.Errorf("cannot safely expire Cryptomus order %s with invoice status %q: %w", o.OrderNo, invoice.state(), ErrUnconfirmed)
}

// QueryPayment reports the invoice of a paid order; the lookup changes
// nothing at the gateway.
func (g *cryptomusGateway) QueryPayment(ctx context.Context, o *Order) (string, error) {
	if o.PaymentCurrency == "" && o.TradeNo == "" {
		return "", nil
	}
	invoice, err := g.matchingInvoice(ctx, o)
	if err != nil {
		return "", err
	}
	if cryptomusPaidStatus(invoice.state()) {
		return invoice.UUID, nil
	}
	return "", nil
}

// matchingInvoice loads the order's invoice and checks its identity and
// immutable amount before any of its state is trusted. The trade number is
// claimed right after invoice creation, but a checkout may have crashed
// between the two steps; the order-number lookup still finds the invoice
// the gateway holds for the order.
func (g *cryptomusGateway) matchingInvoice(ctx context.Context, o *Order) (*cryptomusInvoice, error) {
	invoice, err := g.getInvoice(ctx, o.TradeNo, o.OrderNo)
	if err != nil {
		return nil, err
	}
	amount, err := parseCryptomusMoney(invoice.Amount)
	if err != nil || invoice.OrderNo != o.OrderNo || (o.TradeNo != "" && invoice.UUID != o.TradeNo) ||
		amount != o.PaymentAmount || !strings.EqualFold(invoice.Currency, o.PaymentCurrency) {
		return nil, fmt.Errorf("cryptomus order %s query does not match the payment expectation", o.OrderNo)
	}
	return invoice, nil
}

// ParseCallback verifies the webhook signature and the payload: an invoice
// payment of a known status for a well-formed order.
func (g *cryptomusGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	var notification struct {
		Type          string `json:"type"`
		UUID          string `json:"uuid"`
		OrderNo       string `json:"order_id"`
		Amount        string `json:"amount"`
		PaymentAmount string `json:"payment_amount"`
		Currency      string `json:"currency"`
		PayerCurrency string `json:"payer_currency"`
		IsFinal       bool   `json:"is_final"`
		Status        string `json:"status"`
		Sign          string `json:"sign"`
	}
	if err := common.Unmarshal(n.Body, &notification); err != nil {
		return nil, fmt.Errorf("decode Cryptomus notification: %w", err)
	}
	if !g.verifyNotificationSign(n.Body, notification.Sign) {
		return nil, errors.New("verify sign failed")
	}
	// The gateway also emits wallet webhooks of the same shape; only invoice
	// payments may settle orders.
	if notification.Type != "" && notification.Type != "payment" {
		return nil, errors.New("unsupported notification type")
	}
	if err := validateOrderNo(notification.OrderNo); err != nil {
		return nil, err
	}
	if err := ValidateTradeNo(notification.UUID); err != nil {
		return nil, err
	}
	switch notification.Status {
	case cryptomusPaid, cryptomusPaidOver, cryptomusWrongAmount, cryptomusWrongAmountWaiting,
		cryptomusProcess, cryptomusConfirmCheck, cryptomusCheck, cryptomusFail, cryptomusCancel,
		cryptomusSystemFail, cryptomusRefundProcess, cryptomusRefundFail, cryptomusRefundPaid, cryptomusLocked:
	default:
		return nil, errors.New("unknown payment status")
	}
	amount, err := parseCryptomusMoney(notification.Amount)
	if err != nil {
		return nil, errors.New("invalid callback amount")
	}
	notice := &Notice{
		OrderNo:  notification.OrderNo,
		TradeNo:  notification.UUID,
		Amount:   amount,
		Currency: strings.ToUpper(notification.Currency),
		Paid:     cryptomusPaidStatus(notification.Status),
		Status:   notification.Status,
		Details: map[string]string{
			"is_final":       fmt.Sprint(notification.IsFinal),
			"payment_amount": notification.PaymentAmount,
			"payer_currency": notification.PayerCurrency,
		},
	}
	switch notification.Status {
	case cryptomusWrongAmount, cryptomusLocked, cryptomusRefundProcess, cryptomusRefundFail, cryptomusRefundPaid:
		notice.ManualReview = true
	}
	return notice, nil
}

// CheckOrder requires the invoice to be the one the order claimed.
func (g *cryptomusGateway) CheckOrder(o *Order, notice *Notice) error {
	if o.TradeNo != "" && o.TradeNo != notice.TradeNo {
		return errors.New("order trade number mismatch")
	}
	return nil
}

// ConfirmPayment re-reads the invoice from the gateway: the signature proves
// the gateway sent the webhook, the invoice proves the payment.
func (g *cryptomusGateway) ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error {
	invoice, err := g.getInvoice(ctx, notice.TradeNo, "")
	if err != nil {
		return err
	}
	if !cryptomusPaidStatus(invoice.state()) {
		return errors.New("gateway invoice is not paid")
	}
	if invoice.UUID != notice.TradeNo || invoice.OrderNo != o.OrderNo {
		return errors.New("gateway invoice identity mismatch")
	}
	amount, err := parseCryptomusMoney(invoice.Amount)
	if err != nil || amount != o.PaymentAmount || !strings.EqualFold(invoice.Currency, o.PaymentCurrency) {
		return errors.New("gateway invoice amount mismatch")
	}
	return nil
}

// getInvoice fetches the invoice by gateway UUID or, when uuid is empty, by
// the merchant order number.
func (g *cryptomusGateway) getInvoice(ctx context.Context, uuid, orderNo string) (*cryptomusInvoice, error) {
	request := map[string]string{"uuid": uuid}
	if uuid == "" {
		if orderNo == "" {
			return nil, errors.New("invoice lookup requires a uuid or order number")
		}
		request = map[string]string{"order_id": orderNo}
	}
	var invoice cryptomusInvoice
	if err := g.post(ctx, "/v1/payment/info", request, &invoice); err != nil {
		return nil, err
	}
	if invoice.UUID == "" {
		return nil, errors.New("the Cryptomus invoice has no uuid")
	}
	return &invoice, nil
}

// post sends a signed API request and decodes its result into target.
func (g *cryptomusGateway) post(ctx context.Context, path string, payload, target any) error {
	body, err := common.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Cryptomus request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(g.baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Cryptomus request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("merchant", g.config.MerchantID)
	req.Header.Set("sign", g.signPayload(body))
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return errors.New("cryptomus request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	value, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return fmt.Errorf("read Cryptomus response: %w", err)
	}
	if len(value) > maxQueryResponseSize {
		return errors.New("cryptomus response is too large")
	}
	var response struct {
		State   int             `json:"state"`
		Result  json.RawMessage `json:"result"`
		Message string          `json:"message"`
		Errors  json.RawMessage `json:"errors"`
	}
	if err := common.Unmarshal(value, &response); err != nil {
		return fmt.Errorf("cryptomus API answered HTTP %d with a non-JSON response", resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || response.State != 0 {
		message := response.Message
		if message == "" && len(response.Errors) > 0 {
			message = string(response.Errors)
		}
		return fmt.Errorf("cryptomus API error: http=%d state=%d message=%s", resp.StatusCode, response.State, message)
	}
	if len(response.Result) == 0 {
		return errors.New("cryptomus response has no result")
	}
	if err := common.Unmarshal(response.Result, target); err != nil {
		return fmt.Errorf("decode Cryptomus result: %w", err)
	}
	return nil
}

// signPayload is the documented request signature: md5(base64(body) +
// api_key) as lowercase hex, over the exact bytes sent.
func (g *cryptomusGateway) signPayload(body []byte) string {
	sum := md5.Sum([]byte(base64.StdEncoding.EncodeToString(body) + g.config.APIKey))
	return hex.EncodeToString(sum[:])
}

// cryptomusSignMember matches the top-level sign member Cryptomus appends to
// webhook payloads. The value is a 32-character hex digest, and quotes
// inside JSON strings are escaped, so the pattern cannot match inside another
// field's value.
var cryptomusSignMember = regexp.MustCompile(`,\s*"sign"\s*:\s*"[0-9a-fA-F]{32}"`)

// verifyNotificationSign authenticates a webhook payload. Cryptomus signs
// the JSON body without its sign member: md5(base64(json) + api_key). The
// body is encoded by PHP, which escapes "/" as "\/", so the remaining bytes
// must stay untouched: the sign member is cut out of the raw payload instead
// of re-encoding it.
func (g *cryptomusGateway) verifyNotificationSign(body []byte, sign string) bool {
	if g.config.APIKey == "" || len(sign) != 32 {
		return false
	}
	if len(cryptomusSignMember.FindAll(body, 2)) != 1 {
		return false
	}
	expected := g.signPayload(cryptomusSignMember.ReplaceAll(body, nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(strings.ToLower(sign))) == 1
}

// parseCryptomusMoney converts a gateway decimal amount into hundredths. The
// gateway may report fiat amounts with up to eight decimal places; only
// trailing zeros beyond two decimals are tolerated, anything else cannot be
// represented in hundredths and is rejected.
func parseCryptomusMoney(value string) (int64, error) {
	if whole, fraction, ok := strings.Cut(value, "."); ok && len(fraction) > 2 {
		trimmed := strings.TrimRight(fraction, "0")
		if len(trimmed) > 2 {
			return 0, errors.New("amount has more than two decimal places")
		}
		if trimmed == "" {
			value = whole
		} else {
			value = whole + "." + trimmed
		}
	}
	return ParseAmount(value)
}
