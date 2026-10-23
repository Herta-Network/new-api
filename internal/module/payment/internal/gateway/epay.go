// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/epay.go
//   - internal/module/billing/internal/payment/epay/epay.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the protocol client and the gateway are one type; an
// empty configured payment type is no longer compared with the type the
// gateway reports (Reconcile and the callback re-query rejected every
// payment of a method that lets the buyer choose the type); the configuration
// is validated when it is saved (http(s) URL without query or fragment, pid
// and key required); empty parameters are left out of the payment URL;
// unpaid orders close after EPayUnpaidCloseAge; JSON goes through the
// common wrappers.

package gateway

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// epayCurrency is the only currency EPay-compatible gateways collect.
const epayCurrency = "CNY"

// EPayUnpaidCloseAge is the age from which a pending EPay order that the
// gateway lists as awaiting payment may close. EPay has no cancellation API,
// so the order stays open well past its payment window for a payer who
// opened the gateway page near expiry.
const EPayUnpaidCloseAge = 30 * time.Minute

// maxQueryResponseSize caps the gateway answers read.
const maxQueryResponseSize = 1 << 20

// errEPayQueryNotSupported is returned when the gateway implements neither
// the standard EPay order query nor the EasyPay-compatible fallback: the
// query endpoint answers 404 or with something that is not JSON at all.
var errEPayQueryNotSupported = errors.New("gateway does not support the order query API")

// EPayConfig is the stored configuration of an EPay payment method.
type EPayConfig struct {
	URL string `json:"url"`
	PID string `json:"pid"`
	Key string `json:"key"`
	// Type is the EPay payment type (alipay, wxpay, ...); empty lets the
	// buyer choose on the gateway's page.
	Type string `json:"type"`
}

type epayGateway struct {
	config     EPayConfig
	httpClient *http.Client
}

func openEPay(r *Registry, method Method) (Gateway, error) {
	var config EPayConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, fmt.Errorf("decode EPay configuration: %w", err)
	}
	if config.PID == "" || config.URL == "" || config.Key == "" {
		return nil, errors.New("incomplete EPay configuration")
	}
	return &epayGateway{config: config, httpClient: r.httpClient}, nil
}

func normalizeEPay(raw []byte) (string, error) {
	var config EPayConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("decode EPay configuration: %w", err)
	}
	config.URL = strings.TrimSpace(config.URL)
	config.PID = strings.TrimSpace(config.PID)
	config.Key = strings.TrimSpace(config.Key)
	config.Type = strings.TrimSpace(config.Type)
	endpoint, err := url.Parse(config.URL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return "", errors.New("EPay URL must be an http(s) URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return "", errors.New("EPay URL must not carry user information, a query or a fragment")
	}
	if config.PID == "" || config.Key == "" {
		return "", errors.New("EPay merchant ID and key are required")
	}
	content, err := common.Marshal(config)
	return string(content), err
}

func (g *epayGateway) Platform() string { return PlatformEPay }

func (g *epayGateway) ChargeCurrency() string { return epayCurrency }

func (g *epayGateway) NeedsNotifyURL(*Order) bool { return true }

// StartPayment builds the signed redirect to the gateway's submit.php page.
func (g *epayGateway) StartPayment(_ context.Context, c Checkout) (*Action, error) {
	if !strings.EqualFold(c.Charge.Currency, epayCurrency) || c.Charge.Amount <= 0 {
		return nil, fmt.Errorf("EPay cannot collect %s %s", FormatAmount(c.Charge.Amount), c.Charge.Currency)
	}
	endpoint, err := g.endpoint("submit.php")
	if err != nil {
		return nil, err
	}
	name := c.Subject
	if name == "" {
		name = c.Order.OrderNo
	}
	params := map[string]string{
		"money":        FormatAmount(c.Charge.Amount),
		"name":         name,
		"notify_url":   c.NotifyURL,
		"out_trade_no": c.Order.OrderNo,
		"pid":          g.config.PID,
		"type":         g.config.Type,
		"return_url":   c.ReturnURL,
	}
	query := url.Values{}
	for key, value := range params {
		if value != "" {
			query.Set(key, value)
		}
	}
	query.Set("sign", g.sign(params))
	query.Set("sign_type", "MD5")
	endpoint.RawQuery = query.Encode()
	return &Action{Type: ActionURL, URL: endpoint.String()}, nil
}

// Reconcile queries the order at the gateway. EPay-compatible gateways have
// no cancellation API. A paid order is settled; an order the gateway
// explicitly lists as awaiting payment closes once it is EPayUnpaidCloseAge
// old, so a payer who opened the gateway page near expiry still finishes.
// Any other answer (a failed, unsupported or unavailable query, or a status
// that is neither paid nor awaiting payment) leaves the payment state
// unknown, so the order stays pending for retry or manual resolution instead
// of losing funds. An explicit close is the exception: absent any evidence of
// payment it proceeds.
func (g *epayGateway) Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error) {
	o := req.Order
	if o.PaymentCurrency == "" {
		// Checkout never started; safe to close if it still has not when
		// the close commits.
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	result, err := g.queryOrder(ctx, o.OrderNo)
	if err != nil {
		if req.Explicit {
			return Reconciliation{Note: "explicit close of an EPay order the gateway could not confirm: " + err.Error()}, nil
		}
		return Reconciliation{}, fmt.Errorf("cannot safely expire EPay order %s: %w: %w", o.OrderNo, err, ErrUnconfirmed)
	}
	if !result.paid {
		if req.Explicit {
			return Reconciliation{}, nil
		}
		if !result.unpaid {
			return Reconciliation{}, fmt.Errorf("cannot safely expire EPay order %s: gateway reports it neither paid nor awaiting payment: %w", o.OrderNo, ErrUnconfirmed)
		}
		if time.Since(o.CreatedAt) < EPayUnpaidCloseAge {
			return Reconciliation{}, fmt.Errorf("unpaid EPay order %s stays pending until it is %s old: %w", o.OrderNo, EPayUnpaidCloseAge, ErrUnconfirmed)
		}
		return Reconciliation{}, nil
	}
	tradeNo, err := g.paidTrade(o, result)
	if err != nil {
		return Reconciliation{}, err
	}
	return Reconciliation{TradeNo: tradeNo}, nil
}

// QueryPayment reports the trade of a paid order; the query changes nothing
// at the gateway.
func (g *epayGateway) QueryPayment(ctx context.Context, o *Order) (string, error) {
	if o.PaymentCurrency == "" {
		return "", nil
	}
	result, err := g.queryOrder(ctx, o.OrderNo)
	if err != nil {
		return "", err
	}
	if !result.paid {
		return "", nil
	}
	return g.paidTrade(o, result)
}

// paidTrade returns the trade of a query answer that reports the order paid:
// it must name the order, this merchant and payment type, and exactly the
// payment expectation. A status-only answer names no trade to settle with.
func (g *epayGateway) paidTrade(o *Order, result *epayQueryResult) (string, error) {
	if result.statusOnly {
		return "", fmt.Errorf("paid EPay order %s cannot settle: the gateway query has no transaction details", o.OrderNo)
	}
	amount, err := ParseAmount(result.money)
	if err != nil || result.orderNo != o.OrderNo || result.merchantID != g.config.PID ||
		(g.config.Type != "" && result.paymentType != g.config.Type) ||
		amount != o.PaymentAmount || ValidateTradeNo(result.tradeNo) != nil {
		return "", fmt.Errorf("EPay order %s query does not match the payment expectation", o.OrderNo)
	}
	return result.tradeNo, nil
}

// ParseCallback verifies the MD5 signature and the callback fields: the
// merchant, the payment type when the method fixes one, a successful trade
// status and the amount.
func (g *epayGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	params := n.Params
	if params == nil {
		params = map[string]string{}
	}
	if !g.verifySign(params) {
		return nil, errors.New("verify sign failed")
	}
	orderNo, tradeNo := params["out_trade_no"], params["trade_no"]
	if err := validateOrderNo(orderNo); err != nil {
		return nil, err
	}
	if err := ValidateTradeNo(tradeNo); err != nil {
		return nil, err
	}
	if params["pid"] != g.config.PID {
		return nil, errors.New("merchant id mismatch")
	}
	if g.config.Type != "" && params["type"] != g.config.Type {
		return nil, errors.New("payment type mismatch")
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		return nil, errors.New("trade status is not success")
	}
	if !strings.EqualFold(params["sign_type"], "MD5") {
		return nil, errors.New("unsupported signature type")
	}
	amount, err := ParseAmount(params["money"])
	if err != nil {
		return nil, errors.New("invalid callback money")
	}
	return &Notice{
		OrderNo:  orderNo,
		TradeNo:  tradeNo,
		Amount:   amount,
		Currency: epayCurrency,
		Paid:     true,
		Status:   params["trade_status"],
		Details:  map[string]string{"type": params["type"]},
	}, nil
}

func (g *epayGateway) CheckOrder(*Order, *Notice) error { return nil }

// ConfirmPayment re-queries the gateway. A gateway without the query API is
// accepted on the strength of the verified signature alone, which the
// notice details record.
func (g *epayGateway) ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error {
	result, err := g.queryOrder(ctx, o.OrderNo)
	if errors.Is(err, errEPayQueryNotSupported) {
		if notice.Details == nil {
			notice.Details = map[string]string{}
		}
		notice.Details["confirmation"] = "signature only: the gateway has no order query"
		return nil
	}
	if err != nil {
		return err
	}
	if !result.paid {
		return errors.New("gateway order is not paid")
	}
	// Some compatible gateways expose only a payment status from their query
	// endpoint. The callback is signature-verified and its payment fields
	// are validated before this point, so status confirmation is useful
	// without pretending omitted fields were verified by the query.
	if result.statusOnly {
		return nil
	}
	if result.orderNo != notice.OrderNo {
		return errors.New("gateway order number mismatch")
	}
	if result.tradeNo == "" || result.tradeNo != notice.TradeNo {
		return errors.New("gateway trade number mismatch")
	}
	if result.merchantID != g.config.PID {
		return errors.New("gateway merchant id mismatch")
	}
	if g.config.Type != "" && result.paymentType != g.config.Type {
		return errors.New("gateway payment type mismatch")
	}
	amount, err := ParseAmount(result.money)
	if err != nil || amount != notice.Amount {
		return errors.New("gateway payment amount mismatch")
	}
	return nil
}

// sign is the EPay signature: the non-empty parameters except sign and
// sign_type, sorted by name and joined as k=v&k=v, followed by the merchant
// key, as a lowercase MD5 hex digest.
func (g *epayGateway) sign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if value != "" && key != "sign" && key != "sign_type" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var text strings.Builder
	for i, key := range keys {
		if i > 0 {
			text.WriteByte('&')
		}
		text.WriteString(key + "=" + params[key])
	}
	text.WriteString(g.config.Key)
	sum := md5.Sum([]byte(text.String()))
	return hex.EncodeToString(sum[:])
}

func (g *epayGateway) verifySign(params map[string]string) bool {
	received := strings.ToLower(params["sign"])
	expected := g.sign(params)
	return len(received) == len(expected) && subtle.ConstantTimeCompare([]byte(expected), []byte(received)) == 1
}

type epayQueryResult struct {
	merchantID  string
	tradeNo     string
	orderNo     string
	paymentType string
	money       string
	paid        bool
	// unpaid reports that the gateway explicitly lists the order as awaiting
	// payment (standard status 0). A result that is neither paid nor unpaid,
	// such as a refunded or frozen order or a status-only answer, leaves the
	// payment state unknown.
	unpaid bool
	// statusOnly reports that the gateway returned only a payment status;
	// its omitted payment details are not verified.
	statusOnly bool
}

// epayPID is the merchant id of a query answer, which gateways send as a
// JSON number or a string.
type epayPID string

func (p *epayPID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return errors.New("merchant id is missing")
	}
	if data[0] == '"' {
		var text string
		if err := common.Unmarshal(data, &text); err != nil {
			return err
		}
		*p = epayPID(text)
		return nil
	}
	var number json.Number
	if err := common.Unmarshal(data, &number); err != nil {
		return err
	}
	*p = epayPID(number.String())
	return nil
}

// queryOrder obtains payment details directly from the gateway. It uses the
// standard EPay api.php?act=order query and, when that endpoint is absent,
// falls back to a known EasyPay-compatible POST query that exposes only a
// status.
func (g *epayGateway) queryOrder(ctx context.Context, orderNo string) (*epayQueryResult, error) {
	if orderNo == "" {
		return nil, errors.New("order number is empty")
	}
	result, err := g.queryStandardOrder(ctx, orderNo)
	if !errors.Is(err, errEPayQueryNotSupported) {
		return result, err
	}
	return g.queryEasyPayOrder(ctx, orderNo)
}

func (g *epayGateway) queryStandardOrder(ctx context.Context, orderNo string) (*epayQueryResult, error) {
	endpoint, err := g.endpoint("api.php")
	if err != nil {
		return nil, err
	}
	endpoint.RawQuery = url.Values{
		"act":          {"order"},
		"pid":          {g.config.PID},
		"key":          {g.config.Key},
		"out_trade_no": {orderNo},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("create EPay query request failed")
	}
	body, err := g.do(req, "EPay")
	if err != nil {
		return nil, err
	}
	var response struct {
		Code       int     `json:"code"`
		Msg        string  `json:"msg"`
		TradeNo    string  `json:"trade_no"`
		OutTradeNo string  `json:"out_trade_no"`
		Type       string  `json:"type"`
		Money      string  `json:"money"`
		PID        epayPID `json:"pid"`
		// Status is a pointer so an omitted status is not read as 0 (unpaid).
		Status *int `json:"status"`
	}
	if err := decodeEPayQuery("EPay", body, &response); err != nil {
		return nil, err
	}
	if response.Code != 1 {
		return nil, fmt.Errorf("EPay order lookup failed: code=%d msg=%q", response.Code, response.Msg)
	}
	if response.PID == "" {
		return nil, errors.New("EPay query answer has no merchant id")
	}
	return &epayQueryResult{
		merchantID:  string(response.PID),
		tradeNo:     response.TradeNo,
		orderNo:     response.OutTradeNo,
		paymentType: response.Type,
		money:       response.Money,
		paid:        response.Status != nil && *response.Status == 1,
		unpaid:      response.Status != nil && *response.Status == 0,
	}, nil
}

// queryEasyPayOrder implements the status-only fallback of a known modified
// EPay gateway. It is attempted only after the standard api.php query is
// found unsupported, so standard integrations keep their normal protocol.
func (g *epayGateway) queryEasyPayOrder(ctx context.Context, orderNo string) (*epayQueryResult, error) {
	endpoint, err := g.endpoint("api/EasyPay/queryOrder")
	if err != nil {
		return nil, err
	}
	form := url.Values{"orderNo": {orderNo}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form))
	if err != nil {
		return nil, errors.New("create EasyPay query request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := g.do(req, "EasyPay")
	if err != nil {
		return nil, err
	}
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := decodeEPayQuery("EasyPay", body, &response); err != nil {
		return nil, err
	}
	if response.Code != 1 {
		return nil, fmt.Errorf("EasyPay order lookup failed: code=%d msg=%q", response.Code, response.Msg)
	}
	if response.Data == nil || strings.TrimSpace(response.Data.Status) == "" {
		return nil, errors.New("EasyPay query answer has no order status")
	}
	return &epayQueryResult{paid: strings.EqualFold(response.Data.Status, "success"), statusOnly: true}, nil
}

// decodeEPayQuery parses a query answer. A body that is not JSON at all is
// evidence the gateway does not implement the query protocol, so it maps to
// errEPayQueryNotSupported instead of a decode failure; otherwise a gateway
// serving HTML error pages on the query path would also block
// signature-verified callbacks and close attempts.
func decodeEPayQuery(name string, body []byte, target any) error {
	err := common.Unmarshal(body, target)
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("%s query returned a non-JSON response: %w", name, errEPayQueryNotSupported)
	}
	if err != nil {
		return fmt.Errorf("decode %s query response: %w", name, err)
	}
	return nil
}

// do sends a query and returns its body. A 404 means the query protocol is
// not implemented; any other non-2xx status is a gateway failure.
func (g *epayGateway) do(req *http.Request, name string) ([]byte, error) {
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s query request failed", name)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errEPayQueryNotSupported
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s query returned HTTP %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s query response: %w", name, err)
	}
	if len(body) > maxQueryResponseSize {
		return nil, fmt.Errorf("%s query response is too large", name)
	}
	return body, nil
}

// endpoint resolves script against the configured gateway URL, which may
// name the gateway root or one of its scripts.
func (g *epayGateway) endpoint(script string) (*url.URL, error) {
	endpoint, err := url.Parse(g.config.URL)
	if err != nil {
		return nil, fmt.Errorf("parse EPay endpoint: %w", err)
	}
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, errors.New("unsupported EPay endpoint")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("EPay endpoint must not include a query or fragment")
	}
	path := strings.TrimRight(endpoint.Path, "/")
	for _, knownScript := range []string{"submit.php", "api.php"} {
		if trimmed, ok := strings.CutSuffix(path, "/"+knownScript); ok {
			path = trimmed
			break
		}
	}
	endpoint.Path = path + "/" + script
	endpoint.RawPath = ""
	return endpoint, nil
}
