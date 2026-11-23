package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// GMPay is epusdt (https://github.com/GMWalletApp/epusdt), a self-hosted
// gateway that collects crypto tokens such as USDT for orders priced in a
// fiat currency. This is its GMPay API as of commit 78ac7cc97637
// (src/docs/swagger.json, wiki/API.md and the source they describe):
//
//   - POST /payments/gmpay/v1/order/create-transaction creates an order,
//     signed with the merchant's API key (pid and secret_key) by HMAC-SHA256
//     over the sorted non-empty parameters. The order is answered with the
//     GMPay trade ID that names it from then on.
//   - Without a token and network the order waits for the buyer to choose
//     them at GMPay's cashier (status 4); with them it waits for a transfer
//     (status 1). Either way the buyer pays at the cashier,
//     /pay/checkout-counter/{trade_id}.
//   - GMPay posts the paid order (status 2) as JSON to the notify URL,
//     signed like a request, and retries until it reads 200 with ok or
//     success.
//   - /pay/checkout-counter-resp/{trade_id} and /pay/check-status/{trade_id}
//     report an order's fiat amount, currency and current status to anyone
//     who knows its trade ID. There is no merchant query by order number and
//     no merchant API to cancel an order: an order GMPay holds expires there
//     on its own (status 3).

// GMPay order statuses.
const (
	gmpayStatusWaitPay      = 1
	gmpayStatusPaid         = 2
	gmpayStatusExpired      = 3
	gmpayStatusWaitSelect   = 4
	gmpayStatusConfirming   = 5
	gmpayStatusManualReview = 6
)

// gmpaySuccess is the status_code of a successful GMPay answer; failures
// carry the HTTP status they are sent with (400 for a system error, 401 for
// a signature GMPay rejects) or a business code from 10001 on.
const gmpaySuccess = 200

// gmpayTradeID is what a GMPay trade ID looks like: Base64 URL text of 18
// random bytes, or digits in older releases. A trade ID is put into the
// path of GMPay's queries and its cashier, so nothing else is accepted.
var gmpayTradeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// gmpayAsset is a token or network name: GMPay's own are short words such
// as USDT, USDT0, tron or binance.
var gmpayAsset = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

var errGMPayAssetPair error = common.NewMessage("Set both the GMPay token and network, or leave both empty")

// GMPayConfig is the stored configuration of a GMPay payment method.
type GMPayConfig struct {
	// URL is where GMPay answers its API and buyers open its cashier.
	URL       string `json:"url"`
	PID       string `json:"pid"`
	SecretKey string `json:"secret_key"`
	// Currency is the fiat currency the method's orders are priced in; GMPay
	// converts it into the token at its own rate.
	Currency string `json:"currency"`
	// Token and Network fix what the buyer pays with; both empty let the
	// buyer choose at GMPay's cashier.
	Token   string `json:"token"`
	Network string `json:"network"`
}

type gmpayGateway struct {
	config GMPayConfig
	// base is the configured address without a trailing slash, which GMPay's
	// paths are appended to.
	base       string
	httpClient *http.Client
}

func openGMPay(r *Registry, method Method) (Gateway, error) {
	var config GMPayConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, fmt.Errorf("decode GMPay configuration: %w", err)
	}
	if config.PID == "" || config.SecretKey == "" || config.Currency == "" {
		return nil, errors.New("incomplete GMPay configuration")
	}
	endpoint, err := url.Parse(config.URL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, errors.New("unsupported GMPay URL")
	}
	base := strings.TrimRight(endpoint.Scheme+"://"+endpoint.Host+endpoint.EscapedPath(), "/")
	return &gmpayGateway{config: config, base: base, httpClient: r.httpClient}, nil
}

// normalizeGMPay trims the configuration, upper-cases the currency and the
// token and lower-cases the network as GMPay stores them.
func normalizeGMPay(raw []byte) (string, error) {
	var config GMPayConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("decode GMPay configuration: %w", err)
	}
	config.URL = strings.TrimSpace(config.URL)
	config.PID = strings.TrimSpace(config.PID)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	config.Currency = strings.ToUpper(strings.TrimSpace(config.Currency))
	config.Token = strings.ToUpper(strings.TrimSpace(config.Token))
	config.Network = strings.ToLower(strings.TrimSpace(config.Network))
	endpoint, err := url.Parse(config.URL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return "", errors.New("GMPay URL must be an http(s) URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return "", errors.New("GMPay URL must not carry user information, a query or a fragment")
	}
	if config.PID == "" || config.SecretKey == "" {
		return "", errors.New("the GMPay PID and secret key are required")
	}
	if len(config.Currency) != 3 || strings.Trim(config.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", errors.New("the GMPay order currency must be a three-letter code such as USD")
	}
	if (config.Token == "") != (config.Network == "") {
		return "", errGMPayAssetPair
	}
	if config.Token != "" && (!gmpayAsset.MatchString(config.Token) || !gmpayAsset.MatchString(config.Network)) {
		return "", errors.New("the GMPay token and network are names such as USDT and tron")
	}
	content, err := common.Marshal(config)
	return string(content), err
}

func (g *gmpayGateway) Platform() string { return PlatformGMPay }

func (g *gmpayGateway) ChargeCurrency() string { return g.config.Currency }

// NeedsNotifyURL is true only for a new order; a claimed one already
// carries its callback.
func (g *gmpayGateway) NeedsNotifyURL(o *Order) bool { return o.TradeNo == "" }

func (g *gmpayGateway) ServerCheckout() bool { return true }

// gmpayOrder is GMPay's view of an order: the answer to its creation or to
// /pay/checkout-counter-resp, which names no order number.
type gmpayOrder struct {
	TradeID  string      `json:"trade_id"`
	OrderID  string      `json:"order_id"`
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
	Status   int         `json:"status"`
}

// StartPayment creates the order's only GMPay order and sends the buyer to
// its cashier (see cashier). The claimed order keeps every checkout retry on
// the same cashier, since GMPay takes an order number once.
func (g *gmpayGateway) StartPayment(ctx context.Context, c Checkout) (*Action, error) {
	if c.Order.TradeNo != "" {
		return g.cashier(ctx, c.Order, c.Order.TradeNo)
	}
	if !strings.EqualFold(c.Charge.Currency, g.config.Currency) || c.Charge.Amount <= 0 {
		return nil, fmt.Errorf("GMPay cannot collect %s %s", FormatAmount(c.Charge.Amount), c.Charge.Currency)
	}
	name := c.Subject
	if name == "" {
		name = c.Order.OrderNo
	}
	// The form carries the amount as the text that is signed; JSON would be
	// signed as GMPay reads the number back.
	params := map[string]string{
		"pid":          g.config.PID,
		"order_id":     c.Order.OrderNo,
		"currency":     g.config.Currency,
		"amount":       FormatAmount(c.Charge.Amount),
		"notify_url":   c.NotifyURL,
		"redirect_url": c.ReturnURL,
		"name":         name,
		"token":        g.config.Token,
		"network":      g.config.Network,
	}
	form := url.Values{}
	for key, value := range params {
		if value != "" {
			form.Set(key, value)
		}
	}
	form.Set("signature", g.sign(params))
	var created gmpayOrder
	if err := g.call(ctx, http.MethodPost, "/payments/gmpay/v1/order/create-transaction", form, &created); err != nil {
		return nil, fmt.Errorf("create the GMPay order of %s: %w", c.Order.OrderNo, err)
	}
	amount, err := ParseAmount(created.Amount.String())
	if err != nil || created.OrderID != c.Order.OrderNo || amount != c.Charge.Amount || !strings.EqualFold(created.Currency, c.Charge.Currency) {
		return nil, fmt.Errorf("the GMPay order of %s does not match the payment expectation", c.Order.OrderNo)
	}
	if !gmpayTradeID.MatchString(created.TradeID) || (created.Status != gmpayStatusWaitPay && created.Status != gmpayStatusWaitSelect) {
		return nil, fmt.Errorf("GMPay answered the order of %s with trade %q in status %d", c.Order.OrderNo, created.TradeID, created.Status)
	}
	winner, err := c.Claim(ctx, created.TradeID)
	if err != nil {
		return nil, err
	}
	if winner != created.TradeID {
		// A concurrent checkout claimed its GMPay order first; ours is never
		// handed out and expires at GMPay.
		return g.cashier(ctx, c.Order, winner)
	}
	return &Action{Type: ActionURL, URL: g.base + "/pay/checkout-counter/" + url.PathEscape(created.TradeID)}, nil
}

// cashier sends the buyer back to the cashier of a GMPay order the order
// claimed, unless it expired. The cashier's address is built from the
// configured one, as GMPay builds it, rather than taken from an unsigned
// answer, so nothing between the site and GMPay can send buyers elsewhere.
func (g *gmpayGateway) cashier(ctx context.Context, o *Order, tradeID string) (*Action, error) {
	order, err := g.lookup(ctx, o, tradeID)
	if err != nil {
		return nil, err
	}
	if order.Status == gmpayStatusExpired {
		return nil, fmt.Errorf("the GMPay order of %s expired", o.OrderNo)
	}
	return &Action{Type: ActionURL, URL: g.base + "/pay/checkout-counter/" + url.PathEscape(tradeID)}, nil
}

// Reconcile asks GMPay about the order's GMPay order before it closes. A
// paid order is settled and an expired one closes. GMPay cannot cancel an
// order, so one still awaiting a transfer or the buyer's choice keeps the
// order pending until it expires at GMPay, unless a person gives the order
// up: then it closes, and a payment made before GMPay expires it still
// reaches the callback, which settles a closed order. An order whose
// transfer GMPay is confirming or holds for its operator's review stays
// pending in any case.
//
// GMPay names an order only by the trade ID its creation answered with. A
// checkout that recorded its payment expectation but bound no trade never
// handed a cashier out; if GMPay created the order anyway, a payment of it
// still reaches the callback. The close only requires that checkout to stay
// as it is, so a trade claimed meanwhile is asked about first.
func (g *gmpayGateway) Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error) {
	o := req.Order
	if o.TradeNo == "" {
		verdict := Reconciliation{RequireStableCheckout: true}
		if o.PaymentCurrency != "" {
			verdict.Note = "no GMPay order was bound to the checkout"
		}
		return verdict, nil
	}
	status, err := g.status(ctx, o, o.TradeNo)
	if err != nil {
		if req.Explicit {
			return Reconciliation{Note: "explicit close of a GMPay order the gateway could not confirm: " + err.Error()}, nil
		}
		return Reconciliation{}, fmt.Errorf("cannot safely expire GMPay order %s: %w: %w", o.OrderNo, err, ErrUnconfirmed)
	}
	switch status {
	case gmpayStatusPaid:
		return Reconciliation{TradeNo: o.TradeNo}, nil
	case gmpayStatusExpired:
		return Reconciliation{RequireStableCheckout: true}, nil
	case gmpayStatusWaitPay, gmpayStatusWaitSelect:
		if req.Explicit {
			return Reconciliation{Note: "GMPay cannot cancel the order; it takes payments until it expires there"}, nil
		}
		return Reconciliation{}, fmt.Errorf("GMPay order %s awaits payment until it expires at GMPay: %w", o.OrderNo, ErrUnconfirmed)
	case gmpayStatusConfirming, gmpayStatusManualReview:
		return Reconciliation{}, fmt.Errorf("GMPay holds a transfer to order %s in status %d: %w", o.OrderNo, status, ErrUnconfirmed)
	}
	return Reconciliation{}, fmt.Errorf("cannot safely expire GMPay order %s in unknown status %d: %w", o.OrderNo, status, ErrUnconfirmed)
}

// QueryPayment reports the trade of a paid order; the queries change
// nothing at GMPay.
func (g *gmpayGateway) QueryPayment(ctx context.Context, o *Order) (string, error) {
	if o.TradeNo == "" {
		return "", nil
	}
	status, err := g.status(ctx, o, o.TradeNo)
	if err != nil || status != gmpayStatusPaid {
		return "", err
	}
	return o.TradeNo, nil
}

// ParseCallback verifies the signature of a paid order's callback. GMPay
// signs every member of the JSON body but the signature, as it signs a
// request: a string as it is, a number as the shortest decimal of its
// float64 (Go's 'f' format), empty strings and nulls left out. The
// callback names no currency; it is the currency the method prices its
// GMPay orders in, which ConfirmPayment checks against GMPay's order.
func (g *gmpayGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	if n.HTTPMethod != http.MethodPost {
		return nil, errors.New("GMPay posts its callbacks")
	}
	var members map[string]json.RawMessage
	if err := common.Unmarshal(n.Body, &members); err != nil {
		return nil, fmt.Errorf("decode GMPay callback: %w", err)
	}
	params := make(map[string]string, len(members))
	for key, raw := range members {
		raw = bytes.TrimSpace(raw)
		switch {
		case string(raw) == "null":
		case len(raw) > 0 && raw[0] == '"':
			var text string
			if err := common.Unmarshal(raw, &text); err != nil {
				return nil, fmt.Errorf("decode GMPay callback member %s: %w", key, err)
			}
			params[key] = text
		default:
			// Only the signature is computed from the float: amounts are read
			// from the decimal text below.
			var number json.Number
			if err := common.Unmarshal(raw, &number); err != nil {
				return nil, fmt.Errorf("GMPay callback member %s is neither a string nor a number", key)
			}
			value, err := strconv.ParseFloat(number.String(), 64)
			if err != nil {
				return nil, fmt.Errorf("GMPay callback member %s is not a number GMPay signs", key)
			}
			params[key] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	if !hmac.Equal([]byte(g.sign(params)), []byte(params["signature"])) {
		return nil, errors.New("verify sign failed")
	}
	var callback struct {
		PID                string      `json:"pid"`
		TradeID            string      `json:"trade_id"`
		OrderID            string      `json:"order_id"`
		Amount             json.Number `json:"amount"`
		ActualAmount       json.Number `json:"actual_amount"`
		ReceiveAddress     string      `json:"receive_address"`
		Token              string      `json:"token"`
		BlockTransactionID string      `json:"block_transaction_id"`
		Status             int         `json:"status"`
	}
	if err := common.Unmarshal(n.Body, &callback); err != nil {
		return nil, fmt.Errorf("decode GMPay callback: %w", err)
	}
	if callback.PID != g.config.PID {
		return nil, errors.New("merchant id mismatch")
	}
	if err := validateOrderNo(callback.OrderID); err != nil {
		return nil, err
	}
	if !gmpayTradeID.MatchString(callback.TradeID) {
		return nil, errors.New("invalid trade number")
	}
	if callback.Status != gmpayStatusPaid {
		return nil, fmt.Errorf("GMPay reports the order in status %d", callback.Status)
	}
	amount, err := ParseAmount(callback.Amount.String())
	if err != nil {
		return nil, errors.New("invalid callback amount")
	}
	return &Notice{
		OrderNo:  callback.OrderID,
		TradeNo:  callback.TradeID,
		Amount:   amount,
		Currency: g.config.Currency,
		Paid:     true,
		Status:   "paid",
		Details: map[string]string{
			"token":                callback.Token,
			"actual_amount":        callback.ActualAmount.String(),
			"receive_address":      callback.ReceiveAddress,
			"block_transaction_id": callback.BlockTransactionID,
		},
	}, nil
}

// CheckOrder requires the GMPay order to be the one the order claimed.
func (g *gmpayGateway) CheckOrder(o *Order, notice *Notice) error {
	if o.TradeNo != "" && o.TradeNo != notice.TradeNo {
		return errors.New("order trade number mismatch")
	}
	return nil
}

// ConfirmPayment asks GMPay itself: its order of the notice's trade must be
// paid and price exactly the order's payment expectation.
func (g *gmpayGateway) ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error {
	status, err := g.status(ctx, o, notice.TradeNo)
	if err != nil {
		return err
	}
	if status != gmpayStatusPaid {
		return fmt.Errorf("GMPay reports the order in status %d", status)
	}
	return nil
}

// lookup reads GMPay's order of a trade and checks that it prices exactly
// the order's payment expectation before any of its state is trusted.
//
// GMPay's answer names no order number, nor does any query a merchant can
// make, so the trade's identity is settled before it is looked up: a
// claimed trade is the one whose creation answer named the order
// (StartPayment), and an unclaimed one reaches ConfirmPayment only from a
// callback GMPay signed with the order number and the trade together.
func (g *gmpayGateway) lookup(ctx context.Context, o *Order, tradeID string) (*gmpayOrder, error) {
	if !gmpayTradeID.MatchString(tradeID) {
		return nil, fmt.Errorf("invalid GMPay trade %q", tradeID)
	}
	var order gmpayOrder
	if err := g.call(ctx, http.MethodGet, "/pay/checkout-counter-resp/"+url.PathEscape(tradeID), nil, &order); err != nil {
		return nil, fmt.Errorf("look up GMPay trade %s: %w", tradeID, err)
	}
	amount, err := ParseAmount(order.Amount.String())
	if err != nil || order.TradeID != tradeID || amount != o.PaymentAmount || !strings.EqualFold(order.Currency, o.PaymentCurrency) {
		return nil, fmt.Errorf("GMPay trade %s does not match the payment expectation of %s", tradeID, o.OrderNo)
	}
	return &order, nil
}

// status returns the current status of a trade whose order matches the
// payment expectation. GMPay documents /pay/check-status as the source of
// an order's current status, apart from the order data the lookup reads.
func (g *gmpayGateway) status(ctx context.Context, o *Order, tradeID string) (int, error) {
	if _, err := g.lookup(ctx, o, tradeID); err != nil {
		return 0, err
	}
	var answer struct {
		TradeID string `json:"trade_id"`
		Status  int    `json:"status"`
	}
	if err := g.call(ctx, http.MethodGet, "/pay/check-status/"+url.PathEscape(tradeID), nil, &answer); err != nil {
		return 0, fmt.Errorf("check GMPay trade %s: %w", tradeID, err)
	}
	if answer.TradeID != tradeID {
		return 0, fmt.Errorf("GMPay answered the status of trade %q for %s", answer.TradeID, tradeID)
	}
	return answer.Status, nil
}

// sign is the GMPay signature: the non-empty parameters except signature,
// sorted by name and joined as k=v&k=v without escaping, as a lowercase hex
// HMAC-SHA256 keyed with the secret key.
func (g *gmpayGateway) sign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if value != "" && key != "signature" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	mac := hmac.New(sha256.New, []byte(g.config.SecretKey))
	for i, key := range keys {
		if i > 0 {
			mac.Write([]byte{'&'})
		}
		mac.Write([]byte(key + "=" + params[key]))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// call sends a request to GMPay and decodes the data of its answer into
// target. An answer that turns the request down with GMPay's own code (a
// business code, or 401 for a signature it rejects) is marked refused.
func (g *gmpayGateway) call(ctx context.Context, method, path string, form url.Values, target any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base+path, body)
	if err != nil {
		return fmt.Errorf("create GMPay request: %w", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GMPay request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	value, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return fmt.Errorf("read GMPay answer: %w", err)
	}
	if len(value) > maxQueryResponseSize {
		return errors.New("GMPay answer is too large")
	}
	var answer struct {
		StatusCode int             `json:"status_code"`
		Message    string          `json:"message"`
		Data       json.RawMessage `json:"data"`
	}
	if err := common.Unmarshal(value, &answer); err != nil {
		return fmt.Errorf("GMPay answered HTTP %d with a non-JSON response", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || answer.StatusCode != gmpaySuccess {
		err := fmt.Errorf("GMPay answered HTTP %d: status_code=%d message=%q", resp.StatusCode, answer.StatusCode, answer.Message)
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError &&
			(answer.StatusCode == http.StatusUnauthorized || answer.StatusCode > 10000) {
			return Refused(err)
		}
		return err
	}
	if len(answer.Data) == 0 || string(answer.Data) == "null" {
		return errors.New("GMPay answer has no data")
	}
	if err := common.Unmarshal(answer.Data, target); err != nil {
		return fmt.Errorf("decode GMPay answer: %w", err)
	}
	return nil
}
