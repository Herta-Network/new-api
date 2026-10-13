package payment

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
)

type cryptomusGateway struct {
	config  Config
	runtime Runtime
}
type cryptomusInvoice struct {
	UUID          string `json:"uuid"`
	OrderNo       string `json:"order_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
	PaymentStatus string `json:"payment_status"`
	URL           string `json:"url"`
	Type          string `json:"type"`
}

func newCryptomus(config Config, runtime Runtime) (Gateway, error) {
	if config.MerchantID == "" || config.Key == "" {
		return nil, errors.New("invalid Cryptomus configuration")
	}
	if runtime.CryptomusURL == "" {
		runtime.CryptomusURL = "https://api.cryptomus.com"
	}
	return &cryptomusGateway{config: config, runtime: runtime}, nil
}
func (g *cryptomusGateway) AccountKey(context.Context) (string, error) {
	return AccountFingerprint(Cryptomus, g.config.MerchantID), nil
}
func (g *cryptomusGateway) PaymentType() string { return Cryptomus }
func (g *cryptomusGateway) Quote(_ context.Context, c Checkout) (Money, error) {
	if c.Money.Currency != "USD" || c.Money.Amount <= 0 {
		return Money{}, ErrInvalidPayment
	}
	return c.Money, nil
}
func cryptomusSignature(body []byte, key string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(base64.StdEncoding.EncodeToString(body)+key)))
}
func (g *cryptomusGateway) request(ctx context.Context, path string, data any) (*cryptomusInvoice, error) {
	body, err := common.Marshal(data)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.runtime.CryptomusURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("merchant", g.config.MerchantID)
	req.Header.Set("sign", cryptomusSignature(body, g.config.Key))
	client := *g.runtime.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Cryptomus request failed")
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(payload) > 1<<20 {
		return nil, errors.New("invalid Cryptomus response")
	}
	var response struct {
		State  int              `json:"state"`
		Result cryptomusInvoice `json:"result"`
	}
	if res.StatusCode != http.StatusOK || common.Unmarshal(payload, &response) != nil || response.State != 0 || response.Result.UUID == "" {
		return nil, errors.New("Cryptomus API request failed")
	}
	return &response.Result, nil
}
func (g *cryptomusGateway) StartPayment(ctx context.Context, c Checkout) (*CheckoutResult, error) {
	invoice, err := g.request(ctx, "/v1/payment", map[string]any{"amount": c.Money.Decimal(), "currency": c.Money.Currency, "order_id": c.OrderNo, "url_callback": c.NotifyURL, "url_return": c.ReturnURL, "url_success": c.ReturnURL, "lifetime": 3600, "accuracy_payment_percent": 0})
	if err != nil {
		invoice, err = g.request(ctx, "/v1/payment/info", map[string]string{"order_id": c.OrderNo})
	}
	if err != nil {
		return nil, err
	}
	money, err := ParseMoney(invoice.Amount, invoice.Currency)
	if err != nil || money != c.Money || invoice.OrderNo != c.OrderNo {
		return nil, ErrInvalidPayment
	}
	uri, err := url.Parse(invoice.URL)
	if err != nil || uri.Scheme != "https" || uri.Host == "" || uri.User != nil {
		return nil, errors.New("invalid Cryptomus checkout URL")
	}
	return &CheckoutResult{URL: invoice.URL, CheckoutID: invoice.UUID}, nil
}
func (g *cryptomusGateway) ParseCallback(_ context.Context, callback Callback) (*Notice, error) {
	var invoice cryptomusInvoice
	if common.Unmarshal(callback.Body, &invoice) != nil {
		return nil, ErrInvalidPayment
	}
	parsed := gjson.ParseBytes(callback.Body)
	if !parsed.IsObject() {
		return nil, ErrInvalidPayment
	}
	var unsigned strings.Builder
	unsigned.WriteByte('{')
	keys := map[string]bool{}
	sign := ""
	valid := true
	count := 0
	parsed.ForEach(func(key, value gjson.Result) bool {
		if keys[key.Str] {
			valid = false
			return false
		}
		keys[key.Str] = true
		if key.Str == "sign" {
			if value.Type != gjson.String {
				valid = false
				return false
			}
			sign = value.Str
			return true
		}
		if count > 0 {
			unsigned.WriteByte(',')
		}
		count++
		name, _ := common.Marshal(key.Str)
		unsigned.Write(name)
		unsigned.WriteByte(':')
		unsigned.Write(pretty.Ugly([]byte(value.Raw)))
		return true
	})
	unsigned.WriteByte('}')
	if !valid || len(sign) != 32 || subtle.ConstantTimeCompare([]byte(strings.ToLower(sign)), []byte(cryptomusSignature([]byte(unsigned.String()), g.config.Key))) != 1 {
		return nil, ErrInvalidPayment
	}
	if invoice.Type != "payment" || invoice.UUID == "" || invoice.OrderNo == "" {
		return nil, ErrInvalidPayment
	}
	return cryptoNotice(invoice)
}
func cryptoNotice(invoice cryptomusInvoice) (*Notice, error) {
	money, err := ParseMoney(invoice.Amount, invoice.Currency)
	if err != nil || money.Amount <= 0 {
		return nil, ErrInvalidPayment
	}
	state := invoice.Status
	if state == "" {
		state = invoice.PaymentStatus
	}
	paid := state == "paid" || state == "paid_over"
	switch state {
	case "paid", "paid_over", "confirm_check", "process", "check", "wrong_amount_waiting":
	case "cancel", "fail", "system_fail":
		state = "failed"
	case "wrong_amount", "locked", "refund_process", "refund_fail", "refund_paid":
		state = "review"
	default:
		return nil, ErrInvalidPayment
	}
	return &Notice{OrderNo: invoice.OrderNo, TradeNo: invoice.UUID, CheckoutID: invoice.UUID, PaymentType: Cryptomus, Money: money, Subtotal: money.Amount, Paid: paid, State: state}, nil
}
func (g *cryptomusGateway) Confirm(ctx context.Context, c Checkout, n Notice) (*Notice, error) {
	if n.OrderNo != c.OrderNo || n.Money != c.Money || (c.CheckoutID != "" && c.CheckoutID != n.CheckoutID) {
		return nil, ErrInvalidPayment
	}
	invoice, err := g.request(ctx, "/v1/payment/info", map[string]string{"uuid": n.TradeNo})
	if err != nil {
		return nil, err
	}
	verified, err := cryptoNotice(*invoice)
	if err != nil || verified.OrderNo != c.OrderNo || verified.TradeNo != n.TradeNo || verified.Money != c.Money {
		return nil, ErrInvalidPayment
	}
	return verified, nil
}
