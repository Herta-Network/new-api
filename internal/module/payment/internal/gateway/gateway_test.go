// Partly ported from perfect-panel/server at commit 944dc363cc6a:
//   - the EPay signature vector of internal/payment/epay/epay_test.go
//   - cases of internal/gateway/gateway_test.go, internal/payment/money_test.go,
//     internal/payment/epay/epay_security_test.go,
//     internal/payment/cryptomus/cryptomus_test.go,
//     internal/payment/stripe/stripe_test.go and units_test.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: one test file with testify; the fake Stripe API it
// uses lives in gatewaytest; EPay API checkout with its payment forms, EPay
// V2 and telling phones by their User-Agent are covered.

package gateway

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway/gatewaytest"
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
		PlatformEPay:      {"key", "private_key"},
		PlatformStripe:    {"secret_key", "webhook_secret"},
		PlatformCryptomus: {"api_key"},
	}, secrets)

	// Each form offers exactly the choices a save accepts, and a field shown
	// only under a choice names one that exists.
	base := map[string]map[string]string{
		PlatformEPay: {"url": "https://pay.example", "pid": "1", "key": "k", "type": "alipay",
			"private_key": epayV2PrivatePEM(), "platform_public_key": epayV2PlatformPublicKey()},
		PlatformStripe: {"public_key": "pk_test_a", "secret_key": "sk_test_b"},
	}
	choices := map[string][]string{}
	for _, info := range Platforms() {
		for _, field := range info.Fields {
			if field.Type != FieldSelect {
				continue
			}
			for _, option := range field.Options {
				choices[info.Platform+"."+field.Key] = append(choices[info.Platform+"."+field.Key], option.Value)
				config := maps.Clone(base[info.Platform])
				config[field.Key] = option.Value
				raw, err := common.Marshal(config)
				require.NoError(t, err)
				_, err = registry.NormalizeConfig(info.Platform, raw)
				assert.NoError(t, err, "%s.%s=%s", info.Platform, field.Key, option.Value)
			}
		}
		for _, field := range info.Fields {
			for key, value := range field.When {
				assert.Contains(t, choices[info.Platform+"."+key], value, "%s.%s is shown under an unknown choice", info.Platform, field.Key)
			}
		}
	}
	assert.Equal(t, map[string][]string{
		"epay.version":               {EPayV2, EPayV1},
		"epay.checkout":              {EPayCheckoutAPI, EPayCheckoutPage},
		"epay.pay_form":              {EPayFormJump, EPayFormQRCode, EPayFormAuto},
		"stripe.payment_method_type": {"card", "alipay", "wechat_pay"},
	}, choices)
}

func TestNormalizeConfig(t *testing.T) {
	registry := NewRegistry()
	valid := []struct {
		platform, raw, want string
	}{
		// A configuration saved before versions and checkout modes speaks V1
		// through the payment page.
		{PlatformEPay, `{"url":" https://pay.example/ ","pid":" 1001 ","key":" k ","type":"","extra":1}`,
			`{"url":"https://pay.example/","pid":"1001","version":"v1","checkout":"page","key":"k","type":""}`},
		{PlatformEPay, `{"url":"http://pay.example/submit.php","pid":"1","key":"k","type":"alipay"}`,
			`{"url":"http://pay.example/submit.php","pid":"1","version":"v1","checkout":"page","key":"k","type":"alipay"}`},
		// Only the credentials of the version are kept; V2 keys are stored as
		// Base64 DER, also when PEM lost its line breaks in a one-line field.
		{PlatformEPay, `{"url":"https://pay.example","pid":"1","version":"v1","checkout":"api","key":"k","type":"wxpay","private_key":"x","platform_public_key":"y"}`,
			`{"url":"https://pay.example","pid":"1","version":"v1","checkout":"api","key":"k","type":"wxpay","pay_form":"jump"}`},
		// API orders ask for the payment page unless the method says
		// otherwise; the payment page has no payment form.
		{PlatformEPay, `{"url":"https://pay.example","pid":"1","checkout":"api","key":"k","type":"wxpay","pay_form":" auto "}`,
			`{"url":"https://pay.example","pid":"1","version":"v1","checkout":"api","key":"k","type":"wxpay","pay_form":"auto"}`},
		{PlatformEPay, `{"url":"https://pay.example","pid":"1","checkout":"page","key":"k","type":"","pay_form":"qrcode"}`,
			`{"url":"https://pay.example","pid":"1","version":"v1","checkout":"page","key":"k","type":""}`},
		{PlatformEPay, `{"url":"https://pay.example","pid":"1","version":"v2","checkout":"page","key":"k","type":"",` +
			`"private_key":"` + strings.ReplaceAll(epayV2PrivatePEM(), "\n", "") + `","platform_public_key":" ` + epayV2PlatformPublicKey() + `\n"}`,
			`{"url":"https://pay.example","pid":"1","version":"v2","checkout":"page","private_key":"` + epayV2PrivateDER() +
				`","platform_public_key":"` + epayV2PlatformPublicKey() + `","type":""}`},
		{PlatformStripe, `{"public_key":"pk_test_a","secret_key":"sk_test_b","payment_method_type":" wechat_pay "}`,
			`{"public_key":"pk_test_a","secret_key":"sk_test_b","payment_method_type":"wechat_pay","webhook_endpoint_id":"","webhook_url":"","webhook_secret":""}`},
		{PlatformStripe, `{"public_key":"pk_live_a","secret_key":"rk_live_b","payment_method_type":"card"}`,
			`{"public_key":"pk_live_a","secret_key":"rk_live_b","payment_method_type":"card","webhook_endpoint_id":"","webhook_url":"","webhook_secret":""}`},
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
			`{"url":"https://pay.example","pid":"1","key":"k","version":"v3"}`,
			`{"url":"https://pay.example","pid":"1","key":"k","checkout":"form"}`,
			// scan charges the buyer's payment code; it shows none.
			`{"url":"https://pay.example","pid":"1","key":"k","checkout":"api","type":"alipay","pay_form":"scan"}`,
			`{"url":"https://pay.example","pid":"1","version":"v2","platform_public_key":"` + epayV2PlatformPublicKey() + `"}`,
			`{"url":"https://pay.example","pid":"1","version":"v2","private_key":"` + epayV2PrivateDER() + `"}`,
			// The platform's public key is no private key, and a 1024-bit key
			// is too weak.
			`{"url":"https://pay.example","pid":"1","version":"v2","private_key":"` + epayV2PlatformPublicKey() + `","platform_public_key":"` + epayV2PlatformPublicKey() + `"}`,
			`{"url":"https://pay.example","pid":"1","version":"v2","private_key":"` + epayV2PrivateDER() + `","platform_public_key":"` + weakPublicKey() + `"}`,
		},
		PlatformStripe: {
			`{"public_key":"pk_test_a","secret_key":"sk_live_b","payment_method_type":"card"}`,
			`{"public_key":"pk_live_a","secret_key":"rk_test_b","payment_method_type":"card"}`,
			`{"public_key":"sk_test_a","secret_key":"sk_test_b","payment_method_type":"card"}`,
			`{"public_key":"pk_test_a","secret_key":"pk_test_b","payment_method_type":"card"}`,
			`{"public_key":"pk_test_","secret_key":"sk_test_b","payment_method_type":"card"}`,
			`{"public_key":"pk_test_a","payment_method_type":"card"}`,
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
	// The payment method type is a message the administrator reads.
	for _, raw := range []string{
		`{"public_key":"pk_test_a","secret_key":"sk_test_b"}`,
		`{"public_key":"pk_test_a","secret_key":"sk_test_b","payment_method_type":"paypal"}`,
		`{"public_key":"pk_test_a","secret_key":"sk_test_b","payment_method_type":"Card"}`,
	} {
		_, err := registry.NormalizeConfig(PlatformStripe, []byte(raw))
		var message *common.Message
		require.ErrorAs(t, err, &message, raw)
		assert.Equal(t, "Select the Stripe payment method type: card, Alipay or WeChat Pay", message.Key)
	}
	// An API checkout cannot let the buyer choose the payment type, which
	// the administrator reads.
	_, err := registry.NormalizeConfig(PlatformEPay, []byte(`{"url":"https://pay.example","pid":"1","key":"k","checkout":"api","type":" "}`))
	var message *common.Message
	require.ErrorAs(t, err, &message)
	assert.Equal(t, "EPay API checkout needs a payment type", message.Key)
	for platform, config := range map[string]string{
		PlatformEPay:      `{"url":"https://pay.example","pid":"","key":"k"}`,
		PlatformStripe:    `{"public_key":"pk_test_a","secret_key":"sk_test_b"}`,
		PlatformCryptomus: `{"api_key":"k"}`,
	} {
		_, err := registry.Open(Method{Platform: platform, Config: config})
		assert.Error(t, err, "%s: an incomplete configuration opened a gateway", platform)
	}
}

func TestChargeFor(t *testing.T) {
	registry := NewRegistry()
	epay := mustOpen(t, registry, Method{Platform: PlatformEPay, Config: `{"url":"https://pay.example","pid":"1","key":"k"}`})
	stripeGW := mustOpen(t, registry, Method{Platform: PlatformStripe, Config: `{"public_key":"pk_test_a","secret_key":"sk_test_a","payment_method_type":"card"}`})

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

// epayRequest reads the single-valued parameters of a request to a fake
// gateway: the query of a GET, the form of a POST.
func epayRequest(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	require.NoError(t, r.ParseForm())
	params := map[string]string{}
	for key, values := range r.Form {
		require.Len(t, values, 1, key)
		params[key] = values[0]
	}
	return params
}

// An API checkout creates the payment at mapi.php for the buyer's address
// and turns the one way to pay the gateway answers into the buyer's action.
// A gateway that refuses or fails is noticed before the buyer leaves.
func TestEPayV1APICheckout(t *testing.T) {
	checkout := Checkout{
		Order: &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", ClientIP: "203.0.113.9"}, Charge: Charge{1990, "CNY"},
		Subject:   strings.Repeat("会员", 30),
		NotifyURL: "https://www.example.test/api/payment/notify/epay/tok", ReturnURL: "https://www.example.test/wallet?payment_order=order-1",
	}
	tests := []struct {
		name, answer string
		status       int
		want         *Action
	}{
		{"payment page", `{"code":1,"trade_no":"T1","payurl":"https://pay.example/pay/T1"}`, http.StatusOK, &Action{Type: ActionURL, URL: "https://pay.example/pay/T1"}},
		{"QR code with a string code", `{"code":"1","trade_no":20240101,"qrcode":"weixin://wxpay/bizpayurl?pr=abc"}`, http.StatusOK, &Action{Type: ActionQRCode, QRCode: "weixin://wxpay/bizpayurl?pr=abc"}},
		{"app link", `{"code":1,"trade_no":"T1","urlscheme":"alipays://platformapi/startapp?appId=1"}`, http.StatusOK, &Action{Type: ActionURL, URL: "alipays://platformapi/startapp?appId=1"}},
		{"refused", `{"code":-1,"msg":"merchant disabled"}`, http.StatusOK, nil},
		{"gateway failure", `bad gateway`, http.StatusBadGateway, nil},
		{"no trade", `{"code":1,"payurl":"https://pay.example/pay/T1"}`, http.StatusOK, nil},
		{"two ways to pay", `{"code":1,"trade_no":"T1","payurl":"https://pay.example/pay/T1","qrcode":"x"}`, http.StatusOK, nil},
		{"script page", `{"code":1,"trade_no":"T1","payurl":"javascript:alert(1)"}`, http.StatusOK, nil},
		{"script app link", `{"code":1,"trade_no":"T1","urlscheme":"javascript:alert(1)"}`, http.StatusOK, nil},
	}
	for _, tt := range tests {
		var sent map[string]string
		gw := epayServer(t, `{"url":"SERVER/submit.php","pid":"1001","key":"secret","version":"v1","checkout":"api","type":"wxpay"}`, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/mapi.php", r.URL.Path)
			sent = epayRequest(t, r)
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.answer))
		})
		action, err := gw.StartPayment(context.Background(), checkout)
		if tt.want == nil {
			assert.Error(t, err, tt.name)
			continue
		}
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, action, tt.name)
		assert.Equal(t, "19.90", sent["money"])
		assert.Equal(t, "203.0.113.9", sent["clientip"])
		assert.Equal(t, "jump", sent["device"])
		assert.Equal(t, "wxpay", sent["type"])
		assert.Equal(t, strings.Repeat("会员", 21), sent["name"], "the name is cut to the 127 bytes the gateway keeps")
		assert.Equal(t, "MD5", sent["sign_type"])
		assert.Equal(t, gw.(*epayGateway).sign(sent), sent["sign"])
	}

	gw := epayServer(t, `{"url":"SERVER","pid":"1001","key":"secret","version":"v1","checkout":"api","type":"wxpay"}`, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an order without the buyer's address must not reach the gateway")
	})
	_, err := gw.StartPayment(context.Background(), Checkout{Order: &Order{OrderNo: "order-1"}, Charge: Charge{1990, "CNY"}})
	assert.Error(t, err)
}

// ---- EPay V2 ----

// epayV2Keys are the merchant's and the platform's RSA keys of the EPay V2
// tests.
var epayV2TestKeys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		keys[i] = key
	}
	return keys
})

func epayV2PrivateDER() string {
	der, err := x509.MarshalPKCS8PrivateKey(epayV2TestKeys()[0])
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// epayV2PrivatePEM is the merchant's private key as a PEM file holds it.
func epayV2PrivatePEM() string {
	der, _ := base64.StdEncoding.DecodeString(epayV2PrivateDER())
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func epayV2PlatformPublicKey() string {
	der, err := x509.MarshalPKIXPublicKey(&epayV2TestKeys()[1].PublicKey)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func weakPublicKey() string {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func epayV2Config(checkout, paymentType string) string {
	return `{"url":"SERVER","pid":"1001","version":"v2","checkout":"` + checkout + `","type":"` + paymentType +
		`","private_key":"` + epayV2PrivateDER() + `","platform_public_key":"` + epayV2PlatformPublicKey() + `"}`
}

// epayV2FormConfig is an EPay V2 method of API orders in the payment form.
func epayV2FormConfig(form string) string {
	return strings.Replace(epayV2Config(EPayCheckoutAPI, "alipay"), `"checkout":"api"`, `"checkout":"api","pay_form":"`+form+`"`, 1)
}

// signEPayV2 signs params with key, adding the current timestamp and the
// RSA sign_type unless params carry them.
func signEPayV2(t *testing.T, key *rsa.PrivateKey, params map[string]string) map[string]string {
	t.Helper()
	signed := maps.Clone(params)
	if _, ok := signed["timestamp"]; !ok {
		signed["timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)
	}
	if _, ok := signed["sign_type"]; !ok {
		signed["sign_type"] = "RSA"
	}
	canonical, err := epayV2Canonical(signed)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(canonical))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	signed["sign"] = base64.StdEncoding.EncodeToString(signature)
	return signed
}

// verifiesEPayV2 reports whether params carry a valid signature of key.
func verifiesEPayV2(t *testing.T, key *rsa.PublicKey, params map[string]string) bool {
	t.Helper()
	canonical, err := epayV2Canonical(params)
	require.NoError(t, err)
	signature, err := base64.StdEncoding.DecodeString(params["sign"])
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(canonical))
	return params["sign_type"] == "RSA" && rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) == nil
}

// epayV2Server is an EPay V2 gateway, served over HTTPS, that checks every
// request's merchant signature and answers with what answer returns for its
// API path, signed with the platform's key when sign is set. Integer values
// are sent as JSON numbers, and a blank sign_type is left out, as gateways
// that default it to RSA answer.
func epayV2Server(t *testing.T, config string, answer func(path string, params map[string]string) (fields map[string]string, sign bool)) Gateway {
	t.Helper()
	keys := epayV2TestKeys()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := epayRequest(t, r)
		assert.True(t, verifiesEPayV2(t, &keys[0].PublicKey, params), "the request is signed by the merchant")
		assert.Equal(t, "1001", params["pid"])
		fields, sign := answer(r.URL.Path, params)
		if sign {
			fields = signEPayV2(t, keys[1], fields)
		}
		body := map[string]any{}
		for key, value := range fields {
			if key == "sign_type" && value == "" {
				continue
			}
			if number, err := strconv.Atoi(value); err == nil && key != "trade_no" && key != "out_trade_no" && key != "timestamp" {
				body[key] = number
				continue
			}
			body[key] = value
		}
		raw, err := common.Marshal(body)
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	config = strings.ReplaceAll(config, "SERVER", server.URL)
	return mustOpen(t, NewRegistry(WithHTTPClient(server.Client())), Method{Platform: PlatformEPay, Config: config})
}

// The V2 signature covers the scalar parameters except sign, sign_type and
// blank values, in name order, with their bytes as they are.
func TestEPayV2Canonical(t *testing.T) {
	canonical, err := epayV2Canonical(map[string]string{
		"name": "会员 A&B", "money": "1.00", "fee_mode": "0", "param": "", "blank": " \t\r\n", "sign_type": "RSA", "sign": "x",
	})
	require.NoError(t, err)
	assert.Equal(t, "fee_mode=0&money=1.00&name=会员 A&B", canonical)
	_, err = epayV2Canonical(map[string]string{"images[0]": "x"})
	assert.Error(t, err, "a parameter PHP reads as an array cannot be signed as a scalar")
}

// The payment page and an API order are signed with the merchant's key; a
// signed answer must carry the gateway's valid, fresh signature, whose
// sign_type defaults to RSA. NiuNiu Pay answers a payment creation without
// any signature, which is taken over HTTPS only.
func TestEPayV2Checkout(t *testing.T) {
	keys := epayV2TestKeys()
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", ClientIP: "203.0.113.9"}
	checkout := Checkout{Order: order, Charge: Charge{1990, "CNY"}, Subject: "Top-up",
		NotifyURL: "https://www.example.test/api/payment/notify/epay/tok", ReturnURL: "https://www.example.test/wallet?payment_order=order-1"}

	page := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: strings.ReplaceAll(epayV2Config("page", ""), "SERVER", "https://pay.example")})
	action, err := page.StartPayment(context.Background(), checkout)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(action.URL, "https://pay.example/api/pay/submit?"), action.URL)
	parsed, err := url.Parse(action.URL)
	require.NoError(t, err)
	params := map[string]string{}
	for key := range parsed.Query() {
		params[key] = parsed.Query().Get(key)
	}
	assert.True(t, verifiesEPayV2(t, &keys[0].PublicKey, params))
	assert.Equal(t, "19.90", params["money"])
	assert.Equal(t, "1001", params["pid"])
	assert.NotContains(t, params, "type", "an empty payment type lets the buyer choose")

	created := map[string]string{"code": "0", "msg": "", "trade_no": "T1", "pay_type": "jump", "pay_info": "https://pay.example/pay/T1"}
	tests := []struct {
		name   string
		answer map[string]string
		sign   bool
		want   *Action
	}{
		{"payment page", created, true, &Action{Type: ActionURL, URL: "https://pay.example/pay/T1"}},
		{"QR code", withField(created, "pay_type", "qrcode", "pay_info", "https://qr.alipay.com/abc"), true, &Action{Type: ActionQRCode, QRCode: "https://qr.alipay.com/abc"}},
		{"no signature type", withField(created, "sign_type", ""), true, &Action{Type: ActionURL, URL: "https://pay.example/pay/T1"}},
		{"another signature type", withField(created, "sign_type", "MD5"), true, nil},
		{"unsigned success, as NiuNiu Pay answers", map[string]string{"code": "0", "trade_no": "2026102522575628450", "pay_type": "jump", "pay_info": "https://pay.example/pay/submit/2026102522575628450/"},
			false, &Action{Type: ActionURL, URL: "https://pay.example/pay/submit/2026102522575628450/"}},
		{"signed without a signature", withField(created, "sign", ""), false, nil},
		{"stale signature", withField(created, "timestamp", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)), true, nil},
		{"another merchant", withField(created, "pid", "2002"), true, nil},
		{"refused unsigned", map[string]string{"code": "-1", "msg": "channel closed"}, false, nil},
		{"HTML payment", withField(created, "pay_type", "html", "pay_info", "<form></form>"), true, nil},
	}
	for _, tt := range tests {
		var sent map[string]string
		gw := epayV2Server(t, epayV2Config("api", "alipay"), func(path string, params map[string]string) (map[string]string, bool) {
			assert.Equal(t, "/api/pay/create", path)
			sent = params
			return tt.answer, tt.sign
		})
		action, err := gw.StartPayment(context.Background(), checkout)
		if tt.want == nil {
			assert.Error(t, err, tt.name)
			continue
		}
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, action, tt.name)
		assert.Equal(t, "jump", sent["method"])
		assert.Equal(t, "203.0.113.9", sent["clientip"])
		assert.Equal(t, "alipay", sent["type"])
	}

	// A signature that does not verify is not trusted, whatever it says.
	gw := epayServer(t, epayV2Config("api", "alipay"), func(w http.ResponseWriter, r *http.Request) {
		signed := signEPayV2(t, keys[1], created)
		signed["pay_info"] = "https://evil.example/pay"
		raw, err := common.Marshal(signed)
		require.NoError(t, err)
		_, _ = w.Write(raw)
	})
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err)

	// Over plain HTTP an unsigned answer may come from anyone on the way.
	plain := epayServer(t, epayV2Config("api", "alipay"), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"trade_no":"T1","pay_type":"jump","pay_info":"https://evil.example/pay"}`))
	})
	_, err = plain.StartPayment(context.Background(), checkout)
	assert.ErrorContains(t, err, "HTTPS")
}

// An API order asks for the payment form its method sets: the payment page
// (V1 device=jump, V2 method=jump), what a computer pays with (V1 device=pc,
// V2 method=web with device=pc), or by the buyer's device. A method saved
// before payment forms existed asks for the page. Asked for a QR code, the
// buyer gets one whatever link the gateway answers.
func TestEPayAPIPaymentForm(t *testing.T) {
	checkout := Checkout{Order: &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", ClientIP: "203.0.113.9"}, Charge: Charge{1990, "CNY"},
		NotifyURL: "https://www.example.test/api/payment/notify/epay/tok"}
	const page = "https://pay.example/pay/T1"
	v1Config := func(form string) string {
		return `{"url":"SERVER","pid":"1001","key":"secret","version":"v1","checkout":"api","type":"wxpay","pay_form":"` + form + `"}`
	}
	tests := []struct {
		form   string
		mobile bool
		// v1 is the device asked of mapi.php, v2 the method and device asked
		// of api/pay/create.
		v1 string
		v2 [2]string
	}{
		{"", false, "jump", [2]string{"jump", ""}},
		{EPayFormJump, false, "jump", [2]string{"jump", ""}},
		{EPayFormJump, true, "jump", [2]string{"jump", ""}},
		{EPayFormQRCode, false, "pc", [2]string{"web", "pc"}},
		{EPayFormQRCode, true, "pc", [2]string{"web", "pc"}},
		{EPayFormAuto, false, "pc", [2]string{"web", "pc"}},
		{EPayFormAuto, true, "jump", [2]string{"jump", ""}},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("form %q, mobile %v", tt.form, tt.mobile)
		checkout.Mobile = tt.mobile
		want := &Action{Type: ActionURL, URL: page}
		if tt.v1 == "pc" {
			want = &Action{Type: ActionQRCode, QRCode: page}
		}

		var v1Sent map[string]string
		v1 := epayServer(t, v1Config(tt.form), func(w http.ResponseWriter, r *http.Request) {
			v1Sent = epayRequest(t, r)
			_, _ = w.Write([]byte(`{"code":1,"trade_no":"T1","payurl":"` + page + `"}`))
		})
		action, err := v1.StartPayment(context.Background(), checkout)
		require.NoError(t, err, name)
		assert.Equal(t, want, action, name)
		assert.Equal(t, tt.v1, v1Sent["device"], name)
		assert.Equal(t, "19.90", v1Sent["money"], name)
		assert.Equal(t, v1.(*epayGateway).sign(v1Sent), v1Sent["sign"], name)

		var v2Sent map[string]string
		v2 := epayV2Server(t, epayV2FormConfig(tt.form), func(_ string, params map[string]string) (map[string]string, bool) {
			v2Sent = params
			return map[string]string{"code": "0", "trade_no": "T1", "pay_type": "jump", "pay_info": page}, true
		})
		action, err = v2.StartPayment(context.Background(), checkout)
		require.NoError(t, err, name)
		assert.Equal(t, want, action, name)
		assert.Equal(t, tt.v2, [2]string{v2Sent["method"], v2Sent["device"]}, name)
		assert.Equal(t, "19.90", v2Sent["money"], name)
	}

	// Links are checked as for the payment page, and HTML is still refused.
	checkout.Mobile = false
	answers := []struct {
		name string
		// v1 is what mapi.php answers, if V1 has the answer; v2 the pay_type
		// and pay_info of api/pay/create.
		v1   string
		v2   [2]string
		want *Action
	}{
		{"QR code", `{"code":1,"trade_no":"T1","qrcode":"weixin://wxpay/bizpayurl?pr=abc"}`, [2]string{"qrcode", "weixin://wxpay/bizpayurl?pr=abc"},
			&Action{Type: ActionQRCode, QRCode: "weixin://wxpay/bizpayurl?pr=abc"}},
		{"app link", `{"code":1,"trade_no":"T1","urlscheme":"alipays://platformapi/startapp?appId=1"}`, [2]string{"urlscheme", "alipays://platformapi/startapp?appId=1"},
			&Action{Type: ActionQRCode, QRCode: "alipays://platformapi/startapp?appId=1"}},
		{"script page", `{"code":1,"trade_no":"T1","payurl":"javascript:alert(1)"}`, [2]string{"jump", "javascript:alert(1)"}, nil},
		{"script app link", `{"code":1,"trade_no":"T1","urlscheme":"javascript:alert(1)"}`, [2]string{"urlscheme", "javascript:alert(1)"}, nil},
		{"page that is no web address", `{"code":1,"trade_no":"T1","payurl":"alipays://platformapi/startapp"}`, [2]string{"jump", "alipays://platformapi/startapp"}, nil},
		{"HTML", "", [2]string{"html", "<form></form>"}, nil},
	}
	for _, tt := range answers {
		var gateways []Gateway
		if tt.v1 != "" {
			gateways = append(gateways, epayServer(t, v1Config(EPayFormQRCode), func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.v1))
			}))
		}
		gateways = append(gateways, epayV2Server(t, epayV2FormConfig(EPayFormQRCode), func(string, map[string]string) (map[string]string, bool) {
			return map[string]string{"code": "0", "trade_no": "T1", "pay_type": tt.v2[0], "pay_info": tt.v2[1]}, true
		}))
		for _, gw := range gateways {
			action, err := gw.StartPayment(context.Background(), checkout)
			if tt.want == nil {
				assert.Error(t, err, tt.name)
				continue
			}
			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.want, action, tt.name)
		}
	}
}

func TestMobileUserAgent(t *testing.T) {
	for userAgent, mobile := range map[string]bool{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1":                                                      true,
		"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36":                                                                              true,
		"Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36":                                                                               true,
		"Mozilla/5.0 (iPad; CPU OS 12_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/12.1.2 Mobile/15E148 Safari/604.1":                                                             true,
		"Mozilla/5.0 (Phone; OpenHarmony 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 ArkWeb/4.1.6.1":                                                                    true,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.50(0x1800323c) NetType/WIFI":                                 true,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36":                                                                              false,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15":                                                                        false,
		"Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/81.0.4044.138 Safari/537.36 NetType/WIFI MicroMessenger/7.0.20.1781(0x6700143B) WindowsWechat(0x63090c11)": false,
		"": false,
	} {
		assert.Equal(t, mobile, MobileUserAgent(userAgent), userAgent)
	}
}

func withField(fields map[string]string, pairs ...string) map[string]string {
	changed := maps.Clone(fields)
	for i := 0; i < len(pairs); i += 2 {
		changed[pairs[i]] = pairs[i+1]
	}
	return changed
}

func TestEPayV2Callback(t *testing.T) {
	ctx := context.Background()
	platform := epayV2TestKeys()[1]
	gw := mustOpen(t, NewRegistry(), Method{Platform: PlatformEPay, Config: strings.ReplaceAll(epayV2Config("api", "alipay"), "SERVER", "https://pay.example")})
	callback := map[string]string{
		"pid": "1001", "trade_no": "T1", "out_trade_no": "order-1", "api_trade_no": "2026102522001", "type": "alipay",
		"trade_status": "TRADE_SUCCESS", "name": "Top-up", "money": "19.90", "param": " ", "buyer": "b***@example.test",
	}
	notice, err := gw.ParseCallback(ctx, Notification{Params: signEPayV2(t, platform, callback)})
	require.NoError(t, err)
	assert.Equal(t, Notice{OrderNo: "order-1", TradeNo: "T1", Amount: 1990, Currency: "CNY", Paid: true, Status: "TRADE_SUCCESS",
		Details: map[string]string{"type": "alipay"}}, *notice)
	unlabelled := signEPayV2(t, platform, callback)
	delete(unlabelled, "sign_type")
	_, err = gw.ParseCallback(ctx, Notification{Params: unlabelled})
	require.NoError(t, err, "sign_type defaults to RSA")

	tampered := signEPayV2(t, platform, callback)
	tampered["money"] = "0.01"
	stale := withField(callback, "timestamp", strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10))
	md5Signed := signEPay(gw, callback)
	for name, params := range map[string]map[string]string{
		"tampered":         tampered,
		"stale":            signEPayV2(t, platform, stale),
		"merchant key":     signEPayV2(t, epayV2TestKeys()[0], callback),
		"MD5":              md5Signed,
		"MD5 sign_type":    signEPayV2(t, platform, withField(callback, "sign_type", "MD5")),
		"another merchant": signEPayV2(t, platform, withField(callback, "pid", "2002")),
		"another type":     signEPayV2(t, platform, withField(callback, "type", "wxpay")),
		"not paid":         signEPayV2(t, platform, withField(callback, "trade_status", "WAIT_BUYER_PAY")),
	} {
		_, err := gw.ParseCallback(ctx, Notification{Params: params})
		assert.Error(t, err, name)
	}
}

// A V2 gateway's signed query decides a close: a paid order settles, an
// unpaid one is closed at the gateway and closes at once, and one the
// gateway cannot close waits like a V1 order. The query is always asked; a
// missing query API confirms nothing.
func TestEPayV2QueryAndClose(t *testing.T) {
	young, old := time.Now(), time.Now().Add(-2*EPayUnpaidCloseAge)
	paid := map[string]string{"code": "0", "pid": "1001", "trade_no": "T1", "out_trade_no": "order-1", "type": "alipay", "money": "19.90", "status": "1"}
	unpaid := withField(paid, "status", "0")
	closed := map[string]string{"code": "0", "msg": "succ"}
	cannotClose := map[string]string{"code": "-1", "msg": "channel cannot close orders"}
	tests := []struct {
		name          string
		query, close  map[string]string
		createdAt     time.Time
		explicit      bool
		want          Reconciliation
		wantErr       string
		wantCloseCall bool
	}{
		{name: "paid", query: paid, createdAt: young, want: Reconciliation{TradeNo: "T1"}},
		{name: "paid another amount", query: withField(paid, "money", "1.00"), createdAt: young, wantErr: "error"},
		{name: "unpaid and closed", query: unpaid, close: closed, createdAt: young, wantCloseCall: true},
		{name: "unpaid, cannot close, young", query: unpaid, close: cannotClose, createdAt: young, wantErr: "unconfirmed", wantCloseCall: true},
		{name: "unpaid, cannot close, old", query: unpaid, close: cannotClose, createdAt: old, wantCloseCall: true},
		{name: "explicit close", query: unpaid, close: cannotClose, createdAt: young, explicit: true, wantCloseCall: true},
		{name: "frozen", query: withField(paid, "status", "3"), createdAt: old, wantErr: "unconfirmed"},
		{name: "another order", query: withField(unpaid, "out_trade_no", "order-2"), createdAt: old, wantErr: "unconfirmed"},
	}
	for _, tt := range tests {
		closeCalled := false
		gw := epayV2Server(t, epayV2Config("api", "alipay"), func(path string, params map[string]string) (map[string]string, bool) {
			assert.Equal(t, "order-1", params["out_trade_no"])
			if path == "/api/pay/close" {
				closeCalled = true
				return tt.close, true
			}
			assert.Equal(t, "/api/pay/query", path)
			return tt.query, true
		})
		order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", CreatedAt: tt.createdAt}
		got, err := gw.Reconcile(context.Background(), CloseRequest{Order: order, Explicit: tt.explicit})
		assert.Equal(t, tt.wantCloseCall, closeCalled, tt.name)
		switch tt.wantErr {
		case "unconfirmed":
			assert.ErrorIs(t, err, ErrUnconfirmed, tt.name)
		case "error":
			assert.Error(t, err, tt.name)
			assert.NotErrorIs(t, err, ErrUnconfirmed, tt.name)
		default:
			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.want.TradeNo, got.TradeNo, tt.name)
		}
	}

	notice := &Notice{OrderNo: "order-1", TradeNo: "T1", Amount: 1990, Currency: "CNY", Paid: true}
	order := &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY"}
	gw := epayV2Server(t, epayV2Config("api", "alipay"), func(string, map[string]string) (map[string]string, bool) { return paid, true })
	require.NoError(t, gw.ConfirmPayment(context.Background(), order, notice))
	tradeNo, err := gw.QueryPayment(context.Background(), order)
	require.NoError(t, err)
	assert.Equal(t, "T1", tradeNo)
	missing := epayServer(t, epayV2Config("api", "alipay"), http.NotFound)
	assert.Error(t, missing.ConfirmPayment(context.Background(), order, notice), "V2 has a query API: its absence is a failure")

	// Only a payment creation may answer unsigned: an unsigned query proves
	// no payment, and an unsigned close does not close the order.
	unsignedQuery := epayV2Server(t, epayV2Config("api", "alipay"), func(string, map[string]string) (map[string]string, bool) { return paid, false })
	_, err = unsignedQuery.QueryPayment(context.Background(), order)
	assert.ErrorContains(t, err, "not signed")
	unsignedClose := epayV2Server(t, epayV2Config("api", "alipay"), func(path string, _ map[string]string) (map[string]string, bool) {
		if path == "/api/pay/close" {
			return closed, false
		}
		return unpaid, true
	})
	_, err = unsignedClose.Reconcile(context.Background(), CloseRequest{Order: &Order{OrderNo: "order-1", PaymentAmount: 1990, PaymentCurrency: "CNY", CreatedAt: young}})
	assert.ErrorIs(t, err, ErrUnconfirmed)
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

func stripeTestConfig(paymentType string) string {
	return `{"public_key":"pk_test_a","secret_key":"sk_test_a","payment_method_type":"` + paymentType + `","webhook_secret":"whsec_test"}`
}

// openFakeStripe opens card method methodID on the fake.
func openFakeStripe(t *testing.T, fake *gatewaytest.FakeStripe, methodID int64) Gateway {
	t.Helper()
	return openFakeStripeType(t, fake, methodID, "card")
}

func openFakeStripeType(t *testing.T, fake *gatewaytest.FakeStripe, methodID int64, paymentType string) Gateway {
	t.Helper()
	return mustOpen(t, NewRegistry(WithStripeBackends(fake.Backends)), Method{ID: methodID, Platform: PlatformStripe, Config: stripeTestConfig(paymentType)})
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
	assert.Equal(t, Action{Type: ActionStripe, Stripe: &StripeAction{PublishableKey: "pk_test_a", ClientSecret: "pi_1_secret", PaymentMethodType: "card"}}, *action)
	created, keys, _ := fake.Snapshot()
	require.Len(t, created, 1)
	assert.Equal(t, []string{"newapi:pi:order-1"}, keys)
	assert.Equal(t, "1990", created[0].Get("amount"))
	assert.Equal(t, "usd", created[0].Get("currency"))
	assert.Equal(t, []string{"card"}, created[0]["payment_method_types[0]"])
	assert.False(t, created[0].Has("payment_method_types[1]"), "the intent collects the method's one payment method type")
	assert.False(t, created[0].Has("automatic_payment_methods[enabled]"))
	assert.Equal(t, "order-1", created[0].Get("metadata[newapi_order_no]"))
	assert.Equal(t, "7", created[0].Get("metadata[newapi_method_id]"))
	assert.Equal(t, "Top-up", created[0].Get("description"))
	assert.False(t, created[0].Has("customer"), "no Stripe customer is attached")

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
	_, err = openFakeStripeType(t, fake, 7, "alipay").StartPayment(context.Background(), checkout)
	assert.Error(t, err, "an intent of another payment method type is not reused")
	fake.Set("pi_1", "payment_method_types", []string{"card", "alipay"})
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err, "an intent that also collects other types is not reused")
	fake.Seed(gatewaytest.LegacyIntent(gatewaytest.Intent("pi_1", 1990, "usd", "requires_payment_method", "order-1", "7")))
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err, "an intent with automatic payment methods is not handed out again")
	fake.Set("pi_1", "payment_method_types", []string{"card"})
	fake.Set("pi_1", "automatic_payment_methods", nil)
	fake.Set("pi_1", "status", "canceled")
	_, err = gw.StartPayment(context.Background(), checkout)
	assert.Error(t, err, "a canceled intent is not offered again")
}

// Each Stripe method creates its intents for its one payment method type
// and tells the client which one to confirm.
func TestStripeIntentsCollectTheMethodsType(t *testing.T) {
	for i, paymentType := range []string{"card", "alipay", "wechat_pay"} {
		fake := gatewaytest.NewFakeStripe(t)
		gw := openFakeStripeType(t, fake, 7, paymentType)
		order := &Order{OrderNo: fmt.Sprintf("order-%d", i), PaymentAmount: 1990, PaymentCurrency: "CNY"}
		action, err := gw.StartPayment(context.Background(), Checkout{Order: order, Charge: Charge{1990, "CNY"}, Claim: claimAs("", nil)})
		require.NoError(t, err, paymentType)
		assert.Equal(t, paymentType, action.Stripe.PaymentMethodType)
		created, _, _ := fake.Snapshot()
		require.Len(t, created, 1)
		assert.Equal(t, []string{paymentType}, created[0]["payment_method_types[0]"], paymentType)
		assert.False(t, created[0].Has("payment_method_types[1]"), paymentType)

		order.TradeNo = "pi_1"
		action, err = gw.StartPayment(context.Background(), Checkout{Order: order, Charge: Charge{1990, "CNY"}})
		require.NoError(t, err, "%s: a resumed checkout reuses the intent", paymentType)
		assert.Equal(t, paymentType, action.Stripe.PaymentMethodType)
	}
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

	assert.Empty(t, notice.Unmatched)

	notice, err = gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_test", "payment_intent.payment_failed", failed))
	require.NoError(t, err)
	assert.False(t, notice.Paid, "a failed payment is a lifecycle event")
	assert.False(t, notice.Ignore)
	assert.Equal(t, int64(100100), notice.Amount, "a lifecycle event reports the requested amount")

	// A payment of another type is authentic, so it is reported for an
	// operator instead of rejected; one checked out before the method had a
	// type settles.
	otherType := maps.Clone(succeeded)
	otherType["payment_method_types"] = []string{"wechat_pay"}
	notice, err = gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_test", "payment_intent.succeeded", otherType))
	require.NoError(t, err)
	assert.True(t, notice.Paid)
	assert.Equal(t, "the Stripe payment intent collected wechat_pay, not the payment method's card", notice.Unmatched)
	notice, err = gw.ParseCallback(context.Background(), stripeEvent(t, "whsec_test", "payment_intent.succeeded", gatewaytest.LegacyIntent(maps.Clone(succeeded))))
	require.NoError(t, err)
	assert.Empty(t, notice.Unmatched)

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
	unregistered := mustOpen(t, NewRegistry(), Method{ID: 7, Platform: PlatformStripe, Config: `{"public_key":"pk_test_a","secret_key":"sk_test_a","payment_method_type":"card"}`})
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
		{name: "other payment method type", change: map[string]any{"payment_method_types": []string{"alipay"}}, wantErr: true},
		{name: "checked out with automatic payment methods", change: gatewaytest.LegacyIntent(map[string]any{})},
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
	fake.Set("pi_1", "amount", 1990)
	fake.Set("pi_1", "payment_method_types", []string{"alipay"})
	_, err = gw.Reconcile(context.Background(), CloseRequest{Order: order})
	assert.Error(t, err, "an intent of another payment method type does not settle the order")

	// An order checked out before the method had a type still settles, or
	// closes with its intent cancelled.
	for status, want := range map[string]Reconciliation{"succeeded": {TradeNo: "pi_1"}, "requires_payment_method": {}} {
		fake := gatewaytest.NewFakeStripe(t)
		fake.Seed(gatewaytest.LegacyIntent(gatewaytest.Intent("pi_1", 1990, "usd", status, "order-1", "7")))
		got, err := openFakeStripe(t, fake, 7).Reconcile(context.Background(), CloseRequest{Order: order})
		require.NoError(t, err, status)
		assert.Equal(t, want, got, status)
	}
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
