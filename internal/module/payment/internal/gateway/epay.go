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
// common wrappers. A method speaks EPay V1 or EPay V2 (epay_v2.go: RSA
// signatures, signed answers, closing an unpaid order at the gateway) and
// starts its payments either with the gateway's payment page or with the
// API (mapi.php, /api/pay/create), which reports a gateway failure before
// the buyer leaves and can answer with a QR code; answers read the code,
// pid and status as numbers or strings; the order name is cut to what the
// gateway keeps; transport failures of requests without the merchant key in
// their URL keep their cause.

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
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

// epayCurrency is the only currency EPay-compatible gateways collect.
const epayCurrency = "CNY"

// EPayUnpaidCloseAge is the age from which a pending EPay order that the
// gateway lists as awaiting payment may close. EPay V1 has no cancellation
// API, so the order stays open well past its payment window for a payer who
// opened the gateway page near expiry.
const EPayUnpaidCloseAge = 30 * time.Minute

// maxQueryResponseSize caps the gateway answers read.
const maxQueryResponseSize = 1 << 20

// epayMaxNameBytes is the longest order name EPay gateways keep; a longer
// one is cut at a character boundary.
const epayMaxNameBytes = 127

// EPay protocol versions: V1 signs with the merchant key (MD5), V2 with the
// merchant's RSA private key and verifies the gateway with its public key.
const (
	EPayV1 = "v1"
	EPayV2 = "v2"
)

// EPay checkout modes. An API checkout creates the payment with a server
// request, so a gateway that cannot take it fails before the buyer leaves;
// a page checkout sends the buyer to the gateway's payment page unasked.
const (
	EPayCheckoutAPI  = "api"
	EPayCheckoutPage = "page"
)

// errEPayQueryNotSupported is returned when the gateway implements neither
// the standard EPay order query nor the EasyPay-compatible fallback: the
// query endpoint answers 404 or with something that is not JSON at all.
var errEPayQueryNotSupported = errors.New("gateway does not support the order query API")

var errEPayAPITypeMissing error = common.NewMessage("EPay API checkout needs a payment type")

// EPayConfig is the stored configuration of an EPay payment method. Key
// belongs to V1, PrivateKey and PlatformPublicKey to V2.
type EPayConfig struct {
	URL      string `json:"url"`
	PID      string `json:"pid"`
	Version  string `json:"version"`
	Checkout string `json:"checkout"`
	Key      string `json:"key,omitempty"`
	// PrivateKey is the merchant's RSA private key and PlatformPublicKey the
	// gateway's RSA public key, both as Base64 DER.
	PrivateKey        string `json:"private_key,omitempty"`
	PlatformPublicKey string `json:"platform_public_key,omitempty"`
	// Type is the EPay payment type (alipay, wxpay, ...); empty lets the
	// buyer choose on the gateway's page, which an API checkout cannot.
	Type string `json:"type"`
}

type epayGateway struct {
	config EPayConfig
	// v2 holds the keys of an EPay V2 method.
	v2         *epayV2Keys
	httpClient *http.Client
}

// decodeEPayConfig reads a stored or submitted configuration. A method saved
// before versions and checkout modes existed speaks V1 through the gateway
// page.
func decodeEPayConfig(raw []byte) (EPayConfig, error) {
	var config EPayConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return config, fmt.Errorf("decode EPay configuration: %w", err)
	}
	if config.Version == "" {
		config.Version = EPayV1
	}
	if config.Checkout == "" {
		config.Checkout = EPayCheckoutPage
	}
	return config, nil
}

func openEPay(r *Registry, method Method) (Gateway, error) {
	config, err := decodeEPayConfig([]byte(method.Config))
	if err != nil {
		return nil, err
	}
	if config.PID == "" || config.URL == "" {
		return nil, errors.New("incomplete EPay configuration")
	}
	g := &epayGateway{config: config, httpClient: r.httpClient}
	switch config.Version {
	case EPayV1:
		if config.Key == "" {
			return nil, errors.New("incomplete EPay configuration")
		}
	case EPayV2:
		if g.v2, err = parseEPayV2Keys(config.PrivateKey, config.PlatformPublicKey); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported EPay version %q", config.Version)
	}
	return g, nil
}

// normalizeEPay validates a configuration and stores only the credentials of
// its version, the V2 keys as Base64 DER.
func normalizeEPay(raw []byte) (string, error) {
	config, err := decodeEPayConfig(raw)
	if err != nil {
		return "", err
	}
	config.URL = strings.TrimSpace(config.URL)
	config.PID = strings.TrimSpace(config.PID)
	config.Version = strings.TrimSpace(config.Version)
	config.Checkout = strings.TrimSpace(config.Checkout)
	config.Type = strings.TrimSpace(config.Type)
	endpoint, err := url.Parse(config.URL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return "", errors.New("EPay URL must be an http(s) URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return "", errors.New("EPay URL must not carry user information, a query or a fragment")
	}
	if config.PID == "" {
		return "", errors.New("EPay merchant ID is required")
	}
	switch config.Version {
	case EPayV1:
		config.Key = strings.TrimSpace(config.Key)
		config.PrivateKey, config.PlatformPublicKey = "", ""
		if config.Key == "" {
			return "", errors.New("EPay merchant key is required")
		}
	case EPayV2:
		config.Key = ""
		keys, err := parseEPayV2Keys(config.PrivateKey, config.PlatformPublicKey)
		if err != nil {
			return "", err
		}
		if err := keys.private.Validate(); err != nil {
			return "", errEPayV2PrivateKey
		}
		config.PrivateKey = base64.StdEncoding.EncodeToString(keys.privateDER)
		config.PlatformPublicKey = base64.StdEncoding.EncodeToString(keys.publicDER)
	default:
		return "", fmt.Errorf("unsupported EPay version %q", config.Version)
	}
	switch config.Checkout {
	case EPayCheckoutAPI:
		if config.Type == "" {
			return "", errEPayAPITypeMissing
		}
	case EPayCheckoutPage:
	default:
		return "", fmt.Errorf("unsupported EPay checkout mode %q", config.Checkout)
	}
	content, err := common.Marshal(config)
	return string(content), err
}

func (g *epayGateway) Platform() string { return PlatformEPay }

func (g *epayGateway) ChargeCurrency() string { return epayCurrency }

func (g *epayGateway) NeedsNotifyURL(*Order) bool { return true }

// StartPayment creates the payment with the gateway's API, or builds the
// signed redirect to its payment page.
func (g *epayGateway) StartPayment(ctx context.Context, c Checkout) (*Action, error) {
	if !strings.EqualFold(c.Charge.Currency, epayCurrency) || c.Charge.Amount <= 0 {
		return nil, fmt.Errorf("EPay cannot collect %s %s", FormatAmount(c.Charge.Amount), c.Charge.Currency)
	}
	name := c.Subject
	if name == "" {
		name = c.Order.OrderNo
	}
	for len(name) > epayMaxNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	params := map[string]string{
		"money":        FormatAmount(c.Charge.Amount),
		"name":         name,
		"notify_url":   c.NotifyURL,
		"out_trade_no": c.Order.OrderNo,
		"type":         g.config.Type,
		"return_url":   c.ReturnURL,
	}
	if g.config.Checkout == EPayCheckoutAPI {
		if net.ParseIP(c.Order.ClientIP) == nil {
			return nil, fmt.Errorf("EPay API checkout of order %s needs the buyer's IP address", c.Order.OrderNo)
		}
		params["clientip"] = c.Order.ClientIP
		if g.config.Version == EPayV2 {
			return g.createV2Payment(ctx, params)
		}
		return g.createV1Payment(ctx, params)
	}
	if g.config.Version == EPayV2 {
		return g.v2PageAction(params)
	}
	endpoint, err := g.endpoint("submit.php")
	if err != nil {
		return nil, err
	}
	params["pid"] = g.config.PID
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

// createV1Payment creates the payment at mapi.php. The gateway answers with
// exactly one way to pay: a payment page, a QR code or an app link.
func (g *epayGateway) createV1Payment(ctx context.Context, params map[string]string) (*Action, error) {
	endpoint, err := g.endpoint("mapi.php")
	if err != nil {
		return nil, err
	}
	params["pid"] = g.config.PID
	// jump asks for the address of a payment page rather than a page for
	// the buyer's device.
	params["device"] = "jump"
	form := url.Values{}
	for key, value := range params {
		if value != "" {
			form.Set(key, value)
		}
	}
	form.Set("sign", g.sign(params))
	form.Set("sign_type", "MD5")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("create EPay payment request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := g.send(req, "EPay payment")
	if err != nil {
		return nil, err
	}
	var response struct {
		Code      epayScalar `json:"code"`
		Msg       string     `json:"msg"`
		TradeNo   epayScalar `json:"trade_no"`
		PayURL    string     `json:"payurl"`
		QRCode    string     `json:"qrcode"`
		URLScheme string     `json:"urlscheme"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode EPay payment answer: %w", err)
	}
	if response.Code != "1" {
		return nil, fmt.Errorf("EPay refused the payment of order %s: code=%s msg=%q", params["out_trade_no"], response.Code, response.Msg)
	}
	if response.TradeNo == "" {
		return nil, fmt.Errorf("EPay payment answer of order %s has no trade number", params["out_trade_no"])
	}
	payURL, qrCode, appURL := strings.TrimSpace(response.PayURL), strings.TrimSpace(response.QRCode), strings.TrimSpace(response.URLScheme)
	switch {
	case payURL != "" && qrCode == "" && appURL == "":
		return epayPayAction("jump", payURL)
	case payURL == "" && qrCode != "" && appURL == "":
		return epayPayAction("qrcode", qrCode)
	case payURL == "" && qrCode == "" && appURL != "":
		return epayPayAction("urlscheme", appURL)
	}
	return nil, fmt.Errorf("EPay payment answer of order %s must offer exactly one of a payment page, a QR code and an app link", params["out_trade_no"])
}

// epayPayAction turns what a gateway answered a payment creation with into
// the buyer's action: a payment page (jump) must be an http(s) URL, an app
// link (urlscheme) any link but a script or document one, and a QR code is
// shown as it is.
func epayPayAction(payType, value string) (*Action, error) {
	if value == "" {
		return nil, errors.New("EPay answered an empty way to pay")
	}
	switch payType {
	case "qrcode":
		return &Action{Type: ActionQRCode, QRCode: value}, nil
	case "jump", "urlscheme":
		target, err := url.Parse(value)
		if err != nil || target.Scheme == "" {
			return nil, errors.New("EPay answered an invalid payment link")
		}
		scheme := strings.ToLower(target.Scheme)
		web := (scheme == "http" || scheme == "https") && target.Host != ""
		if payType == "jump" && !web {
			return nil, errors.New("EPay answered a payment page that is not an http(s) URL")
		}
		if slices.Contains([]string{"javascript", "data", "vbscript", "file", "blob", "about"}, scheme) {
			return nil, fmt.Errorf("EPay answered a %s link", scheme)
		}
		return &Action{Type: ActionURL, URL: value}, nil
	}
	return nil, fmt.Errorf("EPay answered the unsupported payment form %q", payType)
}

// Reconcile queries the order at the gateway. A paid order is settled. An
// order the gateway explicitly lists as awaiting payment is closed at an
// EPay V2 gateway, which then takes no payment for it, and closes at once;
// when the gateway cannot close it, or speaks V1, which has no cancellation
// API, it closes once it is EPayUnpaidCloseAge old, so a payer who opened the
// gateway page near expiry still finishes. Any other answer (a failed,
// unsupported or unavailable query, or a status that is neither paid nor
// awaiting payment) leaves the payment state unknown, so the order stays
// pending for retry or manual resolution instead of losing funds. An
// explicit close is the exception: absent any evidence of payment it
// proceeds, after asking a V2 gateway to close the order too.
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
	if result.paid {
		tradeNo, err := g.paidTrade(o, result)
		if err != nil {
			return Reconciliation{}, err
		}
		return Reconciliation{TradeNo: tradeNo}, nil
	}
	if req.Explicit {
		if g.config.Version == EPayV2 {
			if err := g.closeV2Order(ctx, o.OrderNo); err != nil {
				return Reconciliation{Note: "the EPay gateway did not close the order: " + err.Error()}, nil
			}
		}
		return Reconciliation{}, nil
	}
	if !result.unpaid {
		return Reconciliation{}, fmt.Errorf("cannot safely expire EPay order %s: gateway reports it neither paid nor awaiting payment: %w", o.OrderNo, ErrUnconfirmed)
	}
	if g.config.Version == EPayV2 {
		err := g.closeV2Order(ctx, o.OrderNo)
		if err == nil {
			return Reconciliation{}, nil
		}
		if time.Since(o.CreatedAt) < EPayUnpaidCloseAge {
			return Reconciliation{}, fmt.Errorf("unpaid EPay order %s could not be closed at the gateway (%w) and stays pending until it is %s old: %w", o.OrderNo, err, EPayUnpaidCloseAge, ErrUnconfirmed)
		}
		return Reconciliation{Note: "the EPay gateway did not close the order: " + err.Error()}, nil
	}
	if time.Since(o.CreatedAt) < EPayUnpaidCloseAge {
		return Reconciliation{}, fmt.Errorf("unpaid EPay order %s stays pending until it is %s old: %w", o.OrderNo, EPayUnpaidCloseAge, ErrUnconfirmed)
	}
	return Reconciliation{}, nil
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

// ParseCallback verifies the signature (MD5 for V1; RSA and a fresh
// timestamp for V2) and the callback fields: the merchant, the payment type
// when the method fixes one, a successful trade status and the amount.
func (g *epayGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	params := n.Params
	if params == nil {
		params = map[string]string{}
	}
	if g.config.Version == EPayV2 {
		if err := g.verifyV2(params); err != nil {
			return nil, err
		}
	} else if !g.verifySign(params) || !strings.EqualFold(params["sign_type"], "MD5") {
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

// ConfirmPayment re-queries the gateway. A V1 gateway without the query API
// is accepted on the strength of the verified signature alone, which the
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

// epayScalar is a field of a gateway answer that gateways send as a JSON
// string or number, such as the code, the merchant id or the status; a
// number keeps its literal text and null reads as empty.
type epayScalar string

func (v *epayScalar) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		*v = ""
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := common.Unmarshal(data, &text); err != nil {
			return err
		}
		*v = epayScalar(text)
		return nil
	}
	var number json.Number
	if err := common.Unmarshal(data, &number); err != nil {
		return err
	}
	*v = epayScalar(number.String())
	return nil
}

// queryOrder obtains payment details directly from the gateway. A V2
// gateway answers its signed /api/pay/query. A V1 gateway is asked with the
// standard EPay api.php?act=order query and, when that endpoint is absent,
// with a known EasyPay-compatible POST query that exposes only a status.
func (g *epayGateway) queryOrder(ctx context.Context, orderNo string) (*epayQueryResult, error) {
	if orderNo == "" {
		return nil, errors.New("order number is empty")
	}
	if g.config.Version == EPayV2 {
		return g.queryV2Order(ctx, orderNo)
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
		Code       epayScalar `json:"code"`
		Msg        string     `json:"msg"`
		TradeNo    epayScalar `json:"trade_no"`
		OutTradeNo string     `json:"out_trade_no"`
		Type       string     `json:"type"`
		Money      epayScalar `json:"money"`
		PID        epayScalar `json:"pid"`
		// An omitted status reads as empty, not as 0 (unpaid).
		Status epayScalar `json:"status"`
	}
	if err := decodeEPayQuery("EPay", body, &response); err != nil {
		return nil, err
	}
	if response.Code != "1" {
		return nil, fmt.Errorf("EPay order lookup failed: code=%s msg=%q", response.Code, response.Msg)
	}
	if response.PID == "" {
		return nil, errors.New("EPay query answer has no merchant id")
	}
	return &epayQueryResult{
		merchantID:  string(response.PID),
		tradeNo:     string(response.TradeNo),
		orderNo:     response.OutTradeNo,
		paymentType: response.Type,
		money:       string(response.Money),
		paid:        response.Status == "1",
		unpaid:      response.Status == "0",
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

// do sends a V1 query and returns its body. A 404 means the query protocol
// is not implemented; any other non-2xx status is a gateway failure.
func (g *epayGateway) do(req *http.Request, name string) ([]byte, error) {
	body, err := g.send(req, name+" query")
	var status *epayStatusError
	if errors.As(err, &status) && status.code == http.StatusNotFound {
		return nil, errEPayQueryNotSupported
	}
	return body, err
}

// epayStatusError is a gateway answer with a non-2xx status.
type epayStatusError struct {
	name string
	code int
}

func (e *epayStatusError) Error() string { return fmt.Sprintf("%s returned HTTP %d", e.name, e.code) }

// send sends a request to the gateway and returns the body of its 2xx
// answer. The transport failure of a GET request is reported without its
// cause, which quotes the URL: V1 queries carry the merchant key in theirs.
func (g *epayGateway) send(req *http.Request, name string) ([]byte, error) {
	resp, err := g.httpClient.Do(req)
	if err != nil {
		if req.Method == http.MethodGet {
			return nil, fmt.Errorf("%s request failed", name)
		}
		return nil, fmt.Errorf("%s request failed: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &epayStatusError{name: name, code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s answer: %w", name, err)
	}
	if len(body) > maxQueryResponseSize {
		return nil, fmt.Errorf("%s answer is too large", name)
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
	for _, knownScript := range []string{"submit.php", "mapi.php", "api.php"} {
		if trimmed, ok := strings.CutSuffix(path, "/"+knownScript); ok {
			path = trimmed
			break
		}
	}
	endpoint.Path = path + "/" + script
	endpoint.RawPath = ""
	return endpoint, nil
}
