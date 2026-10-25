package payment

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/logger"
)

// CheckoutTypePaid answers a checkout that completed without a gateway: a
// balance or free purchase.
const CheckoutTypePaid = "paid"

var (
	errOrderNotPayable error = common.NewMessage("This order can no longer be paid. Create a new order.")
	errCheckoutFailed  error = common.NewMessage("The payment could not be started. Try again later.")
)

// Checkout tells the client how to pay an order: Type is gateway.ActionURL
// (open URL), gateway.ActionStripe (confirm with Stripe.js),
// gateway.ActionQRCode (show QRCode to scan) or CheckoutTypePaid.
type Checkout struct {
	Type    string                `json:"type"`
	URL     string                `json:"url,omitempty"`
	Stripe  *gateway.StripeAction `json:"stripe,omitempty"`
	QRCode  string                `json:"qr_code,omitempty"`
	OrderNo string                `json:"order_no"`
}

// CreateOrder prices a purchase, stores it as a pending order with an
// invoice through the first channel of the chosen payment option and starts
// its payment in the same request, switching to the next channels when one
// cannot start it (see checkoutThrough). A balance or free purchase needs no
// invoice and completes at once; if it fails the order is closed again.
func CreateOrder(ctx context.Context, userId int, req PurchaseRequest, clientIp string) (*entity.PaymentOrder, *Checkout, error) {
	quote, err := QuotePurchase(ctx, userId, req)
	if err != nil {
		return nil, nil, err
	}
	orderNo, err := newOrderNo()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	order := &entity.PaymentOrder{
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
		PaymentOptionId: quote.option.Id,
		PaymentMethodId: quote.method.Id,
		Platform:        quote.method.Platform,
		ClientIp:        clientIp,
		CreatedAt:       now.Unix(),
		ExpiresAt:       now.Add(paymentWindow).Unix(),
	}
	limits := store.OrderLimits{MaxPending: maxPendingOrders}
	if quote.plan != nil {
		limits.MaxPlanPurchases = quote.plan.MaxPurchasePerUser
	}
	if order.Platform == entity.PaymentPlatformBalance {
		if err := store.CreateOrder(order, limits, nil); err != nil {
			return nil, nil, err
		}
		paid, err := store.PayOrderWithBalance(order.OrderNo, userId)
		if err != nil {
			if _, closeErr := store.CloseOrder(order, "payment_failed"); closeErr != nil {
				logger.LogWarn(ctx, common.LogText("close failed balance order %s: %v", order.OrderNo, closeErr))
			}
			return nil, nil, err
		}
		return paid, &Checkout{Type: CheckoutTypePaid, OrderNo: order.OrderNo}, nil
	}
	invoice := &entity.PaymentInvoice{PaymentMethodId: quote.method.Id, Platform: quote.method.Platform}
	if err := store.CreateOrder(order, limits, invoice); err != nil {
		return nil, nil, err
	}
	checkout, err := checkoutThrough(ctx, order, invoice, quote.channels, true)
	if err != nil {
		return order, nil, err
	}
	return order, checkout, nil
}

// maxCheckoutChannels bounds the channels one checkout tries, so a buyer
// does not wait for every failing gateway.
const maxCheckoutChannels = 3

// checkoutFailure is a channel that could not start a payment; the order
// may try another.
type checkoutFailure struct{ err error }

func (f *checkoutFailure) Error() string { return f.err.Error() }
func (f *checkoutFailure) Unwrap() error { return f.err }

// checkoutThrough starts the payment of a pending order through the first
// of channels that can, invoicing the order anew through each; invoice, when
// given, is the order's invoice through channels[0]. When a channel fails,
// the next channel whose checkout reaches its provider is tried: a channel
// that only redirects is never switched to, because its failures cannot be
// seen. The failed invoice is closed without asking its gateway when its
// checkout started in this call, since the buyer never got it and a payment
// that arrives anyway still settles; one whose checkout the buyer got before
// stays pending, so the order's close still asks its gateway about it. A
// failure counts towards the channel's breaker unless the provider refused
// the request or worded it for the buyer, or it is the renewal of a checkout
// that started before (countFirst false), which a gateway may refuse without
// being down.
func checkoutThrough(ctx context.Context, order *entity.PaymentOrder, invoice *entity.PaymentInvoice, channels []channel, countFirst bool) (*Checkout, error) {
	failure := errCheckoutFailed
	tried := 0
	for i, candidate := range channels {
		if tried == maxCheckoutChannels {
			break
		}
		if i > 0 && !candidate.gw.ServerCheckout() {
			continue
		}
		if invoice == nil {
			issued, err := store.IssueInvoice(order, candidate.method)
			if errors.Is(err, store.ErrOrderNotPending) {
				return nil, errOrderNotPayable
			}
			if err != nil {
				return nil, err
			}
			invoice = issued
		}
		tried++
		handedOut := invoice.PaymentCurrency != ""
		checkout, err := startCheckout(ctx, order, invoice, candidate.method)
		var failed *checkoutFailure
		if !errors.As(err, &failed) {
			if err == nil {
				checkoutStarted(candidate.method.Id)
			}
			return checkout, err
		}
		logger.LogWarn(ctx, common.LogText("payment channel %d could not start invoice %s: %v", candidate.method.Id, invoice.InvoiceNo, failed.err))
		var message *common.Message
		switch {
		case errors.As(failed.err, &message):
			failure = message
		case errors.Is(failed.err, gateway.ErrRefused):
		case i > 0 || countFirst:
			checkoutFailed(candidate.method.Id, failed.err)
		}
		if !handedOut {
			if _, err := store.CloseInvoice(invoice, false, closeReasonFailed); err != nil {
				return nil, err
			}
		}
		invoice = nil
	}
	return nil, failure
}

// ResumeCheckout starts the payment of a user's pending order again, for a
// buyer who lost the payment page or did not get through it. Its latest
// pending invoice is checked out again when its channel is enabled and not
// tripped; otherwise the order is invoiced through its payment option's
// channels, those it already tried last. The replaced invoice is closed
// through its gateway as the system would close it, so it takes no payment
// any more; one the gateway cannot confirm stays pending, and the order's
// close asks about it again.
func ResumeCheckout(ctx context.Context, userId int, orderNo string) (*Checkout, error) {
	order, err := UserOrder(userId, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Platform == entity.PaymentPlatformBalance || order.Status != entity.PaymentOrderPending || time.Now().Unix() >= order.ExpiresAt {
		return nil, errOrderNotPayable
	}
	invoices, err := store.ListInvoices(order.Id)
	if err != nil {
		return nil, err
	}
	var latest *entity.PaymentInvoice
	tried := make([]int, 0, len(invoices))
	for _, invoice := range invoices {
		tried = append(tried, invoice.PaymentMethodId)
		if invoice.Status == entity.PaymentInvoicePending {
			latest = invoice
		}
	}
	var channels []channel
	if latest != nil {
		method, err := store.GetMethod(latest.PaymentMethodId)
		if err == nil && method.Enabled && !channelTripped(method.Id) {
			if gw, err := openGateway(method); err == nil {
				channels = append(channels, channel{method: method, gw: gw})
			}
		}
	}
	if option, err := store.GetOption(order.PaymentOptionId); err == nil && option.Enabled {
		routed, err := routeChannels(ctx, option, order.Price, order.Currency, tried)
		if err == nil {
			channels = append(channels, routed...)
		}
	}
	if len(channels) == 0 {
		return nil, errMethodUnavailable
	}
	if latest != nil && channels[0].method.Id == latest.PaymentMethodId {
		return checkoutThrough(ctx, order, latest, channels, latest.PaymentCurrency == "")
	}
	if latest != nil {
		settled, err := closeInvoice(ctx, order, latest, false, closeReasonReplaced)
		if settled != nil {
			return &Checkout{Type: CheckoutTypePaid, OrderNo: order.OrderNo}, nil
		}
		if err != nil && !errors.Is(err, ErrCloseUnconfirmed) {
			return nil, err
		}
	}
	return checkoutThrough(ctx, order, nil, channels, true)
}

// startCheckout creates, or resumes, the gateway payment of a pending
// invoice of a pending order. The payment expectation is recorded before the
// gateway is asked for anything and never changes afterwards, so a resumed
// checkout asks for the same amount even when the exchange rate moved.
func startCheckout(ctx context.Context, order *entity.PaymentOrder, invoice *entity.PaymentInvoice, method *entity.PaymentMethod) (*Checkout, error) {
	if order.Status != entity.PaymentOrderPending || invoice.Status != entity.PaymentInvoicePending || time.Now().Unix() >= order.ExpiresAt {
		return nil, errOrderNotPayable
	}
	gw, err := openGateway(method)
	if err != nil {
		return nil, &checkoutFailure{err: err}
	}
	if invoice.PaymentCurrency == "" {
		rate, err := chargeRate(ctx, gw, order.Currency)
		if err != nil {
			return nil, &checkoutFailure{err: err}
		}
		charge, err := gateway.ChargeFor(gw, order.Amount, order.Currency, rate)
		if err != nil {
			return nil, &checkoutFailure{err: err}
		}
		if rate == "" {
			rate = "1"
		}
		if _, err := store.SetInvoiceExpectation(invoice, charge.Amount, charge.Currency, rate); err != nil {
			return nil, err
		}
		// A concurrent checkout may have recorded its expectation first; the
		// stored one is the invoice's.
		if invoice, err = store.GetInvoice(invoice.Id); err != nil {
			return nil, err
		}
		if invoice.Status != entity.PaymentInvoicePending {
			return nil, errOrderNotPayable
		}
	}
	view := invoiceView(order, invoice)
	checkout := gateway.Checkout{
		Order:     view,
		Charge:    gateway.Charge{Amount: invoice.PaymentAmount, Currency: invoice.PaymentCurrency},
		ReturnURL: siteAddress() + "/user/wallet?payment_order=" + url.QueryEscape(order.OrderNo),
		Subject:   order.Subject,
		Claim: func(_ context.Context, tradeNo string) (string, error) {
			return store.ClaimInvoiceTrade(invoice, tradeNo)
		},
	}
	if gw.NeedsNotifyURL(view) {
		if checkout.NotifyURL, err = gateway.NotifyURL(methodView(method), siteAddress()); err != nil {
			logger.LogError(ctx, common.LogText("payment method %d has no notify URL: %v", method.Id, err))
			return nil, &checkoutFailure{err: err}
		}
	}
	action, err := gw.StartPayment(ctx, checkout)
	if err != nil {
		return nil, &checkoutFailure{err: err}
	}
	return &Checkout{Type: action.Type, URL: action.URL, Stripe: action.Stripe, QRCode: action.QRCode, OrderNo: order.OrderNo}, nil
}

// UserOrder loads an order of userId.
func UserOrder(userId int, orderNo string) (*entity.PaymentOrder, error) {
	order, err := store.GetOrderByNo(orderNo)
	if err != nil {
		return nil, err
	}
	if order.UserId != userId {
		return nil, store.ErrOrderNotFound
	}
	return order, nil
}
