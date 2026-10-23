// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/gateway.go
//   - internal/module/billing/internal/gateway/method.go (ChargeFor)
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the package does not depend on the order and payment
// method models; it defines the minimal Order and Method views it needs. A
// gateway returns an Action instead of the HTTP response DTO, the Payer and
// the legacy system-currency fallback are gone (no Stripe customers, every
// order records its payment expectation), ChargeFor takes the rate as a
// decimal string, notices carry plain string details for the log, and a
// notice can report an authentic payment that must not settle its order as
// unmatched.

// Package gateway is the protocol layer of new-api payments. A Gateway
// speaks one payment provider's protocol for one configured payment method:
// it starts a payment, reconciles a pending order before it closes, and
// authenticates and re-confirms the provider's callbacks. The package never
// touches the database; the payment service converts its models into the
// Order and Method views defined here.
package gateway

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrUnconfirmed reports that the gateway could not confirm a pending order
// safe to close, so the order intentionally stays pending. Schedulers treat
// it as an expected outcome and check again later.
var ErrUnconfirmed = errors.New("gateway could not confirm the order as paid")

// ErrInvalidCallback marks a callback that cannot be authenticated or does
// not describe the order it names: a forged or malformed payload, an unknown
// order, an amount that is not the order's. Redelivering it cannot succeed,
// unlike a processing failure, which the next delivery may get past.
var ErrInvalidCallback = errors.New("invalid payment callback")

// InvalidCallback marks err as ErrInvalidCallback without changing its text.
func InvalidCallback(err error) error {
	if err == nil {
		return nil
	}
	return invalidCallbackError{err: err}
}

type invalidCallbackError struct{ err error }

func (e invalidCallbackError) Error() string   { return e.err.Error() }
func (e invalidCallbackError) Unwrap() []error { return []error{ErrInvalidCallback, e.err} }

// Order is the gateway's view of a payment order. Amounts count hundredths
// of their currency.
type Order struct {
	OrderNo string
	// Amount is what the order costs in Currency, the site currency.
	Amount   int64
	Currency string
	// PaymentAmount and PaymentCurrency are the payment expectation recorded
	// before the checkout started: exactly what the gateway is asked to
	// collect. An empty PaymentCurrency means no checkout has started.
	PaymentAmount   int64
	PaymentCurrency string
	// TradeNo is the provider payment bound to the order, if any.
	TradeNo   string
	CreatedAt time.Time
	// ExpiresAt ends the payment window of the pending order.
	ExpiresAt time.Time
}

// Charge is an amount a gateway collects, in hundredths of Currency.
type Charge struct {
	Amount   int64
	Currency string
}

// Checkout is one payment attempt for a pending order.
type Checkout struct {
	// Order is the pending order. Its recorded payment expectation equals
	// Charge; the gateway is asked for exactly that amount.
	Order     *Order
	Charge    Charge
	NotifyURL string
	ReturnURL string
	// Subject names the purchase on the provider's payment page.
	Subject string
	// Claim binds a provider-side payment (a Stripe intent, a Cryptomus
	// invoice) as the order's only one and returns the payment the order
	// holds afterwards: tradeNo when the claim won, the concurrent winner's
	// otherwise.
	Claim func(ctx context.Context, tradeNo string) (string, error)
}

// Action types tell the client how to complete a payment.
const (
	// ActionURL sends the buyer to URL.
	ActionURL = "url"
	// ActionStripe confirms the payment in the page with Stripe.js.
	ActionStripe = "stripe"
)

// Action is what the buyer needs to pay a started checkout.
type Action struct {
	Type   string        `json:"type"`
	URL    string        `json:"url,omitempty"`
	Stripe *StripeAction `json:"stripe,omitempty"`
}

// StripeAction is the client-side handle of a Stripe PaymentIntent.
type StripeAction struct {
	PublishableKey string `json:"publishable_key"`
	ClientSecret   string `json:"client_secret"`
	// PaymentMethodType is the one Stripe payment method type the intent
	// collects (card, alipay or wechat_pay); it selects the client's flow.
	PaymentMethodType string `json:"payment_method_type"`
}

// CloseRequest asks whether a pending order may close locally.
type CloseRequest struct {
	Order *Order
	// Explicit is set when a person gives the order up (its owner or an
	// administrator), which forfeits a payment the gateway cannot confirm
	// on gateways that cannot cancel one.
	Explicit bool
}

// Reconciliation is a gateway's verdict on a pending order that is about to
// close.
type Reconciliation struct {
	// TradeNo is set when the gateway proves the order was paid: the order
	// must be settled with it instead of closed.
	TradeNo string
	// RequireStableCheckout makes the close fail with ErrUnconfirmed if the
	// order's checkout changed after the gateway was consulted, because the
	// verdict only covers the checkout the gateway saw.
	RequireStableCheckout bool
	// Note explains a verdict reached without the gateway's confirmation,
	// for the log.
	Note string
}

// Notification is a gateway callback as delivered to the notify URL.
type Notification struct {
	HTTPMethod string
	// Params holds the form and query parameters when each occurs exactly
	// once; Form holds all of them.
	Params map[string]string
	Form   url.Values
	// Body is the raw request body, and Signature the value of the
	// CallbackStyle's signature header for gateways that sign the body.
	Body      []byte
	Signature string
}

// Notice is an authenticated callback.
type Notice struct {
	OrderNo  string
	TradeNo  string
	Amount   int64
	Currency string
	// Paid reports that the callback announces a completed payment. A notice
	// that is not paid is a lifecycle event: it is checked against the order
	// and acknowledged without settling.
	Paid bool
	// Ignore marks a callback that concerns no order of this payment method:
	// it is acknowledged without looking an order up.
	Ignore bool
	// Status is the provider's payment status or event type, for the log.
	Status string
	// ManualReview marks a lifecycle event that needs an operator, such as
	// an underpaid or frozen payment.
	ManualReview bool
	// Unmatched explains why an authentic payment must not settle the order,
	// such as a Stripe intent of another payment method type. The payment is
	// recorded for an operator and the callback acknowledged.
	Unmatched string
	// Details describe the callback in the log. ConfirmPayment may add to
	// them.
	Details map[string]string
}

// CallbackStyle is how a gateway delivers its callbacks and expects them to
// be answered. Failures are answered the same way for every gateway: 400 for
// an ErrInvalidCallback, 500 for what a redelivery may get past.
type CallbackStyle struct {
	// Body reports that the payload is the raw request body, which the
	// caller caps in size.
	Body bool
	// SignatureHeader names the request header that carries the body
	// signature; empty when the signature is part of the payload.
	SignatureHeader string
	// UniqueParams requires every form and query parameter to occur exactly
	// once.
	UniqueParams bool
	// TextReply acknowledges a callback with the plain text "success".
	TextReply bool
}

// Gateway is the protocol of one payment provider, bound to one configured
// payment method.
type Gateway interface {
	Platform() string
	// ChargeCurrency is the currency the gateway collects; empty means the
	// site currency, unconverted.
	ChargeCurrency() string
	// NeedsNotifyURL reports whether starting a payment for the order
	// registers a callback URL with the provider.
	NeedsNotifyURL(o *Order) bool
	// StartPayment creates, or resumes, the order's payment at the provider
	// and returns what the buyer needs to pay.
	StartPayment(ctx context.Context, c Checkout) (*Action, error)
	// Reconcile consults the provider before a pending order closes locally.
	// An error wrapping ErrUnconfirmed keeps the order pending.
	Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error)
	// QueryPayment asks the provider whether the order's checkout was paid,
	// without changing anything there: it returns the trade number of a
	// payment that matches the order's payment expectation, or "" when the
	// provider reports none (yet).
	QueryPayment(ctx context.Context, o *Order) (string, error)
	// ParseCallback authenticates a callback and extracts what it reports.
	ParseCallback(ctx context.Context, n Notification) (*Notice, error)
	// CheckOrder binds an authenticated notice to the order it names.
	CheckOrder(o *Order, notice *Notice) error
	// ConfirmPayment asks the provider itself whether the notice's payment
	// is complete and matches the order before the order settles.
	ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error
}

// chargeRounder is a gateway that collects some currencies at a coarser
// precision than hundredths (Stripe collects whole yen). ChargeFor rounds the
// charge before the expectation is recorded, so the amount the provider
// reports back matches the expectation exactly.
type chargeRounder interface {
	roundCharge(Charge) Charge
}

// ChargeFor returns what gw collects for amount hundredths of currency, the
// site currency: the amount itself when the gateway collects the site
// currency, otherwise its conversion at rate, the price of one site-currency
// unit in the gateway's currency (see ConvertAmount for the rounding). The
// rate is ignored when no conversion is needed.
func ChargeFor(gw Gateway, amount int64, currency, rate string) (Charge, error) {
	site := strings.ToUpper(strings.TrimSpace(currency))
	if site == "" {
		return Charge{}, errors.New("site currency is not configured")
	}
	charge := Charge{Amount: amount, Currency: site}
	if target := strings.ToUpper(gw.ChargeCurrency()); target != "" && target != site {
		converted, err := ConvertAmount(amount, rate)
		if err != nil {
			return Charge{}, err
		}
		charge = Charge{Amount: converted, Currency: target}
	}
	if rounder, ok := gw.(chargeRounder); ok {
		charge = rounder.roundCharge(charge)
	}
	return charge, nil
}
