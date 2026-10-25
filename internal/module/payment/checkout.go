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
// invoice of the chosen method and starts its payment in the same request.
// A balance or free purchase needs no invoice and completes at once; if it
// fails the order is closed again.
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
	checkout, err := startCheckout(ctx, order, invoice, quote.method)
	if err != nil {
		return order, nil, err
	}
	return order, checkout, nil
}

// ResumeCheckout starts the payment of a user's pending order again, for a
// client that lost the payment page or client secret: its latest invoice is
// checked out again.
func ResumeCheckout(ctx context.Context, userId int, orderNo string) (*Checkout, error) {
	order, err := UserOrder(userId, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Platform == entity.PaymentPlatformBalance {
		return nil, errOrderNotPayable
	}
	invoices, err := store.ListInvoices(order.Id)
	if err != nil {
		return nil, err
	}
	if len(invoices) == 0 {
		return nil, errOrderNotPayable
	}
	invoice := invoices[len(invoices)-1]
	method, err := store.GetMethod(invoice.PaymentMethodId)
	if err != nil || !method.Enabled {
		return nil, errMethodUnavailable
	}
	return startCheckout(ctx, order, invoice, method)
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
		return nil, errMethodUnavailable
	}
	if invoice.PaymentCurrency == "" {
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
			return nil, errMethodUnavailable
		}
	}
	action, err := gw.StartPayment(ctx, checkout)
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			return nil, err
		}
		logger.LogError(ctx, common.LogText("start payment of invoice %s failed: %v", invoice.InvoiceNo, err))
		return nil, errCheckoutFailed
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
