package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertPaymentTestUser(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: id, Username: "payer" + time.Now().Format("150405.000000"), Status: common.UserStatusEnabled, Quota: quota}).Error)
}

func insertPaymentTestPlan(t *testing.T, id int, maxPurchases int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Id: id, Title: "Pro", PriceAmount: 9.99, DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, Enabled: true, TotalAmount: 1000, MaxPurchasePerUser: maxPurchases,
	}
	require.NoError(t, DB.Create(plan).Error)
	// Plans are cached by id across tests.
	InvalidateSubscriptionPlanCache(id)
	return plan
}

// insertPaymentTestOrder stores a pending order whose checkout started: it
// expects 1990 hundredths of USD.
func insertPaymentTestOrder(t *testing.T, order PaymentOrder) *PaymentOrder {
	t.Helper()
	now := common.GetTimestamp()
	defaults := PaymentOrder{
		Purpose: PaymentPurposeTopup, Currency: "USD", Price: 1990, Amount: 1990, QuotaAmount: 500000, QuotaRate: "1",
		PaymentMethodId: 3, Platform: "stripe", PaymentAmount: 1990, PaymentCurrency: "USD", ExchangeRate: "1",
		Status: PaymentOrderPending, ExpiresAt: now + 900,
	}
	if order.Purpose == "" {
		order.Purpose = defaults.Purpose
	}
	if order.Currency == "" {
		order.Currency, order.Price, order.Amount = defaults.Currency, defaults.Price, defaults.Amount
	}
	if order.Purpose == PaymentPurposeTopup && order.QuotaAmount == 0 {
		order.QuotaAmount, order.QuotaRate = defaults.QuotaAmount, defaults.QuotaRate
	}
	if order.PaymentMethodId == 0 {
		order.PaymentMethodId, order.Platform = defaults.PaymentMethodId, defaults.Platform
	}
	if order.PaymentCurrency == "" && order.Platform != PaymentPlatformBalance {
		order.PaymentAmount, order.PaymentCurrency, order.ExchangeRate = defaults.PaymentAmount, defaults.PaymentCurrency, defaults.ExchangeRate
	}
	if order.Status == 0 {
		order.Status = defaults.Status
	}
	if order.ExpiresAt == 0 {
		order.ExpiresAt = defaults.ExpiresAt
	}
	require.NoError(t, DB.Create(&order).Error)
	return &order
}

func paymentTestQuota(t *testing.T, userID int) int {
	t.Helper()
	quota, err := GetUserQuota(userID, true)
	require.NoError(t, err)
	return quota
}

func paymentTestUnmatched(t *testing.T, orderNo string) []PaymentUnmatched {
	t.Helper()
	var records []PaymentUnmatched
	require.NoError(t, DB.Where("order_no = ?", orderNo).Order("id").Find(&records).Error)
	return records
}

func reloadPaymentOrder(t *testing.T, orderNo string) *PaymentOrder {
	t.Helper()
	order, err := GetPaymentOrderByNo(orderNo)
	require.NoError(t, err)
	return order
}

// Gateways redeliver callbacks and several nodes receive them at once: one
// payment must credit the wallet exactly once, and the Redis balance must
// follow the database.
func TestSettlePaymentOrderCreditsOnceUnderConcurrency(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	insertPaymentTestUser(t, 1, 100)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-concurrent", UserId: 1})
	cached, err := GetUserQuota(1, false)
	require.NoError(t, err)
	require.Equal(t, 100, cached, "the cache holds the balance before the payment")

	outcomes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "usd"})
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
	cached, err = GetUserQuota(1, false)
	require.NoError(t, err)
	assert.Equal(t, 500100, cached, "the cache follows the committed credit")

	settled := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, PaymentOrderFinished, settled.Status)
	assert.Equal(t, "pi_1", settled.TradeNo)
	assert.NotZero(t, settled.PaidAt)
	assert.NotZero(t, settled.FinishedAt)
	assert.Empty(t, paymentTestUnmatched(t, order.OrderNo))
}

// A payment that cannot settle its order is recorded once for an operator
// and changes nothing else.
func TestSettlePaymentOrderRecordsUnmatchedPayments(t *testing.T) {
	tests := []struct {
		name       string
		order      PaymentOrder
		settlement PaymentSettlement
		wantStatus int
	}{
		{
			name:       "amount below the expectation",
			order:      PaymentOrder{},
			settlement: PaymentSettlement{TradeNo: "pi_1", Amount: 1000, Currency: "USD"},
			wantStatus: PaymentOrderPending,
		},
		{
			name:       "another currency",
			order:      PaymentOrder{},
			settlement: PaymentSettlement{TradeNo: "pi_1", Amount: 1990, Currency: "EUR"},
			wantStatus: PaymentOrderPending,
		},
		{
			name:       "bound to another trade",
			order:      PaymentOrder{TradeNo: "pi_claimed"},
			settlement: PaymentSettlement{TradeNo: "pi_other", Amount: 1990, Currency: "USD"},
			wantStatus: PaymentOrderPending,
		},
		{
			name:       "second payment of a finished order",
			order:      PaymentOrder{TradeNo: "pi_first", Status: PaymentOrderFinished},
			settlement: PaymentSettlement{TradeNo: "pi_second", Amount: 1990, Currency: "USD"},
			wantStatus: PaymentOrderFinished,
		},
		{
			name:       "closed with another amount",
			order:      PaymentOrder{Status: PaymentOrderClosed},
			settlement: PaymentSettlement{TradeNo: "pi_late", Amount: 2000, Currency: "USD"},
			wantStatus: PaymentOrderClosed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateTables(t)
			insertPaymentTestUser(t, 1, 0)
			tt.order.OrderNo, tt.order.UserId = "P-unmatched", 1
			order := insertPaymentTestOrder(t, tt.order)
			tt.settlement.OrderNo = order.OrderNo
			for range 2 {
				result, err := SettlePaymentOrder(tt.settlement)
				require.NoError(t, err)
				assert.Equal(t, SettleUnmatched, result.Outcome)
				assert.NotEmpty(t, result.Reason)
			}
			records := paymentTestUnmatched(t, order.OrderNo)
			require.Len(t, records, 1, "a redelivered payment is recorded once")
			assert.Equal(t, tt.settlement.TradeNo, records[0].TradeNo)
			assert.Equal(t, tt.settlement.Amount, records[0].Amount)
			assert.Equal(t, 1, records[0].UserId)
			assert.Equal(t, tt.wantStatus, reloadPaymentOrder(t, order.OrderNo).Status)
			assert.Equal(t, 0, paymentTestQuota(t, 1))
		})
	}
}

// A paid order whose top-up would cross the wallet ceiling stays paid with
// the reason, the payment is recorded for an operator, and the order can be
// fulfilled once the wallet has room.
func TestSettlePaymentOrderKeepsPaidOrderWhenFulfilmentFails(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, common.MaxWalletQuota-10)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-ceiling", UserId: 1, QuotaAmount: 100})

	result, err := SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFulfilFailed, result.Outcome)
	paid := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, PaymentOrderPaid, paid.Status)
	assert.Equal(t, "pi_1", paid.TradeNo)
	assert.NotEmpty(t, paid.FulfilError)
	assert.Equal(t, common.MaxWalletQuota-10, paymentTestQuota(t, 1))
	records := paymentTestUnmatched(t, order.OrderNo)
	require.Len(t, records, 1)
	assert.Zero(t, records[0].ResolvedAt)

	// The redelivered callback acknowledges without a second record.
	result, err = SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleDuplicate, result.Outcome)

	_, err = FulfilPaymentOrder(order.OrderNo, "")
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded, "the wallet is still full")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 0).Error)
	fulfilled, err := FulfilPaymentOrder(order.OrderNo, "")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderFinished, fulfilled.Status)
	assert.Equal(t, 100, paymentTestQuota(t, 1))
	assert.Empty(t, reloadPaymentOrder(t, order.OrderNo).FulfilError)
	assert.NotZero(t, paymentTestUnmatched(t, order.OrderNo)[0].ResolvedAt, "fulfilling resolves the record")
}

func TestSettlePaymentOrderCreatesTheSubscription(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	plan := insertPaymentTestPlan(t, 7, 1)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-sub", UserId: 1, Purpose: PaymentPurposeSubscription, PlanId: plan.Id})
	// A plan disabled after the purchase is still delivered: it was paid for.
	require.NoError(t, DB.Model(plan).Update("enabled", false).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	result, err := SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	require.Equal(t, SettleFinished, result.Outcome)
	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 1).Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.Equal(t, subscriptions[0].Id, reloadPaymentOrder(t, order.OrderNo).UserSubscriptionId)

	// The purchase limit was reached meanwhile: the second order stays paid.
	second := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-sub-2", UserId: 1, Purpose: PaymentPurposeSubscription, PlanId: plan.Id})
	result, err = SettlePaymentOrder(PaymentSettlement{OrderNo: second.OrderNo, TradeNo: "pi_2", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFulfilFailed, result.Outcome)
	assert.Equal(t, PaymentOrderPaid, reloadPaymentOrder(t, second.OrderNo).Status)
	assert.Len(t, paymentTestUnmatched(t, second.OrderNo), 1)
}

// A payment that arrives after its order closed settles it when it is
// exactly what the closed checkout asked for.
func TestSettlePaymentOrderSettlesALatePayment(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-late", UserId: 1})
	closed, err := ClosePaymentOrder(order, false, "expired")
	require.NoError(t, err)
	require.True(t, closed)

	result, err := SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, TradeNo: "pi_late", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	assert.Equal(t, PaymentOrderFinished, reloadPaymentOrder(t, order.OrderNo).Status)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))
}

func TestPaymentExpectationAndTradeClaimAreSetOnce(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-cas", UserId: 1, Platform: "epay", PaymentMethodId: 4, PaymentCurrency: ""})
	require.NoError(t, DB.Model(order).Updates(map[string]any{"payment_currency": "", "payment_amount": 0, "exchange_rate": ""}).Error)

	recorded, err := SetPaymentExpectation(order.Id, 14411, "CNY", "7.2419")
	require.NoError(t, err)
	assert.True(t, recorded)
	recorded, err = SetPaymentExpectation(order.Id, 14000, "CNY", "7.0")
	require.NoError(t, err)
	assert.False(t, recorded, "the first expectation stays")
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, int64(14411), stored.PaymentAmount)
	assert.Equal(t, "7.2419", stored.ExchangeRate)

	winner, err := ClaimPaymentTrade(order.Id, "inv_a")
	require.NoError(t, err)
	assert.Equal(t, "inv_a", winner)
	winner, err = ClaimPaymentTrade(order.Id, "inv_b")
	require.NoError(t, err)
	assert.Equal(t, "inv_a", winner, "a later claim gets the order's trade")

	unclaimed := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-cas-closed", UserId: 1})
	_, err = ClosePaymentOrder(unclaimed, false, "expired")
	require.NoError(t, err)
	_, err = ClaimPaymentTrade(unclaimed.Id, "inv_c")
	assert.ErrorIs(t, err, ErrPaymentOrderNotPending)
}

// A close decided on a checkout that changed since must not commit: the
// gateway verdict covered the old checkout only.
func TestClosePaymentOrderRequiresTheCheckoutItJudged(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-close", UserId: 1})
	snapshot := *order
	_, err := ClaimPaymentTrade(order.Id, "pi_new")
	require.NoError(t, err)

	closed, err := ClosePaymentOrder(&snapshot, true, "expired")
	assert.ErrorIs(t, err, ErrPaymentCheckoutChanged)
	assert.False(t, closed)
	assert.Equal(t, PaymentOrderPending, reloadPaymentOrder(t, order.OrderNo).Status)

	current := reloadPaymentOrder(t, order.OrderNo)
	closed, err = ClosePaymentOrder(current, true, "user")
	require.NoError(t, err)
	assert.True(t, closed)
	closed, err = ClosePaymentOrder(current, true, "user")
	require.NoError(t, err)
	assert.False(t, closed, "a closed order closes once")
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, PaymentOrderClosed, stored.Status)
	assert.Equal(t, "user", stored.CloseReason)
}

func TestPayPaymentOrderWithBalanceIsAtomic(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 1500)
	plan := insertPaymentTestPlan(t, 7, 0)
	newOrder := func(orderNo string) *PaymentOrder {
		return insertPaymentTestOrder(t, PaymentOrder{
			OrderNo: orderNo, UserId: 1, Purpose: PaymentPurposeSubscription, PlanId: plan.Id, QuotaAmount: 1000,
			PaymentMethodId: BalancePaymentMethodID, Platform: PaymentPlatformBalance,
		})
	}
	first, second := newOrder("P-bal-1"), newOrder("P-bal-2")

	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for _, orderNo := range []string{first.OrderNo, first.OrderNo, second.OrderNo, second.OrderNo} {
		wg.Go(func() {
			_, err := PayPaymentOrderWithBalance(orderNo, 1)
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
		assert.True(t, err == ErrInsufficientBalance || err == ErrPaymentOrderNotPending, "unexpected error %v", err)
	}
	assert.Equal(t, 1, succeeded, "the balance covers one purchase")
	assert.Equal(t, 500, paymentTestQuota(t, 1))
	var subscriptions int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", 1).Count(&subscriptions).Error)
	assert.EqualValues(t, 1, subscriptions)

	_, err := PayPaymentOrderWithBalance(first.OrderNo, 2)
	assert.ErrorIs(t, err, ErrPaymentOrderNotFound, "another user's order cannot be paid")
}

func TestCreatePaymentOrderEnforcesLimits(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	plan := insertPaymentTestPlan(t, 7, 2)
	newOrder := func(orderNo string, purpose string) *PaymentOrder {
		return &PaymentOrder{
			OrderNo: orderNo, UserId: 1, Purpose: purpose, PlanId: plan.Id, Currency: "USD", Price: 999, Amount: 999,
			PaymentMethodId: 3, Platform: "stripe", ExpiresAt: common.GetTimestamp() + 900,
		}
	}
	limits := PaymentOrderLimits{MaxPending: 2, MaxPlanPurchases: plan.MaxPurchasePerUser}
	require.NoError(t, CreatePaymentOrder(newOrder("P-1", PaymentPurposeSubscription), limits))
	// An existing subscription and the order in flight reach the limit of 2.
	require.NoError(t, DB.Create(&UserSubscription{UserId: 1, PlanId: plan.Id, Status: "active", EndTime: common.GetTimestamp() + 3600}).Error)
	assert.ErrorIs(t, CreatePaymentOrder(newOrder("P-2", PaymentPurposeSubscription), limits), ErrPaymentPlanPurchaseLimit)

	require.NoError(t, CreatePaymentOrder(newOrder("P-3", PaymentPurposeTopup), limits))
	assert.ErrorIs(t, CreatePaymentOrder(newOrder("P-4", PaymentPurposeTopup), limits), ErrTooManyPendingPaymentOrders)
	// Expired orders waiting for their close do not block new ones.
	require.NoError(t, DB.Model(&PaymentOrder{}).Where("order_no = ?", "P-3").Update("expires_at", common.GetTimestamp()-1).Error)
	require.NoError(t, CreatePaymentOrder(newOrder("P-5", PaymentPurposeTopup), limits))
}

func TestCompletePaymentOrderManually(t *testing.T) {
	truncateTables(t)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-manual", UserId: 1, Platform: "epay", PaymentMethodId: 4})
	result, err := SettlePaymentOrder(PaymentSettlement{OrderNo: order.OrderNo, Manual: true})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, ManualTradeNo(order.OrderNo), stored.TradeNo)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))

	claimed := insertPaymentTestOrder(t, PaymentOrder{OrderNo: "P-manual-claimed", UserId: 1, TradeNo: "pi_1"})
	_, err = SettlePaymentOrder(PaymentSettlement{OrderNo: claimed.OrderNo, Manual: true})
	assert.Error(t, err, "an order bound to a gateway payment is reconciled, not completed by hand")
	assert.Equal(t, PaymentOrderPending, reloadPaymentOrder(t, claimed.OrderNo).Status)
}

// The payment tables migrate on a fresh database, a second startup changes
// nothing, and one gateway payment can bind one order of its platform.
func TestPaymentSchemaMigration(t *testing.T) {
	recorder := &migrationSQLRecorder{}
	db := pgtest.NewSchemaDB(t).Session(&gorm.Session{Logger: recorder})
	for pass := range 2 {
		recorder.reset()
		require.NoError(t, db.AutoMigrate(&PaymentMethod{}, &PaymentOrder{}, &PaymentUnmatched{}, &PaymentExchangeRate{}))
		require.NoError(t, ensureBalancePaymentMethod(db))
		if pass == 1 {
			assert.Empty(t, recorder.schemaMutations(), "repeated startup must not change the schema")
		}
		// An administrator's choice survives the seed.
		require.NoError(t, db.Model(&PaymentMethod{}).Where("id = ?", BalancePaymentMethodID).Update("enabled", false).Error)
	}
	var balance PaymentMethod
	require.NoError(t, db.Where("id = ?", BalancePaymentMethodID).First(&balance).Error)
	assert.Equal(t, PaymentPlatformBalance, balance.Platform)
	assert.False(t, balance.Enabled)
	assert.ErrorIs(t, db.Delete(&balance).Error, ErrBalancePaymentMethodUndeletable)

	order := func(orderNo, platform, tradeNo string) error {
		return db.Create(&PaymentOrder{OrderNo: orderNo, UserId: 1, Purpose: PaymentPurposeTopup, Currency: "USD", Platform: platform, TradeNo: tradeNo, Status: PaymentOrderPending}).Error
	}
	require.NoError(t, order("P-1", "stripe", ""))
	require.NoError(t, order("P-2", "stripe", ""), "orders without a trade do not collide")
	require.NoError(t, order("P-3", "stripe", "pi_1"))
	require.NoError(t, order("P-4", "cryptomus", "pi_1"), "trade numbers are unique per platform")
	assert.Error(t, order("P-5", "stripe", "pi_1"), "a trade binds one order of its platform")
}

// An upgraded database loses the tables, provider columns and options of the
// payment system service/payment replaced, keeps everything else, and a
// second startup changes nothing.
func TestDropRetiredPayments(t *testing.T) {
	recorder := &migrationSQLRecorder{}
	db := pgtest.NewSchemaDB(t).Session(&gorm.Session{Logger: recorder})
	require.NoError(t, db.AutoMigrate(&User{}, &SubscriptionPlan{}, &Option{}))
	for _, statement := range []string{
		`CREATE TABLE top_ups (id bigint PRIMARY KEY, trade_no varchar(255) UNIQUE)`,
		`CREATE TABLE subscription_orders (id bigint PRIMARY KEY, trade_no varchar(255) UNIQUE)`,
		`INSERT INTO top_ups VALUES (1, 'USR1NO1')`,
		`ALTER TABLE subscription_plans ADD COLUMN currency varchar(8) NOT NULL DEFAULT 'USD',
			ADD COLUMN stripe_price_id varchar(128) DEFAULT '', ADD COLUMN creem_product_id varchar(128) DEFAULT '',
			ADD COLUMN waffo_pancake_product_id varchar(128) DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN stripe_customer varchar(64)`,
		`CREATE INDEX idx_users_stripe_customer ON users (stripe_customer)`,
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
	require.NoError(t, db.Create(&SubscriptionPlan{Id: 1, Title: "Pro", PriceAmount: 9.99, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}).Error)
	require.NoError(t, db.Exec(`UPDATE subscription_plans SET stripe_price_id = 'price_1' WHERE id = 1`).Error)
	for _, key := range []string{
		"Price", "MinTopUp", "PayMethods", "PayAddress", "CustomCallbackAddress", "EpayId", "EpayKey",
		"StripeApiSecret", "StripeWebhookSecret", "CreemProducts", "WaffoPrivateKey", "WaffoPancakePrivateKey",
		"WaffoPancakeWebhookTestKey", "USDExchangeRate", "TopUpLink", "QuotaPerUnit", "payment_setting.min_topup",
	} {
		require.NoError(t, db.Create(&Option{Key: key, Value: "1"}).Error)
	}

	for pass := range 2 {
		recorder.reset()
		require.NoError(t, dropRetiredPayments(db))
		if pass == 1 {
			assert.Empty(t, recorder.schemaMutations(), "repeated startup must not change the schema")
		}
	}

	migrator := db.Migrator()
	assert.False(t, migrator.HasTable("top_ups"))
	assert.False(t, migrator.HasTable("subscription_orders"))
	for _, column := range []string{"currency", "stripe_price_id", "creem_product_id", "waffo_pancake_product_id"} {
		assert.False(t, migrator.HasColumn("subscription_plans", column), column)
	}
	assert.False(t, migrator.HasColumn("users", "stripe_customer"))
	assert.False(t, migrator.HasIndex("users", "idx_users_stripe_customer"))
	var plan SubscriptionPlan
	require.NoError(t, db.First(&plan, 1).Error)
	assert.Equal(t, "Pro", plan.Title)
	var keys []string
	require.NoError(t, db.Model(&Option{}).Pluck("key", &keys).Error)
	assert.ElementsMatch(t, []string{"USDExchangeRate", "TopUpLink", "QuotaPerUnit", "payment_setting.min_topup"}, keys)
}
