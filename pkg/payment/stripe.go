package payment

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/invoice"
	"github.com/stripe/stripe-go/v81/price"
	"github.com/stripe/stripe-go/v81/webhook"
	"github.com/tidwall/gjson"
)

type stripeGateway struct {
	config  Config
	backend stripe.Backend
}

func newStripe(config Config, runtime Runtime) (Gateway, error) {
	if (!strings.HasPrefix(config.Key, "sk_") && !strings.HasPrefix(config.Key, "rk_")) || config.WebhookSecret == "" {
		return nil, errors.New("invalid Stripe configuration")
	}
	backendConfig := &stripe.BackendConfig{HTTPClient: runtime.HTTPClient, MaxNetworkRetries: stripe.Int64(0)}
	if runtime.StripeURL != "" {
		backendConfig.URL = stripe.String(runtime.StripeURL)
	}
	return &stripeGateway{config: config, backend: stripe.GetBackendWithConfig(stripe.APIBackend, backendConfig)}, nil
}
func (g *stripeGateway) AccountKey(ctx context.Context) (string, error) {
	var account stripe.Account
	params := &stripe.Params{Context: ctx}
	if err := g.backend.Call(http.MethodGet, "/v1/account", g.config.Key, params, &account); err != nil {
		return "", err
	}
	if account.ID == "" {
		return "", ErrInvalidPayment
	}
	mode := "live"
	if strings.Contains(g.config.Key, "_test_") {
		mode = "test"
	}
	return AccountFingerprint(Stripe, account.ID, mode), nil
}
func (g *stripeGateway) PaymentType() string { return Stripe }
func (g *stripeGateway) Quote(ctx context.Context, c Checkout) (Money, error) {
	if c.PriceID == "" || c.Quantity <= 0 || c.Quantity > 10000 {
		return Money{}, errors.New("invalid Stripe price or quantity")
	}
	params := &stripe.PriceParams{}
	params.Context = ctx
	item, err := (price.Client{B: g.backend, Key: g.config.Key}).Get(c.PriceID, params)
	if err != nil {
		return Money{}, err
	}
	if !item.Active || (item.Recurring != nil) != c.Subscription || item.BillingScheme != stripe.PriceBillingSchemePerUnit {
		return Money{}, errors.New("unsupported Stripe price")
	}
	amount := decimal.NewFromInt(item.UnitAmount)
	if item.UnitAmountDecimal > 0 {
		amount = decimal.NewFromFloat(item.UnitAmountDecimal)
	}
	amount = amount.Mul(decimal.NewFromInt(c.Quantity)).Round(0)
	if !amount.IsPositive() || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return Money{}, ErrInvalidPayment
	}
	return Money{Amount: amount.IntPart(), Currency: strings.ToUpper(string(item.Currency))}, nil
}
func (g *stripeGateway) StartPayment(ctx context.Context, c Checkout) (*CheckoutResult, error) {
	mode := stripe.CheckoutSessionModePayment
	if c.Subscription {
		mode = stripe.CheckoutSessionModeSubscription
	}
	params := &stripe.CheckoutSessionParams{
		AdaptivePricing:   &stripe.CheckoutSessionAdaptivePricingParams{Enabled: stripe.Bool(false)},
		ClientReferenceID: stripe.String(c.OrderNo), SuccessURL: stripe.String(c.ReturnURL), CancelURL: stripe.String(c.CancelURL),
		Mode: stripe.String(string(mode)), LineItems: []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(c.PriceID), Quantity: stripe.Int64(c.Quantity)}},
		AllowPromotionCodes: stripe.Bool(c.PromotionCodes), Metadata: map[string]string{"new_api_order": c.OrderNo},
	}
	params.Context = ctx
	params.SetIdempotencyKey("new-api-checkout:" + c.OrderNo)
	if c.Subscription {
		params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{Metadata: map[string]string{"new_api_order": c.OrderNo}}
	}
	if c.CustomerID != "" {
		params.Customer = stripe.String(c.CustomerID)
	} else {
		if c.Email != "" {
			params.CustomerEmail = stripe.String(c.Email)
		}
		if !c.Subscription {
			params.CustomerCreation = stripe.String("always")
		}
	}
	result, err := (session.Client{B: g.backend, Key: g.config.Key}).New(params)
	if err != nil {
		return nil, err
	}
	if result.ID == "" || result.URL == "" || result.AmountSubtotal != c.Money.Amount || !strings.EqualFold(string(result.Currency), c.Money.Currency) {
		return nil, ErrInvalidPayment
	}
	return &CheckoutResult{URL: result.URL, CheckoutID: result.ID}, nil
}
func (g *stripeGateway) ParseCallback(_ context.Context, callback Callback) (*Notice, error) {
	event, err := webhook.ConstructEventWithOptions(callback.Body, callback.Signature, g.config.WebhookSecret, webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return nil, ErrInvalidPayment
	}
	expectedLive := !strings.Contains(g.config.Key, "_test_")
	if event.Livemode != expectedLive {
		return nil, ErrInvalidPayment
	}
	data := gjson.ParseBytes(event.Data.Raw)
	notice := &Notice{EventID: event.ID, PaymentType: Stripe}
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted, stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded, stripe.EventTypeCheckoutSessionAsyncPaymentFailed, stripe.EventTypeCheckoutSessionExpired:
		notice.OrderNo = data.Get("client_reference_id").String()
		notice.CheckoutID = data.Get("id").String()
		notice.TradeNo = notice.CheckoutID
		notice.Money = Money{Amount: data.Get("amount_total").Int(), Currency: strings.ToUpper(data.Get("currency").String())}
		notice.Subtotal = data.Get("amount_subtotal").Int()
		notice.State = "pending"
		if event.Type == stripe.EventTypeCheckoutSessionExpired {
			notice.State = "expired"
		}
		if event.Type == stripe.EventTypeCheckoutSessionAsyncPaymentFailed {
			notice.State = "failed"
		}
	case stripe.EventTypeInvoicePaid, stripe.EventTypeInvoicePaymentFailed:
		notice.OrderNo = data.Get("subscription_details.metadata.new_api_order").String()
		if notice.OrderNo == "" {
			notice.OrderNo = data.Get("parent.subscription_details.metadata.new_api_order").String()
		}
		notice.InvoiceID = data.Get("id").String()
		notice.TradeNo = notice.InvoiceID
		notice.Recurring = data.Get("billing_reason").String() == "subscription_cycle"
		notice.Money = Money{Amount: data.Get("total").Int(), Currency: strings.ToUpper(data.Get("currency").String())}
		notice.Subtotal = data.Get("subtotal").Int()
	default:
		return nil, nil
	}
	if notice.OrderNo == "" {
		return nil, nil
	} // Not created by this application.
	if event.ID == "" || notice.TradeNo == "" {
		return nil, ErrInvalidPayment
	}
	return notice, nil
}
func (g *stripeGateway) Confirm(ctx context.Context, c Checkout, n Notice) (*Notice, error) {
	if n.OrderNo != c.OrderNo {
		return nil, ErrInvalidPayment
	}
	if n.InvoiceID != "" {
		return g.confirmInvoice(ctx, c, n)
	}
	if c.CheckoutID != "" && n.CheckoutID != c.CheckoutID {
		return nil, ErrInvalidPayment
	}
	params := &stripe.CheckoutSessionParams{}
	params.Context = ctx
	params.AddExpand("line_items.data.price")
	params.AddExpand("invoice")
	item, err := (session.Client{B: g.backend, Key: g.config.Key}).Get(n.CheckoutID, params)
	if err != nil {
		return nil, err
	}
	expectedMode := stripe.CheckoutSessionModePayment
	if c.Subscription {
		expectedMode = stripe.CheckoutSessionModeSubscription
	}
	if item.ClientReferenceID != c.OrderNo || item.ID != n.CheckoutID || item.Mode != expectedMode || item.AmountSubtotal != c.Money.Amount || !strings.EqualFold(string(item.Currency), c.Money.Currency) {
		return nil, ErrInvalidPayment
	}
	if item.LineItems == nil || item.LineItems.HasMore || len(item.LineItems.Data) != 1 {
		return nil, ErrInvalidPayment
	}
	line := item.LineItems.Data[0]
	if line.Price == nil || line.Price.ID != c.PriceID || line.Quantity != c.Quantity {
		return nil, ErrInvalidPayment
	}
	discount, tax := int64(0), int64(0)
	if item.TotalDetails != nil {
		discount = item.TotalDetails.AmountDiscount
		tax = item.TotalDetails.AmountTax
		if item.TotalDetails.AmountShipping != 0 {
			return nil, ErrInvalidPayment
		}
	}
	if !matchesStripeTotal(c, item.AmountSubtotal, item.AmountTotal, discount, tax) {
		return nil, ErrInvalidPayment
	}
	if n.State != "failed" {
		n.State = "pending"
	}
	n.Subtotal = item.AmountSubtotal
	n.Money = Money{Amount: item.AmountTotal, Currency: c.Money.Currency}
	n.Paid = item.Status == stripe.CheckoutSessionStatusComplete && (item.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid || (item.PaymentStatus == stripe.CheckoutSessionPaymentStatusNoPaymentRequired && item.AmountTotal == 0))
	if item.Status == stripe.CheckoutSessionStatusExpired {
		n.State = "expired"
	}
	if item.Customer != nil {
		n.CustomerID = item.Customer.ID
	}
	if item.Subscription != nil {
		n.SubscriptionID = item.Subscription.ID
	}
	if n.Paid && c.Subscription {
		if item.Invoice == nil {
			return nil, ErrInvalidPayment
		}
		n.InvoiceID = item.Invoice.ID
		n.TradeNo = n.InvoiceID
		return g.confirmInvoice(ctx, c, n)
	}
	return &n, nil
}
func matchesStripeTotal(c Checkout, subtotal, total, discount, tax int64) bool {
	if subtotal != c.Money.Amount || discount < 0 || discount > subtotal || tax < 0 || total < 0 || total > common.MaxWalletQuota || (!c.PromotionCodes && discount != 0) {
		return false
	}
	return decimal.NewFromInt(subtotal).Sub(decimal.NewFromInt(discount)).Add(decimal.NewFromInt(tax)).Equal(decimal.NewFromInt(total))
}
func (g *stripeGateway) confirmInvoice(ctx context.Context, c Checkout, n Notice) (*Notice, error) {
	params := &stripe.InvoiceParams{}
	params.Context = ctx
	params.AddExpand("subscription")
	params.AddExpand("lines.data.price")
	item, err := (invoice.Client{B: g.backend, Key: g.config.Key}).Get(n.InvoiceID, params)
	if err != nil {
		return nil, err
	}
	if item.ID != n.InvoiceID || !c.Subscription || item.Subscription == nil || item.Subscription.Metadata["new_api_order"] != c.OrderNo || !strings.EqualFold(string(item.Currency), c.Money.Currency) {
		return nil, ErrInvalidPayment
	}
	if item.Lines == nil || item.Lines.HasMore || len(item.Lines.Data) != 1 {
		return nil, ErrInvalidPayment
	}
	line := item.Lines.Data[0]
	if line.Price == nil || line.Price.ID != c.PriceID || line.Quantity != c.Quantity || line.Proration || line.Period == nil || line.Period.Start <= 0 || line.Period.End <= line.Period.Start {
		return nil, ErrInvalidPayment
	}
	discount := int64(0)
	for _, part := range item.TotalDiscountAmounts {
		if part.Amount < 0 || part.Amount > item.Subtotal-discount {
			return nil, ErrInvalidPayment
		}
		discount += part.Amount
	}
	if !matchesStripeTotal(c, item.Subtotal, item.Total, discount, item.Tax) {
		return nil, ErrInvalidPayment
	}
	n.TradeNo = item.ID
	n.InvoiceID = item.ID
	n.Money = Money{Amount: item.Total, Currency: c.Money.Currency}
	n.Subtotal = item.Subtotal
	n.Paid = item.Paid && item.Status == stripe.InvoiceStatusPaid
	n.SubscriptionID = item.Subscription.ID
	n.Recurring = item.BillingReason == stripe.InvoiceBillingReasonSubscriptionCycle
	if item.BillingReason != stripe.InvoiceBillingReasonSubscriptionCycle && item.BillingReason != stripe.InvoiceBillingReasonSubscriptionCreate {
		return nil, ErrInvalidPayment
	}
	n.PeriodStart = line.Period.Start
	n.PeriodEnd = line.Period.End
	if item.Customer != nil {
		n.CustomerID = item.Customer.ID
	}
	if !n.Paid {
		n.State = "pending"
	}
	return &n, nil
}
