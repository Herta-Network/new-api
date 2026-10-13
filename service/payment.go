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
	"gorm.io/gorm"
)

type PaymentCheckout struct {
	Method        string
	VersionID     int
	UserID        int
	Money         payment.Money
	CreditedQuota int
	TopUpAmount   int64
	Plan          *model.SubscriptionPlan
	SuccessURL    string
	CancelURL     string
}

func LoadPaymentConfiguration(name string) (*model.PaymentMethod, *model.PaymentMethodVersion, payment.Config, error) {
	provider, value, ok := strings.Cut(name, ":")
	id, err := strconv.Atoi(value)
	if !ok || err != nil || id <= 0 {
		return nil, nil, payment.Config{}, common.NewMessage("Payment method does not exist")
	}
	method, version, err := model.GetPaymentMethodVersion(id, true)
	if err != nil || method.Provider != provider {
		return nil, nil, payment.Config{}, common.NewMessage("Payment method does not exist")
	}
	var config payment.Config
	if err := common.UnmarshalJsonStr(version.Config, &config); err != nil {
		return nil, nil, config, err
	}
	return method, version, config, nil
}

func QuotePayment(ctx context.Context, input PaymentCheckout) (payment.Money, error) {
	method, version, config, err := LoadPaymentConfiguration(input.Method)
	if err != nil {
		return payment.Money{}, err
	}
	if input.VersionID != version.ID {
		return payment.Money{}, model.ErrPaymentConfigChanged
	}
	gw, err := payment.Open(method.Provider, version.Config)
	if err != nil {
		return payment.Money{}, err
	}
	checkout := payment.Checkout{Money: input.Money, Quantity: input.TopUpAmount, PriceID: config.PriceID, PromotionCodes: config.PromotionCodes}
	if input.Plan != nil {
		checkout.Quantity = 1
		checkout.PriceID = input.Plan.StripePriceId
		checkout.Subscription = true
		checkout.PromotionCodes = false
	}
	return gw.Quote(ctx, checkout)
}

func StartPayment(ctx context.Context, input PaymentCheckout) (*payment.CheckoutResult, error) {
	method, version, config, err := LoadPaymentConfiguration(input.Method)
	if err != nil {
		return nil, err
	}
	if input.VersionID != version.ID {
		return nil, model.ErrPaymentConfigChanged
	}
	gw, err := payment.Open(method.Provider, version.Config)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(GetCallbackAddress())
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, common.NewMessage("Callback URL configuration error")
	}
	accountKey, err := gw.AccountKey(ctx)
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserById(input.UserID, false)
	if err != nil {
		return nil, err
	}
	order := &model.PaymentOrder{OrderNo: "PAY" + common.GetUUID(), MethodID: method.ID, VersionID: version.ID, Provider: method.Provider, AccountKey: accountKey, UserID: input.UserID, Amount: input.Money.Amount, Currency: input.Money.Currency, CreditedQuota: input.CreditedQuota, PlanSnapshot: "null", Status: common.TopUpStatusPending, CreatedAt: common.GetTimestamp()}
	checkout := payment.Checkout{OrderNo: order.OrderNo, Money: input.Money, Quantity: input.TopUpAmount, PriceID: config.PriceID, PromotionCodes: config.PromotionCodes, Email: user.Email, CustomerID: model.GetPaymentCustomer(accountKey, input.UserID)}
	majorAmount, _ := strconv.ParseFloat(input.Money.Decimal(), 64)
	var topUp *model.TopUp
	var subscription *model.SubscriptionOrder
	if input.Plan == nil {
		order.Business = model.PaymentBusinessTopUp
		topUp = &model.TopUp{UserId: input.UserID, Amount: input.TopUpAmount, Money: majorAmount, TradeNo: order.OrderNo, PaymentMethod: gw.PaymentType(), PaymentProvider: method.Provider, CreateTime: order.CreatedAt, Status: common.TopUpStatusPending}
	} else {
		order.Business = model.PaymentBusinessSubscription
		encoded, err := common.Marshal(input.Plan)
		if err != nil {
			return nil, err
		}
		order.PlanSnapshot = string(encoded)
		checkout.PriceID = input.Plan.StripePriceId
		checkout.Quantity = 1
		checkout.Subscription = true
		checkout.PromotionCodes = false
		subscription = &model.SubscriptionOrder{UserId: input.UserID, PlanId: input.Plan.Id, Money: majorAmount, TradeNo: order.OrderNo, PaymentMethod: gw.PaymentType(), PaymentProvider: method.Provider, CreateTime: order.CreatedAt, Status: common.TopUpStatusPending}
	}
	checkout.Subject = "New API " + order.Business
	checkout.NotifyURL = base.JoinPath("api/payments", method.Provider, "notify", strconv.Itoa(version.ID)).String()
	checkout.ReturnURL = base.JoinPath("api/payments/return", order.OrderNo).String()
	checkout.CancelURL = checkout.ReturnURL
	if input.SuccessURL != "" {
		checkout.ReturnURL = input.SuccessURL
	}
	if input.CancelURL != "" {
		checkout.CancelURL = input.CancelURL
	}
	snapshot, err := common.Marshal(checkout)
	if err != nil {
		return nil, err
	}
	order.CheckoutSnapshot = string(snapshot)
	if err := model.CreatePaymentOrder(ctx, order, topUp, subscription); err != nil {
		return nil, err
	}
	if order.OrderNo != checkout.OrderNo {
		if order.CheckoutResult != nil {
			var result payment.CheckoutResult
			if err := common.UnmarshalJsonStr(*order.CheckoutResult, &result); err != nil {
				return nil, err
			}
			return &result, nil
		}
		// Resume the exact checkout snapshot even after a configuration change or
		// an interrupted provider request. Provider order IDs remain idempotent.
		checkout = payment.Checkout{}
		if err := common.UnmarshalJsonStr(order.CheckoutSnapshot, &checkout); err != nil {
			return nil, err
		}
		_, reservedVersion, err := model.GetPaymentCallbackVersion(order.VersionID)
		if err != nil {
			return nil, err
		}
		gw, err = payment.Open(order.Provider, reservedVersion.Config)
		if err != nil {
			return nil, err
		}
	}
	result, err := gw.StartPayment(ctx, checkout)
	if err != nil {
		return nil, err
	}
	if err := model.BindPaymentCheckout(ctx, order.OrderNo, result); err != nil {
		return nil, err
	}
	return result, nil
}

func HandlePaymentCallback(ctx context.Context, provider string, versionID int, callback payment.Callback, callerIP string) error {
	method, version, err := model.GetPaymentCallbackVersion(versionID)
	if err != nil {
		return err
	}
	if method.Provider != provider {
		return payment.ErrInvalidPayment
	}
	gw, err := payment.Open(provider, version.Config)
	if err != nil {
		return err
	}
	notice, err := gw.ParseCallback(ctx, callback)
	if err != nil {
		return err
	}
	if notice == nil {
		return nil
	}
	return recordPaymentNotice(ctx, method, version, notice, callerIP)
}

// Stripe registers a stable method URL before it issues a webhook signing key.
// Previous versions authenticate events for orders created before key rotation.
func HandleStripePaymentCallback(ctx context.Context, methodID int, callback payment.Callback, callerIP string) error {
	method, versions, err := model.GetPaymentMethodVersions(methodID)
	if err != nil {
		return err
	}
	if method.Provider != payment.Stripe {
		return payment.ErrInvalidPayment
	}
	for _, version := range versions {
		gw, err := payment.Open(payment.Stripe, version.Config)
		if err != nil {
			continue
		}
		notice, err := gw.ParseCallback(ctx, callback)
		if errors.Is(err, payment.ErrInvalidPayment) {
			continue
		}
		if err != nil {
			return err
		}
		if notice == nil {
			return nil
		}
		return recordPaymentNotice(ctx, method, &version, notice, callerIP)
	}
	return payment.ErrInvalidPayment
}

func recordPaymentNotice(ctx context.Context, method *model.PaymentMethod, version *model.PaymentMethodVersion, notice *payment.Notice, callerIP string) error {
	order, _, err := model.GetPaymentOrder(ctx, notice.OrderNo)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return payment.ErrInvalidPayment
		}
		return err
	}
	if order.MethodID != method.ID || order.Provider != method.Provider {
		return payment.ErrInvalidPayment
	}
	if method.Provider == payment.Stripe && version.ID < order.VersionID {
		return payment.ErrInvalidPayment
	}
	event, err := model.ReceivePaymentEvent(ctx, order, notice, callerIP, version.ID)
	if err != nil {
		return err
	}
	return ProcessPaymentEvent(ctx, event.ID)
}

func ProcessPaymentEvent(ctx context.Context, id int64) error {
	var event model.PaymentEvent
	if err := model.DB.WithContext(ctx).First(&event, id).Error; err != nil {
		return err
	}
	if event.Status == model.PaymentEventRejected {
		return payment.ErrInvalidPayment
	}
	if event.Status != model.PaymentEventPending {
		return nil
	}
	order, _, err := model.GetPaymentOrder(ctx, event.OrderNo)
	if err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	_, version, err := model.GetPaymentCallbackVersion(event.VersionID)
	if err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	gw, err := payment.Open(order.Provider, version.Config)
	if err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	var notice payment.Notice
	var checkout payment.Checkout
	if err := common.UnmarshalJsonStr(event.Payload, &notice); err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	if err := common.UnmarshalJsonStr(order.CheckoutSnapshot, &checkout); err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	if order.CheckoutID != nil {
		checkout.CheckoutID = *order.CheckoutID
	}
	accountKey, err := gw.AccountKey(ctx)
	if err != nil {
		// A rotated API key may be revoked while an authenticated inbox item is
		// pending. Only reuse current credentials after proving the same account.
		_, current, loadErr := model.GetPaymentMethodVersion(order.MethodID, false)
		if loadErr == nil && current.ID != version.ID {
			candidate, openErr := payment.Open(order.Provider, current.Config)
			if openErr == nil {
				currentAccount, accountErr := candidate.AccountKey(ctx)
				if accountErr == nil && currentAccount == order.AccountKey {
					gw = candidate
					accountKey = currentAccount
					err = nil
				}
			}
		}
	}
	if err == nil && accountKey != order.AccountKey {
		err = payment.ErrInvalidPayment
	}
	var verified *payment.Notice
	if err == nil {
		verified, err = gw.Confirm(ctx, checkout, notice)
	}
	if err != nil {
		return model.FailPaymentEvent(ctx, id, err)
	}
	return model.ProcessPaymentEvent(ctx, id, verified)
}

func RetryPaymentEvents(ctx context.Context) error {
	var ids []int64
	if err := model.DB.WithContext(ctx).Model(&model.PaymentEvent{}).Where("status = ? AND next_attempt_at <= ?", model.PaymentEventPending, common.GetTimestamp()).Order("next_attempt_at ASC, id ASC").Limit(100).Pluck("id", &ids).Error; err != nil {
		return err
	}
	var result error
	for _, id := range ids {
		if err := ProcessPaymentEvent(ctx, id); err != nil && !errors.Is(err, payment.ErrInvalidPayment) {
			result = errors.Join(result, err)
		}
	}
	return result
}

var paymentRecoveryOnce sync.Once

func StartPaymentRecovery() {
	paymentRecoveryOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				err := RetryPaymentEvents(ctx)
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					common.SysError(common.LogText("payment event recovery failed: %s", err.Error()))
				}
				<-ticker.C
			}
		}()
	})
}
