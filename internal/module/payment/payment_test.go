package payment

import (
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/currency"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway/gatewaytest"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
	"gorm.io/gorm"
)

const (
	testEPayMethod   = 10
	testStripeMethod = 11
	testUser         = 1
	testPlan         = 7
	testFreePlan     = 8
	testOddPlan      = 9
	testEPaySecret   = "epay-key"
	testStripeSecret = "whsec_test"
)

// fakeEPay answers the EPay order query, and a payment created at mapi.php,
// with whatever the test set. It keeps the form of the last payment.
type fakeEPay struct {
	mu      sync.Mutex
	status  int
	answer  string
	payment string
	created url.Values
}

func (f *fakeEPay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/mapi.php" && f.payment != "" {
		_ = r.ParseForm()
		f.created = r.PostForm
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = w.Write([]byte(f.payment))
		return
	}
	if r.URL.Path != "/api.php" || f.answer == "" {
		http.NotFound(w, r)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
	}
	_, _ = w.Write([]byte(f.answer))
}

func (f *fakeEPay) set(status int, answer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.answer = status, answer
}

func (f *fakeEPay) setPayment(status int, payment string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.payment, f.created = status, payment, nil
}

func (f *fakeEPay) lastPayment() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

type paymentFixture struct {
	stripe *gatewaytest.FakeStripe
	epay   *fakeEPay
}

// setupPayment gives the test an empty database with a USD site, the
// balance, an EPay method (3 % fee) and a Stripe method (0.30 fixed fee),
// a user of the "vip" top-up group (ratio 0.8), a 9.99 plan, a free plan,
// a USD→CNY rate pinned by an override, which the currency module resolves
// without its rate API or cache, and a 10 % discount on top-ups of 100.
func setupPayment(t *testing.T) *paymentFixture {
	t.Helper()
	pgtest.Reset(t, model.DB)
	t.Cleanup(func() { pgtest.Reset(t, model.DB) })

	settings := operation_setting.GetPaymentSetting()
	savedSettings := *settings
	t.Cleanup(func() { *settings = savedSettings })
	settings.Currency, settings.MinTopup, settings.ExchangeRateAPIKey = "USD", "1", ""
	settings.ExchangeRateOverrides = map[string]string{"USD/CNY": "7.2419"}
	settings.AmountDiscount = map[int]float64{100: 0.9}
	savedRatios := common.TopupGroupRatio2JSONString()
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":0.8}`))
	t.Cleanup(func() { _ = common.UpdateTopupGroupRatioByJSONString(savedRatios) })
	savedAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://www.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = savedAddress })

	f := &paymentFixture{stripe: gatewaytest.NewFakeStripe(t), epay: &fakeEPay{}}
	epayServer := httptest.NewServer(f.epay)
	t.Cleanup(epayServer.Close)
	registry := gateway.NewRegistry(
		gateway.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
		gateway.WithStripeBackends(f.stripe.Backends),
	)
	savedRegistry := gatewayRegistry
	gatewayRegistry = func() *gateway.Registry { return registry }
	t.Cleanup(func() { gatewayRegistry = savedRegistry })
	// The breaker remembers this process's checkouts; each test starts clean.
	healthMu.Lock()
	health = map[int]*ChannelHealth{}
	healthMu.Unlock()
	// Work off the buyer's request finishes before the request returns.
	savedBackground := runInBackground
	runInBackground = func(work func()) { work() }
	t.Cleanup(func() { runInBackground = savedBackground })

	require.NoError(t, model.DB.Create(&model.User{Id: testUser, Username: "payer", Status: common.UserStatusEnabled, Group: "vip"}).Error)
	// Each option has one channel under the same id, as the migration that
	// introduced options left every method.
	for _, option := range []*entity.PaymentOption{
		{Id: entity.BalancePaymentOptionID, Name: "Balance", Enabled: true},
		{Id: testEPayMethod, Name: "Alipay", Enabled: true, FeeMode: entity.PaymentFeeRate, FeeRateBp: 300},
		{Id: testStripeMethod, Name: "Card", Enabled: true, FeeMode: entity.PaymentFeeFixed, FeeFixed: 30},
	} {
		require.NoError(t, model.DB.Create(option).Error)
	}
	for _, method := range []*entity.PaymentMethod{
		{Id: entity.BalancePaymentMethodID, Name: "Balance", Platform: entity.PaymentPlatformBalance, OptionId: entity.BalancePaymentOptionID, Enabled: true, Token: "balance-token"},
		{Id: testEPayMethod, Name: "Alipay", Platform: gateway.PlatformEPay, OptionId: testEPayMethod, Enabled: true, Token: "epay-token",
			Config: `{"url":"` + epayServer.URL + `","pid":"1001","key":"` + testEPaySecret + `","type":""}`},
		// The Stripe configuration is in the key order the startup migration
		// that gave Stripe methods their payment method type writes.
		{Id: testStripeMethod, Name: "Card", Platform: gateway.PlatformStripe, OptionId: testStripeMethod, Enabled: true, Token: "stripe-token",
			Config: `{"payment_method_type":"card","public_key":"pk_test_a","secret_key":"sk_test_a","webhook_endpoint_id":"we_0","webhook_secret":"` + testStripeSecret + `","webhook_url":""}`},
	} {
		require.NoError(t, model.DB.Create(method).Error)
	}
	for _, plan := range []*model.SubscriptionPlan{
		{Id: testPlan, Title: "Pro", PriceAmount: 9.99},
		{Id: testFreePlan, Title: "Free", PriceAmount: 0},
		{Id: testOddPlan, Title: "Odd", PriceAmount: 9.999},
	} {
		plan.DurationUnit, plan.DurationValue, plan.Enabled, plan.TotalAmount = model.SubscriptionDurationMonth, 1, true, 1000
		require.NoError(t, model.DB.Create(plan).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
	}
	return f
}

func topup(amount string, option int) PurchaseRequest {
	return PurchaseRequest{Purpose: entity.PaymentPurposeTopup, PaymentOptionId: option, Amount: json.Number(amount)}
}

func subscription(plan, option int) PurchaseRequest {
	return PurchaseRequest{Purpose: entity.PaymentPurposeSubscription, PaymentOptionId: option, PlanId: plan}
}

func userQuota(t *testing.T) int {
	t.Helper()
	quota, err := model.GetUserQuota(testUser, true)
	require.NoError(t, err)
	return quota
}

func TestQuotePurchase(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		req      PurchaseRequest
		want     Quote
	}{
		{
			name: "top-up with group ratio and fixed fee", currency: "USD", req: topup("10", testStripeMethod),
			want: Quote{Price: 1000, Adjustment: -200, FeeAmount: 30, Amount: 830, QuotaAmount: 5_000_000, QuotaRate: "1",
				PaymentAmount: 830, PaymentCurrency: "USD", TopupAmount: 1000},
		},
		{
			// 10000 × 0.8 × 0.9 = 7200; fee ⌊7200 × 3 %⌋ = 216; 7416 × 7.2419 = 53705.93
			name: "top-up with discount, rate fee and conversion to CNY", currency: "USD", req: topup("100", testEPayMethod),
			want: Quote{Price: 10000, Adjustment: -2800, FeeAmount: 216, Amount: 7416, QuotaAmount: 50_000_000, QuotaRate: "1",
				PaymentAmount: 53706, PaymentCurrency: "CNY", TopupAmount: 10000},
		},
		{
			// 100 CNY × 0.1381 USD × 500000 quota per USD
			name: "top-up in a CNY site", currency: "CNY", req: topup("100", testEPayMethod),
			want: Quote{Price: 10000, Adjustment: -2800, FeeAmount: 216, Amount: 7416, QuotaAmount: 6_905_000, QuotaRate: "0.1381",
				PaymentAmount: 7416, PaymentCurrency: "CNY", TopupAmount: 10000},
		},
		{
			name: "plan through a gateway pays the fee but no group ratio", currency: "USD", req: subscription(testPlan, testStripeMethod),
			want: Quote{PlanId: testPlan, Price: 999, FeeAmount: 30, Amount: 1029, PaymentAmount: 1029, PaymentCurrency: "USD"},
		},
		{
			// ⌈9.99 × 500000⌉
			name: "plan with the balance has no fee", currency: "USD", req: subscription(testPlan, entity.BalancePaymentOptionID),
			want: Quote{PlanId: testPlan, Price: 999, Amount: 999, QuotaAmount: 4_995_000, QuotaRate: "1"},
		},
		{
			name: "free plan needs no method", currency: "USD", req: subscription(testFreePlan, 0),
			want: Quote{PlanId: testFreePlan},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupPayment(t)
			settings := operation_setting.GetPaymentSetting()
			settings.Currency = tt.currency
			if tt.currency == "CNY" {
				// EPay collects the site currency; only the quota converts.
				settings.ExchangeRateOverrides = map[string]string{"CNY/USD": "0.1381"}
			}
			quote, err := QuotePurchase(context.Background(), testUser, tt.req)
			require.NoError(t, err)
			tt.want.Purpose, tt.want.Currency, tt.want.Subject = tt.req.Purpose, tt.currency, quote.Subject
			quote.option, quote.channels, quote.method, quote.plan = nil, nil, nil, nil
			assert.Equal(t, tt.want, *quote)
		})
	}

	setupPayment(t)
	for name, req := range map[string]PurchaseRequest{
		"below the minimum top-up":   topup("0.5", testStripeMethod),
		"three decimal places":       topup("10.001", testStripeMethod),
		"top-up with the balance":    topup("10", entity.BalancePaymentOptionID),
		"plan price of three places": subscription(testOddPlan, testStripeMethod),
		"unknown method":             subscription(testPlan, 404),
		"unknown purpose":            {Purpose: "gift", PaymentOptionId: testStripeMethod},
		"above the order ceiling":    topup("100000000.01", testStripeMethod),
	} {
		_, err := QuotePurchase(context.Background(), testUser, req)
		assert.Error(t, err, name)
	}

	// A top-up that would lift the wallet past common.MaxWalletQuota is
	// refused before the buyer pays; settlement could not credit it.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", testUser).Update("quota", common.MaxWalletQuota-100_000).Error)
	_, err := QuotePurchase(context.Background(), testUser, topup("1", testStripeMethod))
	assert.ErrorIs(t, err, model.ErrTopUpQuotaLimitExceeded)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", testUser).Update("quota", 0).Error)
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testStripeMethod).Update("enabled", false).Error)
	_, err = QuotePurchase(context.Background(), testUser, topup("10", testStripeMethod))
	assert.ErrorIs(t, err, errMethodUnavailable, "an option without an enabled channel takes no order")
}

// addChannel adds a gateway channel to an option: Stripe collects USD, EPay
// CNY at the fixture's rate.
func addChannel(t *testing.T, id, optionID int, platform string, minAmount, maxAmount int64, priority, weight int) {
	t.Helper()
	config := `{"payment_method_type":"card","public_key":"pk_test_a","secret_key":"sk_test_a","webhook_endpoint_id":"we_0","webhook_secret":"` + testStripeSecret + `","webhook_url":""}`
	if platform == gateway.PlatformEPay {
		config = `{"url":"https://pay.example.test","pid":"1001","key":"` + testEPaySecret + `","type":"alipay"}`
	}
	require.NoError(t, model.DB.Create(&entity.PaymentMethod{
		Id: id, Name: fmt.Sprintf("channel %d", id), Platform: platform, OptionId: optionID, Enabled: true, Token: fmt.Sprintf("token-%d", id),
		MinAmount: minAmount, MaxAmount: maxAmount, Priority: priority, Weight: weight, Config: config,
	}).Error)
}

func routedIds(quote *Quote) []int {
	ids := make([]int, 0, len(quote.channels))
	for _, routed := range quote.channels {
		ids = append(ids, routed.method.Id)
	}
	return ids
}

// An option invoices the channels that take an order's price, by priority,
// and the channels of one priority share the orders by weight. Its price
// never depends on the channel; what a channel collects is previewed only
// when every candidate collects the same currency.
func TestPaymentOptionsRouteOrders(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	savedRandom := routeRandom
	t.Cleanup(func() { routeRandom = savedRandom })
	// Option 10, Alipay: EPay channels up to 50.00 share by weight 1:3,
	// Stripe takes above, and another Stripe channel backs both up.
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testEPayMethod).Update("max_amount", 5000).Error)
	addChannel(t, 20, testEPayMethod, gateway.PlatformStripe, 5001, 0, 0, 1)
	addChannel(t, 21, testEPayMethod, gateway.PlatformStripe, 0, 0, 1, 1)
	addChannel(t, 22, testEPayMethod, gateway.PlatformEPay, 0, 5000, 0, 3)

	var draws []int64
	routeRandom = func(n int64) int64 {
		draws = append(draws, n)
		return 0
	}
	small, err := QuotePurchase(ctx, testUser, topup("10", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{testEPayMethod, 22, 21}, routedIds(small))
	assert.Equal(t, []int64{4, 3, 1}, draws, "each draw weighs the channels left in the priority")
	assert.Equal(t, int64(24), small.FeeAmount, "the option's 3 % of 8.00, whatever the channel")
	assert.Empty(t, small.PaymentCurrency, "EPay and Stripe collect different currencies")
	routeRandom = func(n int64) int64 { return n - 1 }
	small, err = QuotePurchase(ctx, testUser, topup("10", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{22, testEPayMethod, 21}, routedIds(small), "the heavier channel draws the upper range")

	large, err := QuotePurchase(ctx, testUser, topup("100", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{20, 21}, routedIds(large))
	assert.Equal(t, "USD", large.PaymentCurrency, "both Stripe channels collect USD")
	assert.Equal(t, large.Amount, large.PaymentAmount)

	order, checkout, err := CreateOrder(ctx, testUser, topup("100", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionStripe, checkout.Type)
	assert.Equal(t, testEPayMethod, order.PaymentOptionId)
	invoices, err := OrderInvoices(order.OrderNo)
	require.NoError(t, err)
	require.Len(t, invoices, 1)
	assert.Equal(t, 20, invoices[0].PaymentMethodId)

	// Channels that cannot take orders now are skipped: a disabled one, and
	// EPay without the rate into CNY.
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", 20).Update("enabled", false).Error)
	operation_setting.GetPaymentSetting().ExchangeRateOverrides = map[string]string{}
	small, err = QuotePurchase(ctx, testUser, topup("10", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{21}, routedIds(small))
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", 21).Update("max_amount", 5000).Error)
	_, err = QuotePurchase(ctx, testUser, topup("100", testEPayMethod))
	assert.ErrorIs(t, err, errAmountNotAccepted)
}

func invoiceStates(t *testing.T, orderNo string) [][]any {
	t.Helper()
	invoices, err := OrderInvoices(orderNo)
	require.NoError(t, err)
	states := make([][]any, 0, len(invoices))
	for _, invoice := range invoices {
		states = append(states, []any{invoice.InvoiceNo, invoice.PaymentMethodId, invoice.Status, invoice.CloseReason})
	}
	return states
}

// useEPayAPI makes the fixture's EPay channel create its payments at
// mapi.php, so its failures show.
func useEPayAPI(t *testing.T) {
	t.Helper()
	method, err := store.GetMethod(testEPayMethod)
	require.NoError(t, err)
	apiConfig := strings.Replace(method.Config, `"type":""`, `"version":"v1","checkout":"api","type":"alipay"`, 1)
	require.NoError(t, model.DB.Model(method).Update("config", apiConfig).Error)
}

// A channel that cannot start a payment hands the order to the next channel
// whose checkout reaches its provider, under the same order number. A
// channel that keeps failing is tripped and tried last until its cooldown
// passes; a checkout through it clears it.
func TestCheckoutSwitchesChannels(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	savedRandom, savedNow := routeRandom, healthNow
	t.Cleanup(func() { routeRandom, healthNow = savedRandom, savedNow })
	routeRandom = func(int64) int64 { return 0 }
	now := time.Now()
	healthNow = func() time.Time { return now }
	useEPayAPI(t)
	addChannel(t, 20, testEPayMethod, gateway.PlatformStripe, 0, 0, 1, 1)

	// A gateway that refuses one order is switched away from but not held
	// against the channel.
	f.epay.setPayment(0, `{"code":-1,"msg":"amount below the minimum"}`)
	_, checkout, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionStripe, checkout.Type)
	assert.Nil(t, channelHealth(testEPayMethod))

	f.epay.setPayment(http.StatusBadGateway, `bad gateway`)
	order, checkout, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionStripe, checkout.Type)
	assert.Equal(t, order.OrderNo, checkout.OrderNo, "the buyer keeps one order")
	assert.Equal(t, [][]any{
		{order.OrderNo, testEPayMethod, entity.PaymentInvoiceClosed, "failed"},
		{order.OrderNo + "R2", 20, entity.PaymentInvoicePending, ""},
	}, invoiceStates(t, order.OrderNo))
	stored, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, 20, stored.PaymentMethodId, "the order shows its current invoice")
	require.NotNil(t, channelHealth(testEPayMethod))
	assert.Equal(t, 1, channelHealth(testEPayMethod).Failures)
	assert.Contains(t, channelHealth(testEPayMethod).LastError, "HTTP 502")

	for range 2 {
		_, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
		require.NoError(t, err)
	}
	assert.NotZero(t, channelHealth(testEPayMethod).TrippedUntil)
	quote, err := QuotePurchase(ctx, testUser, topup("10", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{20, testEPayMethod}, routedIds(quote), "a tripped channel is tried last")

	now = now.Add(breakerCooldown + time.Second)
	f.epay.setPayment(0, `{"code":1,"trade_no":"T9","payurl":"https://pay.example/pay/T9"}`)
	_, checkout, err = CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example/pay/T9", checkout.URL, "after its cooldown the channel takes its place again")
	assert.Nil(t, channelHealth(testEPayMethod), "a checkout that starts clears the failures")
}

// A channel that only redirects the buyer cannot show its failures, so an
// order is never switched to one.
func TestCheckoutNeverSwitchesToPaymentPages(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	useEPayAPI(t)
	addChannel(t, 22, testEPayMethod, gateway.PlatformEPay, 0, 0, 1, 1)
	f.epay.setPayment(http.StatusBadGateway, `bad gateway`)

	order, checkout, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	assert.ErrorIs(t, err, errCheckoutFailed)
	assert.Nil(t, checkout)
	assert.Equal(t, [][]any{{order.OrderNo, testEPayMethod, entity.PaymentInvoiceClosed, "failed"}}, invoiceStates(t, order.OrderNo))
}

// A buyer who pays again gets other channels first: a new order after one
// that was not paid, and a resumed order whose channel can no longer take
// it, which gets a new invoice. The old invoice is closed only when its
// gateway confirms it was not paid; a young EPay V1 order cannot be, so it
// stays pending for the order's close to ask about again.
func TestPayingAgainTriesOtherChannelsFirst(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	savedRandom := routeRandom
	t.Cleanup(func() { routeRandom = savedRandom })
	routeRandom = func(int64) int64 { return 0 }
	addChannel(t, 22, testEPayMethod, gateway.PlatformEPay, 0, 0, 0, 1)

	order, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, testEPayMethod, invoiceStates(t, order.OrderNo)[0][1])
	quote, err := QuotePurchase(ctx, testUser, topup("10", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, []int{22, testEPayMethod}, routedIds(quote))

	_, err = ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{})
	require.NoError(t, err)
	assert.Len(t, invoiceStates(t, order.OrderNo), 1, "a channel that still works keeps its invoice")

	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testEPayMethod).Update("enabled", false).Error)
	f.epay.set(0, `{"code":1,"pid":"1001","out_trade_no":"`+order.OrderNo+`","status":0}`)
	resumed, err := ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionURL, resumed.Type)
	assert.Equal(t, order.OrderNo, resumed.OrderNo)
	assert.Equal(t, [][]any{
		{order.OrderNo, testEPayMethod, entity.PaymentInvoicePending, ""},
		{order.OrderNo + "R2", 22, entity.PaymentInvoicePending, ""},
	}, invoiceStates(t, order.OrderNo))
}

// A renewed checkout that its gateway refuses hands the buyer another
// channel, but the invoice the buyer already holds stays payable and is
// still asked about: checking the order finds its payment.
func TestRenewalKeepsTheInvoiceTheBuyerHolds(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	useEPayAPI(t)
	addChannel(t, 20, testEPayMethod, gateway.PlatformStripe, 0, 0, 1, 1)
	f.epay.setPayment(0, `{"code":1,"trade_no":"T1","payurl":"https://pay.example/pay/T1"}`)
	order, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)

	f.epay.setPayment(0, `{"code":-1,"msg":"order number exists"}`)
	resumed, err := ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionStripe, resumed.Type)
	assert.Equal(t, [][]any{
		{order.OrderNo, testEPayMethod, entity.PaymentInvoicePending, ""},
		{order.OrderNo + "R2", 20, entity.PaymentInvoicePending, ""},
	}, invoiceStates(t, order.OrderNo))
	assert.Nil(t, channelHealth(testEPayMethod), "a refused renewal says nothing of the channel")

	stored, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	invoice, err := store.GetInvoiceByNo(order.OrderNo)
	require.NoError(t, err)
	f.epay.set(0, `{"code":1,"pid":"1001","trade_no":"T1","out_trade_no":"`+order.OrderNo+`","type":"alipay","money":"`+gateway.FormatAmount(invoice.PaymentAmount)+`","status":1}`)
	synced, err := SyncOrder(ctx, stored, "")
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, synced.Status)
	assert.Equal(t, "T1", synced.TradeNo)
}

// Checking an order skips the invoices whose channel failed to start them,
// which no buyer got and which their gateways may not know, and a gateway
// that cannot answer keeps the others from nothing.
func TestSyncAsksEveryInvoiceThatReachedTheBuyer(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	useEPayAPI(t)
	addChannel(t, 20, testEPayMethod, gateway.PlatformStripe, 0, 0, 1, 1)
	f.epay.setPayment(http.StatusBadGateway, `bad gateway`)
	order, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	f.epay.set(http.StatusInternalServerError, `{}`)

	stored, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	synced, err := SyncOrder(ctx, stored, "")
	require.NoError(t, err, "the failed EPay invoice is not asked about")
	assert.Equal(t, entity.PaymentOrderPending, synced.Status)
	invoice, err := store.GetInvoiceByNo(order.OrderNo + "R2")
	require.NoError(t, err)
	f.stripe.Set(invoice.TradeNo, "status", "succeeded")
	f.stripe.Set(invoice.TradeNo, "amount_received", invoice.PaymentAmount)
	synced, err = SyncOrder(ctx, stored, "")
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, synced.Status)
}

// fakeEPayV2 is an EPay V2 gateway that answers a payment creation with
// whatever the test set and the query and close of an order with signed
// answers, without sign_type, which defaults to RSA. It records the API paths
// it was asked and the order each named.
type fakeEPayV2 struct {
	mu       sync.Mutex
	platform *rsa.PrivateKey
	created  string
	status   string
	money    string
	asked    []string
}

func (f *fakeEPayV2) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	orderNo := r.PostForm.Get("out_trade_no")
	f.asked = append(f.asked, r.URL.Path+" "+orderNo)
	var answer map[string]string
	switch r.URL.Path {
	case "/api/pay/create":
		_, _ = w.Write([]byte(f.created))
		return
	case "/api/pay/query":
		answer = map[string]string{"code": "0", "pid": "1001", "trade_no": "T" + orderNo, "out_trade_no": orderNo, "type": "alipay", "money": f.money, "status": f.status}
	case "/api/pay/close":
		answer = map[string]string{"code": "0", "msg": "succ"}
	default:
		http.NotFound(w, r)
		return
	}
	answer["timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)
	var canonical []string
	for _, key := range slices.Sorted(maps.Keys(answer)) {
		if answer[key] != "" {
			canonical = append(canonical, key+"="+answer[key])
		}
	}
	digest := sha256.Sum256([]byte(strings.Join(canonical, "&")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.platform, crypto.SHA256, digest[:])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	answer["sign"] = base64.StdEncoding.EncodeToString(signature)
	raw, _ := common.Marshal(answer)
	_, _ = w.Write(raw)
}

func (f *fakeEPayV2) answer(created, status, money string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created, f.status, f.money, f.asked = created, status, money, nil
}

func (f *fakeEPayV2) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// A provider may create a payment and still fail the checkout, as an EPay
// gateway whose answer cannot be read. The buyer is switched to the next
// channel at once; the failed channel's gateway is asked afterwards, off the
// buyer's request, to close the payment, and a payment it proves settles
// the order. A provider that refused the payment created none and is not
// asked.
func TestFailedCheckoutClosesAtTheGateway(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	var background []func()
	runInBackground = func(work func()) { background = append(background, work) }
	runBackground := func() {
		for _, work := range background {
			work()
		}
		background = nil
	}
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	merchantDER, err := x509.MarshalPKCS8PrivateKey(merchant)
	require.NoError(t, err)
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	platformDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	require.NoError(t, err)
	fake := &fakeEPayV2{platform: platform}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	config := `{"url":"` + server.URL + `","pid":"1001","version":"v2","checkout":"api","type":"alipay","private_key":"` +
		base64.StdEncoding.EncodeToString(merchantDER) + `","platform_public_key":"` + base64.StdEncoding.EncodeToString(platformDER) + `"}`
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testEPayMethod).Update("config", config).Error)
	addChannel(t, 20, testEPayMethod, gateway.PlatformStripe, 0, 0, 1, 1)

	fake.answer(`<html>502 Bad Gateway</html>`, "0", "")
	order, checkout, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionStripe, checkout.Type)
	assert.Equal(t, []string{"/api/pay/create " + order.OrderNo}, fake.paths(), "the buyer does not wait for the gateway's close")
	runBackground()
	assert.Equal(t, []string{"/api/pay/create " + order.OrderNo, "/api/pay/query " + order.OrderNo, "/api/pay/close " + order.OrderNo}, fake.paths())
	assert.Equal(t, [][]any{
		{order.OrderNo, testEPayMethod, entity.PaymentInvoiceClosed, "failed"},
		{order.OrderNo + "R2", 20, entity.PaymentInvoicePending, ""},
	}, invoiceStates(t, order.OrderNo))

	fake.answer(`{"code":-1,"msg":"amount below the minimum"}`, "0", "")
	refused, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	runBackground()
	assert.Equal(t, []string{"/api/pay/create " + refused.OrderNo}, fake.paths())

	fake.answer(`<html>502 Bad Gateway</html>`, "1", "")
	paid, _, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	invoice, err := store.GetInvoiceByNo(paid.OrderNo)
	require.NoError(t, err)
	fake.mu.Lock()
	fake.money = gateway.FormatAmount(invoice.PaymentAmount)
	fake.mu.Unlock()
	runBackground()
	assert.Equal(t, []string{"/api/pay/create " + paid.OrderNo, "/api/pay/query " + paid.OrderNo}, fake.paths(), "a paid order is not closed")
	settled, err := store.GetOrderByNo(paid.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, settled.Status)
	assert.Equal(t, "T"+paid.OrderNo, settled.TradeNo)
}

// Buyers see payment options, the prices they take and their fees, never
// the channels behind them.
func TestBuyersSeeOptionsNotChannels(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testEPayMethod).Updates(map[string]any{"min_amount": 100, "max_amount": 5000}).Error)
	addChannel(t, 22, testEPayMethod, gateway.PlatformEPay, 4000, 8000, 0, 1)
	addChannel(t, 23, testEPayMethod, gateway.PlatformStripe, 10000, 20000, 1, 1)

	options, err := BuyerOptions(ctx, entity.PaymentPurposeTopup)
	require.NoError(t, err)
	require.Len(t, options, 2, "the balance does not top up")
	byName := map[string]OptionView{}
	for _, option := range options {
		byName[option.Name] = option
	}
	assert.Equal(t, []AmountRange{{Min: 100, Max: 8000}, {Min: 10000, Max: 20000}}, byName["Alipay"].AmountRanges)
	assert.Equal(t, entity.PaymentFeeRate, byName["Alipay"].FeeMode)
	assert.Empty(t, byName["Card"].AmountRanges, "a channel without a range takes any price")
	options, err = BuyerOptions(ctx, entity.PaymentPurposeSubscription)
	require.NoError(t, err)
	require.Len(t, options, 3)
	assert.True(t, options[0].Balance || options[1].Balance || options[2].Balance)
	listed, err := common.Marshal(options)
	require.NoError(t, err)
	assert.NotContains(t, string(listed), "platform")

	order, _, err := CreateOrder(ctx, testUser, topup("10", testStripeMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	view, err := BuyerOrderOf(order)
	require.NoError(t, err)
	assert.Equal(t, "Card", view.PaymentOption)
	shown, err := common.Marshal(view)
	require.NoError(t, err)
	for _, field := range []string{"platform", "payment_method_id", "trade_no", "exchange_rate"} {
		assert.NotContains(t, string(shown), `"`+field+`"`, "a buyer's order names no channel")
	}
}

// An option takes orders through the channels administrators give it; the
// balance option can neither be deleted nor charge a fee, and an option
// with channels keeps them until they move or go.
func TestAdminPaymentOptions(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	option, err := CreateOption(OptionInput{Name: " WeChat Pay ", Icon: "RiWechatPayFill", Enabled: true, FeeMode: entity.PaymentFeeRate, FeeRateBp: 100})
	require.NoError(t, err)
	assert.Equal(t, "WeChat Pay", option.Name)
	_, err = CreateOption(OptionInput{Name: " "})
	assert.ErrorIs(t, err, errOptionNameRequired)
	_, err = CreateOption(OptionInput{Name: "Bad fee", FeeMode: entity.PaymentFeeRate, FeeRateBp: 10001})
	assert.ErrorIs(t, err, errMethodFee)

	balance, err := UpdateOption(entity.BalancePaymentOptionID, OptionInput{Name: "Wallet", Enabled: true, FeeMode: entity.PaymentFeeFixed, FeeFixed: 50})
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentFeeNone, balance.FeeMode)
	assert.ErrorIs(t, DeleteOption(entity.BalancePaymentOptionID), errBalanceOptionDeletion)

	channel := epayInput("k")
	for name, change := range map[string]func(in *MethodInput){
		"no option":         func(in *MethodInput) { in.OptionId = 0 },
		"the balance":       func(in *MethodInput) { in.OptionId = entity.BalancePaymentOptionID },
		"range upside down": func(in *MethodInput) { in.MinAmount, in.MaxAmount = 5000, 100 },
		"negative amount":   func(in *MethodInput) { in.MinAmount = -1 },
		"no weight":         func(in *MethodInput) { in.Weight = 0 },
		"priority too high": func(in *MethodInput) { in.Priority = 1001 },
	} {
		invalid := channel
		change(&invalid)
		_, err := CreateMethod(ctx, invalid)
		assert.Error(t, err, name)
	}
	channel.OptionId, channel.MinAmount, channel.MaxAmount, channel.Priority, channel.Weight = option.Id, 100, 5000, 2, 5
	created, err := CreateMethod(ctx, channel)
	require.NoError(t, err)
	assert.Equal(t, []any{option.Id, int64(100), int64(5000), 2, 5},
		[]any{created.OptionId, created.MinAmount, created.MaxAmount, created.Priority, created.Weight})
	assert.ErrorIs(t, DeleteOption(option.Id), errOptionHasMethods)
	require.NoError(t, DeleteMethod(ctx, created.Id))
	require.NoError(t, DeleteOption(option.Id))
	details, err := AdminOptions()
	require.NoError(t, err)
	for _, detail := range details {
		assert.NotEqual(t, option.Id, detail.Id, "a deleted option is gone")
		if detail.Id == testEPayMethod {
			assert.EqualValues(t, 1, detail.Methods)
		}
	}
	channel.Platform = entity.PaymentPlatformBalance
	_, err = UpdateMethod(ctx, entity.BalancePaymentMethodID, channel)
	assert.ErrorIs(t, err, errBalanceChannel)
}

// Prices convert at the rate the currency module resolves. Without one an
// order is refused and the methods say so; a reversed override converts
// exactly, and the credited quota shows as the amount paid. The EPay
// methods make a USD site convert into CNY too.
func TestPricesConvertAtTheResolvedRate(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	settings := operation_setting.GetPaymentSetting()
	settings.Currency = "CNY"
	settings.ExchangeRateOverrides = map[string]string{}

	_, err := QuotePurchase(ctx, testUser, topup("100", testEPayMethod))
	assert.ErrorIs(t, err, currency.ErrRateUnavailable)
	options, err := BuyerOptions(ctx, entity.PaymentPurposeTopup)
	require.NoError(t, err)
	require.NotEmpty(t, options)
	for _, option := range options {
		assert.False(t, option.Available, option.Name)
		require.NotNil(t, option.Unavailable)
		assert.Equal(t, "The exchange rate from {{from}} to {{to}} is unavailable. Try again later.", option.Unavailable.Key)
	}

	settings.ExchangeRateOverrides = map[string]string{"USD/CNY": "7.3"}
	quote, err := QuotePurchase(ctx, testUser, topup("100", testEPayMethod))
	require.NoError(t, err)
	assert.Equal(t, 6_849_315, quote.QuotaAmount)
	amount, shownIn := operation_setting.QuotaDisplayAmount(int64(quote.QuotaAmount))
	assert.Equal(t, "CNY", shownIn)
	assert.Equal(t, "100.00", amount.StringFixed(2), "the credited quota shows as the amount paid")

	pairs, err := currency.RateOverview("USD")
	require.NoError(t, err)
	assert.Equal(t, []currency.RatePair{{Base: "USD", Quote: "CNY", Uses: []string{currency.RateUseEPay}, Override: "7.3", Status: currency.RateStatusMissing}}, pairs)
}

func signEPayCallback(params map[string]string) map[string]string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if value != "" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + testEPaySecret))
	signed := map[string]string{"sign": hex.EncodeToString(sum[:]), "sign_type": "MD5"}
	for key, value := range params {
		signed[key] = value
	}
	return signed
}

func TestEPayCheckoutAndCallbackChain(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	order, checkout, err := CreateOrder(ctx, testUser, topup("100", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionURL, checkout.Type)
	payURL, err := url.Parse(checkout.URL)
	require.NoError(t, err)
	assert.Equal(t, "537.06", payURL.Query().Get("money"), "the converted expectation is what EPay is asked for")
	assert.Equal(t, "https://www.example.test/api/payment/notify/epay/epay-token", payURL.Query().Get("notify_url"))
	assert.Equal(t, "https://www.example.test/user/wallet?payment_order="+order.OrderNo, payURL.Query().Get("return_url"))
	stored, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, int64(53706), stored.PaymentAmount)
	assert.Equal(t, "CNY", stored.PaymentCurrency)
	assert.Equal(t, "7.2419", stored.ExchangeRate)

	callback := map[string]string{
		"pid": "1001", "trade_no": "T1", "out_trade_no": order.OrderNo, "type": "alipay",
		"name": order.Subject, "money": "537.06", "trade_status": "TRADE_SUCCESS",
	}
	notify := func(token string, params map[string]string) error {
		return HandleNotify(ctx, gateway.PlatformEPay, token, gateway.Notification{HTTPMethod: http.MethodGet, Params: params}, "198.51.100.1")
	}
	assert.ErrorIs(t, notify("no-such-token", signEPayCallback(callback)), ErrNotifyTargetNotFound)
	assert.ErrorIs(t, HandleNotify(ctx, gateway.PlatformStripe, "epay-token", gateway.Notification{}, ""), ErrNotifyTargetNotFound,
		"the platform in the path must be the method's")
	forged := signEPayCallback(callback)
	forged["money"] = "1.00"
	assert.ErrorIs(t, notify("epay-token", forged), gateway.ErrInvalidCallback)

	// A signed payment of another amount is acknowledged and recorded for
	// an operator; the order stays unpaid.
	underpaid := withParams(callback, map[string]string{"money": "100.00", "trade_no": "T0"})
	require.NoError(t, notify("epay-token", signEPayCallback(underpaid)))
	var unmatched []entity.PaymentUnmatched
	require.NoError(t, model.DB.Where("order_no = ?", order.OrderNo).Find(&unmatched).Error)
	require.Len(t, unmatched, 1)
	assert.Equal(t, int64(10000), unmatched[0].Amount)

	// The confirmation query failing is retried by the gateway.
	f.epay.set(http.StatusInternalServerError, `{}`)
	err = notify("epay-token", signEPayCallback(callback))
	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, NotifyReply(gateway.CallbackStyle{TextReply: true}, err).Status)
	assert.Equal(t, 0, userQuota(t))

	f.epay.set(0, `{"code":1,"pid":"1001","trade_no":"T1","out_trade_no":"`+order.OrderNo+`","type":"alipay","money":"537.06","status":1}`)
	for range 2 {
		require.NoError(t, notify("epay-token", signEPayCallback(callback)))
	}
	assert.Equal(t, 50_000_000, userQuota(t), "the redelivered callback credits nothing more")
	settled, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, settled.Status)
	assert.Equal(t, "T1", settled.TradeNo)
	invoices, err := OrderInvoices(order.OrderNo)
	require.NoError(t, err)
	require.Len(t, invoices, 1)
	assert.Equal(t, order.OrderNo, invoices[0].InvoiceNo, "the gateway knows the first invoice by the order number")
	assert.Equal(t, entity.PaymentInvoicePaid, invoices[0].Status)
	assert.Equal(t, "T1", invoices[0].TradeNo)
}

// An API checkout asks the gateway for the payment while the buyer waits:
// the buyer gets what it answers, a gateway that fails is reported instead
// of a payment page that would not work, and a resumed checkout asks again
// for the buyer's address the order was made from. A channel saved before
// payment forms existed asks for the payment page, which its form shows; one
// that picks the form by device asks by the device of each request.
func TestEPayAPICheckout(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	method, err := store.GetMethod(testEPayMethod)
	require.NoError(t, err)
	apiConfig := strings.Replace(method.Config, `"type":""`, `"version":"v1","checkout":"api","type":"alipay"`, 1)
	require.NoError(t, model.DB.Model(method).Update("config", apiConfig).Error)
	details, err := AdminMethods()
	require.NoError(t, err)
	listed := slices.IndexFunc(details, func(detail *MethodDetail) bool { return detail.Id == testEPayMethod })
	require.NotEqual(t, -1, listed)
	assert.Equal(t, gateway.EPayFormJump, details[listed].Config["pay_form"])

	f.epay.setPayment(0, `{"code":1,"trade_no":"T1","payurl":"https://pay.example/pay/T1"}`)
	_, checkout, err := CreateOrder(ctx, testUser, topup("100", testEPayMethod), Buyer{IP: "203.0.113.9", UserAgent: desktopUserAgent})
	require.NoError(t, err)
	assert.Equal(t, &Checkout{Type: gateway.ActionURL, URL: "https://pay.example/pay/T1", OrderNo: checkout.OrderNo}, checkout)
	created := f.epay.lastPayment()
	assert.Equal(t, "537.06", created.Get("money"))
	assert.Equal(t, "203.0.113.9", created.Get("clientip"))
	assert.Equal(t, "jump", created.Get("device"))
	assert.Equal(t, "https://www.example.test/api/payment/notify/epay/epay-token", created.Get("notify_url"))

	f.epay.setPayment(0, `{"code":1,"trade_no":"T2","qrcode":"https://qr.alipay.com/abc"}`)
	_, checkout, err = CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, gateway.ActionQRCode, checkout.Type)
	assert.Equal(t, "https://qr.alipay.com/abc", checkout.QRCode)

	f.epay.setPayment(http.StatusBadGateway, `bad gateway`)
	order, checkout, err := CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.10"})
	assert.ErrorIs(t, err, errCheckoutFailed)
	assert.Nil(t, checkout)
	require.NotNil(t, order)
	assert.Equal(t, entity.PaymentOrderPending, order.Status)

	f.epay.setPayment(0, `{"code":1,"trade_no":"T3","payurl":"https://pay.example/pay/T3"}`)
	resumed, err := ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{})
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example/pay/T3", resumed.URL)
	assert.Equal(t, "203.0.113.10", f.epay.lastPayment().Get("clientip"))

	autoConfig := strings.Replace(apiConfig, `"checkout":"api"`, `"checkout":"api","pay_form":"auto"`, 1)
	require.NoError(t, model.DB.Model(method).Update("config", autoConfig).Error)
	f.epay.setPayment(0, `{"code":1,"trade_no":"T4","payurl":"https://pay.example/pay/T4"}`)
	order, checkout, err = CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{IP: "203.0.113.9", UserAgent: desktopUserAgent})
	require.NoError(t, err)
	assert.Equal(t, &Checkout{Type: gateway.ActionQRCode, QRCode: "https://pay.example/pay/T4", OrderNo: order.OrderNo}, checkout,
		"a computer is shown the payment page as a QR code")
	assert.Equal(t, "pc", f.epay.lastPayment().Get("device"))
	resumed, err = ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{UserAgent: phoneUserAgent})
	require.NoError(t, err)
	assert.Equal(t, &Checkout{Type: gateway.ActionURL, URL: "https://pay.example/pay/T4", OrderNo: order.OrderNo}, resumed)
	assert.Equal(t, "jump", f.epay.lastPayment().Get("device"))
}

const (
	desktopUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	phoneUserAgent   = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

func withParams(base, changes map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range changes {
		merged[key] = value
	}
	return merged
}

func stripeEvent(t *testing.T, eventType string, intent map[string]any) gateway.Notification {
	t.Helper()
	payload, err := common.Marshal(map[string]any{
		"id": "evt_1", "object": "event", "type": eventType, "api_version": stripe.APIVersion, "data": map[string]any{"object": intent},
	})
	require.NoError(t, err)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: testStripeSecret, Timestamp: time.Now()})
	return gateway.Notification{HTTPMethod: http.MethodPost, Body: signed.Payload, Signature: signed.Header}
}

func TestStripeCheckoutAndWebhook(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	order, checkout, err := CreateOrder(ctx, testUser, topup("10", testStripeMethod), Buyer{})
	require.NoError(t, err)
	require.Equal(t, gateway.ActionStripe, checkout.Type)
	assert.Equal(t, &gateway.StripeAction{PublishableKey: "pk_test_a", ClientSecret: "pi_1_secret", PaymentMethodType: "card"}, checkout.Stripe)
	again, err := ResumeCheckout(ctx, testUser, order.OrderNo, Buyer{})
	require.NoError(t, err)
	assert.Equal(t, checkout.Stripe, again.Stripe, "a resumed checkout reuses the order's intent")
	created, _, _ := f.stripe.Snapshot()
	assert.Len(t, created, 1)
	_, err = ResumeCheckout(ctx, testUser+1, order.OrderNo, Buyer{})
	assert.ErrorIs(t, err, store.ErrOrderNotFound, "another user's order is not theirs to pay")

	intent := gatewaytest.Intent("pi_1", 830, "usd", "succeeded", order.OrderNo, "11")
	intent["amount_received"] = 830
	f.stripe.Set("pi_1", "status", "succeeded")
	f.stripe.Set("pi_1", "amount_received", 830)
	for range 2 {
		require.NoError(t, HandleNotify(ctx, gateway.PlatformStripe, "stripe-token", stripeEvent(t, "payment_intent.succeeded", intent), ""))
	}
	assert.Equal(t, 5_000_000, userQuota(t))

	other := gatewaytest.Intent("pi_9", 830, "usd", "succeeded", "P-other-site", "99")
	assert.NoError(t, HandleNotify(ctx, gateway.PlatformStripe, "stripe-token", stripeEvent(t, "payment_intent.succeeded", other), ""),
		"another method's events are acknowledged and ignored")
	forged := stripeEvent(t, "payment_intent.succeeded", intent)
	forged.Signature = "t=1,v1=00"
	assert.ErrorIs(t, HandleNotify(ctx, gateway.PlatformStripe, "stripe-token", forged, ""), gateway.ErrInvalidCallback)

	// A payment of another payment method type does not settle the order:
	// it is recorded for an operator, once, and acknowledged.
	second, _, err := CreateOrder(ctx, testUser, topup("10", testStripeMethod), Buyer{})
	require.NoError(t, err)
	wechat := gatewaytest.Intent("pi_2", 830, "usd", "succeeded", second.OrderNo, "11")
	wechat["amount_received"], wechat["payment_method_types"] = 830, []string{"wechat_pay"}
	for range 2 {
		require.NoError(t, HandleNotify(ctx, gateway.PlatformStripe, "stripe-token", stripeEvent(t, "payment_intent.succeeded", wechat), ""))
	}
	stored, err := store.GetOrderByNo(second.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderPending, stored.Status)
	assert.Equal(t, 5_000_000, userQuota(t))
	var unmatched []entity.PaymentUnmatched
	require.NoError(t, model.DB.Find(&unmatched).Error)
	require.Len(t, unmatched, 1)
	assert.Equal(t, entity.PaymentUnmatched{
		Id: unmatched[0].Id, Platform: gateway.PlatformStripe, TradeNo: "pi_2", OrderNo: second.OrderNo, InvoiceNo: second.OrderNo, PaymentMethodId: testStripeMethod,
		UserId: testUser, Amount: 830, Currency: "USD", CreatedAt: unmatched[0].CreatedAt,
		Reason: "the Stripe payment intent collected wechat_pay, not the payment method's card",
	}, unmatched[0])
}

func TestCloseAndSyncAskTheGateway(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()

	newStripeOrder := func() *entity.PaymentOrder {
		order, _, err := CreateOrder(ctx, testUser, topup("10", testStripeMethod), Buyer{})
		require.NoError(t, err)
		stored, err := store.GetOrderByNo(order.OrderNo)
		require.NoError(t, err)
		return stored
	}
	// A user's check never cancels the payment; an unpaid intent closes
	// cancelled, so its client secret cannot be paid afterwards.
	order := newStripeOrder()
	synced, err := SyncOrder(ctx, order, "")
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderPending, synced.Status)
	closed, err := CloseOrder(ctx, order, CloseBySystem)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderClosed, closed.Status)
	assert.Equal(t, "canceled", f.stripe.Status(order.TradeNo))

	// A succeeded intent settles the order instead of closing it, through
	// the user's check or the close.
	paid := newStripeOrder()
	f.stripe.Set(paid.TradeNo, "status", "succeeded")
	f.stripe.Set(paid.TradeNo, "amount_received", 830)
	synced, err = SyncOrder(ctx, paid, "")
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, synced.Status)
	late := newStripeOrder()
	f.stripe.Set(late.TradeNo, "status", "succeeded")
	f.stripe.Set(late.TradeNo, "amount_received", 830)
	closed, err = CloseOrder(ctx, late, CloseBySystem)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, closed.Status)

	// EPay cannot cancel: an unpaid order closes by itself only once it is
	// old enough; its owner may give it up earlier.
	order, _, err = CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{})
	require.NoError(t, err)
	order, err = store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	f.epay.set(0, `{"code":1,"pid":"1001","out_trade_no":"`+order.OrderNo+`","status":0}`)
	_, err = CloseOrder(ctx, order, CloseBySystem)
	assert.ErrorIs(t, err, ErrCloseUnconfirmed)

	// The reconcile task backs off from an order it cannot close yet.
	require.NoError(t, model.DB.Model(order).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	now := time.Now()
	runPaymentReconcileOnce(ctx, now)
	deferred, err := store.GetOrderByNo(order.OrderNo)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderPending, deferred.Status)
	assert.Equal(t, 1, deferred.CheckAttempts)
	assert.Equal(t, now.Add(2*time.Minute).Unix(), deferred.NextCheckAt)

	closed, err = CloseOrder(ctx, deferred, CloseByOwner)
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderClosed, closed.Status)
	assert.Equal(t, "user", closed.CloseReason)
}

func TestBalancePurchase(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", testUser).Update("quota", 5_000_000).Error)

	order, checkout, err := CreateOrder(ctx, testUser, subscription(testPlan, entity.BalancePaymentMethodID), Buyer{})
	require.NoError(t, err)
	assert.Equal(t, CheckoutTypePaid, checkout.Type)
	assert.Equal(t, entity.PaymentOrderFinished, order.Status)
	assert.Equal(t, 5_000, userQuota(t))

	_, _, err = CreateOrder(ctx, testUser, subscription(testPlan, entity.BalancePaymentMethodID), Buyer{})
	assert.ErrorIs(t, err, model.ErrInsufficientBalance)
	var orders []entity.PaymentOrder
	require.NoError(t, model.DB.Order("id").Find(&orders).Error)
	require.Len(t, orders, 2)
	assert.Equal(t, entity.PaymentOrderClosed, orders[1].Status, "the failed balance order does not stay pending")

	order, checkout, err = CreateOrder(ctx, testUser, subscription(testFreePlan, 0), Buyer{})
	require.NoError(t, err)
	assert.Equal(t, CheckoutTypePaid, checkout.Type)
	assert.Equal(t, int64(0), order.Amount)
}

func epayInput(key string) MethodInput {
	return MethodInput{
		Name: "WeChat", Platform: gateway.PlatformEPay, OptionId: testEPayMethod, Enabled: true, Weight: 1,
		Config: map[string]any{"url": "https://pay.example.test", "pid": "2002", "key": key, "type": "wxpay"},
	}
}

func storedConfig(t *testing.T, id int) map[string]any {
	t.Helper()
	method, err := store.GetMethodIncludingDeleted(id)
	require.NoError(t, err)
	config := map[string]any{}
	require.NoError(t, common.UnmarshalJsonStr(method.Config, &config))
	return config
}

// Secrets never leave the server, and an empty secret in a form keeps the
// stored one.
func TestAdminMethodsKeepSecrets(t *testing.T) {
	setupPayment(t)
	ctx := context.Background()
	detail, err := CreateMethod(ctx, epayInput("merchant-secret"))
	require.NoError(t, err)
	assert.Equal(t, "", detail.Config["key"])
	assert.True(t, detail.SecretsSet["key"])
	assert.Equal(t, "https://www.example.test/api/payment/notify/epay/"+detail.Token, detail.NotifyURL)

	renamed := epayInput("")
	renamed.Name = "WeChat Pay"
	detail, err = UpdateMethod(ctx, detail.Id, renamed)
	require.NoError(t, err)
	assert.Equal(t, "merchant-secret", storedConfig(t, detail.Id)["key"])
	_, err = UpdateMethod(ctx, detail.Id, epayInput("rotated-secret"))
	require.NoError(t, err)
	assert.Equal(t, "rotated-secret", storedConfig(t, detail.Id)["key"])

	// Pending orders depend on the configuration: it is frozen, the label
	// is not, and the method cannot be deleted. The new channel is the only
	// enabled one of its option.
	require.NoError(t, model.DB.Model(&entity.PaymentMethod{}).Where("id = ?", testEPayMethod).Update("enabled", false).Error)
	_, _, err = CreateOrder(ctx, testUser, topup("10", testEPayMethod), Buyer{})
	require.NoError(t, err)
	changed := epayInput("")
	changed.Config["pid"] = "3003"
	_, err = UpdateMethod(ctx, detail.Id, changed)
	assert.ErrorIs(t, err, errMethodPendingOrders)
	renamed.Name = "WeChat Pay 2"
	_, err = UpdateMethod(ctx, detail.Id, renamed)
	assert.NoError(t, err)
	assert.ErrorIs(t, DeleteMethod(ctx, detail.Id), errMethodPendingOrders)

	// The same holds for the payment method type of a Stripe method, whose
	// pending intents collect the old type; a migrated configuration in
	// another key order is not a change.
	_, _, err = CreateOrder(ctx, testUser, topup("10", testStripeMethod), Buyer{})
	require.NoError(t, err)
	stripeMethod := stripeInput("", "")
	stripeMethod.Name = "Card 2"
	_, err = UpdateMethod(ctx, testStripeMethod, stripeMethod)
	require.NoError(t, err)
	stripeMethod.Config["payment_method_type"] = "alipay"
	_, err = UpdateMethod(ctx, testStripeMethod, stripeMethod)
	assert.ErrorIs(t, err, errMethodPendingOrders)
	assert.Equal(t, "card", storedConfig(t, testStripeMethod)["payment_method_type"])

	assert.ErrorIs(t, DeleteMethod(ctx, entity.BalancePaymentMethodID), errBalanceChannel)
	_, err = CreateMethod(ctx, MethodInput{Name: "Balance 2", Platform: entity.PaymentPlatformBalance, OptionId: testEPayMethod, Weight: 1})
	assert.ErrorIs(t, err, errMethodPlatform)
	local := epayInput("k")
	local.Domain = "http://localhost:3000"
	_, err = CreateMethod(ctx, local)
	assert.ErrorIs(t, err, errMethodDomain)
	moved := epayInput("k")
	moved.Platform = gateway.PlatformCryptomus
	_, err = UpdateMethod(ctx, testEPayMethod, moved)
	assert.ErrorIs(t, err, errMethodPlatformChanged)

	// The EPay V2 private key is a secret as well; the platform's public key
	// is not. A method that goes back to V1 keeps no V2 key.
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	merchantDER, err := x509.MarshalPKCS8PrivateKey(merchant)
	require.NoError(t, err)
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	platformDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	require.NoError(t, err)
	platformKey := base64.StdEncoding.EncodeToString(platformDER)
	v2 := MethodInput{Name: "Alipay V2", Platform: gateway.PlatformEPay, OptionId: testEPayMethod, Enabled: true, Weight: 1, Config: map[string]any{
		"url": "https://pay.example.test", "pid": "2002", "version": "v2", "checkout": "api", "type": "alipay",
		"private_key":         string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: merchantDER})),
		"platform_public_key": platformKey,
	}}
	detail, err = CreateMethod(ctx, v2)
	require.NoError(t, err)
	assert.Equal(t, "", detail.Config["private_key"])
	assert.True(t, detail.SecretsSet["private_key"])
	assert.Equal(t, platformKey, detail.Config["platform_public_key"])
	v2.Config["private_key"] = ""
	v2.Name = "Alipay"
	_, err = UpdateMethod(ctx, detail.Id, v2)
	require.NoError(t, err)
	assert.Equal(t, base64.StdEncoding.EncodeToString(merchantDER), storedConfig(t, detail.Id)["private_key"])
	_, err = UpdateMethod(ctx, detail.Id, epayInput("v1-key"))
	require.NoError(t, err)
	assert.NotContains(t, storedConfig(t, detail.Id), "private_key")
	assert.Equal(t, "v1-key", storedConfig(t, detail.Id)["key"])
}

func stripeInput(secretKey, domain string) MethodInput {
	return MethodInput{
		Name: "Card", Platform: gateway.PlatformStripe, OptionId: testStripeMethod, Enabled: true, Weight: 1, Domain: domain,
		Config: map[string]any{"public_key": "pk_test_a", "secret_key": secretKey, "payment_method_type": "card", "webhook_secret": "whsec_planted"},
	}
}

// A Stripe method's webhook endpoint follows its account and callback URL.
func TestStripeWebhookFollowsTheMethod(t *testing.T) {
	f := setupPayment(t)
	ctx := context.Background()
	// A method without a payment method type is refused, in words the
	// administrator reads, before anything is registered at Stripe.
	untyped := stripeInput("sk_test_a", "")
	delete(untyped.Config, "payment_method_type")
	_, err := CreateMethod(ctx, untyped)
	var message *common.Message
	require.ErrorAs(t, err, &message)
	assert.Equal(t, "Select the Stripe payment method type: card, Alipay or WeChat Pay", message.Key)
	assert.Empty(t, f.stripe.Webhooks())

	detail, err := CreateMethod(ctx, stripeInput("sk_test_a", ""))
	require.NoError(t, err)
	webhooks := f.stripe.Webhooks()
	require.Len(t, webhooks, 1)
	first := webhooks["we_1"]
	assert.Equal(t, "https://www.example.test/api/payment/notify/stripe/"+detail.Token, first["url"])
	assert.Equal(t, "sk_test_a", first["account"])
	assert.Equal(t, "whsec_we_1", storedConfig(t, detail.Id)["webhook_secret"], "a form cannot plant the signing secret")
	assert.False(t, detail.WebhookStale)

	// A new callback domain moves the endpoint in place.
	detail, err = UpdateMethod(ctx, detail.Id, stripeInput("", "https://pay.example.test"))
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example.test/api/payment/notify/stripe/"+detail.Token, f.stripe.Webhook("we_1")["url"])
	assert.Equal(t, "whsec_we_1", storedConfig(t, detail.Id)["webhook_secret"])

	// Another secret key is another account: a new endpoint there, and the
	// old one removed from the old account.
	_, err = UpdateMethod(ctx, detail.Id, stripeInput("sk_test_b", "https://pay.example.test"))
	require.NoError(t, err)
	webhooks = f.stripe.Webhooks()
	require.Len(t, webhooks, 1)
	assert.Equal(t, "sk_test_b", webhooks["we_2"]["account"])
	assert.Equal(t, "we_2", storedConfig(t, detail.Id)["webhook_endpoint_id"])

	// A method without a domain follows the server address.
	second, err := CreateMethod(ctx, stripeInput("sk_test_c", ""))
	require.NoError(t, err)
	system_setting.ServerAddress = "https://new.example.test"
	require.NoError(t, SyncStripeWebhooks(ctx))
	assert.Equal(t, "https://new.example.test/api/payment/notify/stripe/"+second.Token, f.stripe.Webhook("we_3")["url"])
	// The fixture's method records an endpoint Stripe does not know: it
	// gets a new one.
	assert.Equal(t, "we_4", storedConfig(t, testStripeMethod)["webhook_endpoint_id"])
	assert.Equal(t, "https://new.example.test/api/payment/notify/stripe/stripe-token", f.stripe.Webhook("we_4")["url"])

	// An endpoint registered for a method that cannot be saved is removed.
	const callback = "test:fail_payment_method_create"
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
		if db.Statement.Table == "payment_methods" {
			_ = db.AddError(errors.New("database unavailable"))
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callback) })
	_, err = CreateMethod(ctx, stripeInput("sk_test_d", ""))
	require.Error(t, err)
	for id, endpoint := range f.stripe.Webhooks() {
		assert.NotEqual(t, "sk_test_d", endpoint["account"], "endpoint %s of the unsaved method was left behind", id)
	}
}

func TestNotifyReply(t *testing.T) {
	text := gateway.CallbackStyle{TextReply: true}
	status := gateway.CallbackStyle{Body: true, SignatureHeader: "Stripe-Signature"}
	for _, tt := range []struct {
		name  string
		style gateway.CallbackStyle
		err   error
		want  NotifyResponse
	}{
		{"handled text", text, nil, NotifyResponse{http.StatusOK, "text/plain; charset=utf-8", "success"}},
		{"invalid text", text, gateway.InvalidCallback(errors.New("verify sign failed")), NotifyResponse{http.StatusBadRequest, "text/plain; charset=utf-8", "fail"}},
		{"retry text", text, errors.New("database unavailable"), NotifyResponse{http.StatusInternalServerError, "text/plain; charset=utf-8", "fail"}},
		{"handled status", status, nil, NotifyResponse{http.StatusOK, "application/json", `{"success":true}`}},
		{"unknown target", status, ErrNotifyTargetNotFound, NotifyResponse{http.StatusNotFound, "application/json", `{"success":false}`}},
		{"retry status", status, errors.New("confirm payment: timeout"), NotifyResponse{http.StatusInternalServerError, "application/json", `{"success":false}`}},
	} {
		assert.Equal(t, tt.want, NotifyReply(tt.style, tt.err), tt.name)
	}
}
