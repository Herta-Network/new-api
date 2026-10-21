// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/stripe.go
//   - internal/module/billing/internal/payment/stripe/stripe.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the protocol client and the gateway are one type; a
// PaymentIntent enables automatic_payment_methods instead of naming one
// payment method type, carries the newapi_order_no and newapi_method_id
// metadata and the idempotency key newapi:pi:<order number>; no Stripe
// customer or ephemeral key is created; a replayed creation is re-read so a
// cancelled intent is never handed out again; events of other sites or
// other payment methods on the same account are ignored; payment_failed and
// canceled events are acknowledged as lifecycle events; ConfirmPayment
// checks the status, AmountReceived, currency and metadata of the intent; a
// processing intent is not cancelled; the webhook endpoint pins the SDK's
// API version, subscribes to the canceled event as well, and its URL can be
// updated in place; the configuration is validated when it is saved (key
// prefixes, test or live mode on both keys).

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"github.com/stripe/stripe-go/v81/webhook"
)

// Metadata keys new-api writes on its PaymentIntents. They tell this site's
// intents apart from other integrations of the same Stripe account, and
// one payment method's from another's.
const (
	stripeMetadataOrderNo  = "newapi_order_no"
	stripeMetadataMethodID = "newapi_method_id"
)

// stripeWebhookEvents are the events a payment method's webhook endpoint
// subscribes to.
var stripeWebhookEvents = []string{
	string(stripe.EventTypePaymentIntentSucceeded),
	string(stripe.EventTypePaymentIntentPaymentFailed),
	string(stripe.EventTypePaymentIntentCanceled),
}

// StripeConfig is the stored configuration of a Stripe payment method. The
// webhook fields are maintained by the server when it registers the
// method's webhook endpoint; administrators never set them.
type StripeConfig struct {
	PublicKey         string `json:"public_key"`
	SecretKey         string `json:"secret_key"`
	WebhookEndpointID string `json:"webhook_endpoint_id"`
	WebhookURL        string `json:"webhook_url"`
	WebhookSecret     string `json:"webhook_secret"`
}

type stripeGateway struct {
	// methodID is the payment method id as written in intent metadata.
	methodID string
	config   StripeConfig
	// api carries the method's own secret key on every request; nothing is
	// written to the SDK's process-wide key, so several Stripe methods serve
	// concurrent requests without borrowing each other's account.
	api *client.API
}

func openStripe(r *Registry, method Method) (Gateway, error) {
	var config StripeConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, fmt.Errorf("decode Stripe configuration: %w", err)
	}
	if config.PublicKey == "" || config.SecretKey == "" {
		return nil, errors.New("incomplete Stripe configuration")
	}
	return &stripeGateway{
		methodID: strconv.FormatInt(method.ID, 10),
		config:   config,
		api:      client.New(config.SecretKey, r.stripeBackends),
	}, nil
}

// normalizeStripe requires a publishable key (pk_) and a secret or
// restricted key (sk_, rk_) of the same mode, test or live.
func normalizeStripe(raw []byte) (string, error) {
	var config StripeConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("decode Stripe configuration: %w", err)
	}
	config.PublicKey = strings.TrimSpace(config.PublicKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	publicMode := stripeKeyMode(config.PublicKey, "pk")
	if publicMode == "" {
		return "", errors.New("the Stripe publishable key must start with pk_test_ or pk_live_")
	}
	secretMode := stripeKeyMode(config.SecretKey, "sk", "rk")
	if secretMode == "" {
		return "", errors.New("the Stripe secret key must start with sk_test_, sk_live_, rk_test_ or rk_live_")
	}
	if publicMode != secretMode {
		return "", errors.New("the Stripe publishable and secret keys must both be test keys or both be live keys")
	}
	content, err := common.Marshal(config)
	return string(content), err
}

// stripeKeyMode returns "test" or "live" for a key of one of kinds, empty
// for anything else.
func stripeKeyMode(key string, kinds ...string) string {
	for _, kind := range kinds {
		for _, mode := range []string{"test", "live"} {
			if rest, ok := strings.CutPrefix(key, kind+"_"+mode+"_"); ok && rest != "" {
				return mode
			}
		}
	}
	return ""
}

func (g *stripeGateway) Platform() string { return PlatformStripe }

func (g *stripeGateway) ChargeCurrency() string { return "" }

func (g *stripeGateway) roundCharge(c Charge) Charge {
	c.Amount = roundStripeHundredths(c.Amount, c.Currency)
	return c
}

// NeedsNotifyURL is false: Stripe delivers events to the webhook endpoint
// registered when the payment method was saved.
func (g *stripeGateway) NeedsNotifyURL(*Order) bool { return false }

// StartPayment returns the client secret of the order's only PaymentIntent.
// Reusing it is essential: accepting two client secrets would let a buyer
// pay an older intent whose callback no longer matches the stored trade
// number.
func (g *stripeGateway) StartPayment(ctx context.Context, c Checkout) (*Action, error) {
	var (
		intent *stripe.PaymentIntent
		err    error
	)
	if c.Order.TradeNo != "" {
		intent, err = g.payableIntent(ctx, c.Order.OrderNo, c.Charge, c.Order.TradeNo)
	} else {
		intent, err = g.claimNewIntent(ctx, c)
	}
	if err != nil {
		return nil, err
	}
	return &Action{
		Type:   ActionStripe,
		Stripe: &StripeAction{PublishableKey: g.config.PublicKey, ClientSecret: intent.ClientSecret},
	}, nil
}

// claimNewIntent creates the order's PaymentIntent and binds it to the
// order. A failed claim cancels the intent; when a concurrent checkout won
// the order with another intent, ours is cancelled and the winner returned.
func (g *stripeGateway) claimNewIntent(ctx context.Context, c Checkout) (*stripe.PaymentIntent, error) {
	if c.Charge.Amount <= 0 || c.Charge.Currency == "" {
		return nil, errors.New("invalid Stripe charge")
	}
	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(toStripeAmount(c.Charge.Amount, c.Charge.Currency)),
		Currency: stripe.String(strings.ToLower(c.Charge.Currency)),
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
		Metadata: map[string]string{
			stripeMetadataOrderNo:  c.Order.OrderNo,
			stripeMetadataMethodID: g.methodID,
		},
	}
	if c.Subject != "" {
		params.Description = stripe.String(c.Subject)
	}
	params.Context = ctx
	// Retrying the checkout after a network failure must return the same
	// PaymentIntent rather than create another chargeable one.
	params.SetIdempotencyKey("newapi:pi:" + c.Order.OrderNo)
	intent, err := g.api.PaymentIntents.New(params)
	if err != nil {
		return nil, fmt.Errorf("create Stripe payment intent: %w", err)
	}
	if intent.LastResponse != nil && intent.LastResponse.Header.Get("Idempotent-Replayed") == "true" {
		// A replay returns the intent as it was created; a failed claim may
		// have cancelled it since.
		if intent, err = g.payableIntent(ctx, c.Order.OrderNo, c.Charge, intent.ID); err != nil {
			return nil, err
		}
	}
	winner, err := c.Claim(ctx, intent.ID)
	if err != nil {
		_ = g.cancelIntent(ctx, intent.ID)
		return nil, err
	}
	if winner == intent.ID {
		return intent, nil
	}
	if err := g.cancelIntent(ctx, intent.ID); err != nil {
		return nil, fmt.Errorf("cancel duplicate Stripe payment intent: %w", err)
	}
	return g.payableIntent(ctx, c.Order.OrderNo, c.Charge, winner)
}

// payableIntent loads the order's intent before its client secret is handed
// out again: it must still match the order and not be cancelled.
func (g *stripeGateway) payableIntent(ctx context.Context, orderNo string, charge Charge, tradeNo string) (*stripe.PaymentIntent, error) {
	intent, err := g.matchingIntent(ctx, orderNo, charge, tradeNo)
	if err != nil {
		return nil, err
	}
	if intent.Status == stripe.PaymentIntentStatusCanceled {
		return nil, errors.New("the Stripe payment intent of the order is canceled")
	}
	return intent, nil
}

// matchingIntent loads PaymentIntent tradeNo and checks that it was created
// for the order by this payment method: same order number, method, amount
// and currency.
func (g *stripeGateway) matchingIntent(ctx context.Context, orderNo string, charge Charge, tradeNo string) (*stripe.PaymentIntent, error) {
	if tradeNo == "" {
		return nil, errors.New("the Stripe payment intent is missing")
	}
	params := &stripe.PaymentIntentParams{}
	params.Context = ctx
	intent, err := g.api.PaymentIntents.Get(tradeNo, params)
	if err != nil {
		return nil, fmt.Errorf("get Stripe payment intent: %w", err)
	}
	if intent.ID != tradeNo || intent.Metadata[stripeMetadataOrderNo] != orderNo || intent.Metadata[stripeMetadataMethodID] != g.methodID ||
		intent.Amount != toStripeAmount(charge.Amount, charge.Currency) || !strings.EqualFold(string(intent.Currency), charge.Currency) {
		return nil, errors.New("the Stripe payment intent does not match the order")
	}
	return intent, nil
}

func (g *stripeGateway) cancelIntent(ctx context.Context, tradeNo string) error {
	params := &stripe.PaymentIntentCancelParams{}
	params.Context = ctx
	_, err := g.api.PaymentIntents.Cancel(tradeNo, params)
	return err
}

// Reconcile settles an intent that succeeded and cancels one that did not,
// so a still-pending client secret cannot be paid after the local close. The
// intent is verified against the payment expectation recorded at checkout.
// An intent that is already canceled can never be paid, so the order closes
// without asking Stripe to cancel it again, which Stripe refuses; one that
// is processing cannot be cancelled and keeps the order pending.
func (g *stripeGateway) Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error) {
	o := req.Order
	if o.TradeNo == "" {
		// No intent was claimed. The checkout records its expectation before
		// it creates the intent, so the close must find the order unchanged
		// when it commits.
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	charge := Charge{Amount: o.PaymentAmount, Currency: o.PaymentCurrency}
	intent, err := g.matchingIntent(ctx, o.OrderNo, charge, o.TradeNo)
	if err != nil {
		return Reconciliation{}, err
	}
	if verdict, final := stripeVerdict(intent); final {
		return verdict, nil
	}
	if intent.Status == stripe.PaymentIntentStatusProcessing {
		return Reconciliation{}, fmt.Errorf("stripe payment intent %s is processing: %w", o.TradeNo, ErrUnconfirmed)
	}
	if err := g.cancelIntent(ctx, o.TradeNo); err == nil {
		return Reconciliation{}, nil
	}
	// A payment can finish, or the intent be canceled elsewhere, between the
	// status query and the cancellation. Recheck once so a paid intent is
	// settled rather than closed locally and a canceled one closes.
	intent, err = g.matchingIntent(ctx, o.OrderNo, charge, o.TradeNo)
	if err != nil {
		return Reconciliation{}, err
	}
	if verdict, final := stripeVerdict(intent); final {
		return verdict, nil
	}
	return Reconciliation{}, fmt.Errorf("cancel Stripe payment intent %s failed", o.TradeNo)
}

// QueryPayment reports the claimed intent when it succeeded with exactly the
// payment expectation; unlike Reconcile it never cancels the intent.
func (g *stripeGateway) QueryPayment(ctx context.Context, o *Order) (string, error) {
	if o.TradeNo == "" {
		return "", nil
	}
	charge := Charge{Amount: o.PaymentAmount, Currency: o.PaymentCurrency}
	intent, err := g.matchingIntent(ctx, o.OrderNo, charge, o.TradeNo)
	if err != nil {
		return "", err
	}
	if intent.Status != stripe.PaymentIntentStatusSucceeded || intent.AmountReceived != toStripeAmount(charge.Amount, charge.Currency) {
		return "", nil
	}
	return intent.ID, nil
}

// stripeVerdict maps a terminal intent status to the close verdict: a
// succeeded intent settles the order, a canceled one lets it close. Any
// other status is not final.
func stripeVerdict(intent *stripe.PaymentIntent) (Reconciliation, bool) {
	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		return Reconciliation{TradeNo: intent.ID}, true
	case stripe.PaymentIntentStatusCanceled:
		return Reconciliation{}, true
	default:
		return Reconciliation{}, false
	}
}

// ParseCallback verifies the webhook signature. A succeeded PaymentIntent of
// this payment method announces a payment and a failed or canceled one is a
// lifecycle event; every other event, and every intent of another site or
// payment method on the same account, is ignored.
func (g *stripeGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	if g.config.WebhookSecret == "" {
		return nil, errors.New("the Stripe webhook of the payment method is not registered")
	}
	// The endpoint pins the SDK's API version when it is registered. An
	// event of another version is still read: only the stable intent fields
	// are used, and ConfirmPayment re-reads the intent through the API.
	event, err := webhook.ConstructEventWithOptions(n.Body, n.Signature, g.config.WebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return nil, err
	}
	if !slices.Contains(stripeWebhookEvents, string(event.Type)) {
		return &Notice{Ignore: true, Status: string(event.Type)}, nil
	}
	var intent stripe.PaymentIntent
	if err := common.Unmarshal(event.Data.Raw, &intent); err != nil {
		return nil, fmt.Errorf("decode Stripe payment intent: %w", err)
	}
	orderNo := intent.Metadata[stripeMetadataOrderNo]
	if orderNo == "" || intent.Metadata[stripeMetadataMethodID] != g.methodID {
		return &Notice{Ignore: true, Status: string(event.Type), Details: map[string]string{"trade_no": intent.ID}}, nil
	}
	if err := validateOrderNo(orderNo); err != nil {
		return nil, err
	}
	if err := ValidateTradeNo(intent.ID); err != nil {
		return nil, err
	}
	paid := event.Type == stripe.EventTypePaymentIntentSucceeded && intent.Status == stripe.PaymentIntentStatusSucceeded
	amount := intent.Amount
	if paid {
		amount = intent.AmountReceived
	}
	currency := strings.ToUpper(string(intent.Currency))
	return &Notice{
		OrderNo:  orderNo,
		TradeNo:  intent.ID,
		Amount:   fromStripeAmount(amount, currency),
		Currency: currency,
		Paid:     paid,
		Status:   string(event.Type),
		Details:  map[string]string{"event_id": event.ID, "intent_status": string(intent.Status)},
	}, nil
}

// CheckOrder has nothing to add: ParseCallback only accepts intents whose
// metadata names this payment method, and the order is looked up by the
// order number in the same metadata.
func (g *stripeGateway) CheckOrder(*Order, *Notice) error { return nil }

// ConfirmPayment re-reads the intent from Stripe: it must have succeeded,
// still belong to the order and this payment method, and have received
// exactly the recorded payment expectation.
func (g *stripeGateway) ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error {
	charge := Charge{Amount: o.PaymentAmount, Currency: o.PaymentCurrency}
	intent, err := g.matchingIntent(ctx, o.OrderNo, charge, notice.TradeNo)
	if err != nil {
		return err
	}
	if intent.Status != stripe.PaymentIntentStatusSucceeded {
		return fmt.Errorf("stripe payment intent %s has status %s", intent.ID, intent.Status)
	}
	if intent.AmountReceived != toStripeAmount(charge.Amount, charge.Currency) {
		return errors.New("stripe payment intent received another amount")
	}
	return nil
}

// ErrStripeWebhookMissing reports a webhook endpoint Stripe does not know,
// such as one deleted in the Stripe dashboard.
var ErrStripeWebhookMissing = errors.New("the Stripe webhook endpoint does not exist")

// StripeWebhookEndpoint is a registered webhook endpoint. Secret, the
// signing secret, is only returned when the endpoint is created.
type StripeWebhookEndpoint struct {
	ID     string
	URL    string
	Secret string
}

// StripeWebhooks manages the webhook endpoints of one Stripe account.
type StripeWebhooks struct {
	api *client.API
}

// StripeWebhooks returns the webhook management of the Stripe account whose
// secret key is secretKey.
func (r *Registry) StripeWebhooks(secretKey string) *StripeWebhooks {
	return &StripeWebhooks{api: client.New(secretKey, r.stripeBackends)}
}

// Create registers url for the payment events callbacks settle. The
// endpoint pins the SDK's API version, so its events decode as the SDK
// expects.
func (w *StripeWebhooks) Create(ctx context.Context, url string) (StripeWebhookEndpoint, error) {
	params := &stripe.WebhookEndpointParams{
		URL:           stripe.String(url),
		EnabledEvents: stripe.StringSlice(stripeWebhookEvents),
		APIVersion:    stripe.String(stripe.APIVersion),
		Description:   stripe.String("new-api payment notifications"),
	}
	params.Context = ctx
	endpoint, err := w.api.WebhookEndpoints.New(params)
	if err != nil {
		return StripeWebhookEndpoint{}, fmt.Errorf("create Stripe webhook endpoint: %w", err)
	}
	if endpoint.ID == "" || endpoint.Secret == "" {
		return StripeWebhookEndpoint{}, errors.New("stripe returned a webhook endpoint without an id or signing secret")
	}
	return StripeWebhookEndpoint{ID: endpoint.ID, URL: endpoint.URL, Secret: endpoint.Secret}, nil
}

// UpdateURL moves endpoint id to url. It also restores the subscribed
// events and re-enables an endpoint Stripe disabled after failed
// deliveries; the signing secret stays the same.
func (w *StripeWebhooks) UpdateURL(ctx context.Context, id, url string) error {
	params := &stripe.WebhookEndpointParams{
		URL:           stripe.String(url),
		EnabledEvents: stripe.StringSlice(stripeWebhookEvents),
		Disabled:      stripe.Bool(false),
	}
	params.Context = ctx
	if _, err := w.api.WebhookEndpoints.Update(id, params); err != nil {
		var stripeErr *stripe.Error
		if errors.As(err, &stripeErr) && stripeErr.HTTPStatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %w", ErrStripeWebhookMissing, err)
		}
		return fmt.Errorf("update Stripe webhook endpoint: %w", err)
	}
	return nil
}

// Delete removes endpoint id: the endpoint of a payment method that could
// not be saved, or of an account the method no longer uses.
func (w *StripeWebhooks) Delete(ctx context.Context, id string) error {
	params := &stripe.WebhookEndpointParams{}
	params.Context = ctx
	if _, err := w.api.WebhookEndpoints.Del(id, params); err != nil {
		return fmt.Errorf("delete Stripe webhook endpoint: %w", err)
	}
	return nil
}
