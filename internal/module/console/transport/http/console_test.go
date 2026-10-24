package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/console"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The console fixture is read on 2026-10-22 at 10:30 in Asia/Shanghai, with
// CNY as the site currency. Several records sit just before and after the
// local midnight, which is 16:00 UTC of the day before.
var consoleTestZone = func() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return zone
}()

func consoleTestTime(month time.Month, day, hour, minute, second int) int64 {
	return time.Date(2026, month, day, hour, minute, second, 0, consoleTestZone).Unix()
}

func setupConsoleControllerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false
	pgtest.Reset(t, model.DB)
	consoleNow = func() time.Time { return time.Date(2026, time.October, 22, 10, 30, 0, 0, consoleTestZone) }
	setting := operation_setting.GetPaymentSetting()
	previousCurrency, previousExport := setting.Currency, common.DataExportEnabled
	previousOverrides, previousAPIKey := setting.ExchangeRateOverrides, setting.ExchangeRateAPIKey
	setting.Currency, common.DataExportEnabled = "CNY", true
	setting.ExchangeRateOverrides, setting.ExchangeRateAPIKey = map[string]string{}, ""
	t.Cleanup(func() {
		consoleNow = time.Now
		setting.Currency, common.DataExportEnabled = previousCurrency, previousExport
		setting.ExchangeRateOverrides, setting.ExchangeRateAPIKey = previousOverrides, previousAPIKey
	})

	users := []model.User{
		{Id: 1, Username: "alice", AffCode: "aff-alice", CreatedAt: consoleTestTime(time.August, 15, 12, 0, 0)},
		{Id: 2, Username: "bob", AffCode: "aff-bob", CreatedAt: consoleTestTime(time.October, 21, 23, 59, 0)},
		{Id: 3, Username: "carol", AffCode: "aff-carol", CreatedAt: consoleTestTime(time.October, 22, 0, 0, 0)},
		{Id: 4, Username: "dave", AffCode: "aff-dave", CreatedAt: consoleTestTime(time.October, 22, 9, 15, 0)},
		{Id: 5, Username: "erin", AffCode: "aff-erin", CreatedAt: consoleTestTime(time.September, 3, 8, 0, 0)},
	}
	require.NoError(t, model.DB.Create(&users).Error)
	// A deleted account still counts as a registration.
	require.NoError(t, model.DB.Delete(&model.User{Id: 4}).Error)

	channels := []model.Channel{
		{Id: 1, Name: "a", Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "b", Status: common.ChannelStatusEnabled},
		{Id: 3, Name: "c", Status: common.ChannelStatusEnabled},
		{Id: 4, Name: "d", Status: common.ChannelStatusManuallyDisabled},
		{Id: 5, Name: "e", Status: common.ChannelStatusAutoDisabled},
		{Id: 6, Name: "f", Status: common.ChannelStatusAutoDisabled},
	}
	require.NoError(t, model.DB.Create(&channels).Error)

	paid := func(order entity.PaymentOrder, paidAt int64) entity.PaymentOrder {
		order.PaidAt, order.FinishedAt = paidAt, paidAt
		if order.Status == 0 {
			order.Status = entity.PaymentOrderFinished
		}
		if order.Status == entity.PaymentOrderPaid {
			order.FinishedAt = 0
		}
		return order
	}
	orders := []entity.PaymentOrder{
		paid(entity.PaymentOrder{OrderNo: "o1", UserId: 1, Purpose: entity.PaymentPurposeTopup, Platform: "stripe", TradeNo: "st-1", Currency: "CNY", Amount: 10000}, consoleTestTime(time.August, 20, 10, 0, 0)),
		paid(entity.PaymentOrder{OrderNo: "o2", UserId: 1, Purpose: entity.PaymentPurposeSubscription, Platform: "epay", TradeNo: "ep-2", Currency: "CNY", Amount: 5000}, consoleTestTime(time.October, 22, 0, 0, 0)),
		paid(entity.PaymentOrder{OrderNo: "o3", UserId: 2, Purpose: entity.PaymentPurposeTopup, Platform: "epay", TradeNo: "ep-3", Currency: "CNY", Amount: 2000}, consoleTestTime(time.October, 21, 23, 59, 59)),
		// Paid, but its fulfilment failed.
		paid(entity.PaymentOrder{OrderNo: "o4", UserId: 3, Purpose: entity.PaymentPurposeTopup, Platform: "stripe", TradeNo: "st-4", Currency: "CNY", Amount: 3000, Status: entity.PaymentOrderPaid}, consoleTestTime(time.October, 22, 9, 20, 0)),
		// Completed by an administrator.
		paid(entity.PaymentOrder{OrderNo: "o5", UserId: 3, Purpose: entity.PaymentPurposeSubscription, Platform: "epay", TradeNo: payment.ManualTradeNo("o5"), Currency: "CNY", Amount: 1500}, consoleTestTime(time.October, 22, 10, 5, 0)),
		// Paid with the wallet balance.
		paid(entity.PaymentOrder{OrderNo: "o6", UserId: 5, Purpose: entity.PaymentPurposeSubscription, Platform: entity.PaymentPlatformBalance, Currency: "CNY", Amount: 800}, consoleTestTime(time.October, 22, 8, 0, 0)),
		// Priced in a former site currency.
		paid(entity.PaymentOrder{OrderNo: "o7", UserId: 5, Purpose: entity.PaymentPurposeTopup, Platform: "cryptomus", TradeNo: "cm-7", Currency: "USD", Amount: 1200}, consoleTestTime(time.September, 10, 12, 0, 0)),
		{OrderNo: "o8", UserId: 4, Purpose: entity.PaymentPurposeTopup, Platform: "stripe", Currency: "CNY", Amount: 9900, Status: entity.PaymentOrderPending},
		{OrderNo: "o9", UserId: 2, Purpose: entity.PaymentPurposeTopup, Platform: "stripe", Currency: "CNY", Amount: 4000, Status: entity.PaymentOrderClosed},
		// Finished without a paid_at: it counts from its finish.
		{OrderNo: "o10", UserId: 2, Purpose: entity.PaymentPurposeTopup, Platform: "stripe", TradeNo: "st-10", Currency: "CNY", Amount: 2500, Status: entity.PaymentOrderFinished, FinishedAt: consoleTestTime(time.October, 22, 10, 10, 0)},
		paid(entity.PaymentOrder{OrderNo: "o11", UserId: 1, Purpose: entity.PaymentPurposeTopup, Platform: "epay", TradeNo: "ep-11", Currency: "EUR", Amount: 700}, consoleTestTime(time.October, 22, 7, 0, 0)),
		paid(entity.PaymentOrder{OrderNo: "o12", UserId: 5, Purpose: entity.PaymentPurposeSubscription, Platform: entity.PaymentPlatformBalance, Currency: "USD", Amount: 300}, consoleTestTime(time.October, 22, 6, 0, 0)),
	}
	require.NoError(t, model.DB.Create(&orders).Error)

	unmatched := []entity.PaymentUnmatched{
		// The record of o4's failed fulfilment: counted as the order.
		{Platform: "stripe", TradeNo: "st-4", OrderNo: "o4", UserId: 3, Amount: 3000, Currency: "CNY", Reason: "fulfilment failed"},
		{Platform: "epay", TradeNo: "ep-dup", OrderNo: "o2", UserId: 1, Amount: 5000, Currency: "CNY", Reason: "the order was already paid with another trade"},
		{Platform: "stripe", TradeNo: "st-x", OrderNo: "o3", UserId: 2, Amount: 1999, Currency: "USD", Reason: "the paid amount differs from the payment expectation"},
		{Platform: "stripe", TradeNo: "st-y", OrderNo: "o1", UserId: 1, Amount: 900, Currency: "CNY", Reason: "refunded", ResolvedAt: consoleTestTime(time.October, 1, 0, 0, 0)},
	}
	require.NoError(t, model.DB.Create(&unmatched).Error)

	usage := []model.QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-a", CreatedAt: consoleTestTime(time.October, 22, 0, 0, 0), Count: 3, Quota: 3000, TokenUsed: 300},
		{UserID: 2, Username: "bob", ModelName: "gpt-b", CreatedAt: consoleTestTime(time.October, 22, 9, 0, 0), Count: 2, Quota: 5000, TokenUsed: 500},
		{UserID: 1, Username: "alice", ModelName: "gpt-b", CreatedAt: consoleTestTime(time.October, 22, 10, 0, 0), Count: 1, Quota: 1000, TokenUsed: 100},
		{UserID: 3, Username: "carol", ModelName: "gpt-c", CreatedAt: consoleTestTime(time.October, 21, 23, 0, 0), Count: 4, Quota: 8000, TokenUsed: 800},
		{UserID: 2, Username: "bob", ModelName: "gpt-a", CreatedAt: consoleTestTime(time.October, 21, 12, 0, 0), Count: 1, Quota: 2000, TokenUsed: 200},
		{UserID: 1, Username: "alice", ModelName: "gpt-a", CreatedAt: consoleTestTime(time.October, 20, 12, 0, 0), Count: 9, Quota: 9000, TokenUsed: 900},
	}
	require.NoError(t, model.DB.Create(&usage).Error)
}

func getConsole[T any](t *testing.T, handler gin.HandlerFunc, target string) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    T      `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success, payload.Message)
	return payload.Data
}

func TestGetConsoleOverviewCountsTodayInServerZone(t *testing.T) {
	setupConsoleControllerTestDB(t)
	// Pinned as the price of one CNY in EUR, so 7.00 EUR is 7.00 / 0.128.
	operation_setting.GetPaymentSetting().ExchangeRateOverrides = map[string]string{"CNY/EUR": "0.128"}

	overview := getConsole[console.Overview](t, GetConsoleOverview, "/api/admin/console/overview")

	assert.Equal(t, console.Overview{
		TimeZone:         "Asia/Shanghai",
		UTCOffset:        8 * 3600,
		UsageDataEnabled: true,
		Requests:         6,
		Quota:            9000,
		Tokens:           900,
		ActiveUsers:      2,
		Registrations:    2,
		Currency:         "CNY",
		// The EUR order is converted in; the wallet balance's USD order is
		// not revenue.
		Revenue:       12000 + 5469,
		RevenueOrders: 5,
		OtherRevenue:  []console.Amount{},
		Channels:      console.ChannelCounts{Total: 6, Enabled: 3, ManuallyDisabled: 1, AutoDisabled: 2},
		Attention: console.Attention{
			Count:             3,
			UnfulfilledOrders: 1,
			UnmatchedPayments: 2,
			Amounts:           []console.Amount{{Currency: "CNY", Amount: 8000}, {Currency: "USD", Amount: 1999}},
		},
	}, overview)
}

// Revenue in a currency without a rate to the site currency is not added in
// but reported apart.
func TestGetConsoleOverviewReportsRevenueWithoutRateApart(t *testing.T) {
	setupConsoleControllerTestDB(t)

	overview := getConsole[console.Overview](t, GetConsoleOverview, "/api/admin/console/overview")

	assert.Equal(t, int64(12000), overview.Revenue)
	assert.Equal(t, int64(4), overview.RevenueOrders)
	assert.Equal(t, []console.Amount{{Currency: "EUR", Amount: 700}}, overview.OtherRevenue)
}

func TestGetConsoleRevenueSeparatesBalanceManualAndOtherCurrencies(t *testing.T) {
	setupConsoleControllerTestDB(t)

	revenue := getConsole[console.Revenue](t, GetConsoleRevenue, "/api/admin/console/revenue")

	assert.Equal(t, "Asia/Shanghai", revenue.TimeZone)
	assert.Equal(t, "CNY", revenue.Currency)

	// Today, by hour up to the current one. o3 was paid one second before
	// the local midnight, the same UTC day as o2.
	today := revenue.Today
	require.Len(t, today.List, 11)
	assert.Equal(t, console.RevenueBucket{Date: "2026-10-22 00:00", Start: consoleTestTime(time.October, 22, 0, 0, 0), Total: 5000, Subscription: 5000}, today.List[0])
	assert.Equal(t, console.RevenueBucket{Date: "2026-10-22 09:00", Start: consoleTestTime(time.October, 22, 9, 0, 0), Total: 3000, Topup: 3000}, today.List[9])
	assert.Equal(t, console.RevenueBucket{Date: "2026-10-22 10:00", Start: consoleTestTime(time.October, 22, 10, 0, 0), Total: 4000, Topup: 2500, Subscription: 1500}, today.List[10])
	for _, bucket := range today.List[1:9] {
		assert.Zero(t, bucket.Total, bucket.Date)
	}
	today.List = nil
	assert.Equal(t, console.RevenuePeriod{
		Total: 12000, Topup: 5500, Subscription: 6500, Orders: 4,
		Manual: 1500, ManualOrders: 1,
		BalanceSpending: 800, BalanceOrders: 1,
		OtherCurrencies: []console.CurrencyRevenue{
			{Currency: "EUR", Revenue: 700, Orders: 1},
			{Currency: "USD", BalanceSpending: 300, BalanceOrders: 1},
		},
	}, today)

	monthly := revenue.Monthly
	require.Len(t, monthly.List, 22)
	assert.Equal(t, "2026-10-01", monthly.List[0].Date)
	assert.Equal(t, console.RevenueBucket{Date: "2026-10-21", Start: consoleTestTime(time.October, 21, 0, 0, 0), Total: 2000, Topup: 2000}, monthly.List[20])
	assert.Equal(t, console.RevenueBucket{Date: "2026-10-22", Start: consoleTestTime(time.October, 22, 0, 0, 0), Total: 12000, Topup: 5500, Subscription: 6500}, monthly.List[21])
	monthly.List = nil
	assert.Equal(t, console.RevenuePeriod{
		Total: 14000, Topup: 7500, Subscription: 6500, Orders: 5,
		Manual: 1500, ManualOrders: 1,
		BalanceSpending: 800, BalanceOrders: 1,
		OtherCurrencies: []console.CurrencyRevenue{
			{Currency: "EUR", Revenue: 700, Orders: 1},
			{Currency: "USD", BalanceSpending: 300, BalanceOrders: 1},
		},
	}, monthly)

	// All time, by month from the first paid order.
	assert.Equal(t, []console.RevenueBucket{
		{Date: "2026-08", Start: consoleTestTime(time.August, 1, 0, 0, 0), Total: 10000, Topup: 10000},
		{Date: "2026-09", Start: consoleTestTime(time.September, 1, 0, 0, 0)},
		{Date: "2026-10", Start: consoleTestTime(time.October, 1, 0, 0, 0), Total: 14000, Topup: 7500, Subscription: 6500},
	}, revenue.All.List)
	revenue.All.List = nil
	assert.Equal(t, console.RevenuePeriod{
		Total: 24000, Topup: 17500, Subscription: 6500, Orders: 6,
		Manual: 1500, ManualOrders: 1,
		BalanceSpending: 800, BalanceOrders: 1,
		OtherCurrencies: []console.CurrencyRevenue{
			{Currency: "EUR", Revenue: 700, Orders: 1},
			{Currency: "USD", Revenue: 1200, Orders: 1, BalanceSpending: 300, BalanceOrders: 1},
		},
	}, revenue.All)
}

func TestGetConsoleUsersSplitsFirstTimeAndReturningPayers(t *testing.T) {
	setupConsoleControllerTestDB(t)

	users := getConsole[console.Users](t, GetConsoleUsers, "/api/admin/console/users")

	// Today alice (o2, o11) and bob (o10) paid again, carol (o4) for the
	// first time; carol's 10:05 order makes her a returning payer of 10:00.
	today := users.Today
	require.Len(t, today.List, 11)
	assert.Equal(t, console.UserBucket{Date: "2026-10-22 00:00", Start: consoleTestTime(time.October, 22, 0, 0, 0), Registrations: 1, ReturningPayers: 1}, today.List[0])
	assert.Equal(t, console.UserBucket{Date: "2026-10-22 07:00", Start: consoleTestTime(time.October, 22, 7, 0, 0), ReturningPayers: 1}, today.List[7])
	assert.Equal(t, console.UserBucket{Date: "2026-10-22 09:00", Start: consoleTestTime(time.October, 22, 9, 0, 0), Registrations: 1, FirstTimePayers: 1}, today.List[9])
	assert.Equal(t, console.UserBucket{Date: "2026-10-22 10:00", Start: consoleTestTime(time.October, 22, 10, 0, 0), ReturningPayers: 2}, today.List[10])
	assert.Equal(t, [3]int64{2, 1, 2}, [3]int64{today.Registrations, today.FirstTimePayers, today.ReturningPayers})

	monthly := users.Monthly
	require.Len(t, monthly.List, 22)
	assert.Equal(t, console.UserBucket{Date: "2026-10-21", Start: consoleTestTime(time.October, 21, 0, 0, 0), Registrations: 1, FirstTimePayers: 1}, monthly.List[20])
	assert.Equal(t, console.UserBucket{Date: "2026-10-22", Start: consoleTestTime(time.October, 22, 0, 0, 0), Registrations: 2, FirstTimePayers: 1, ReturningPayers: 2}, monthly.List[21])
	assert.Equal(t, [3]int64{3, 2, 1}, [3]int64{monthly.Registrations, monthly.FirstTimePayers, monthly.ReturningPayers})

	// All time starts with alice's registration; erin's first payment was
	// in a former site currency and still makes her a payer.
	assert.Equal(t, []console.UserBucket{
		{Date: "2026-08", Start: consoleTestTime(time.August, 1, 0, 0, 0), Registrations: 1, FirstTimePayers: 1},
		{Date: "2026-09", Start: consoleTestTime(time.September, 1, 0, 0, 0), Registrations: 1, FirstTimePayers: 1},
		{Date: "2026-10", Start: consoleTestTime(time.October, 1, 0, 0, 0), Registrations: 3, FirstTimePayers: 2, ReturningPayers: 1},
	}, users.All.List)
	assert.Equal(t, [3]int64{5, 4, 0}, [3]int64{users.All.Registrations, users.All.FirstTimePayers, users.All.ReturningPayers})
}

func TestGetConsoleRankingsRanksTodayAndYesterdayByQuota(t *testing.T) {
	setupConsoleControllerTestDB(t)

	today := getConsole[console.Rankings](t, GetConsoleRankings, "/api/admin/console/rankings")
	assert.Equal(t, console.Rankings{
		TimeZone: "Asia/Shanghai", UTCOffset: 8 * 3600, Period: "today",
		Start: consoleTestTime(time.October, 22, 0, 0, 0), End: consoleTestTime(time.October, 23, 0, 0, 0),
		Models: []console.ModelRank{
			{ModelName: "gpt-b", Quota: 6000, Requests: 3, Tokens: 600},
			{ModelName: "gpt-a", Quota: 3000, Requests: 3, Tokens: 300},
		},
		Users: []console.UserRank{
			{UserId: 2, Username: "bob", Quota: 5000, Requests: 2, Tokens: 500},
			{UserId: 1, Username: "alice", Quota: 4000, Requests: 4, Tokens: 400},
		},
	}, today)

	yesterday := getConsole[console.Rankings](t, GetConsoleRankings, "/api/admin/console/rankings?period=yesterday")
	assert.Equal(t, consoleTestTime(time.October, 21, 0, 0, 0), yesterday.Start)
	assert.Equal(t, []console.ModelRank{
		{ModelName: "gpt-c", Quota: 8000, Requests: 4, Tokens: 800},
		{ModelName: "gpt-a", Quota: 2000, Requests: 1, Tokens: 200},
	}, yesterday.Models)
	assert.Equal(t, []console.UserRank{
		{UserId: 3, Username: "carol", Quota: 8000, Requests: 4, Tokens: 800},
		{UserId: 2, Username: "bob", Quota: 2000, Requests: 1, Tokens: 200},
	}, yesterday.Users)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/console/rankings?period=week", nil)
	GetConsoleRankings(ctx)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Equal(t, "Invalid parameters", payload.Message)
}
