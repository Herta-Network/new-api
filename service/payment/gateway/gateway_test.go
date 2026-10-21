// Partly ported from perfect-panel/server at commit 944dc363cc6a:
//   - the EPay signature vector of internal/payment/epay/epay_test.go
//   - cases of internal/gateway/gateway_test.go, internal/payment/money_test.go,
//     internal/payment/epay/epay_security_test.go,
//     internal/payment/cryptomus/cryptomus_test.go,
//     internal/payment/stripe/stripe_test.go and units_test.go,
//     internal/exchangerate/exchange_rate_test.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: one test file with testify; the fake Stripe API it
// uses lives in gatewaytest.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/payment/gateway/gatewaytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

func TestFormatAmount(t *testing.T) {
	for amount, want := range map[int64]string{
		0: "0.00", 5: "0.05", 58: "0.58", 100: "1.00", 1990: "19.90", 123456: "1234.56",
		-1: "-0.01", -1990: "-19.90",
		math.MaxInt64: "92233720368547758.07",
		math.MinInt64: "-92233720368547758.08",
	} {
		assert.Equal(t, want, FormatAmount(amount), "FormatAmount(%d)", amount)
	}
}

func TestParseAmountIsStrict(t *testing.T) {
	for value, want := range map[string]int64{
		"0": 0, "10": 1000, "10.1": 1010, "10.01": 1001, "0.58": 58, "92233720368547758.07": math.MaxInt64,
	} {
		got, err := ParseAmount(value)
		require.NoError(t, err, value)
		assert.Equal(t, want, got, value)
	}
	for _, value := range []string{
		"", "1.001", "-1.00", "+1", "1e2", " 1.00", "1.00 ", "1.", ".5", "1,00", "0x10", "92233720368547758.08",
	} {
		_, err := ParseAmount(value)
		assert.Error(t, err, "ParseAmount(%q)", value)
	}
}

func TestConvertAmountRoundsHalfAwayFromZero(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		rate   string
		want   int64
	}{
		{"identity", 1990, "1", 1990},
		{"exact product", 1000, "7.25", 7250},
		{"below half rounds down", 3, "7.125", 21},   // 21.375
		{"half rounds up", 7, "0.5", 4},              // 3.5
		{"half of a decimal rate", 5, "0.7", 4},      // 3.5 exactly, not 3.4999… of a binary 0.7
		{"negative half rounds down", -7, "0.5", -4}, // -3.5
		{"decimal rate stays exact", 10000, "7.1234", 71234},
		{"site to CNY", 1990, "7.2419", 14411}, // 14411.381
		{"small rate", 100000, "0.000139", 14}, // 13.9
		{"tiny amount", 1, "0.004", 0},
		{"zero", 0, "7.1", 0},
	}
	for _, tt := range tests {
		got, err := ConvertAmount(tt.amount, tt.rate)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, got, tt.name)
	}
	for _, rate := range []string{"", "0", "0.000", "-1", "+1", "1/3", "1e2", " 1", "1.", ".5", "abc", "NaN"} {
		_, err := ConvertAmount(100, rate)
		assert.Error(t, err, "ConvertAmount accepted rate %q", rate)
	}
	_, err := ConvertAmount(math.MaxInt64, "2")
	assert.Error(t, err, "a result beyond int64 must be rejected")
}

func TestValidateTradeNo(t *testing.T) {
	require.NoError(t, ValidateTradeNo("pi_3NfGx2LkdIwHu7ix0abc"))
	for _, tradeNo := range []string{"", " trade", "trade ", "tra\nde", "tra\x7fde", "\xff\xfe", strings.Repeat("x", 256)} {
		assert.Error(t, ValidateTradeNo(tradeNo), "%q", tradeNo)
	}
}

func TestNotifyURL(t *testing.T) {
	method := Method{Platform: PlatformEPay, Token: "tok"}
	for _, tt := range []struct {
		name, domain, site, want string
	}{
		{"payment domain wins", "https://pay.example.test/", "https://www.example.test", "https://pay.example.test"},
		{"domain with base path", "https://pay.example.test/newapi/", "", "https://pay.example.test/newapi"},
		{"bare domain is https", "pay.example.test", "", "https://pay.example.test"},
		{"site address fallback", "", "https://www.example.test/", "https://www.example.test"},
		{"site address with port", "", "http://panel.example.test:8080", "http://panel.example.test:8080"},
		{"public IP", "", "http://203.0.113.7:3000", "http://203.0.113.7:3000"},
	} {
		method.Domain = tt.domain
		got, err := NotifyURL(method, tt.site)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want+"/api/payment/notify/epay/tok", got, tt.name)
	}
	for _, tt := range []struct {
		name, domain, site string
	}{
		{"nothing configured", "", ""},
		{"localhost", "", "http://localhost:3000"},
		{"localhost subdomain", "https://pay.localhost", "https://www.example.test"},
		{"loopback IPv4", "", "http://127.0.0.1:3000"},
		{"loopback IPv6", "", "http://[::1]:3000"},
		{"wildcard IPv4", "", "0.0.0.0:3000"},
		{"wildcard IPv6", "", "http://[::]:3000"},
		{"user info", "", "https://user:pass@www.example.test"},
		{"query", "", "https://www.example.test/?x=1"},
		{"empty query", "", "https://www.example.test/?"},
		{"fragment", "", "https://www.example.test/#top"},
		{"other scheme", "", "ftp://www.example.test"},
		// An unusable domain is a configuration error, not a reason to send
		// the callback somewhere else.
		{"bad domain does not fall back", "http://127.0.0.1", "https://www.example.test"},
	} {
		method.Domain = tt.domain
		got, err := NotifyURL(method, tt.site)
		assert.ErrorIs(t, err, ErrNotifyAddress, tt.name)
		assert.Empty(t, got, tt.name)
	}
	_, err := NotifyURL(Method{Platform: PlatformEPay}, "https://www.example.test")
	assert.Error(t, err, "a method without a token has no notify URL")
}

func TestRegistryDescribesThePlatforms(t *testing.T) {
	registry := NewRegistry()
	for platform, want := range map[string]CallbackStyle{
		PlatformEPay:      {UniqueParams: true, TextReply: true},
		PlatformStripe:    {Body: true, SignatureHeader: "Stripe-Signature"},
		PlatformCryptomus: {Body: true, TextReply: true},
	} {
		style, ok := registry.CallbackStyle(platform)
		assert.True(t, ok, platform)
		assert.Equal(t, want, style, platform)
	}
	for _, platform := range []string{PlatformBalance, "paypal", "EPay", ""} {
		_, ok := registry.CallbackStyle(platform)
		assert.False(t, ok, platform)
		_, err := registry.Open(Method{Platform: platform, Config: "{}"})
		assert.ErrorIs(t, err, ErrNotGateway, platform)
		_, err = registry.NormalizeConfig(platform, []byte("{}"))
		assert.ErrorIs(t, err, ErrNotGateway, platform)
	}

	secrets := map[string][]string{}
	for _, info := range Platforms() {
		_, ok := registry.CallbackStyle(info.Platform)
		assert.True(t, ok, "described platform %s has no gateway", info.Platform)
		for _, field := range info.Fields {
			if field.Secret {
				secrets[info.Platform] = append(secrets[info.Platform], field.Key)
			}
		}
	}
	assert.Equal(t, map[string][]string{
		PlatformEPay:      {"key"},
		PlatformStripe:    {"secret_key", "webhook_secret"},
		PlatformCryptomus: {"api_key"},
	}, secrets)
}

func TestNormalizeConfig(t *testing.T) {
	registry := NewRegistry()
	valid := []struct {
		platform, raw, want string
	}{
		{PlatformEPay, `{"url":" https://pay.example/ ","pid":" 1001 ","key":" k ","type":"","extra":1}`,
			`{"url":"https://pay.example/","pid":"1001","key":"k","type":""}`},
		{PlatformEPay, `{"url":"http://pay.example/submit.php","pid":"1","key":"k","type":"alipay"}`,
			`{"url":"http://pay.example/submit.php","pid":"1","key":"k","type":"alipay"}`},
		{PlatformStripe, `{"public_key":"pk_test_a","secret_key":"sk_test_b"}`,
			`{"public_key":"pk_test_a","secret_key":"sk_test_b","webhook_endpoint_id":"","webhook_url":"","webhook_secret":""}`},
		{PlatformStripe, `{"public_key":"pk_live_a","secret_key":"rk_live_b"}`,
			`{"public_key":"pk_live_a","secret_key":"rk_live_b","webhook_endpoint_id":"","webhook_url":"","webhook_secret":""}`},
		{PlatformCryptomus, `{"merchant_id":" m ","api_key":" k "}`, `{"merchant_id":"m","api_key":"k"}`},
	}
	for _, tt := range valid {
		got, err := registry.NormalizeConfig(tt.platform, []byte(tt.raw))
		require.NoError(t, err, tt.raw)
		assert.Equal(t, tt.want, got)
	}
	invalid := map[string][]string{
		PlatformEPay: {
			`"not an object"`,
			`{"url":"ftp://pay.example","pid":"1","key":"k"}`,
			`{"url":"pay.example","pid":"1","key":"k"}`,
			`{"url":"https://pay.example/?pid=1","pid":"1","key":"k"}`,
			`{"url":"https://pay.example/#x","pid":"1","key":"k"}`,
			`{"url":"https://pay.example","pid":" ","key":"k"}`,
			`{"url":"https://pay.example","pid":"1","key":""}`,
		},
		PlatformStripe: {
			`{"public_key":"pk_test_a","secret_key":"sk_live_b"}`,
			`{"public_key":"pk_live_a","secret_key":"rk_test_b"}`,
			`{"public_key":"sk_test_a","secret_key":"sk_test_b"}`,
			`{"public_key":"pk_test_a","secret_key":"pk_test_b"}`,
			`{"public_key":"pk_test_","secret_key":"sk_test_b"}`,
			`{"public_key":"pk_test_a"}`,
		},
		PlatformCryptomus: {
			`{"merchant_id":"m"}`,
			`{"merchant_id":" ","api_key":"k"}`,
		},
	}
	for platform, raws := range invalid {
		for _, raw := range raws {
			_, err := registry.NormalizeConfig(platform, []byte(raw))
			assert.Error(t, err, "%s accepted %s", platform, raw)
		}
	}
	for platform, config := range map[string]string{
		PlatformEPay:      `{"url":"https://pay.example","pid":"","key":"k"}`,
		PlatformStripe:    `{"secret_key":"sk_test_b"}`,
		PlatformCryptomus: `{"api_key":"k"}`,
	} {
		_, err := registry.Open(Method{Platform: platform, Config: config})
		assert.Error(t, err, "%s: an incomplete configuration opened a gateway", platform)
	}
}

func TestChargeFor(t *testing.T) {
	registry := NewRegistry()
	epay := mustOpen(t, registry, Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example","pid":"1","key":"k"}`})
	stripeGW := mustOpen(t, registry, Method{Platform: PlatformStripe, Config: `{"public_key":"pk_test_a","secret_key":"sk_test_a"}`})

	tests := []struct {
		name     string
		gw       Gateway
		amount   int64
		currency string
		rate     string
		want     Charge
	}{
		{"CNY site needs no rate", epay, 1990, "cny", "", Charge{1990, "CNY"}},
		{"USD site converts to CNY", epay, 1990, "USD", "7.2419", Charge{14411, "CNY"}},
		{"Stripe collects the site currency", stripeGW, 1990, "usd", "", Charge{1990, "USD"}},
		// Stripe collects whole yen: the expectation is rounded to what it
		// can charge, so the amount it reports back matches.
		{"zero-decimal Stripe charge", stripeGW, 100050, "JPY", "", Charge{100100, "JPY"}},
		{"three-decimal Stripe charge", stripeGW, 1255, "KWD", "", Charge{1255, "KWD"}},
	}
	for _, tt := range tests {
		got, err := ChargeFor(tt.gw, tt.amount, tt.currency, tt.rate)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, got, tt.name)
	}
	_, err := ChargeFor(epay, 1990, "USD", "")
	assert.Error(t, err, "a missing rate must refuse the charge")
	_, err = ChargeFor(epay, 1990, "", "7.2")
	assert.Error(t, err, "a missing site currency must refuse the charge")
}

func TestStripeUnits(t *testing.T) {
	for _, tc := range []struct {
		currency           string
		hundredths, stripe int64
	}{
		{"usd", 1990, 1990},
		{"JPY", 100000, 1000},
		{"krw", 500000, 5000},
		{"KWD", 1250, 12500},
		{"ISK", 50000, 50000},
	} {
		assert.Equal(t, tc.stripe, toStripeAmount(tc.hundredths, tc.currency), tc.currency)
		assert.Equal(t, tc.hundredths, fromStripeAmount(tc.stripe, tc.currency), tc.currency)
	}
	for _, tc := range []struct {
		currency string
		in, want int64
	}{
		{"JPY", 100050, 100100},
		{"JPY", 100049, 100000},
		{"ISK", 12345, 12300},
		{"USD", 100050, 100050},
		{"KWD", 1255, 1255},
	} {
		got := roundStripeHundredths(tc.in, tc.currency)
		assert.Equal(t, tc.want, got, "%s %d", tc.currency, tc.in)
		assert.Equal(t, got, fromStripeAmount(toStripeAmount(got, tc.currency), tc.currency), "%s round trip", tc.currency)
	}
}

func mustOpen(t *testing.T, registry *Registry, method Method) Gateway {
	t.Helper()
	gw, err := registry.Open(method)
	require.NoError(t, err)
	return gw
}

// ---- EPay ----

// signEPay signs callback parameters the way the gateway does.
func signEPay(gw Gateway, params map[string]string) map[string]string {
	signed := maps.Clone(params)
	signed["sign"] = gw.(*epayGateway).sign(params)
	signed["sign_type"] = "MD5"
	return signed
}

// A callback signed by a real gateway for a known merchant key verifies.
func TestEPaySignatureVector(t *testing.T) {
	gw := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example","pid":"1654","key":"LbTabbB580zWyhXhyyww7wwvy5u8k0wl","type":"alipay"}`})
	params := map[string]string{
		"pid":          "1654",
		"trade_no":     "2024121521150860990",
		"out_trade_no": "202412152115078262977262254",
		"type":         "alipay",
		"name":         "product",
		"money":        "10",
		"trade_status": "TRADE_SUCCESS",
		"sign":         "d3181f18ebdf9821f0ab6ee93faa82d1",
		"sign_type":    "MD5",
	}
	notice, err := gw.ParseCallback(context.Background(), Notification{Params: params})
	require.NoError(t, err)
	assert.Equal(t, Notice{
		OrderNo: "202412152115078262977262254", TradeNo: "2024121521150860990", Amount: 1000, Currency: "CNY",
		Paid: true, Status: "TRADE_SUCCESS", Details: map[string]string{"type": "alipay"},
	}, *notice)

	params["money"] = "11"
	_, err = gw.ParseCallback(context.Background(), Notification{Params: params})
	assert.Error(t, err, "a changed amount must not verify")
	delete(params, "sign")
	params["money"] = "10"
	_, err = gw.ParseCallback(context.Background(), Notification{Params: params})
	assert.Error(t, err, "a missing signature must not verify")
}

func TestEPayCallbackChecks(t *testing.T) {
	ctx := context.Background()
	callback := map[string]string{
		"pid": "1001", "trade_no": "T1", "out_trade_no": "order-1", "type": "wxpay",
		"name": "Top-up", "money": "19.90", "trade_status": "TRADE_SUCCESS",
	}
	anyType := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example","pid":"1001","key":"secret"}`})
	alipay := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example","pid":"1001","key":"secret","type":"alipay"}`})

	// A method without a type lets the buyer choose one on the gateway.
	notice, err := anyType.ParseCallback(ctx, Notification{Params: signEPay(anyType, callback)})
	require.NoError(t, err)
	assert.Equal(t, int64(1990), notice.Amount)
	_, err = alipay.ParseCallback(ctx, Notification{Params: signEPay(alipay, callback)})
	assert.Error(t, err, "a method fixed to alipay must reject a wxpay payment")

	for field, value := range map[string]string{
		"pid": "2002", "trade_status": "WAIT_BUYER_PAY", "money": "19.901", "out_trade_no": " order-1", "trade_no": "",
	} {
		changed := maps.Clone(callback)
		changed[field] = value
		_, err := anyType.ParseCallback(ctx, Notification{Params: signEPay(anyType, changed)})
		assert.Error(t, err, "%s=%q", field, value)
	}
}

func TestEPayStartPaymentBuildsSignedRedirect(t *testing.T) {
	gw := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example/epay/submit.php","pid":"1001","key":"secret"}`})
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}
	action, err := gw.StartPayment(context.Background(), Checkout{
		Order: order, Charge: Charge{1990, "CNY"}, Subject: "Top-up",
		NotifyURL: "https://www.example.test/api/payment/notify/epay/tok", ReturnURL: "https://www.example.test/wallet?payment_order=order-1",
	})
	require.NoError(t, err)
	assert.Equal(t, ActionURL, action.Type)
	require.True(t, strings.HasPrefix(action.URL, "https://pay.example/epay/submit.php?"), action.URL)
	parsed, err := url.Parse(action.URL)
	require.NoError(t, err)
	params := map[string]string{}
	for key := range parsed.Query() {
		params[key] = parsed.Query().Get(key)
	}
	assert.Equal(t, "19.90", params["money"])
	assert.Equal(t, "order-1", params["out_trade_no"])
	assert.NotContains(t, params, "type", "an empty payment type is left out of the redirect")
	assert.Equal(t, "MD5", params["sign_type"])
	assert.Equal(t, gw.(*epayGateway).sign(params), params["sign"])

	_, err = gw.StartPayment(context.Background(), Checkout{Order: order, Charge: Charge{1990, "USD"}})
	assert.Error(t, err, "EPay collects only CNY")
}

// epayServer answers the EPay query endpoints with handler.
func epayServer(t *testing.T, config string, handler http.HandlerFunc) Gateway {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config = strings.ReplaceAll(config, "SERVER", server.URL)
	return mustOpen(t, NewRegistry(WithHTTPClient(server.Client())), Method{Platform: PlatformEPay, Config: config})
}

func TestEPayQueryFallsBackToEasyPay(t *testing.T) {
	var standard, fallback int
	gw := epayServer(t, `{"url":"SERVER/gateway","pid":"1001","key":"secret","type":"alipay"}`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway/api.php":
			standard++
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, url.Values{"act": {"order"}, "pid": {"1001"}, "key": {"secret"}, "out_trade_no": {"order-1"}}, r.URL.Query())
			http.NotFound(w, r)
		case "/gateway/api/EasyPay/queryOrder":
			fallback++
			assert.Equal(t, http.MethodPost, r.Method)
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, url.Values{"orderNo": {"order-1"}}, r.PostForm)
			_, _ = w.Write([]byte(`{"code":1,"msg":"query success","data":{"status":"success"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}
	notice := &Notice{OrderNo: "order-1", TradeNo: "T1", Amount: 1990, Currency: "CNY", Paid: true}
	require.NoError(t, gw.ConfirmPayment(context.Background(), order, notice))
	assert.Equal(t, 1, standard)
	assert.Equal(t, 1, fallback)

	// A status-only answer cannot settle an expired order: it names no trade.
	_, err := gw.Reconcile(context.Background(), CloseRequest{Order: order})
	assert.Error(t, err)
}

func TestEPayConfirmPayment(t *testing.T) {
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}
	paid := `{"code":1,"pid":1001,"trade_no":"T1","out_trade_no":"order-1","type":"wxpay","money":"19.90","status":1}`
	tests := []struct {
		name, methodType, body string
		status                 int
		wantErr                bool
	}{
		// The method lets the buyer choose: the queried type is not compared.
		{"untyped method accepts any type", "", paid, http.StatusOK, false},
		{"typed method rejects another type", "alipay", paid, http.StatusOK, true},
		{"pid as a string", "", strings.Replace(paid, `"pid":1001`, `"pid":"1001"`, 1), http.StatusOK, false},
		{"other merchant", "", strings.Replace(paid, `"pid":1001`, `"pid":1002`, 1), http.StatusOK, true},
		{"other amount", "", strings.Replace(paid, `19.90`, `19.00`, 1), http.StatusOK, true},
		{"other trade", "", strings.Replace(paid, `"T1"`, `"T2"`, 1), http.StatusOK, true},
		{"not paid", "", strings.Replace(paid, `"status":1`, `"status":0`, 1), http.StatusOK, true},
		{"lookup failed", "", `{"code":-1,"msg":"order not found"}`, http.StatusOK, true},
		{"gateway failure", "", `{}`, http.StatusInternalServerError, true},
		// An HTML answer means the gateway has no query API: the verified
		// signature is all there is.
		{"query not supported", "", `<html>502 Bad Gateway</html>`, http.StatusOK, false},
	}
	for _, tt := range tests {
		gw := epayServer(t, `{"url":"SERVER","pid":"1001","key":"secret","type":"`+tt.methodType+`"}`, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.body))
		})
		notice := &Notice{OrderNo: "order-1", TradeNo: "T1", Amount: 1990, Currency: "CNY", Paid: true}
		err := gw.ConfirmPayment(context.Background(), order, notice)
		if tt.wantErr {
			assert.Error(t, err, tt.name)
			continue
		}
		assert.NoError(t, err, tt.name)
		if tt.name == "query not supported" {
			assert.NotEmpty(t, notice.Details["confirmation"], "a signature-only confirmation is recorded for the log")
		}
	}
}

func TestEPayReconcile(t *testing.T) {
	young := time.Now()
	old := time.Now().Add(-2 * EPayUnpaidCloseAge)
	paid := `{"code":1,"pid":1001,"trade_no":"T1","out_trade_no":"order-1","type":"wxpay","money":"19.90","status":1}`
	unpaid := `{"code":1,"pid":1001,"out_trade_no":"order-1","status":0}`
	tests := []struct {
		name       string
		methodType string
		body       string
		createdAt  time.Time
		explicit   bool
		want       Reconciliation
		// wantErr is "unconfirmed" for ErrUnconfirmed and "error" for any
		// other failure.
		wantErr string
	}{
		{name: "paid", body: paid, createdAt: young, want: Reconciliation{TradeNo: "T1"}},
		{name: "paid in the configured type", methodType: "wxpay", body: paid, createdAt: young, want: Reconciliation{TradeNo: "T1"}},
		{name: "paid in another type", methodType: "alipay", body: paid, createdAt: young, wantErr: "error"},
		{name: "paid another amount", body: strings.Replace(paid, "19.90", "1.00", 1), createdAt: young, wantErr: "error"},
		{name: "unpaid within the close age", body: unpaid, createdAt: young, wantErr: "unconfirmed"},
		{name: "unpaid past the close age", body: unpaid, createdAt: old},
		{name: "unpaid explicit close", body: unpaid, createdAt: young, explicit: true},
		{name: "refunded", body: strings.Replace(unpaid, `"status":0`, `"status":2`, 1), createdAt: old, wantErr: "unconfirmed"},
		{name: "query failure", body: "", createdAt: old, wantErr: "unconfirmed"},
	}
	for _, tt := range tests {
		gw := epayServer(t, `{"url":"SERVER","pid":"1001","key":"secret","type":"`+tt.methodType+`"}`, func(w http.ResponseWriter, r *http.Request) {
			if tt.body == "" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(tt.body))
		})
		order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", CreatedAt: tt.createdAt}
		got, err := gw.Reconcile(context.Background(), CloseRequest{Order: order, Explicit: tt.explicit})
		switch tt.wantErr {
		case "unconfirmed":
			assert.ErrorIs(t, err, ErrUnconfirmed, tt.name)
		case "error":
			assert.Error(t, err, tt.name)
			assert.NotErrorIs(t, err, ErrUnconfirmed, tt.name)
		default:
			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.want, got, tt.name)
		}
	}

	// An explicit close of an order the gateway cannot answer for proceeds,
	// with a note for the log.
	gw := epayServer(t, `{"url":"SERVER","pid":"1001","key":"secret"}`, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	got, err := gw.Reconcile(context.Background(), CloseRequest{Order: &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}, Explicit: true})
	require.NoError(t, err)
	assert.Empty(t, got.TradeNo)
	assert.NotEmpty(t, got.Note)

	// A checkout that never started closes without asking the gateway, if
	// it is still not started when the close commits.
	got, err = gw.Reconcile(context.Background(), CloseRequest{Order: &Order{OrderNo: "order-1"}})
	require.NoError(t, err)
	assert.Equal(t, Reconciliation{RequireStableCheckout: true}, got)
}

// ---- Cryptomus ----

func TestCryptomusSignatures(t *testing.T) {
	gw := mustOpen(t, NewRegistry(), Method{Platform: PlatformCryptomus, Config: `{"merchant_id":"merchant-1","api_key":"api-key"}`})
	// md5(base64(body) + api_key), computed independently.
	assert.Equal(t, "e6f17e55e0b4565fc50e79548446eb90",
		gw.(*cryptomusGateway).signPayload([]byte(`{"amount":"10.00","currency":"USD","order_id":"order-1"}`)))

	// The webhook body is encoded by PHP, which escapes "/" as "\/" and
	// leaves unicode alone; the signature covers those bytes without the
	// trailing sign member, as computed independently.
	unsigned := `{"type":"payment","uuid":"5f7c0a3e-uuid","order_id":"order-1","amount":"10.00","payment_amount":"10.00",` +
		`"currency":"USD","payer_currency":"USDT","additional_data":"https:\/\/merchant.example\/wallet?plan=年付","status":"paid","is_final":true}`
	const sign = "42df8590b4d8b5505ac676d9238101bb"
	body := strings.TrimSuffix(unsigned, "}") + `,"sign":"` + sign + `"}`

	notice, err := gw.ParseCallback(context.Background(), Notification{Body: []byte(body)})
	require.NoError(t, err)
	assert.Equal(t, "order-1", notice.OrderNo)
	assert.Equal(t, "5f7c0a3e-uuid", notice.TradeNo)
	assert.Equal(t, int64(1000), notice.Amount)
	assert.Equal(t, "USD", notice.Currency)
	assert.True(t, notice.Paid)

	// Re-encoding the decoded payload loses the escaping, which is why the
	// verifier works on the raw bytes.
	var decoded map[string]any
	require.NoError(t, common.Unmarshal([]byte(unsigned), &decoded))
	reencoded, err := common.Marshal(decoded)
	require.NoError(t, err)
	require.NotContains(t, string(reencoded), `\/`)
	assert.NotEqual(t, sign, gw.(*cryptomusGateway).signPayload(reencoded))

	for name, forged := range map[string]string{
		"changed amount":   strings.Replace(body, `"amount":"10.00"`, `"amount":"1.00"`, 1),
		"unescaped slash":  strings.ReplaceAll(body, `\/`, `/`),
		"two sign members": strings.TrimSuffix(body, "}") + `,"sign":"` + sign + `"}`,
		"no sign":          unsigned,
		"sign first":       `{"sign":"` + sign + `",` + strings.TrimPrefix(unsigned, "{"),
	} {
		_, err := gw.ParseCallback(context.Background(), Notification{Body: []byte(forged)})
		assert.Error(t, err, name)
	}
	other := mustOpen(t, NewRegistry(), Method{Platform: PlatformCryptomus, Config: `{"merchant_id":"merchant-1","api_key":"other-key"}`})
	_, err = other.ParseCallback(context.Background(), Notification{Body: []byte(body)})
	assert.Error(t, err, "the signature is bound to the API key")
}

// cryptomusWebhook signs fields like the gateway: the signature covers the
// JSON without the sign member, which is appended last.
func cryptomusWebhook(t *testing.T, gw Gateway, fields map[string]any) Notification {
	t.Helper()
	unsigned, err := common.Marshal(fields)
	require.NoError(t, err)
	sign := gw.(*cryptomusGateway).signPayload(unsigned)
	return Notification{Body: []byte(strings.TrimSuffix(string(unsigned), "}") + `,"sign":"` + sign + `"}`)}
}

func TestCryptomusLifecycleNotices(t *testing.T) {
	gw := mustOpen(t, NewRegistry(), Method{Platform: PlatformCryptomus, Config: `{"merchant_id":"merchant-1","api_key":"api-key"}`})
	for _, tt := range []struct {
		status             string
		paid, manualReview bool
	}{
		{"paid", true, false},
		{"paid_over", true, false},
		{"check", false, false},
		{"cancel", false, false},
		{"wrong_amount", false, true},
		{"locked", false, true},
		{"refund_paid", false, true},
	} {
		notice, err := gw.ParseCallback(context.Background(), cryptomusWebhook(t, gw, map[string]any{
			"type": "payment", "uuid": "uuid-1", "order_id": "order-1", "amount": "10.00000000", "currency": "usd", "status": tt.status,
		}))
		require.NoError(t, err, tt.status)
		assert.Equal(t, tt.paid, notice.Paid, tt.status)
		assert.Equal(t, tt.manualReview, notice.ManualReview, tt.status)
		assert.Equal(t, int64(1000), notice.Amount, tt.status)
		assert.Equal(t, "USD", notice.Currency, tt.status)
	}
	for name, fields := range map[string]map[string]any{
		"unknown status": {"type": "payment", "uuid": "uuid-1", "order_id": "order-1", "amount": "10.00", "currency": "USD", "status": "mystery"},
		"wallet webhook": {"type": "wallet", "uuid": "uuid-1", "order_id": "order-1", "amount": "10.00", "currency": "USD", "status": "paid"},
		"bad amount":     {"type": "payment", "uuid": "uuid-1", "order_id": "order-1", "amount": "10.001", "currency": "USD", "status": "paid"},
		"no order":       {"type": "payment", "uuid": "uuid-1", "order_id": "", "amount": "10.00", "currency": "USD", "status": "paid"},
	} {
		_, err := gw.ParseCallback(context.Background(), cryptomusWebhook(t, gw, fields))
		assert.Error(t, err, name)
	}
	assert.Error(t, gw.CheckOrder(&Order{TradeNo: "uuid-2"}, &Notice{TradeNo: "uuid-1"}), "the order claimed another invoice")
	assert.NoError(t, gw.CheckOrder(&Order{}, &Notice{TradeNo: "uuid-1"}))
}

func TestParseCryptomusMoney(t *testing.T) {
	for value, want := range map[string]int64{"15": 1500, "15.00": 1500, "3.00000000": 300, "3.14000000": 314, "10.5": 1050} {
		got, err := parseCryptomusMoney(value)
		require.NoError(t, err, value)
		assert.Equal(t, want, got, value)
	}
	for _, value := range []string{"3.14159", "-1.00", "", "1e2"} {
		_, err := parseCryptomusMoney(value)
		assert.Error(t, err, value)
	}
}

// cryptomusAPI answers the invoice endpoints; it checks every request's
// merchant and signature headers against the exact bytes received.
func cryptomusServer(t *testing.T, handle func(path string, request map[string]any) string) Gateway {
	t.Helper()
	var gw Gateway
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "merchant-1", r.Header.Get("merchant"))
		assert.Equal(t, gw.(*cryptomusGateway).signPayload(body), r.Header.Get("sign"))
		var request map[string]any
		assert.NoError(t, common.Unmarshal(body, &request))
		answer := handle(r.URL.Path, request)
		if answer == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			answer = `{"state":1,"message":"Payment not found"}`
		}
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	gw = mustOpen(t, NewRegistry(WithHTTPClient(server.Client()), WithCryptomusBaseURL(server.URL)),
		Method{Platform: PlatformCryptomus, Config: `{"merchant_id":"merchant-1","api_key":"api-key"}`})
	return gw
}

func TestCryptomusStartPayment(t *testing.T) {
	invoice := `{"state":0,"result":{"uuid":"uuid-1","order_id":"order-1","amount":"10.50","currency":"USD","url":"https://pay.cryptomus.com/pay/uuid-1","status":"check"}}`
	var created []map[string]any
	gw := cryptomusServer(t, func(path string, request map[string]any) string {
		switch path {
		case "/v1/payment":
			created = append(created, request)
			return invoice
		case "/v1/payment/info":
			if request["uuid"] == "uuid-0" {
				return strings.ReplaceAll(invoice, "uuid-1", "uuid-0")
			}
			return invoice
		}
		return ""
	})
	order := &Order{OrderNo: "order-1", PaymentAmount: 1050, PaymentCurrency: "USD", ExpiresAt: time.Now().Add(15 * time.Minute)}
	checkout := Checkout{
		Order: order, Charge: Charge{1050, "usd"},
		NotifyURL: "https://www.example.test/api/payment/notify/cryptomus/tok", ReturnURL: "https://www.example.test/wallet",
		Claim: func(_ context.Context, tradeNo string) (string, error) { return tradeNo, nil },
	}
	action, err := gw.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	assert.Equal(t, Action{Type: ActionURL, URL: "https://pay.cryptomus.com/pay/uuid-1"}, *action)
	require.Len(t, created, 1)
	assert.Equal(t, "10.50", created[0]["amount"])
	assert.Equal(t, "USD", created[0]["currency"])
	assert.Equal(t, "order-1", created[0]["order_id"])
	assert.Equal(t, checkout.NotifyURL, created[0]["url_callback"])
	assert.InDelta(t, 900, created[0]["lifetime"], 5, "the invoice lives as long as the payment window")

	// A concurrent checkout claimed another invoice first: the payer gets
	// the winner's.
	checkout.Claim = func(context.Context, string) (string, error) { return "uuid-0", nil }
	action, err = gw.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	assert.Equal(t, "https://pay.cryptomus.com/pay/uuid-0", action.URL)
	// A claimed order reuses its invoice and creates none.
	order.TradeNo = "uuid-1"
	action, err = gw.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	assert.Equal(t, "https://pay.cryptomus.com/pay/uuid-1", action.URL)
	assert.Len(t, created, 2)
	// The invoice must charge exactly the recorded expectation.
	order.PaymentAmount = 1000
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err)
}

func TestCryptomusReconcile(t *testing.T) {
	invoice := func(status, paymentAmount string, final bool) string {
		return fmt.Sprintf(`{"state":0,"result":{"uuid":"uuid-1","order_id":"order-1","amount":"10.00","currency":"USD","payment_status":%q,"payment_amount":%q,"is_final":%t}}`,
			status, paymentAmount, final)
	}
	tests := []struct {
		name    string
		answer  string
		order   Order
		want    Reconciliation
		wantErr bool
	}{
		{name: "never started", order: Order{OrderNo: "order-1"}, want: Reconciliation{RequireStableCheckout: true}},
		{name: "paid", answer: invoice("paid", "10.00", true), want: Reconciliation{TradeNo: "uuid-1"}},
		{name: "paid over", answer: invoice("paid_over", "12.00", true), want: Reconciliation{TradeNo: "uuid-1"}},
		{name: "cancelled without funds", answer: invoice("cancel", "0.00000000", true), want: Reconciliation{RequireStableCheckout: true}},
		{name: "cancelled with funds", answer: invoice("cancel", "1.00", true), wantErr: true},
		{name: "awaiting payment", answer: invoice("check", "0", false), wantErr: true},
		{name: "underpaid", answer: invoice("wrong_amount", "5.00", true), wantErr: true},
		{name: "other amount", answer: strings.Replace(invoice("paid", "10.00", true), `"amount":"10.00"`, `"amount":"9.00"`, 1), wantErr: true},
		{name: "lookup failed", answer: "", wantErr: true},
	}
	for _, tt := range tests {
		gw := cryptomusServer(t, func(path string, request map[string]any) string {
			assert.Equal(t, "/v1/payment/info", path)
			assert.Equal(t, "uuid-1", request["uuid"])
			return tt.answer
		})
		order := tt.order
		if order.OrderNo == "" {
			order = Order{OrderNo: "order-1", PaymentAmount: 1000, PaymentCurrency: "USD", TradeNo: "uuid-1"}
		}
		got, err := gw.Reconcile(context.Background(), CloseRequest{Order: &order, Explicit: true})
		if tt.wantErr {
			assert.ErrorIs(t, err, ErrUnconfirmed, tt.name)
			continue
		}
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, got, tt.name)
	}

	// A checkout that crashed before claiming its invoice is found by order
	// number.
	gw := cryptomusServer(t, func(_ string, request map[string]any) string {
		assert.Equal(t, map[string]any{"order_id": "order-1"}, request)
		return invoice("paid", "10.00", true)
	})
	got, err := gw.Reconcile(context.Background(), CloseRequest{Order: &Order{OrderNo: "order-1", PaymentAmount: 1000, PaymentCurrency: "USD"}})
	require.NoError(t, err)
	assert.Equal(t, Reconciliation{TradeNo: "uuid-1"}, got)
}

func TestCryptomusConfirmPayment(t *testing.T) {
	answer := `{"state":0,"result":{"uuid":"uuid-1","order_id":"order-1","amount":"10.00","currency":"USD","status":"paid","is_final":true}}`
	gw := cryptomusServer(t, func(string, map[string]any) string { return answer })
	order := &Order{OrderNo: "order-1", PaymentAmount: 1000, PaymentCurrency: "USD", TradeNo: "uuid-1"}
	notice := &Notice{OrderNo: "order-1", TradeNo: "uuid-1", Amount: 1000, Currency: "USD", Paid: true}
	require.NoError(t, gw.ConfirmPayment(context.Background(), order, notice))
	for _, changed := range []string{
		strings.Replace(answer, `"status":"paid"`, `"status":"check"`, 1),
		strings.Replace(answer, `"order-1"`, `"order-2"`, 1),
		strings.Replace(answer, `"10.00"`, `"9.99"`, 1),
		strings.Replace(answer, `"USD"`, `"EUR"`, 1),
	} {
		answer = changed
		assert.Error(t, gw.ConfirmPayment(context.Background(), order, notice), changed)
	}
}

// ---- Stripe ----

const stripeTestConfig = `{"public_key":"pk_test_a","secret_key":"sk_test_a","webhook_secret":"whsec_test"}`

func openFakeStripe(t *testing.T, fake *gatewaytest.FakeStripe, methodID int64) Gateway {
	t.Helper()
	return mustOpen(t, NewRegistry(WithStripeBackends(fake.Backends)), Method{ID: methodID, Platform: PlatformStripe, Config: stripeTestConfig})
}

func claimAs(winner string, err error) func(context.Context, string) (string, error) {
	return func(_ context.Context, tradeNo string) (string, error) {
		if err != nil {
			return "", err
		}
		if winner == "" {
			return tradeNo, nil
		}
		return winner, nil
	}
}

func TestStripeStartPaymentCreatesAndReusesTheIntent(t *testing.T) {
	fake := gatewaytest.NewFakeStripe(t)
	gw := openFakeStripe(t, fake, 7)
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD"}
	checkout := Checkout{Order: order, Charge: Charge{1990, "USD"}, Subject: "Top-up", Claim: claimAs("", nil)}

	action, err := gw.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	assert.Equal(t, Action{Type: ActionStripe, Stripe: &StripeAction{PublishableKey: "pk_test_a", ClientSecret: "pi_1_secret"}}, *action)
	created, keys, _ := fake.Snapshot()
	require.Len(t, created, 1)
	assert.Equal(t, []string{"newapi:pi:order-1"}, keys)
	assert.Equal(t, "1990", created[0].Get("amount"))
	assert.Equal(t, "usd", created[0].Get("currency"))
	assert.Equal(t, "true", created[0].Get("automatic_payment_methods[enabled]"))
	assert.Equal(t, "order-1", created[0].Get("metadata[newapi_order_no]"))
	assert.Equal(t, "7", created[0].Get("metadata[newapi_method_id]"))
	assert.Equal(t, "Top-up", created[0].Get("description"))
	assert.False(t, created[0].Has("customer"), "no Stripe customer is attached")
	assert.False(t, created[0].Has("payment_method_types[0]"), "payment methods are chosen by Stripe")

	// A claimed order hands out the same client secret and creates nothing.
	order.TradeNo = "pi_1"
	action, err = gw.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	assert.Equal(t, "pi_1_secret", action.Stripe.ClientSecret)
	created, _, _ = fake.Snapshot()
	assert.Len(t, created, 1)

	// The stored intent must still match the order before it is reused.
	for name, changed := range map[string]Checkout{
		"other amount":   {Order: order, Charge: Charge{1000, "USD"}},
		"other currency": {Order: order, Charge: Charge{1990, "EUR"}},
		"other order":    {Order: &Order{OrderNo: "order-2", TradeNo: "pi_1"}, Charge: Charge{1990, "USD"}},
	} {
		_, err := gw.StartPayment(context.Background(), changed)
		assert.Error(t, err, name)
	}
	_, err = openFakeStripe(t, fake, 8).StartPayment(context.Background(), checkout)
	assert.Error(t, err, "another payment method's intent is not reused")
	fake.Set("pi_1", "status", "canceled")
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err, "a canceled intent is not offered again")
}

func TestStripeClaimFailureCancelsTheIntent(t *testing.T) {
	fake := gatewaytest.NewFakeStripe(t)
	gw := openFakeStripe(t, fake, 7)
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD"}
	checkout := Checkout{Order: order, Charge: Charge{1990, "USD"}, Claim: claimAs("", errors.New("database is down"))}

	_, err := gw.StartPayment(context.Background(), checkout)
	require.Error(t, err)
	_, _, canceled := fake.Snapshot()
	assert.Equal(t, []string{"pi_1"}, canceled)

	// A retry replays the idempotent creation; the replayed intent is the
	// cancelled one, which must not be handed out.
	checkout.Claim = claimAs("", nil)
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err)
	created, _, _ := fake.Snapshot()
	assert.Len(t, created, 1, "the retry creates no second intent")
}

func TestStripeClaimLostCancelsOwnIntent(t *testing.T) {
	fake := gatewaytest.NewFakeStripe(t)
	fake.Seed(gatewaytest.Intent("pi_winner", 1990, "usd", "requires_payment_method", "order-1", "7"))
	gw := openFakeStripe(t, fake, 7)
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD"}

	action, err := gw.StartPayment(context.Background(), Checkout{Order: order, Charge: Charge{1990, "USD"}, Claim: claimAs("pi_winner", nil)})
	require.NoError(t, err)
	assert.Equal(t, "pi_winner_secret", action.Stripe.ClientSecret)
	_, _, canceled := fake.Snapshot()
	assert.Equal(t, []string{"pi_1"}, canceled)
	assert.Equal(t, "requires_payment_method", fake.Status("pi_winner"))
}

func TestStripeChargesInStripeUnits(t *testing.T) {
	fake := gatewaytest.NewFakeStripe(t)
	gw := openFakeStripe(t, fake, 7)
	for i, charge := range []Charge{{100100, "JPY"}, {1250, "KWD"}} {
		order := &Order{OrderNo: fmt.Sprintf("order-%d", i), PaymentAmount: charge.Amount, PaymentCurrency: charge.Currency}
		_, err := gw.StartPayment(context.Background(), Checkout{Order: order, Charge: charge, Claim: claimAs("", nil)})
		require.NoError(t, err)
	}
	created, _, _ := fake.Snapshot()
	require.Len(t, created, 2)
	assert.Equal(t, "1001", created[0].Get("amount"), "whole yen")
	assert.Equal(t, "12500", created[1].Get("amount"), "fils")
}

// stripeEvent signs an event the way Stripe delivers it to an endpoint.
func stripeEvent(t *testing.T, secret, eventType string, intent map[string]any) Notification {
	t.Helper()
	payload, err := common.Marshal(map[string]any{
		"id": "evt_1", "object": "event", "type": eventType, "api_version": stripe.APIVersion,
		"data": map[string]any{"object": intent},
	})
	require.NoError(t, err)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: time.Now()})
	return Notification{HTTPMethod: http.MethodPost, Body: signed.Payload, Signature: signed.Header}
}

func TestStripeParseCallback(t *testing.T) {
	gw := openFakeStripe(t, gatewaytest.NewFakeStripe(t), 7)
	// Stripe counts yen: 1001 is 100100 hundredths.
	succeeded := gatewaytest.Intent("pi_1", 1001, "jpy", "succeeded", "order-1", "7")
	succeeded["amount_received"] = 1001
	failed := gatewaytest.Intent("pi_1", 1001, "jpy", "requires_payment_method", "order-1", "7")

	notice, err := gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_test", "payment_intent.succeeded", succeeded))
	require.NoError(t, err)
	assert.Equal(t, "order-1", notice.OrderNo)
	assert.Equal(t, "pi_1", notice.TradeNo)
	assert.Equal(t, int64(100100), notice.Amount, "amount_received in hundredths")
	assert.Equal(t, "JPY", notice.Currency)
	assert.True(t, notice.Paid)
	assert.False(t, notice.Ignore)

	notice, err = gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_test", "payment_intent.payment_failed", failed))
	require.NoError(t, err)
	assert.False(t, notice.Paid, "a failed payment is a lifecycle event")
	assert.False(t, notice.Ignore)
	assert.Equal(t, int64(100100), notice.Amount, "a lifecycle event reports the requested amount")

	for name, n := range map[string]Notification{
		"another site's intent":  stripeEvent(t, "whsec_test", "payment_intent.succeeded", gatewaytest.Intent("pi_2", 100, "usd", "succeeded", "", "")),
		"another payment method": stripeEvent(t, "whsec_test", "payment_intent.succeeded", gatewaytest.Intent("pi_3", 100, "usd", "succeeded", "order-1", "8")),
		"an unsubscribed event":  stripeEvent(t, "whsec_test", "charge.succeeded", map[string]any{"id": "ch_1", "object": "charge"}),
		"a canceled foreign one": stripeEvent(t, "whsec_test", "payment_intent.canceled", gatewaytest.Intent("pi_4", 100, "usd", "canceled", "", "")),
	} {
		notice, err := gw.ParseCallback(context.Background(), n)
		require.NoError(t, err, name)
		assert.True(t, notice.Ignore, name)
	}

	_, err = gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_other", "payment_intent.succeeded", succeeded))
	assert.Error(t, err, "an event signed with another secret is forged")
	unregistered := mustOpen(t, NewRegistry(), Method{ID: 7, Platform: PlatformStripe, Config: `{"public_key":"pk_test_a","secret_key":"sk_test_a"}`})
	_, err = unregistered.ParseCallback(context.Background(), stripeEvent(t, "", "payment_intent.succeeded", succeeded))
	assert.Error(t, err, "a method without a webhook secret verifies nothing")
}

func TestStripeConfirmPayment(t *testing.T) {
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD", TradeNo: "pi_1"}
	notice := &Notice{OrderNo: "order-1", TradeNo: "pi_1", Amount: 1990, Currency: "USD", Paid: true}
	tests := []struct {
		name    string
		change  map[string]any
		wantErr bool
	}{
		{name: "succeeded"},
		{name: "processing", change: map[string]any{"status": "processing"}, wantErr: true},
		{name: "partly received", change: map[string]any{"amount_received": 1000}, wantErr: true},
		{name: "other currency", change: map[string]any{"currency": "eur"}, wantErr: true},
		{name: "other order", change: map[string]any{"metadata": map[string]string{"newapi_order_no": "order-2", "newapi_method_id": "7"}}, wantErr: true},
		{name: "other method", change: map[string]any{"metadata": map[string]string{"newapi_order_no": "order-1", "newapi_method_id": "8"}}, wantErr: true},
	}
	for _, tt := range tests {
		fake := gatewaytest.NewFakeStripe(t)
		intent := gatewaytest.Intent("pi_1", 1990, "usd", "succeeded", "order-1", "7")
		intent["amount_received"] = 1990
		maps.Copy(intent, tt.change)
		fake.Seed(intent)
		err := openFakeStripe(t, fake, 7).ConfirmPayment(context.Background(), order, notice)
		if tt.wantErr {
			assert.Error(t, err, tt.name)
		} else {
			assert.NoError(t, err, tt.name)
		}
	}
}

func TestStripeReconcile(t *testing.T) {
	tests := []struct {
		status       string
		want         Reconciliation
		wantCanceled bool
		wantErr      error
	}{
		{status: "succeeded", want: Reconciliation{TradeNo: "pi_1"}},
		{status: "canceled"},
		{status: "requires_payment_method", wantCanceled: true},
		{status: "requires_action", wantCanceled: true},
		{status: "processing", wantErr: ErrUnconfirmed},
	}
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD", TradeNo: "pi_1"}
	for _, tt := range tests {
		fake := gatewaytest.NewFakeStripe(t)
		fake.Seed(gatewaytest.Intent("pi_1", 1990, "usd", tt.status, "order-1", "7"))
		got, err := openFakeStripe(t, fake, 7).Reconcile(context.Background(), CloseRequest{Order: order})
		_, _, canceled := fake.Snapshot()
		if tt.wantErr != nil {
			assert.ErrorIs(t, err, tt.wantErr, tt.status)
		} else {
			require.NoError(t, err, tt.status)
			assert.Equal(t, tt.want, got, tt.status)
		}
		assert.Equal(t, tt.wantCanceled, len(canceled) == 1, tt.status)
	}

	fake := gatewaytest.NewFakeStripe(t)
	gw := openFakeStripe(t, fake, 7)
	got, err := gw.Reconcile(context.Background(), CloseRequest{Order: &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD"}})
	require.NoError(t, err)
	assert.Equal(t, Reconciliation{RequireStableCheckout: true}, got, "no intent was claimed")
	fake.Seed(gatewaytest.Intent("pi_1", 1000, "usd", "succeeded", "order-1", "7"))
	_, err = gw.Reconcile(context.Background(), CloseRequest{Order: order})
	assert.Error(t, err, "an intent of another amount is not trusted")
}

// A user's "check payment" asks every gateway without touching the payment:
// an unpaid Stripe intent stays payable.
func TestQueryPaymentChangesNothing(t *testing.T) {
	ctx := context.Background()

	fake := gatewaytest.NewFakeStripe(t)
	fake.Seed(gatewaytest.Intent("pi_1", 1990, "usd", "requires_payment_method", "order-1", "7"))
	stripeGW := openFakeStripe(t, fake, 7)
	stripeOrder := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "USD", TradeNo: "pi_1"}
	tradeNo, err := stripeGW.QueryPayment(ctx, stripeOrder)
	require.NoError(t, err)
	assert.Empty(t, tradeNo)
	_, _, canceled := fake.Snapshot()
	assert.Empty(t, canceled, "a query never cancels the intent")
	fake.Set("pi_1", "status", "succeeded")
	fake.Set("pi_1", "amount_received", 1990)
	tradeNo, err = stripeGW.QueryPayment(ctx, stripeOrder)
	require.NoError(t, err)
	assert.Equal(t, "pi_1", tradeNo)
	tradeNo, err = stripeGW.QueryPayment(ctx, &Order{OrderNo: "order-2", PaymentAmount: 1990, PaymentCurrency: "USD"})
	require.NoError(t, err)
	assert.Empty(t, tradeNo, "no intent was claimed")

	epayAnswer := `{"code":1,"pid":1001,"trade_no":"T1","out_trade_no":"order-1","type":"alipay","money":"19.90","status":0}`
	epayGW := epayServer(t, `{"url":"SERVER","pid":"1001","key":"secret"}`, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(epayAnswer))
	})
	epayOrder := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}
	tradeNo, err = epayGW.QueryPayment(ctx, epayOrder)
	require.NoError(t, err)
	assert.Empty(t, tradeNo)
	epayAnswer = strings.Replace(epayAnswer, `"status":0`, `"status":1`, 1)
	tradeNo, err = epayGW.QueryPayment(ctx, epayOrder)
	require.NoError(t, err)
	assert.Equal(t, "T1", tradeNo)
	epayOrder.PaymentAmount = 2000
	_, err = epayGW.QueryPayment(ctx, epayOrder)
	assert.Error(t, err, "a payment of another amount is not reported as the order's")

	cryptomusAnswer := `{"state":0,"result":{"uuid":"uuid-1","order_id":"order-1","amount":"10.00","currency":"USD","payment_status":"check"}}`
	cryptomusGW := cryptomusServer(t, func(string, map[string]any) string { return cryptomusAnswer })
	cryptomusOrder := &Order{OrderNo: "order-1", PaymentAmount: 1000, PaymentCurrency: "USD", TradeNo: "uuid-1"}
	tradeNo, err = cryptomusGW.QueryPayment(ctx, cryptomusOrder)
	require.NoError(t, err)
	assert.Empty(t, tradeNo)
	cryptomusAnswer = strings.Replace(cryptomusAnswer, `"check"`, `"paid"`, 1)
	tradeNo, err = cryptomusGW.QueryPayment(ctx, cryptomusOrder)
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", tradeNo)
}

func TestStripeWebhookEndpoints(t *testing.T) {
	fake := gatewaytest.NewFakeStripe(t)
	webhooks := NewRegistry(WithStripeBackends(fake.Backends)).StripeWebhooks("sk_test_a")
	ctx := context.Background()

	endpoint, err := webhooks.Create(ctx, "https://www.example.test/api/payment/notify/stripe/tok")
	require.NoError(t, err)
	assert.Equal(t, StripeWebhookEndpoint{ID: "we_1", URL: "https://www.example.test/api/payment/notify/stripe/tok", Secret: "whsec_we_1"}, endpoint)
	registered := fake.Webhook("we_1")
	assert.Equal(t, stripe.APIVersion, registered["api_version"])
	assert.Equal(t, []string{"payment_intent.succeeded", "payment_intent.payment_failed", "payment_intent.canceled"}, registered["enabled_events"])

	fake.DisableWebhook("we_1")
	require.NoError(t, webhooks.UpdateURL(ctx, "we_1", "https://pay.example.test/api/payment/notify/stripe/tok"))
	assert.Equal(t, "https://pay.example.test/api/payment/notify/stripe/tok", fake.Webhook("we_1")["url"])
	assert.Equal(t, "enabled", fake.Webhook("we_1")["status"], "an endpoint Stripe disabled is enabled again")
	assert.Error(t, webhooks.UpdateURL(ctx, "we_404", "https://pay.example.test/x"))

	require.NoError(t, webhooks.Delete(ctx, "we_1"))
	assert.Nil(t, fake.Webhook("we_1"))
}

// ---- exchange rates ----

func TestExchangeRateFetch(t *testing.T) {
	var answer string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/currency_data/convert", r.URL.Path)
		assert.Equal(t, "access-key", r.Header.Get("apikey"))
		assert.Equal(t, url.Values{"from": {"USD"}, "to": {"CNY"}, "amount": {"1"}}, r.URL.Query())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	rates := NewRegistry(WithHTTPClient(server.Client()), WithExchangeRateBaseURL(server.URL)).ExchangeRates("access-key")
	ctx := context.Background()

	for result, want := range map[string]string{"7.1234": "7.1234", "7.10": "7.1", "1.39e-04": "0.000139", "7": "7"} {
		answer = `{"success":true,"query":{"from":"USD","to":"CNY","amount":1},"info":{"quote":1},"result":` + result + `}`
		got, err := rates.Fetch(ctx, RateQuery{From: "usd", To: "CNY"})
		require.NoError(t, err, result)
		assert.Equal(t, want, got, result)
	}
	for _, result := range []string{"0", "-7.1"} {
		answer = `{"success":true,"result":` + result + `}`
		_, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
		assert.ErrorIs(t, err, ErrImplausibleRate, result)
	}

	// Within the bound of the previous rate the new one is accepted; a jump
	// beyond it is not.
	answer = `{"success":true,"result":7.1234}`
	got, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "7", MaxChangeBP: 500})
	require.NoError(t, err)
	assert.Equal(t, "7.1234", got)
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "5", MaxChangeBP: 500})
	assert.ErrorIs(t, err, ErrImplausibleRate)
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "5"})
	assert.NoError(t, err, "a zero bound disables the check")

	for name, tt := range map[string]struct {
		status int
		answer string
	}{
		"unsuccessful": {http.StatusOK, `{"success":false,"error":{"code":201,"info":"invalid source currency"}}`},
		"unauthorized": {http.StatusUnauthorized, `{"message":"Invalid authentication credentials"}`},
		"not JSON":     {http.StatusOK, `<html></html>`},
		"no result":    {http.StatusOK, `{"success":true}`},
	} {
		status, answer = tt.status, tt.answer
		_, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
		assert.Error(t, err, name)
		assert.NotErrorIs(t, err, ErrImplausibleRate, name)
	}

	_, err = NewRegistry().ExchangeRates("").Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
	assert.Error(t, err, "without an API key there is no rate")
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "USD"})
	assert.Error(t, err)
}
