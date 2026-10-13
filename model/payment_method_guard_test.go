package model

import (
	"bytes"
	"context"
	"github.com/QuantumNous/new-api/pkg/payment"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"log"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertUserForPaymentGuardTest(t *testing.T, id int, quota int) *User {
	t.Helper()
	user := &User{
		Id:       id,
		Username: "payment_guard_user",
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func insertSubscriptionPlanForPaymentGuardTest(t *testing.T, id int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Id:            id,
		Title:         "Guard Plan",
		PriceAmount:   9.99,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

func insertSubscriptionOrderForPaymentGuardTest(t *testing.T, tradeNo string, userID int, planID int, paymentProvider string) {
	t.Helper()
	order := &SubscriptionOrder{
		UserId:          userID,
		PlanId:          planID,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, order.Insert())
}

func insertTopUpForPaymentGuardTest(t *testing.T, tradeNo string, userID int, paymentProvider string) {
	t.Helper()
	topUp := &TopUp{
		UserId:          userID,
		Amount:          2,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, topUp.Insert())
}

func getTopUpStatusForPaymentGuardTest(t *testing.T, tradeNo string) string {
	t.Helper()
	topUp := GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, topUp)
	return topUp.Status
}

func countUserSubscriptionsForPaymentGuardTest(t *testing.T, userID int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userID).Count(&count).Error)
	return count
}

func getUserQuotaForPaymentGuardTest(t *testing.T, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").Where("id = ?", userID).First(&user).Error)
	return user.Quota
}

func TestUpdatePendingTopUpStatus_RejectsMismatchedPaymentProvider(t *testing.T) {
	testCases := []struct {
		name                    string
		tradeNo                 string
		storedPaymentProvider   string
		expectedPaymentProvider string
		targetStatus            string
	}{
		{
			name:                    "stripe expire",
			tradeNo:                 "stripe-expire-guard",
			storedPaymentProvider:   PaymentProviderCryptomus,
			expectedPaymentProvider: PaymentProviderStripe,
			targetStatus:            common.TopUpStatusExpired,
		},
		{
			name:                    "epay failed",
			tradeNo:                 "epay-failed-guard",
			storedPaymentProvider:   PaymentProviderStripe,
			expectedPaymentProvider: PaymentProviderEpay,
			targetStatus:            common.TopUpStatusFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			insertUserForPaymentGuardTest(t, 150, 0)
			insertTopUpForPaymentGuardTest(t, tc.tradeNo, 150, tc.storedPaymentProvider)

			err := UpdatePendingTopUpStatus(tc.tradeNo, tc.expectedPaymentProvider, tc.targetStatus)
			require.ErrorIs(t, err, ErrPaymentMethodMismatch)
			assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tc.tradeNo))
		})
	}
}

func TestCompleteSubscriptionOrder_RejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 202, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 301)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-guard-order", 202, plan.Id, PaymentProviderStripe)

	err := CompleteSubscriptionOrder("sub-guard-order", `{"provider":"epay"}`, PaymentProviderEpay, "alipay")
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	order := GetSubscriptionOrderByTradeNo("sub-guard-order")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 202))

	topUp := GetTopUpByTradeNo("sub-guard-order")
	assert.Nil(t, topUp)
}

func TestExpireSubscriptionOrder_RejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 303, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 401)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-expire-guard", 303, plan.Id, PaymentProviderStripe)

	err := ExpireSubscriptionOrder("sub-expire-guard", PaymentProviderCryptomus)
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	order := GetSubscriptionOrderByTradeNo("sub-expire-guard")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
}

func TestPaymentEventWalletCeilingAndCache(t *testing.T) {
	for _, tc := range []struct {
		name            string
		balance, credit int
		reject          bool
	}{
		{"credits once and updates cache", 7, 10, false},
		{"exact wallet limit", common.MaxWalletQuota - 10, 10, false},
		{"wallet overflow", common.MaxWalletQuota - 9, 10, true},
		{"invalid credit", 7, common.MaxWalletQuota + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			useUserCacheMiniRedis(t)
			user := insertUserForPaymentGuardTest(t, 501, tc.balance)
			require.NoError(t, populateUserCache(*user))
			order := PaymentOrder{OrderNo: common.GetUUID(), MethodID: 1, VersionID: 1, Provider: PaymentProviderEpay, AccountKey: "merchant", Business: PaymentBusinessTopUp, UserID: user.Id, Amount: 1000, Currency: "CNY", CreditedQuota: tc.credit, PlanSnapshot: "null", CheckoutSnapshot: "null", Status: common.TopUpStatusPending}
			require.NoError(t, DB.Create(&order).Error)
			require.NoError(t, DB.Create(&TopUp{UserId: user.Id, TradeNo: order.OrderNo, Amount: 2, Money: 10, PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending}).Error)
			event, err := ReceivePaymentEvent(context.Background(), &order, &payment.Notice{OrderNo: order.OrderNo, TradeNo: "TRADE" + order.OrderNo, Money: payment.Money{Amount: 1000, Currency: "CNY"}, Paid: true, Subtotal: 1000}, "127.0.0.1", 1)
			require.NoError(t, err)
			err = ProcessPaymentEvent(context.Background(), event.ID, &payment.Notice{OrderNo: order.OrderNo, TradeNo: "TRADE" + order.OrderNo, Money: payment.Money{Amount: 1000, Currency: "CNY"}, Subtotal: 1000, Paid: true})
			wantBalance := tc.balance
			if tc.reject {
				require.Error(t, err)
				assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, order.OrderNo))
			} else {
				require.NoError(t, err)
				require.NoError(t, ProcessPaymentEvent(context.Background(), event.ID, &payment.Notice{OrderNo: order.OrderNo, TradeNo: "TRADE" + order.OrderNo, Money: payment.Money{Amount: 1000, Currency: "CNY"}, Subtotal: 1000, Paid: true}))
				wantBalance += tc.credit
				assert.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, order.OrderNo))
			}
			assert.Equal(t, wantBalance, getUserQuotaForPaymentGuardTest(t, user.Id))
			cached, err := cacheGetUserBase(user.Id)
			require.NoError(t, err)
			assert.Equal(t, wantBalance, cached.Quota)
		})
	}
}

func TestPaymentMethodDoesNotLogMerchantSecret(t *testing.T) {
	truncateTables(t)
	var output bytes.Buffer
	previous := DB
	DB = DB.Session(&gorm.Session{Logger: gormlogger.New(log.New(&output, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})})
	t.Cleanup(func() { DB = previous })
	method := PaymentMethod{Name: "Secret logging", Provider: payment.Epay, Enabled: true}
	config := payment.Config{Endpoint: "https://pay.example.com", MerchantID: "10001", Key: "merchant-secret-must-not-be-logged", PaymentType: "alipay"}
	require.NoError(t, SavePaymentMethod(&method, config))
	assert.NotEmpty(t, output.String())
	assert.NotContains(t, output.String(), config.Key)
}
