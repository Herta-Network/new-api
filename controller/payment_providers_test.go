package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type providerTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t providerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	request := r.Clone(r.Context())
	copied := *r.URL
	request.URL = &copied
	request.URL.Scheme = t.target.Scheme
	request.URL.Host = t.target.Host
	request.Host = t.target.Host
	return t.base.RoundTrip(request)
}

type providerFixture struct {
	currency   string
	revokedKey string
	*paymentFixture
	paid                                             bool
	fail                                             bool
	discount                                         int64
	wrongPrice                                       bool
	orderNo, checkoutID, mode, priceID, cryptoStatus string
	quantity                                         int64
	sentCustomer                                     string
	invoiceID                                        string
	recurring                                        bool
}

func newProviderFixture(t *testing.T, provider string) *providerFixture {
	t.Helper()
	base := newPaymentFixture(t)
	f := &providerFixture{paymentFixture: base, cryptoStatus: "confirm_check", invoiceID: "in_initial", currency: "usd"}
	f.method = model.PaymentMethod{Name: provider, Provider: provider, Enabled: true}
	f.config = payment.Config{Key: "sk_test_fixture", WebhookSecret: "whsec_fixture", PriceID: "price_topup", PromotionCodes: true}
	if provider == payment.Cryptomus {
		f.config = payment.Config{MerchantID: "crypto_merchant", Key: "crypto_test_key", UnitPrice: "1"}
	}
	require.NoError(t, model.SavePaymentMethod(&f.method, f.config))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		if provider == payment.Stripe {
			if f.revokedKey != "" && r.Header.Get("Authorization") == "Bearer "+f.revokedKey {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":{"message":"revoked","type":"authentication_error"}}`))
				return
			}
			if f.fail && strings.Contains(r.URL.Path, "checkout/sessions/") {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"message":"unavailable","type":"api_error"}}`))
				return
			}
			switch {
			case r.URL.Path == "/v1/account":
				result = map[string]any{"id": "acct_fixture"}
			case strings.HasPrefix(r.URL.Path, "/v1/prices/"):
				id := strings.TrimPrefix(r.URL.Path, "/v1/prices/")
				item := map[string]any{"id": id, "active": true, "currency": f.currency, "unit_amount": 150, "billing_scheme": "per_unit", "type": "one_time"}
				if id == "price_plan" {
					item["unit_amount"] = 1000
					item["type"] = "recurring"
					item["recurring"] = map[string]any{"interval": "month"}
				}
				result = item
			case r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions":
				require.NoError(t, r.ParseForm())
				f.orderNo = r.PostForm.Get("client_reference_id")
				f.mode = r.PostForm.Get("mode")
				f.priceID = r.PostForm.Get("line_items[0][price]")
				f.quantity, _ = strconv.ParseInt(r.PostForm.Get("line_items[0][quantity]"), 10, 64)
				f.checkoutID = "cs_" + f.orderNo
				f.sentCustomer = r.PostForm.Get("customer")
				assert.Equal(t, "new-api-checkout:"+f.orderNo, r.Header.Get("Idempotency-Key"))
				if f.mode == "subscription" {
					assert.Equal(t, f.orderNo, r.PostForm.Get("subscription_data[metadata][new_api_order]"))
					assert.Empty(t, r.PostForm.Get("customer_creation"))
				}
				result = f.stripeSession()
			case strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
				result = f.stripeSession()
			case strings.HasPrefix(r.URL.Path, "/v1/invoices/"):
				result = f.stripeInvoice()
			default:
				t.Errorf("unexpected Stripe endpoint: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(404)
				return
			}
		} else {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Equal(t, f.config.MerchantID, r.Header.Get("merchant"))
			assert.Equal(t, cryptoSignature(body, f.config.Key), r.Header.Get("sign"))
			var input map[string]any
			require.NoError(t, common.Unmarshal(body, &input))
			if r.URL.Path == "/v1/payment" {
				f.orderNo = input["order_id"].(string)
				f.checkoutID = "crypto_" + f.orderNo
				assert.Equal(t, "USD", input["currency"])
				assert.Equal(t, "2.00", input["amount"])
			}
			if f.fail && r.URL.Path == "/v1/payment/info" {
				w.WriteHeader(503)
				return
			}
			result = map[string]any{"state": 0, "result": map[string]any{"uuid": f.checkoutID, "order_id": f.orderNo, "amount": "2.00000000", "currency": "USD", "status": f.cryptoStatus, "url": "https://pay.cryptomus.com/fixture"}}
		}
		payload, err := common.Marshal(result)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	original := http.DefaultTransport
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	http.DefaultTransport = providerTransport{base: original, target: target}
	t.Cleanup(func() { http.DefaultTransport = original; server.Close() })
	return f
}
func (f *providerFixture) stripeSession() map[string]any {
	unit := int64(150)
	if f.mode == "subscription" {
		unit = 1000
	}
	priceID := f.priceID
	if f.wrongPrice {
		priceID = "price_wrong"
	}
	status, paymentStatus := "open", "unpaid"
	if f.paid {
		status, paymentStatus = "complete", "paid"
	}
	if f.paid && unit*f.quantity == f.discount {
		paymentStatus = "no_payment_required"
	}
	result := map[string]any{"id": f.checkoutID, "url": "https://checkout.stripe.com/fixture", "client_reference_id": f.orderNo, "mode": f.mode, "status": status, "payment_status": paymentStatus, "currency": f.currency, "amount_subtotal": unit * f.quantity, "amount_total": unit*f.quantity - f.discount, "total_details": map[string]any{"amount_discount": f.discount}, "customer": "cus_fixture", "line_items": map[string]any{"data": []any{map[string]any{"price": map[string]any{"id": priceID}, "quantity": f.quantity}}, "has_more": false}}
	if f.mode == "subscription" {
		result["invoice"] = f.invoiceID
		result["subscription"] = "sub_fixture"
	}
	return result
}
func (f *providerFixture) stripeInvoice() map[string]any {
	reason := "subscription_create"
	if f.recurring {
		reason = "subscription_cycle"
	}
	now := common.GetTimestamp()
	return map[string]any{"id": f.invoiceID, "status": "paid", "paid": f.paid, "currency": f.currency, "subtotal": 1000, "total": 1000, "billing_reason": reason, "customer": "cus_fixture", "subscription": map[string]any{"id": "sub_fixture", "metadata": map[string]string{"new_api_order": f.orderNo}}, "subscription_details": map[string]any{"metadata": map[string]string{"new_api_order": f.orderNo}}, "lines": map[string]any{"data": []any{map[string]any{"price": map[string]any{"id": "price_plan"}, "quantity": 1, "period": map[string]any{"start": now - 60, "end": now + 2592000}}}}}
}
func (f *providerFixture) start(t *testing.T, subscription bool) {
	t.Helper()
	path := "/pay"
	body := map[string]any{"amount": 2, "payment_method": f.method.PublicType()}
	if subscription {
		path = "/subscription/pay"
		body["plan_id"] = 1
	}
	response := f.request(t, "POST", path, body)
	var result struct {
		Message string `json:"message"`
		URL     string `json:"url"`
		Type    string `json:"type"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, "success", result.Message, response.Body.String())
	require.Equal(t, "url", result.Type)
	require.NotEmpty(t, result.URL)
}
func cryptoSignature(data []byte, key string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(base64.StdEncoding.EncodeToString(data)+key)))
}
func (f *providerFixture) webhook(t *testing.T, eventID, eventType string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	signature := ""
	if f.method.Provider == payment.Stripe {
		object := f.stripeSession()
		if strings.HasPrefix(eventType, "invoice.") {
			object = f.stripeInvoice()
		}
		var err error
		body, err = common.Marshal(map[string]any{"id": eventID, "object": "event", "type": eventType, "data": map[string]any{"object": object}})
		require.NoError(t, err)
		stamp := strconv.FormatInt(common.GetTimestamp(), 10)
		mac := hmac.New(sha256.New, []byte(f.config.WebhookSecret))
		_, _ = mac.Write([]byte(stamp + "." + string(body)))
		signature = "t=" + stamp + ",v1=" + fmt.Sprintf("%x", mac.Sum(nil))
	} else {
		unsigned, err := common.Marshal(map[string]any{"type": "payment", "uuid": f.checkoutID, "order_id": f.orderNo, "amount": "2.00000000", "currency": "USD", "status": f.cryptoStatus})
		require.NoError(t, err)
		body = append(bytes.TrimSuffix(unsigned, []byte("}")), []byte(`,"sign":"`+cryptoSignature(unsigned, f.config.Key)+`"}`)...)
	}
	endpoint := "/notify/" + f.method.Provider + "/" + strconv.Itoa(f.method.VersionID)
	if f.method.Provider == payment.Stripe {
		endpoint = "/stripe/webhook/" + strconv.Itoa(f.method.ID)
	}
	req := httptest.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", signature)
	result := httptest.NewRecorder()
	f.router.ServeHTTP(result, req)
	return result
}
func (f *providerFixture) quota(t *testing.T) int {
	t.Helper()
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	return user.Quota
}

func TestUnifiedStripeCheckoutAndDurableConfirmation(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	f.start(t, false)
	assert.Equal(t, "payment", f.mode)
	f.paid = true
	f.discount = 50
	f.fail = true
	assert.Equal(t, http.StatusServiceUnavailable, f.webhook(t, "evt_retry", "checkout.session.completed").Code)
	assert.Zero(t, f.quota(t))
	var event model.PaymentEvent
	require.NoError(t, f.db.First(&event).Error)
	assert.Equal(t, model.PaymentEventPending, event.Status)
	f.fail = false
	f.revokedKey = f.config.Key
	f.config.Key = "sk_test_rotated"
	require.NoError(t, model.SavePaymentMethod(&f.method, f.config))
	require.NoError(t, f.db.Model(&event).Update("next_attempt_at", 0).Error)
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	assert.Equal(t, 200, f.quota(t))
	assert.Equal(t, http.StatusOK, f.webhook(t, "evt_duplicate", "checkout.session.async_payment_succeeded").Code)
	assert.Equal(t, 200, f.quota(t))
	order, _, err := model.GetPaymentOrder(context.Background(), f.orderNo)
	require.NoError(t, err)
	assert.EqualValues(t, 300, order.Amount)
	assert.EqualValues(t, 250, order.PaidAmount)
	f.paid = false
	f.discount = 0
	f.start(t, false)
	assert.Equal(t, "cus_fixture", f.sentCustomer)
}
func TestUnifiedStripeRejectsDifferentPriceAndSupportsFullDiscount(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	f.start(t, false)
	f.paid = true
	f.wrongPrice = true
	assert.Equal(t, http.StatusBadRequest, f.webhook(t, "evt_wrong", "checkout.session.completed").Code)
	assert.Zero(t, f.quota(t))
	f.wrongPrice = false
	f.discount = 300
	assert.Equal(t, http.StatusOK, f.webhook(t, "evt_free", "checkout.session.completed").Code)
	assert.Equal(t, 200, f.quota(t))
}
func TestUnifiedStripeSubscriptionInitialAndRenewalInvoices(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	plan := model.SubscriptionPlan{Id: 1, Title: "Recurring", Currency: "USD", PriceAmount: 10, StripePriceId: "price_plan", DurationUnit: "month", DurationValue: 1, TotalAmount: 1000, MaxPurchasePerUser: 1, Enabled: true}
	require.NoError(t, f.db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(1)
	f.start(t, true)
	assert.Equal(t, "subscription", f.mode)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_initial", "checkout.session.completed").Code)
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_initial_invoice", "invoice.paid").Code)
	var count int64
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	f.recurring = true
	f.invoiceID = "in_renewal"
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_renew", "invoice.paid").Code)
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_renew_duplicate", "invoice.paid").Code)
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	assert.Zero(t, f.quota(t))
	f.paid = false
	f.invoiceID = "in_failed"
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_failed", "invoice.payment_failed").Code)
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
func TestUnifiedCryptomusInvoiceStatesAndRecovery(t *testing.T) {
	f := newProviderFixture(t, payment.Cryptomus)
	f.start(t, false)
	for _, state := range []string{"wrong_amount", "locked", "confirm_check"} {
		f.cryptoStatus = state
		assert.Equal(t, http.StatusOK, f.webhook(t, "", "").Code)
		assert.Zero(t, f.quota(t))
	}
	f.cryptoStatus = "paid_over"
	f.fail = true
	assert.Equal(t, http.StatusServiceUnavailable, f.webhook(t, "", "").Code)
	assert.Zero(t, f.quota(t))
	f.fail = false
	require.NoError(t, f.db.Model(&model.PaymentEvent{}).Where("status = ?", model.PaymentEventPending).Update("next_attempt_at", 0).Error)
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	assert.Equal(t, 200, f.quota(t))
	assert.Equal(t, http.StatusOK, f.webhook(t, "", "").Code)
	assert.Equal(t, 200, f.quota(t))
}

func TestUnifiedCallbacksRejectInvalidSignatures(t *testing.T) {
	for _, provider := range []string{payment.Stripe, payment.Cryptomus} {
		t.Run(provider, func(t *testing.T) {
			f := newProviderFixture(t, provider)
			f.start(t, false)
			endpoint := "/notify/" + provider + "/" + strconv.Itoa(f.method.VersionID)
			if provider == payment.Stripe {
				endpoint = "/stripe/webhook/" + strconv.Itoa(f.method.ID)
			}
			request := httptest.NewRequest("POST", endpoint, strings.NewReader(`{"type":"payment","sign":"00000000000000000000000000000000"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Zero(t, f.quota(t))
			var count int64
			require.NoError(t, f.db.Model(&model.PaymentEvent{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestCryptomusPreservesSignedJSONAndRejectsDuplicateFields(t *testing.T) {
	f := newProviderFixture(t, payment.Cryptomus)
	f.start(t, false)
	unsigned := fmt.Sprintf(`{"type":"payment","uuid":%q,"order_id":%q,"amount":"2.00000000","currency":"USD","status":"confirm_check","additional_data":"路径\/item"}`, f.checkoutID, f.orderNo)
	for _, duplicate := range []bool{false, true} {
		body := unsigned
		if duplicate {
			body = strings.Replace(body, `"amount":"2.00000000"`, `"amount":"1.00","amount":"2.00000000"`, 1)
		}
		signed := strings.TrimSuffix(body, "}") + `,"sign":"` + cryptoSignature([]byte(body), f.config.Key) + `"}`
		request := httptest.NewRequest("POST", "/notify/cryptomus/"+strconv.Itoa(f.method.VersionID), strings.NewReader(signed))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		if duplicate {
			assert.Equal(t, http.StatusBadRequest, response.Code)
		} else {
			assert.Equal(t, http.StatusOK, response.Code)
		}
	}
	assert.Zero(t, f.quota(t))
}

func TestUnifiedStripeCurrencyPrecision(t *testing.T) {
	for _, tc := range []struct {
		currency string
		money    float64
	}{{"jpy", 300}, {"kwd", 0.3}} {
		t.Run(tc.currency, func(t *testing.T) {
			f := newProviderFixture(t, payment.Stripe)
			f.currency = tc.currency
			f.start(t, false)
			f.paid = true
			require.Equal(t, http.StatusOK, f.webhook(t, "evt_currency", "checkout.session.completed").Code)
			receipt := model.GetTopUpByTradeNo(f.orderNo)
			require.NotNil(t, receipt)
			assert.Equal(t, tc.money, receipt.Money)
			assert.Equal(t, 200, f.quota(t))
		})
	}
}
func TestUnifiedCryptomusSubscription(t *testing.T) {
	f := newProviderFixture(t, payment.Cryptomus)
	plan := model.SubscriptionPlan{Id: 1, Title: "Crypto plan", Currency: "USD", PriceAmount: 2, DurationUnit: "month", DurationValue: 1, TotalAmount: 1234, Enabled: true}
	require.NoError(t, f.db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(1)
	f.start(t, true)
	f.cryptoStatus = "paid"
	require.Equal(t, http.StatusOK, f.webhook(t, "", "").Code)
	require.Equal(t, http.StatusOK, f.webhook(t, "", "").Code)
	var subs []model.UserSubscription
	require.NoError(t, f.db.Find(&subs).Error)
	require.Len(t, subs, 1)
	assert.EqualValues(t, 1234, subs[0].AmountTotal)
	assert.Zero(t, f.quota(t))
}

func TestStripeWebhookConfigurationCanBeBootstrapped(t *testing.T) {
	f := newPaymentFixture(t)
	method := model.PaymentMethod{Name: "Stripe", Provider: payment.Stripe, Enabled: false}
	config := payment.Config{Key: "sk_test_setup", PriceID: "price_topup"}
	require.NoError(t, model.SavePaymentMethod(&method, config))
	first := f.request(t, "GET", "/methods", nil)
	endpoint := "https://gateway.example.com/api/payments/stripe/webhook/" + strconv.Itoa(method.ID)
	assert.Contains(t, first.Body.String(), endpoint)
	method.Enabled = true
	require.Error(t, model.SavePaymentMethod(&method, config))
	config.WebhookSecret = "whsec_configured"
	require.NoError(t, model.SavePaymentMethod(&method, config))
	second := f.request(t, "GET", "/methods", nil)
	assert.Contains(t, second.Body.String(), endpoint)
	assert.NotContains(t, second.Body.String(), config.Key)
	assert.NotContains(t, second.Body.String(), config.WebhookSecret)
}

func TestStripeSeparatesTestAndLiveCustomerBindings(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	f.start(t, false)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_test_binding", "checkout.session.completed").Code)
	f.config.Key = "sk_live_fixture"
	require.NoError(t, model.SavePaymentMethod(&f.method, f.config))
	f.paid = false
	f.start(t, false)
	assert.Empty(t, f.sentCustomer)
}
