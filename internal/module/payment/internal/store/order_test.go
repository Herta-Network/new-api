package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// useUserCacheMiniRedis turns the Redis user cache on, backed by miniredis,
// until the test ends.
func useUserCacheMiniRedis(t *testing.T) {
	t.Helper()
	server := miniredis.RunT(t)
	oldRedisEnabled, oldRDB, oldSyncFrequency := common.RedisEnabled, common.RDB, common.SyncFrequency
	common.RedisEnabled, common.SyncFrequency = true, 2
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB, common.SyncFrequency = oldRedisEnabled, oldRDB, oldSyncFrequency
	})
}

func insertPaymentTestUser(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{Id: id, Username: "payer" + time.Now().Format("150405.000000"), Status: common.UserStatusEnabled, Quota: quota}).Error)
}

func insertPaymentTestPlan(t *testing.T, id int, maxPurchases int) *model.SubscriptionPlan {
	t.Helper()
	plan := &model.SubscriptionPlan{
		Id: id, Title: "Pro", PriceAmount: 9.99, DurationUnit: model.SubscriptionDurationMonth,
		DurationValue: 1, Enabled: true, TotalAmount: 1000, MaxPurchasePerUser: maxPurchases,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	// Plans are cached by id across tests.
	model.InvalidateSubscriptionPlanCache(id)
	return plan
}

// insertPaymentTestOrder stores a pending order and, unless it is paid with
// the balance, its first invoice, whose checkout started: it expects 1990
// hundredths of USD through Stripe method 3. Fields set in order and
// invoice override these defaults; the order copies its invoice.
func insertPaymentTestOrder(t *testing.T, order entity.PaymentOrder, invoice entity.PaymentInvoice) (*entity.PaymentOrder, *entity.PaymentInvoice) {
	t.Helper()
	now := common.GetTimestamp()
	if order.Purpose == "" {
		order.Purpose = entity.PaymentPurposeTopup
	}
	if order.Currency == "" {
		order.Currency, order.Price, order.Amount = "USD", 1990, 1990
	}
	if order.Purpose == entity.PaymentPurposeTopup && order.QuotaAmount == 0 {
		order.QuotaAmount, order.QuotaRate = 500000, "1"
	}
	if order.Status == 0 {
		order.Status = entity.PaymentOrderPending
	}
	if order.ExpiresAt == 0 {
		order.ExpiresAt = now + 900
	}
	order.CreatedAt = now
	if order.Platform == entity.PaymentPlatformBalance {
		require.NoError(t, model.DB.Create(&order).Error)
		return &order, nil
	}
	if invoice.PaymentMethodId == 0 {
		invoice.PaymentMethodId, invoice.Platform = 3, "stripe"
	}
	if invoice.PaymentCurrency == "" {
		invoice.PaymentAmount, invoice.PaymentCurrency, invoice.ExchangeRate = 1990, "USD", "1"
	}
	if invoice.Status == 0 {
		invoice.Status = entity.PaymentInvoicePending
	}
	order.PaymentMethodId, order.Platform, order.TradeNo = invoice.PaymentMethodId, invoice.Platform, invoice.TradeNo
	order.PaymentAmount, order.PaymentCurrency, order.ExchangeRate = invoice.PaymentAmount, invoice.PaymentCurrency, invoice.ExchangeRate
	require.NoError(t, model.DB.Create(&order).Error)
	if invoice.InvoiceNo == "" {
		invoice.InvoiceNo = order.OrderNo
	}
	invoice.OrderId, invoice.CreatedAt = order.Id, now
	require.NoError(t, model.DB.Create(&invoice).Error)
	return &order, &invoice
}

// addPaymentTestInvoice issues another invoice of order: a pending checkout
// of 15000 hundredths of CNY through EPay method 4.
func addPaymentTestInvoice(t *testing.T, order *entity.PaymentOrder, invoiceNo string) *entity.PaymentInvoice {
	t.Helper()
	invoice := &entity.PaymentInvoice{
		InvoiceNo: invoiceNo, OrderId: order.Id, PaymentMethodId: 4, Platform: "epay", PaymentAmount: 15000, PaymentCurrency: "CNY",
		ExchangeRate: "7.5376", Status: entity.PaymentInvoicePending, CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(invoice).Error)
	return invoice
}

func paymentTestQuota(t *testing.T, userID int) int {
	t.Helper()
	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	return quota
}

func paymentTestUnmatched(t *testing.T, orderNo string) []entity.PaymentUnmatched {
	t.Helper()
	var records []entity.PaymentUnmatched
	require.NoError(t, model.DB.Where("order_no = ?", orderNo).Order("id").Find(&records).Error)
	return records
}

func reloadPaymentOrder(t *testing.T, orderNo string) *entity.PaymentOrder {
	t.Helper()
	order, err := GetOrderByNo(orderNo)
	require.NoError(t, err)
	return order
}

func reloadPaymentInvoice(t *testing.T, invoiceNo string) *entity.PaymentInvoice {
	t.Helper()
	invoice, err := GetInvoiceByNo(invoiceNo)
	require.NoError(t, err)
	return invoice
}

// Gateways redeliver callbacks and several nodes receive them at once: one
// payment must credit the wallet exactly once, and the Redis balance must
// follow the database.
func TestSettlePaymentOrderCreditsOnceUnderConcurrency(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useUserCacheMiniRedis(t)
	insertPaymentTestUser(t, 1, 100)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-concurrent", UserId: 1}, entity.PaymentInvoice{})
	cached, err := model.GetUserQuota(1, false)
	require.NoError(t, err)
	require.Equal(t, 100, cached, "the cache holds the balance before the payment")

	outcomes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := SettleInvoice(Settlement{InvoiceNo: invoice.InvoiceNo, TradeNo: "pi_1", Amount: 1990, Currency: "usd"})
			if assert.NoError(t, err) {
				outcomes <- result.Outcome
			}
		})
	}
	wg.Wait()
	close(outcomes)
	counts := map[int]int{}
	for outcome := range outcomes {
		counts[outcome]++
	}
	assert.Equal(t, map[int]int{SettleFinished: 1, SettleDuplicate: 7}, counts)
	assert.Equal(t, 500100, paymentTestQuota(t, 1))
	cached, err = model.GetUserQuota(1, false)
	require.NoError(t, err)
	assert.Equal(t, 500100, cached, "the cache follows the committed credit")

	settled := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, entity.PaymentOrderFinished, settled.Status)
	assert.Equal(t, "pi_1", settled.TradeNo)
	assert.NotZero(t, settled.PaidAt)
	assert.NotZero(t, settled.FinishedAt)
	paid := reloadPaymentInvoice(t, invoice.InvoiceNo)
	assert.Equal(t, entity.PaymentInvoicePaid, paid.Status)
	assert.Equal(t, "pi_1", paid.TradeNo)
	assert.Empty(t, paymentTestUnmatched(t, order.OrderNo))
}

// A payment that cannot settle its order is recorded once for an operator
// and changes nothing else.
func TestSettlePaymentOrderRecordsUnmatchedPayments(t *testing.T) {
	tests := []struct {
		name       string
		order      entity.PaymentOrder
		invoice    entity.PaymentInvoice
		settlement Settlement
		wantStatus int
	}{
		{
			name:       "amount below the expectation",
			settlement: Settlement{TradeNo: "pi_1", Amount: 1000, Currency: "USD"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "another currency",
			settlement: Settlement{TradeNo: "pi_1", Amount: 1990, Currency: "EUR"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "bound to another trade",
			invoice:    entity.PaymentInvoice{TradeNo: "pi_claimed"},
			settlement: Settlement{TradeNo: "pi_other", Amount: 1990, Currency: "USD"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "second payment of a finished order",
			order:      entity.PaymentOrder{Status: entity.PaymentOrderFinished},
			invoice:    entity.PaymentInvoice{TradeNo: "pi_first", Status: entity.PaymentInvoicePaid},
			settlement: Settlement{TradeNo: "pi_second", Amount: 1990, Currency: "USD"},
			wantStatus: entity.PaymentOrderFinished,
		},
		{
			name:       "closed with another amount",
			order:      entity.PaymentOrder{Status: entity.PaymentOrderClosed},
			invoice:    entity.PaymentInvoice{Status: entity.PaymentInvoiceClosed},
			settlement: Settlement{TradeNo: "pi_late", Amount: 2000, Currency: "USD"},
			wantStatus: entity.PaymentOrderClosed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.Reset(t, model.DB)
			insertPaymentTestUser(t, 1, 0)
			tt.order.OrderNo, tt.order.UserId = "P-unmatched", 1
			order, invoice := insertPaymentTestOrder(t, tt.order, tt.invoice)
			tt.settlement.InvoiceNo = invoice.InvoiceNo
			for range 2 {
				result, err := SettleInvoice(tt.settlement)
				require.NoError(t, err)
				assert.Equal(t, SettleUnmatched, result.Outcome)
				assert.NotEmpty(t, result.Reason)
			}
			records := paymentTestUnmatched(t, order.OrderNo)
			require.Len(t, records, 1, "a redelivered payment is recorded once")
			assert.Equal(t, tt.settlement.TradeNo, records[0].TradeNo)
			assert.Equal(t, invoice.InvoiceNo, records[0].InvoiceNo)
			assert.Equal(t, tt.settlement.Amount, records[0].Amount)
			assert.Equal(t, 1, records[0].UserId)
			assert.Equal(t, tt.wantStatus, reloadPaymentOrder(t, order.OrderNo).Status)
			assert.Equal(t, invoice.Status, reloadPaymentInvoice(t, invoice.InvoiceNo).Status)
			assert.Equal(t, 0, paymentTestQuota(t, 1))
		})
	}
}

// An order is paid by whichever of its invoices is paid first: the order
// shows that payment, its other pending invoices close, and a payment that
// still arrives for another invoice is recorded for a refund without
// crediting the wallet twice.
func TestSettlePaymentOrderThroughAnyOfItsInvoices(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order, first := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-invoices", UserId: 1}, entity.PaymentInvoice{})
	second := addPaymentTestInvoice(t, order, "P-invoicesR2")
	third := addPaymentTestInvoice(t, order, "P-invoicesR3")

	result, err := SettleInvoice(Settlement{InvoiceNo: second.InvoiceNo, TradeNo: "T2", Amount: 15000, Currency: "CNY"})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	paid := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, entity.PaymentOrderFinished, paid.Status)
	assert.Equal(t, []any{4, "epay", "T2", int64(15000), "CNY", "7.5376"},
		[]any{paid.PaymentMethodId, paid.Platform, paid.TradeNo, paid.PaymentAmount, paid.PaymentCurrency, paid.ExchangeRate},
		"the order shows the payment that paid it")
	assert.Equal(t, entity.PaymentInvoiceClosed, reloadPaymentInvoice(t, first.InvoiceNo).Status)
	assert.Equal(t, "order_paid", reloadPaymentInvoice(t, third.InvoiceNo).CloseReason)

	result, err = SettleInvoice(Settlement{InvoiceNo: first.InvoiceNo, TradeNo: "pi_late", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleUnmatched, result.Outcome)
	assert.Equal(t, entity.PaymentInvoicePaid, reloadPaymentInvoice(t, first.InvoiceNo).Status, "the money arrived for that invoice")
	records := paymentTestUnmatched(t, order.OrderNo)
	require.Len(t, records, 1)
	assert.Equal(t, first.InvoiceNo, records[0].InvoiceNo)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))

	result, err = SettleInvoice(Settlement{InvoiceNo: third.InvoiceNo, TradeNo: "T2", Amount: 15000, Currency: "CNY"})
	require.NoError(t, err)
	assert.Equal(t, SettleUnmatched, result.Outcome, "one trade pays one invoice")
}

// A paid order whose top-up would cross the wallet ceiling stays paid with
// the reason, the payment is recorded for an operator, and the order can be
// fulfilled once the wallet has room.
func TestSettlePaymentOrderKeepsPaidOrderWhenFulfilmentFails(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, common.MaxWalletQuota-10)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-ceiling", UserId: 1, QuotaAmount: 100}, entity.PaymentInvoice{})

	result, err := SettleInvoice(Settlement{InvoiceNo: invoice.InvoiceNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFulfilFailed, result.Outcome)
	paid := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, entity.PaymentOrderPaid, paid.Status)
	assert.Equal(t, "pi_1", paid.TradeNo)
	assert.NotEmpty(t, paid.FulfilError)
	assert.Equal(t, common.MaxWalletQuota-10, paymentTestQuota(t, 1))
	records := paymentTestUnmatched(t, order.OrderNo)
	require.Len(t, records, 1)
	assert.Zero(t, records[0].ResolvedAt)

	// The redelivered callback acknowledges without a second record.
	result, err = SettleInvoice(Settlement{InvoiceNo: invoice.InvoiceNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleDuplicate, result.Outcome)

	_, err = FulfilOrder(order.OrderNo, "")
	require.ErrorIs(t, err, model.ErrTopUpQuotaLimitExceeded, "the wallet is still full")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("quota", 0).Error)
	fulfilled, err := FulfilOrder(order.OrderNo, "")
	require.NoError(t, err)
	assert.Equal(t, entity.PaymentOrderFinished, fulfilled.Status)
	assert.Equal(t, 100, paymentTestQuota(t, 1))
	assert.Empty(t, reloadPaymentOrder(t, order.OrderNo).FulfilError)
	assert.NotZero(t, paymentTestUnmatched(t, order.OrderNo)[0].ResolvedAt, "fulfilling resolves the record")
}

func TestSettlePaymentOrderCreatesTheSubscription(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	plan := insertPaymentTestPlan(t, 7, 1)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-sub", UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id}, entity.PaymentInvoice{})
	// A plan disabled after the purchase is still delivered: it was paid for.
	require.NoError(t, model.DB.Model(plan).Update("enabled", false).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)

	result, err := SettleInvoice(Settlement{InvoiceNo: invoice.InvoiceNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	require.Equal(t, SettleFinished, result.Outcome)
	var subscriptions []model.UserSubscription
	require.NoError(t, model.DB.Where("user_id = ?", 1).Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.Equal(t, subscriptions[0].Id, reloadPaymentOrder(t, order.OrderNo).UserSubscriptionId)

	// The purchase limit was reached meanwhile: the second order stays paid.
	second, secondInvoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-sub-2", UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id}, entity.PaymentInvoice{})
	result, err = SettleInvoice(Settlement{InvoiceNo: secondInvoice.InvoiceNo, TradeNo: "pi_2", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFulfilFailed, result.Outcome)
	assert.Equal(t, entity.PaymentOrderPaid, reloadPaymentOrder(t, second.OrderNo).Status)
	assert.Len(t, paymentTestUnmatched(t, second.OrderNo), 1)
}

// A payment that arrives after its order closed settles it when it is
// exactly what the closed checkout asked for.
func TestSettlePaymentOrderSettlesALatePayment(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-late", UserId: 1}, entity.PaymentInvoice{})
	closed, err := CloseInvoice(invoice, false, "expired")
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = CloseOrder(order, "expired")
	require.NoError(t, err)
	require.True(t, closed)

	result, err := SettleInvoice(Settlement{InvoiceNo: invoice.InvoiceNo, TradeNo: "pi_late", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	assert.Equal(t, entity.PaymentOrderFinished, reloadPaymentOrder(t, order.OrderNo).Status)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))
}

// The expectation and the trade of an invoice are written once, and the
// order shows those of its latest invoice only.
func TestPaymentExpectationAndTradeClaimAreSetOnce(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-cas", UserId: 1}, entity.PaymentInvoice{PaymentMethodId: 4, Platform: "epay"})
	require.NoError(t, model.DB.Model(invoice).Updates(map[string]any{"payment_currency": "", "payment_amount": 0, "exchange_rate": ""}).Error)

	recorded, err := SetInvoiceExpectation(invoice, 14411, "CNY", "7.2419")
	require.NoError(t, err)
	assert.True(t, recorded)
	recorded, err = SetInvoiceExpectation(invoice, 14000, "CNY", "7.0")
	require.NoError(t, err)
	assert.False(t, recorded, "the first expectation stays")
	stored := reloadPaymentInvoice(t, invoice.InvoiceNo)
	assert.Equal(t, int64(14411), stored.PaymentAmount)
	assert.Equal(t, "7.2419", stored.ExchangeRate)
	assert.Equal(t, int64(14411), reloadPaymentOrder(t, order.OrderNo).PaymentAmount, "the order shows its checkout")

	winner, err := ClaimInvoiceTrade(invoice, "inv_a")
	require.NoError(t, err)
	assert.Equal(t, "inv_a", winner)
	winner, err = ClaimInvoiceTrade(invoice, "inv_b")
	require.NoError(t, err)
	assert.Equal(t, "inv_a", winner, "a later claim gets the invoice's trade")
	assert.Equal(t, "inv_a", reloadPaymentOrder(t, order.OrderNo).TradeNo)

	// Once the order has a newer invoice, the older one no longer speaks for
	// it.
	newer, newerInvoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-cas-2", UserId: 1}, entity.PaymentInvoice{})
	addPaymentTestInvoice(t, newer, "P-cas-2R2")
	_, err = ClaimInvoiceTrade(newerInvoice, "pi_old")
	require.NoError(t, err)
	assert.Empty(t, reloadPaymentOrder(t, newer.OrderNo).TradeNo)

	_, unclaimed := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-cas-closed", UserId: 1}, entity.PaymentInvoice{})
	_, err = CloseInvoice(unclaimed, false, "expired")
	require.NoError(t, err)
	_, err = ClaimInvoiceTrade(unclaimed, "inv_c")
	assert.ErrorIs(t, err, ErrOrderNotPending)
}

// A close decided on a checkout that changed since must not commit: the
// gateway verdict covered the old checkout only. An order closes only once
// all its invoices are closed.
func TestClosePaymentOrderRequiresTheCheckoutItJudged(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-close", UserId: 1}, entity.PaymentInvoice{})
	snapshot := *invoice
	_, err := ClaimInvoiceTrade(invoice, "pi_new")
	require.NoError(t, err)

	closed, err := CloseInvoice(&snapshot, true, "expired")
	assert.ErrorIs(t, err, ErrCheckoutChanged)
	assert.False(t, closed)
	assert.Equal(t, entity.PaymentInvoicePending, reloadPaymentInvoice(t, invoice.InvoiceNo).Status)
	_, err = CloseOrder(order, "user")
	assert.ErrorIs(t, err, ErrCheckoutChanged, "a pending invoice keeps its order open")

	current := reloadPaymentInvoice(t, invoice.InvoiceNo)
	closed, err = CloseInvoice(current, true, "user")
	require.NoError(t, err)
	assert.True(t, closed)
	closed, err = CloseInvoice(current, true, "user")
	require.NoError(t, err)
	assert.False(t, closed, "an invoice closes once")
	closed, err = CloseOrder(order, "user")
	require.NoError(t, err)
	assert.True(t, closed)
	closed, err = CloseOrder(order, "user")
	require.NoError(t, err)
	assert.False(t, closed, "an order closes once")
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, entity.PaymentOrderClosed, stored.Status)
	assert.Equal(t, "user", stored.CloseReason)
	assert.Equal(t, "user", reloadPaymentInvoice(t, invoice.InvoiceNo).CloseReason)
}

func TestPayPaymentOrderWithBalanceIsAtomic(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 1500)
	plan := insertPaymentTestPlan(t, 7, 0)
	newOrder := func(orderNo string) *entity.PaymentOrder {
		order, _ := insertPaymentTestOrder(t, entity.PaymentOrder{
			OrderNo: orderNo, UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id, QuotaAmount: 1000,
			PaymentMethodId: entity.BalancePaymentMethodID, Platform: entity.PaymentPlatformBalance,
		}, entity.PaymentInvoice{})
		return order
	}
	first, second := newOrder("P-bal-1"), newOrder("P-bal-2")

	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for _, orderNo := range []string{first.OrderNo, first.OrderNo, second.OrderNo, second.OrderNo} {
		wg.Go(func() {
			_, err := PayOrderWithBalance(orderNo, 1)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		assert.True(t, err == model.ErrInsufficientBalance || err == ErrOrderNotPending, "unexpected error %v", err)
	}
	assert.Equal(t, 1, succeeded, "the balance covers one purchase")
	assert.Equal(t, 500, paymentTestQuota(t, 1))
	var subscriptions int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("user_id = ?", 1).Count(&subscriptions).Error)
	assert.EqualValues(t, 1, subscriptions)

	_, err := PayOrderWithBalance(first.OrderNo, 2)
	assert.ErrorIs(t, err, ErrOrderNotFound, "another user's order cannot be paid")
}

func TestCreatePaymentOrderEnforcesLimits(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	plan := insertPaymentTestPlan(t, 7, 2)
	newOrder := func(orderNo string, purpose string) *entity.PaymentOrder {
		return &entity.PaymentOrder{
			OrderNo: orderNo, UserId: 1, Purpose: purpose, PlanId: plan.Id, Currency: "USD", Price: 999, Amount: 999,
			PaymentMethodId: 3, Platform: "stripe", ExpiresAt: common.GetTimestamp() + 900,
		}
	}
	stripeInvoice := func() *entity.PaymentInvoice {
		return &entity.PaymentInvoice{PaymentMethodId: 3, Platform: "stripe"}
	}
	limits := OrderLimits{MaxPending: 2, MaxPlanPurchases: plan.MaxPurchasePerUser}
	first := stripeInvoice()
	require.NoError(t, CreateOrder(newOrder("P-1", entity.PaymentPurposeSubscription), limits, first))
	assert.Equal(t, "P-1", first.InvoiceNo, "the first invoice takes the order's number")
	assert.Equal(t, entity.PaymentInvoicePending, reloadPaymentInvoice(t, "P-1").Status)
	// An existing subscription and the order in flight reach the limit of 2.
	require.NoError(t, model.DB.Create(&model.UserSubscription{UserId: 1, PlanId: plan.Id, Status: "active", EndTime: common.GetTimestamp() + 3600}).Error)
	assert.ErrorIs(t, CreateOrder(newOrder("P-2", entity.PaymentPurposeSubscription), limits, stripeInvoice()), ErrPlanPurchaseLimit)
	_, err := GetInvoiceByNo("P-2")
	assert.ErrorIs(t, err, ErrInvoiceNotFound, "a refused order issues no invoice")

	require.NoError(t, CreateOrder(newOrder("P-3", entity.PaymentPurposeTopup), limits, stripeInvoice()))
	assert.ErrorIs(t, CreateOrder(newOrder("P-4", entity.PaymentPurposeTopup), limits, stripeInvoice()), ErrTooManyPendingOrders)
	// Expired orders waiting for their close do not block new ones.
	require.NoError(t, model.DB.Model(&entity.PaymentOrder{}).Where("order_no = ?", "P-3").Update("expires_at", common.GetTimestamp()-1).Error)
	require.NoError(t, CreateOrder(newOrder("P-5", entity.PaymentPurposeTopup), limits, stripeInvoice()))
}

func TestCompletePaymentOrderManually(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order, invoice := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-manual", UserId: 1}, entity.PaymentInvoice{PaymentMethodId: 4, Platform: "epay"})
	result, err := CompleteOrderManually(order.OrderNo, "")
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, ManualTradeNo(order.OrderNo), stored.TradeNo)
	assert.Equal(t, int64(1990), stored.PaymentAmount)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))
	closed := reloadPaymentInvoice(t, invoice.InvoiceNo)
	assert.Equal(t, entity.PaymentInvoiceClosed, closed.Status, "the invoice was not paid through its gateway")

	claimed, _ := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-manual-claimed", UserId: 1}, entity.PaymentInvoice{TradeNo: "pi_1"})
	_, err = CompleteOrderManually(claimed.OrderNo, "")
	assert.Error(t, err, "an order bound to a gateway payment is reconciled, not completed by hand")
	assert.Equal(t, entity.PaymentOrderPending, reloadPaymentOrder(t, claimed.OrderNo).Status)
}

// sqlRecorder keeps the statements a GORM session runs.
type sqlRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (recorder *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return recorder }
func (recorder *sqlRecorder) Info(context.Context, string, ...any)     {}
func (recorder *sqlRecorder) Warn(context.Context, string, ...any)     {}
func (recorder *sqlRecorder) Error(context.Context, string, ...any)    {}

func (recorder *sqlRecorder) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	recorder.mu.Lock()
	recorder.statements = append(recorder.statements, statement)
	recorder.mu.Unlock()
}

func (recorder *sqlRecorder) reset() {
	recorder.mu.Lock()
	recorder.statements = nil
	recorder.mu.Unlock()
}

// The payment startup steps seed the balance method once, a second startup
// rewrites nothing, one gateway payment can bind one order of its platform,
// the Stripe methods of an upgraded database collect card payments, and its
// EPay methods speak V1 through the payment page.
func TestPaymentSchemaMigration(t *testing.T) {
	pgtest.Reset(t, model.DB)
	recorder := &sqlRecorder{}
	db := model.DB.Session(&gorm.Session{Logger: recorder})
	const untypedStripe = `{"public_key":"pk_live_a","secret_key":"sk_live_a","webhook_endpoint_id":"we_1","webhook_url":"https://pay.example/n","webhook_secret":"whsec_1"}`
	for pass := range 2 {
		recorder.reset()
		require.NoError(t, EnsureBalanceMethod(db))
		if pass == 0 {
			// Methods saved before each Stripe method had one payment method
			// type, one of them deleted, and one with a type already.
			for _, method := range []entity.PaymentMethod{
				{Id: 2, Platform: "stripe", Token: "t2", Config: untypedStripe},
				{Id: 3, Platform: "stripe", Token: "t3", Config: untypedStripe, DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true}},
				{Id: 4, Platform: "stripe", Token: "t4", Config: `{"public_key":"pk_test_a","secret_key":"sk_test_a","payment_method_type":"wechat_pay"}`},
				{Id: 5, Platform: "epay", Token: "t5", Config: `{"url":"https://pay.example","pid":"1","key":"k","type":""}`},
				{Id: 6, Platform: "stripe", Token: "t6", Config: ""},
				{Id: 7, Platform: "epay", Token: "t7", Config: `{"url":"https://pay.example","pid":"1","version":"v2","checkout":"api","type":"alipay"}`},
				{Id: 8, Platform: "epay", Token: "t8", Config: `{"url":"https://pay.example","pid":"1","key":"k","checkout":"api","type":"alipay"}`},
			} {
				require.NoError(t, db.Create(&method).Error)
			}
		}
		require.NoError(t, EnsureStripeMethodTypes(db))
		require.NoError(t, EnsureEPayProtocols(db))
		if pass == 1 {
			for _, statement := range recorder.statements {
				assert.NotContains(t, statement, `SET "config"`, "repeated startup must not rewrite a configuration")
			}
		}
		// An administrator's choice survives the seed.
		require.NoError(t, db.Model(&entity.PaymentMethod{}).Where("id = ?", entity.BalancePaymentMethodID).Update("enabled", false).Error)
	}
	configs := map[int]map[string]any{}
	var methods []entity.PaymentMethod
	require.NoError(t, db.Unscoped().Where("id > 0").Find(&methods).Error)
	for _, method := range methods {
		var config map[string]any
		_ = common.UnmarshalJsonStr(method.Config, &config)
		configs[method.Id] = config
	}
	card := map[string]any{"public_key": "pk_live_a", "secret_key": "sk_live_a", "payment_method_type": "card",
		"webhook_endpoint_id": "we_1", "webhook_url": "https://pay.example/n", "webhook_secret": "whsec_1"}
	assert.Equal(t, map[int]map[string]any{
		2: card,
		3: card,
		4: {"public_key": "pk_test_a", "secret_key": "sk_test_a", "payment_method_type": "wechat_pay"},
		// An EPay method saved before versions and checkout modes keeps
		// speaking V1 through the payment page; a chosen one is kept.
		5: {"url": "https://pay.example", "pid": "1", "key": "k", "type": "", "version": "v1", "checkout": "page"},
		6: nil,
		7: {"url": "https://pay.example", "pid": "1", "version": "v2", "checkout": "api", "type": "alipay"},
		8: {"url": "https://pay.example", "pid": "1", "key": "k", "version": "v1", "checkout": "api", "type": "alipay"},
	}, configs)

	var balance entity.PaymentMethod
	require.NoError(t, db.Where("id = ?", entity.BalancePaymentMethodID).First(&balance).Error)
	assert.Equal(t, entity.PaymentPlatformBalance, balance.Platform)
	assert.False(t, balance.Enabled)
	assert.ErrorIs(t, db.Delete(&balance).Error, entity.ErrBalancePaymentMethodUndeletable)

	order := func(orderNo, platform, tradeNo string) error {
		return db.Create(&entity.PaymentOrder{OrderNo: orderNo, UserId: 1, Purpose: entity.PaymentPurposeTopup, Currency: "USD", Platform: platform, TradeNo: tradeNo, Status: entity.PaymentOrderPending}).Error
	}
	require.NoError(t, order("P-1", "stripe", ""))
	require.NoError(t, order("P-2", "stripe", ""), "orders without a trade do not collide")
	require.NoError(t, order("P-3", "stripe", "pi_1"))
	require.NoError(t, order("P-4", "cryptomus", "pi_1"), "trade numbers are unique per platform")
	assert.Error(t, order("P-5", "stripe", "pi_1"), "a trade binds one order of its platform")
	invoice := func(invoiceNo, tradeNo string) error {
		return db.Create(&entity.PaymentInvoice{InvoiceNo: invoiceNo, OrderId: 1, PaymentMethodId: 2, Platform: "stripe", TradeNo: tradeNo, Status: entity.PaymentInvoicePending}).Error
	}
	require.NoError(t, invoice("P-1", ""))
	require.NoError(t, invoice("P-1R2", ""), "invoices without a trade do not collide")
	require.NoError(t, invoice("P-1R3", "pi_2"))
	assert.Error(t, invoice("P-1R4", "pi_2"), "a trade binds one invoice of its platform")
	assert.Error(t, invoice("P-1", "pi_3"), "an invoice number is unique")
}
