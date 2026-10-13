package service

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
)

// PaymentCheckout binds a business purchase to one configured gateway method.
type PaymentCheckout struct {
	Method        string
	UserID        int
	Money         payment.Money
	CreditedQuota int
	TopUpAmount   int64
	Plan          *model.SubscriptionPlan
}

func StartPayment(ctx context.Context, input PaymentCheckout) (*payment.CheckoutResult, error) {
	provider, value, ok := strings.Cut(input.Method, ":")
	id, err := strconv.Atoi(value)
	if !ok || provider != payment.Epay || err != nil || id <= 0 {
		return nil, common.NewMessage("Payment method does not exist")
	}
	method, version, err := model.GetPaymentMethodVersion(id, true)
	if err != nil || method.Provider != provider {
		return nil, common.NewMessage("Payment method does not exist")
	}
	gw, err := payment.Open(provider, version.Config)
	if err != nil {
		return nil, common.NewMessage("Payment information has not been configured by administrator")
	}
	base, err := url.Parse(GetCallbackAddress())
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, common.NewMessage("Callback URL configuration error")
	}
	var config payment.EpayConfig
	if err := common.UnmarshalJsonStr(version.Config, &config); err != nil {
		return nil, err
	}
	order := &model.PaymentOrder{
		OrderNo: "PAY" + common.GetUUID(), MethodID: method.ID, VersionID: version.ID, Provider: provider,
		AccountKey: model.PaymentAccountKey(provider, config.Endpoint, config.MerchantID),
		UserID:     input.UserID, Amount: input.Money.Amount, Currency: input.Money.Currency,
		CreditedQuota: input.CreditedQuota, PlanSnapshot: "null", Status: common.TopUpStatusPending, CreatedAt: common.GetTimestamp(),
	}
	var topUp *model.TopUp
	var subscription *model.SubscriptionOrder
	if input.Plan == nil {
		order.Business = model.PaymentBusinessTopUp
		topUp = &model.TopUp{UserId: input.UserID, Amount: input.TopUpAmount, Money: float64(input.Money.Amount) / 100, TradeNo: order.OrderNo, PaymentMethod: config.PaymentType, PaymentProvider: provider, CreateTime: order.CreatedAt, Status: common.TopUpStatusPending}
	} else {
		order.Business = model.PaymentBusinessSubscription
		encoded, err := common.Marshal(input.Plan)
		if err != nil {
			return nil, err
		}
		order.PlanSnapshot = string(encoded)
		subscription = &model.SubscriptionOrder{UserId: input.UserID, PlanId: input.Plan.Id, Money: float64(input.Money.Amount) / 100, TradeNo: order.OrderNo, PaymentMethod: config.PaymentType, PaymentProvider: provider, CreateTime: order.CreatedAt, Status: common.TopUpStatusPending}
	}
	// Persist first: a callback can never arrive before its business snapshot.
	if err := model.CreatePaymentOrder(ctx, order, topUp, subscription); err != nil {
		return nil, err
	}
	return gw.StartPayment(ctx, payment.Checkout{
		OrderNo: order.OrderNo, Subject: "New API " + order.Business, Money: input.Money,
		NotifyURL: base.JoinPath("api/payments/epay/notify", order.OrderNo).String(),
		ReturnURL: base.JoinPath("api/payments/return", order.OrderNo).String(),
	})
}

func HandlePaymentCallback(ctx context.Context, orderNo string, params map[string]string, callerIP string) error {
	order, version, err := model.GetPaymentOrder(ctx, orderNo)
	if err != nil {
		return err
	}
	gw, err := payment.Open(order.Provider, version.Config)
	if err != nil {
		return err
	}
	notice, err := gw.ParseCallback(ctx, params)
	if err != nil || notice.OrderNo != orderNo {
		return payment.ErrInvalidPayment
	}
	event, err := model.ReceivePaymentEvent(ctx, order, notice, callerIP)
	if err != nil {
		return err
	}
	return model.ProcessPaymentEvent(ctx, event.ID)
}

var paymentRecoveryOnce sync.Once

func StartPaymentRecovery() {
	paymentRecoveryOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				err := model.RetryPaymentEvents(ctx)
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					common.SysError(common.LogText("payment event recovery failed: %s", err.Error()))
				}
				<-ticker.C
			}
		}()
	})
}
