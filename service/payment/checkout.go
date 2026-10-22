package payment

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/payment/gateway"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// CheckoutTypePaid answers a checkout that completed without a gateway: a
// balance or free purchase.
const CheckoutTypePaid = "paid"

var (
	errOrderNotPayable error = common.NewMessage("This order can no longer be paid. Create a new order.")
	errCheckoutFailed  error = common.NewMessage("The payment could not be started. Try again later.")
)

// Checkout tells the client how to pay an order: Type is gateway.ActionURL
// (open URL), gateway.ActionStripe (confirm with Stripe.js) or
// CheckoutTypePaid.
type Checkout struct {
	Type    string                `json:"type"`
	URL     string                `json:"url,omitempty"`
	Stripe  *gateway.StripeAction `json:"stripe,omitempty"`
	OrderNo string                `json:"order_no"`
}

// CreateOrder prices a purchase, stores it as a pending order and starts
// its payment in the same request. A balance or free purchase completes
// at once; if it fails the order is closed again.
func CreateOrder(ctx context.Context, userId int, req PurchaseRequest, clientIp string) (*model.PaymentOrder, *Checkout, error) {
	quote, err := QuotePurchase(ctx, userId, req)
	if err != nil {
		return nil, nil, err
	}
	orderNo, err := newOrderNo()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	order := &model.PaymentOrder{
		OrderNo:         orderNo,
		UserId:          userId,
		Purpose:         quote.Purpose,
		PlanId:          quote.PlanId,
		Subject:         quote.Subject,
		TopupAmount:     quote.TopupAmount,
		QuotaAmount:     quote.QuotaAmount,
		QuotaRate:       quote.QuotaRate,
		Currency:        quote.Currency,
		Price:           quote.Price,
		Adjustment:      quote.Adjustment,
		FeeAmount:       quote.FeeAmount,
		Amount:          quote.Amount,
		PaymentMethodId: quote.method.Id,
		Platform:        quote.method.Platform,
		ClientIp:        clientIp,
		CreatedAt:       now.Unix(),
		ExpiresAt:       now.Add(paymentWindow).Unix(),
	}
	limits := model.PaymentOrderLimits{MaxPending: maxPendingOrders}
	if quote.plan != nil {
		limits.MaxPlanPurchases = quote.plan.MaxPurchasePerUser
	}
	if err := model.CreatePaymentOrder(order, limits); err != nil {
		return nil, nil, err
	}
	if order.Platform == model.PaymentPlatformBalance {
		paid, err := model.PayPaymentOrderWithBalance(order.OrderNo, userId)
		if err != nil {
			if _, closeErr := model.ClosePaymentOrder(order, false, "payment_failed"); closeErr != nil {
				logger.LogWarn(ctx, common.LogText("close failed balance order %s: %v", order.OrderNo, closeErr))
			}
			return nil, nil, err
		}
		return paid, &Checkout{Type: CheckoutTypePaid, OrderNo: order.OrderNo}, nil
	}
	checkout, err := StartCheckout(ctx, order, quote.method)
	if err != nil {
		return order, nil, err
	}
	return order, checkout, nil
}

// ResumeCheckout starts the payment of a user's pending order again, for a
// client that lost the payment page or client secret.
func ResumeCheckout(ctx context.Context, userId int, orderNo string) (*Checkout, error) {
	order, err := UserOrder(userId, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Platform == model.PaymentPlatformBalance {
		return nil, errOrderNotPayable
	}
	method, err := model.GetPaymentMethod(order.PaymentMethodId)
	if err != nil || !method.Enabled {
		return nil, errMethodUnavailable
	}
	return StartCheckout(ctx, order, method)
}

// StartCheckout creates, or resumes, the gateway payment of a pending order.
// The payment expectation is recorded before the gateway is asked for
// anything and never changes afterwards, so a resumed checkout asks for the
// same amount even when the exchange rate moved.
func StartCheckout(ctx context.Context, order *model.PaymentOrder, method *model.PaymentMethod) (*Checkout, error) {
	if order.Status != model.PaymentOrderPending || time.Now().Unix() >= order.ExpiresAt {
		return nil, errOrderNotPayable
	}
	gw, err := openGateway(method)
	if err != nil {
		return nil, errMethodUnavailable
	}
	if order.PaymentCurrency == "" {
		rate, err := chargeRate(ctx, gw, order.Currency)
		if err != nil {
			return nil, err
		}
		charge, err := gateway.ChargeFor(gw, order.Amount, order.Currency, rate)
		if err != nil {
			return nil, err
		}
		if rate == "" {
			rate = "1"
		}
		if _, err := model.SetPaymentExpectation(order.Id, charge.Amount, charge.Currency, rate); err != nil {
			return nil, err
		}
		// A concurrent checkout may have recorded its expectation first; the
		// stored one is the order's.
		if order, err = model.GetPaymentOrderByNo(order.OrderNo); err != nil {
			return nil, err
		}
		if order.Status != model.PaymentOrderPending {
			return nil, errOrderNotPayable
		}
	}
	view := orderView(order)
	checkout := gateway.Checkout{
		Order:     view,
		Charge:    gateway.Charge{Amount: order.PaymentAmount, Currency: order.PaymentCurrency},
		ReturnURL: siteAddress() + "/user/wallet?payment_order=" + url.QueryEscape(order.OrderNo),
		Subject:   order.Subject,
		Claim: func(_ context.Context, tradeNo string) (string, error) {
			return model.ClaimPaymentTrade(order.Id, tradeNo)
		},
	}
	if gw.NeedsNotifyURL(view) {
		if checkout.NotifyURL, err = gateway.NotifyURL(methodView(method), siteAddress()); err != nil {
			logger.LogError(ctx, common.LogText("payment method %d has no notify URL: %v", method.Id, err))
			return nil, errMethodUnavailable
		}
	}
	action, err := gw.StartPayment(ctx, checkout)
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			return nil, err
		}
		logger.LogError(ctx, common.LogText("start payment of order %s failed: %v", order.OrderNo, err))
		return nil, errCheckoutFailed
	}
	return &Checkout{Type: action.Type, URL: action.URL, Stripe: action.Stripe, OrderNo: order.OrderNo}, nil
}

// UserOrder loads an order of userId.
func UserOrder(userId int, orderNo string) (*model.PaymentOrder, error) {
	order, err := model.GetPaymentOrderByNo(orderNo)
	if err != nil {
		return nil, err
	}
	if order.UserId != userId {
		return nil, model.ErrPaymentOrderNotFound
	}
	return order, nil
}

// MethodOption is a payment method as a buyer sees it. Unavailable is set
// when the method cannot take an order now, such as when its exchange rate
// is missing.
type MethodOption struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Icon           string `json:"icon"`
	Description    string `json:"description"`
	FeeMode        int    `json:"fee_mode"`
	FeeRateBp      int64  `json:"fee_rate_bp"`
	FeeFixed       int64  `json:"fee_fixed"`
	ChargeCurrency string `json:"charge_currency"`
	// PaymentMethodType is the Stripe payment method type a Stripe method
	// collects; empty for other platforms.
	PaymentMethodType string `json:"payment_method_type,omitempty"`
	Available         bool   `json:"available"`
	// Unavailable says why the method cannot take an order now.
	Unavailable *common.Message `json:"unavailable,omitempty"`
}

// BuyerMethods lists the enabled methods a purpose can be paid with. The
// balance pays subscriptions only. A method whose exchange rate is missing
// is listed as unavailable rather than hidden.
func BuyerMethods(ctx context.Context, purpose string) ([]MethodOption, error) {
	if purpose != model.PaymentPurposeTopup && purpose != model.PaymentPurposeSubscription {
		return nil, errInvalidPurchase
	}
	methods, err := model.ListPaymentMethods()
	if err != nil {
		return nil, err
	}
	site := operation_setting.PaymentCurrency()
	// Top-ups and balance purchases convert the site currency to USD for
	// the quota.
	_, quotaRateErr := ResolveRate(ctx, site, "USD")
	options := make([]MethodOption, 0, len(methods))
	for _, method := range methods {
		if !method.Enabled || (method.Platform == model.PaymentPlatformBalance && purpose != model.PaymentPurposeSubscription) {
			continue
		}
		option := MethodOption{
			Id: method.Id, Name: method.Name, Platform: method.Platform, Icon: method.Icon, Description: method.Description,
			FeeMode: method.FeeMode, FeeRateBp: method.FeeRateBp, FeeFixed: method.FeeFixed, ChargeCurrency: site, Available: true,
		}
		if method.Platform == gateway.PlatformStripe {
			var config gateway.StripeConfig
			_ = common.UnmarshalJsonStr(method.Config, &config)
			option.PaymentMethodType = config.PaymentMethodType
		}
		var unavailable error
		if method.Platform == model.PaymentPlatformBalance {
			option.FeeMode, option.FeeRateBp, option.FeeFixed = model.PaymentFeeNone, 0, 0
			unavailable = quotaRateErr
		} else if gw, err := openGateway(method); err != nil {
			unavailable = errMethodUnavailable
		} else {
			if currency := gw.ChargeCurrency(); currency != "" {
				option.ChargeCurrency = currency
			}
			if _, err := chargeRate(ctx, gw, site); err != nil {
				unavailable = err
			} else if purpose == model.PaymentPurposeTopup {
				unavailable = quotaRateErr
			}
		}
		if unavailable != nil {
			option.Available, option.Unavailable = false, common.NewMessage("This payment method is unavailable")
			var message *common.Message
			if errors.As(unavailable, &message) {
				option.Unavailable = message
			}
		}
		options = append(options, option)
	}
	return options, nil
}
