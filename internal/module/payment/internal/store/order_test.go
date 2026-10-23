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

// insertPaymentTestOrder stores a pending order whose checkout started: it
// expects 1990 hundredths of USD.
func insertPaymentTestOrder(t *testing.T, order entity.PaymentOrder) *entity.PaymentOrder {
	t.Helper()
	now := common.GetTimestamp()
	defaults := entity.PaymentOrder{
		Purpose: entity.PaymentPurposeTopup, Currency: "USD", Price: 1990, Amount: 1990, QuotaAmount: 500000, QuotaRate: "1",
		PaymentMethodId: 3, Platform: "stripe", PaymentAmount: 1990, PaymentCurrency: "USD", ExchangeRate: "1",
		Status: entity.PaymentOrderPending, ExpiresAt: now + 900,
	}
	if order.Purpose == "" {
		order.Purpose = defaults.Purpose
	}
	if order.Currency == "" {
		order.Currency, order.Price, order.Amount = defaults.Currency, defaults.Price, defaults.Amount
	}
	if order.Purpose == entity.PaymentPurposeTopup && order.QuotaAmount == 0 {
		order.QuotaAmount, order.QuotaRate = defaults.QuotaAmount, defaults.QuotaRate
	}
	if order.PaymentMethodId == 0 {
		order.PaymentMethodId, order.Platform = defaults.PaymentMethodId, defaults.Platform
	}
	if order.PaymentCurrency == "" && order.Platform != entity.PaymentPlatformBalance {
		order.PaymentAmount, order.PaymentCurrency, order.ExchangeRate = defaults.PaymentAmount, defaults.PaymentCurrency, defaults.ExchangeRate
	}
	if order.Status == 0 {
		order.Status = defaults.Status
	}
	if order.ExpiresAt == 0 {
		order.ExpiresAt = defaults.ExpiresAt
	}
	require.NoError(t, model.DB.Create(&order).Error)
	return &order
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

// Gateways redeliver callbacks and several nodes receive them at once: one
// payment must credit the wallet exactly once, and the Redis balance must
// follow the database.
func TestSettlePaymentOrderCreditsOnceUnderConcurrency(t *testing.T) {
	pgtest.Reset(t, model.DB)
	useUserCacheMiniRedis(t)
	insertPaymentTestUser(t, 1, 100)
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-concurrent", UserId: 1})
	cached, err := model.GetUserQuota(1, false)
	require.NoError(t, err)
	require.Equal(t, 100, cached, "the cache holds the balance before the payment")

	outcomes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := SettleOrder(Settlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "usd"})
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
	assert.Empty(t, paymentTestUnmatched(t, order.OrderNo))
}

// A payment that cannot settle its order is recorded once for an operator
// and changes nothing else.
func TestSettlePaymentOrderRecordsUnmatchedPayments(t *testing.T) {
	tests := []struct {
		name       string
		order      entity.PaymentOrder
		settlement Settlement
		wantStatus int
	}{
		{
			name:       "amount below the expectation",
			order:      entity.PaymentOrder{},
			settlement: Settlement{TradeNo: "pi_1", Amount: 1000, Currency: "USD"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "another currency",
			order:      entity.PaymentOrder{},
			settlement: Settlement{TradeNo: "pi_1", Amount: 1990, Currency: "EUR"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "bound to another trade",
			order:      entity.PaymentOrder{TradeNo: "pi_claimed"},
			settlement: Settlement{TradeNo: "pi_other", Amount: 1990, Currency: "USD"},
			wantStatus: entity.PaymentOrderPending,
		},
		{
			name:       "second payment of a finished order",
			order:      entity.PaymentOrder{TradeNo: "pi_first", Status: entity.PaymentOrderFinished},
			settlement: Settlement{TradeNo: "pi_second", Amount: 1990, Currency: "USD"},
			wantStatus: entity.PaymentOrderFinished,
		},
		{
			name:       "closed with another amount",
			order:      entity.PaymentOrder{Status: entity.PaymentOrderClosed},
			settlement: Settlement{TradeNo: "pi_late", Amount: 2000, Currency: "USD"},
			wantStatus: entity.PaymentOrderClosed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.Reset(t, model.DB)
			insertPaymentTestUser(t, 1, 0)
			tt.order.OrderNo, tt.order.UserId = "P-unmatched", 1
			order := insertPaymentTestOrder(t, tt.order)
			tt.settlement.OrderNo = order.OrderNo
			for range 2 {
				result, err := SettleOrder(tt.settlement)
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
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, common.MaxWalletQuota-10)
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-ceiling", UserId: 1, QuotaAmount: 100})

	result, err := SettleOrder(Settlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
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
	result, err = SettleOrder(Settlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
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
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-sub", UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id})
	// A plan disabled after the purchase is still delivered: it was paid for.
	require.NoError(t, model.DB.Model(plan).Update("enabled", false).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)

	result, err := SettleOrder(Settlement{OrderNo: order.OrderNo, TradeNo: "pi_1", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	require.Equal(t, SettleFinished, result.Outcome)
	var subscriptions []model.UserSubscription
	require.NoError(t, model.DB.Where("user_id = ?", 1).Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.Equal(t, subscriptions[0].Id, reloadPaymentOrder(t, order.OrderNo).UserSubscriptionId)

	// The purchase limit was reached meanwhile: the second order stays paid.
	second := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-sub-2", UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id})
	result, err = SettleOrder(Settlement{OrderNo: second.OrderNo, TradeNo: "pi_2", Amount: 1990, Currency: "USD"})
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
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-late", UserId: 1})
	closed, err := CloseOrder(order, false, "expired")
	require.NoError(t, err)
	require.True(t, closed)

	result, err := SettleOrder(Settlement{OrderNo: order.OrderNo, TradeNo: "pi_late", Amount: 1990, Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	assert.Equal(t, entity.PaymentOrderFinished, reloadPaymentOrder(t, order.OrderNo).Status)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))
}

func TestPaymentExpectationAndTradeClaimAreSetOnce(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-cas", UserId: 1, Platform: "epay", PaymentMethodId: 4, PaymentCurrency: ""})
	require.NoError(t, model.DB.Model(order).Updates(map[string]any{"payment_currency": "", "payment_amount": 0, "exchange_rate": ""}).Error)

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

	unclaimed := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-cas-closed", UserId: 1})
	_, err = CloseOrder(unclaimed, false, "expired")
	require.NoError(t, err)
	_, err = ClaimPaymentTrade(unclaimed.Id, "inv_c")
	assert.ErrorIs(t, err, ErrOrderNotPending)
}

// A close decided on a checkout that changed since must not commit: the
// gateway verdict covered the old checkout only.
func TestClosePaymentOrderRequiresTheCheckoutItJudged(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-close", UserId: 1})
	snapshot := *order
	_, err := ClaimPaymentTrade(order.Id, "pi_new")
	require.NoError(t, err)

	closed, err := CloseOrder(&snapshot, true, "expired")
	assert.ErrorIs(t, err, ErrCheckoutChanged)
	assert.False(t, closed)
	assert.Equal(t, entity.PaymentOrderPending, reloadPaymentOrder(t, order.OrderNo).Status)

	current := reloadPaymentOrder(t, order.OrderNo)
	closed, err = CloseOrder(current, true, "user")
	require.NoError(t, err)
	assert.True(t, closed)
	closed, err = CloseOrder(current, true, "user")
	require.NoError(t, err)
	assert.False(t, closed, "a closed order closes once")
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, entity.PaymentOrderClosed, stored.Status)
	assert.Equal(t, "user", stored.CloseReason)
}

func TestPayPaymentOrderWithBalanceIsAtomic(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 1500)
	plan := insertPaymentTestPlan(t, 7, 0)
	newOrder := func(orderNo string) *entity.PaymentOrder {
		return insertPaymentTestOrder(t, entity.PaymentOrder{
			OrderNo: orderNo, UserId: 1, Purpose: entity.PaymentPurposeSubscription, PlanId: plan.Id, QuotaAmount: 1000,
			PaymentMethodId: entity.BalancePaymentMethodID, Platform: entity.PaymentPlatformBalance,
		})
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
	limits := OrderLimits{MaxPending: 2, MaxPlanPurchases: plan.MaxPurchasePerUser}
	require.NoError(t, CreateOrder(newOrder("P-1", entity.PaymentPurposeSubscription), limits))
	// An existing subscription and the order in flight reach the limit of 2.
	require.NoError(t, model.DB.Create(&model.UserSubscription{UserId: 1, PlanId: plan.Id, Status: "active", EndTime: common.GetTimestamp() + 3600}).Error)
	assert.ErrorIs(t, CreateOrder(newOrder("P-2", entity.PaymentPurposeSubscription), limits), ErrPlanPurchaseLimit)

	require.NoError(t, CreateOrder(newOrder("P-3", entity.PaymentPurposeTopup), limits))
	assert.ErrorIs(t, CreateOrder(newOrder("P-4", entity.PaymentPurposeTopup), limits), ErrTooManyPendingOrders)
	// Expired orders waiting for their close do not block new ones.
	require.NoError(t, model.DB.Model(&entity.PaymentOrder{}).Where("order_no = ?", "P-3").Update("expires_at", common.GetTimestamp()-1).Error)
	require.NoError(t, CreateOrder(newOrder("P-5", entity.PaymentPurposeTopup), limits))
}

func TestCompletePaymentOrderManually(t *testing.T) {
	pgtest.Reset(t, model.DB)
	insertPaymentTestUser(t, 1, 0)
	order := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-manual", UserId: 1, Platform: "epay", PaymentMethodId: 4})
	result, err := SettleOrder(Settlement{OrderNo: order.OrderNo, Manual: true})
	require.NoError(t, err)
	assert.Equal(t, SettleFinished, result.Outcome)
	stored := reloadPaymentOrder(t, order.OrderNo)
	assert.Equal(t, ManualTradeNo(order.OrderNo), stored.TradeNo)
	assert.Equal(t, 500000, paymentTestQuota(t, 1))

	claimed := insertPaymentTestOrder(t, entity.PaymentOrder{OrderNo: "P-manual-claimed", UserId: 1, TradeNo: "pi_1"})
	_, err = SettleOrder(Settlement{OrderNo: claimed.OrderNo, Manual: true})
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
// and the Stripe methods of an upgraded database collect card payments.
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
			} {
				require.NoError(t, db.Create(&method).Error)
			}
		}
		require.NoError(t, EnsureStripeMethodTypes(db))
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
		5: {"url": "https://pay.example", "pid": "1", "key": "k", "type": ""},
		6: nil,
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
}
