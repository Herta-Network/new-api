package gatewaytest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// FakeGMPay is an in-process GMPay (epusdt) answering the order creation and
// the two public order queries the payment gateway calls, as epusdt does at
// commit 78ac7cc97637. It verifies each creation's HMAC-SHA256 signature
// with the merchant's secret key, failing the test when one does not verify,
// takes each order number once, and signs callbacks as epusdt does.
type FakeGMPay struct {
	t   testing.TB
	URL string
	// PID and SecretKey are the merchant's API key.
	PID, SecretKey string

	mu      sync.Mutex
	orders  map[string]*GMPayOrder
	created []url.Values
	// refuse answers each creation with this status_code.
	refuse int
}

// GMPayOrder is an order the fake holds. Amount is what GMPay keeps of the
// submitted fiat amount: a float64.
type GMPayOrder struct {
	TradeID, OrderID, Currency, Token, Network string
	Amount                                     float64
	Status                                     int
}

// NewFakeGMPay starts a fake GMPay for the test with the API key 1000 that a
// new GMPay seeds.
func NewFakeGMPay(t testing.TB) *FakeGMPay {
	f := &FakeGMPay{t: t, PID: "1000", SecretKey: "gmpay-test-secret", orders: map[string]*GMPayOrder{}}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	f.URL = server.URL
	return f
}

// Config is the configuration of a method collecting currency through the
// fake; token and network may both be empty.
func (f *FakeGMPay) Config(currency, token, network string) string {
	content, err := common.Marshal(map[string]string{
		"url": f.URL, "pid": f.PID, "secret_key": f.SecretKey, "currency": currency, "token": token, "network": network,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return string(content)
}

// GMPaySignature is epusdt's signature (util/sign): the members but
// signature that are neither null nor empty, a number written as the
// shortest decimal of its float64, sorted as name=value and joined with &,
// as the lowercase hex HMAC-SHA256 keyed with secret.
func GMPaySignature(members map[string]any, secret string) string {
	var pairs []string
	for key, value := range members {
		text := ""
		switch value := value.(type) {
		case nil:
		case string:
			text = value
		case float64:
			text = strconv.FormatFloat(value, 'f', -1, 64)
		case int:
			text = strconv.Itoa(value)
		default:
			panic(fmt.Sprintf("GMPay signs no %T", value))
		}
		if key != "signature" && text != "" {
			pairs = append(pairs, key+"="+text)
		}
	}
	sort.Strings(pairs)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(pairs, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}

// Refuse answers each creation with the GMPay status_code code (10003 when
// no wallet address is free, say); 0 serves them again.
func (f *FakeGMPay) Refuse(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = code
}

// Created returns the creation forms the fake received, in order.
func (f *FakeGMPay) Created() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.created...)
}

// Order returns the order of an order number; nil when there is none.
func (f *FakeGMPay) Order(orderID string) *GMPayOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, order := range f.orders {
		if order.OrderID == orderID {
			copied := *order
			return &copied
		}
	}
	return nil
}

// SetStatus moves the order of a trade to status.
func (f *FakeGMPay) SetStatus(tradeID string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders[tradeID].Status = status
}

// Notification is the callback body of the paid order of orderID, signed
// with the merchant's key. It has the members of epusdt's
// OrderNotifyResponse; change them through the returned map and Sign it
// again.
func (f *FakeGMPay) Notification(orderID string) map[string]any {
	order := f.Order(orderID)
	if order == nil {
		f.t.Fatalf("GMPay holds no order %s", orderID)
	}
	notification := map[string]any{
		"pid": f.PID, "trade_id": order.TradeID, "order_id": order.OrderID, "amount": order.Amount, "actual_amount": 1.39,
		"receive_address": "TTestTronAddress001", "token": "USDT", "block_transaction_id": "0xabc123", "status": 2,
	}
	f.Sign(notification, f.SecretKey)
	return notification
}

// Sign sets the signature member of a callback body with secret.
func (f *FakeGMPay) Sign(notification map[string]any, secret string) {
	notification["signature"] = GMPaySignature(notification, secret)
}

func (f *FakeGMPay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost && r.URL.Path == "/payments/gmpay/v1/order/create-transaction" {
		f.create(w, r)
		return
	}
	var order *GMPayOrder
	if r.Method == http.MethodGet {
		if tradeID, ok := strings.CutPrefix(r.URL.Path, "/pay/checkout-counter-resp/"); ok {
			if order = f.orders[tradeID]; order != nil {
				f.answer(w, http.StatusOK, 200, map[string]any{
					"trade_id": order.TradeID, "amount": order.Amount, "currency": order.Currency, "token": order.Token,
					"network": order.Network, "status": order.Status, "payment_type": "gmpay", "is_selected": false,
				})
				return
			}
		}
		if tradeID, ok := strings.CutPrefix(r.URL.Path, "/pay/check-status/"); ok {
			if order = f.orders[tradeID]; order != nil {
				f.answer(w, http.StatusOK, 200, map[string]any{"trade_id": order.TradeID, "status": order.Status})
				return
			}
		}
	}
	f.answer(w, http.StatusBadRequest, 10008, nil)
}

// create is epusdt's CheckApiSign middleware and CreateTransaction for a
// form: the signature is over the form values as they were sent.
func (f *FakeGMPay) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("GMPay creation is no form: %v", err)
		f.answer(w, http.StatusBadRequest, 10009, nil)
		return
	}
	f.created = append(f.created, r.PostForm)
	members := map[string]any{}
	for key := range r.PostForm {
		members[key] = r.PostForm.Get(key)
	}
	if r.PostForm.Get("pid") != f.PID || r.PostForm.Get("signature") != GMPaySignature(members, f.SecretKey) {
		f.t.Errorf("GMPay rejects the signature of %v", r.PostForm)
		f.answer(w, http.StatusUnauthorized, 401, nil)
		return
	}
	if f.refuse != 0 {
		f.answer(w, http.StatusBadRequest, f.refuse, nil)
		return
	}
	orderID, token, network := r.PostForm.Get("order_id"), strings.ToUpper(r.PostForm.Get("token")), strings.ToLower(r.PostForm.Get("network"))
	amount, err := strconv.ParseFloat(r.PostForm.Get("amount"), 64)
	if orderID == "" || len(orderID) > 32 || err != nil || amount <= 0.01 || r.PostForm.Get("notify_url") == "" || (token == "") != (network == "") {
		f.answer(w, http.StatusBadRequest, 10009, nil)
		return
	}
	for _, order := range f.orders {
		if order.OrderID == orderID {
			f.answer(w, http.StatusBadRequest, 10002, nil)
			return
		}
	}
	// epusdt keeps the amount at its precision, two decimals by default.
	amount, _ = strconv.ParseFloat(strconv.FormatFloat(amount, 'f', 2, 64), 64)
	random := make([]byte, 18)
	_, _ = rand.Read(random)
	order := &GMPayOrder{
		TradeID: base64.RawURLEncoding.EncodeToString(random), OrderID: orderID, Amount: amount,
		Currency: strings.ToUpper(r.PostForm.Get("currency")), Token: token, Network: network, Status: 1,
	}
	if token == "" {
		order.Status = 4
	}
	f.orders[order.TradeID] = order
	f.answer(w, http.StatusOK, 200, map[string]any{
		"trade_id": order.TradeID, "order_id": order.OrderID, "amount": order.Amount, "currency": order.Currency,
		"token": order.Token, "status": order.Status, "expiration_time": 1779530812,
		"payment_url": "https://cashier.example.test/pay/checkout-counter/" + order.TradeID,
	})
}

func (f *FakeGMPay) answer(w http.ResponseWriter, status, code int, data any) {
	message := "success"
	if code != 200 {
		message = "error " + strconv.Itoa(code)
	}
	content, err := common.Marshal(map[string]any{"status_code": code, "message": message, "data": data, "request_id": "req-test"})
	if err != nil {
		f.t.Error(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(content)
}
