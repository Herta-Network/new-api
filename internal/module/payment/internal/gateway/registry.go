// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/registry.go
//   - internal/module/billing/internal/payment/platform.go (field descriptions)
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: only EPay, Stripe and Cryptomus are served; platforms
// are lowercase names; NormalizeConfig validates the configuration a
// method is saved with (EPay endpoint and credentials, Stripe key prefixes
// and mode, Cryptomus credentials) instead of only re-encoding it; the
// field descriptions mark secret and server-managed fields, can offer a
// fixed set of choices (the Stripe payment method type, the EPay version and
// checkout mode), take several lines, carry a hint, and apply only under a
// choice of another field (the credentials of each EPay version); an
// injected HTTP client also carries Stripe traffic.

package gateway

import (
	"errors"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v81"
)

// Platforms of payment methods. Balance is the wallet balance, which no
// gateway serves.
const (
	PlatformEPay      = "epay"
	PlatformStripe    = "stripe"
	PlatformCryptomus = "cryptomus"
	PlatformBalance   = "balance"
)

// Method is the gateway's view of a configured payment method.
type Method struct {
	ID       int64
	Platform string
	// Token is the secret path segment of the method's notify URL.
	Token string
	// Domain is the base URL callbacks are sent to; empty selects the site
	// address.
	Domain string
	// Config is the platform's JSON configuration as NormalizeConfig
	// returned it.
	Config string
}

// ErrNotGateway reports a payment method that is not served by a gateway:
// the wallet balance or an unsupported platform.
var ErrNotGateway = errors.New("payment method is not served by a gateway")

// defaultTimeout bounds the gateway requests of a registry that was not
// given an HTTP client.
const defaultTimeout = 10 * time.Second

// Registry opens the gateway of a payment method from its configuration. It
// is the only place that knows which platform maps to which protocol.
type Registry struct {
	httpClient       *http.Client
	stripeBackends   *stripe.Backends
	cryptomusBaseURL string
}

// Option configures a Registry.
type Option func(*Registry)

// WithHTTPClient sends every gateway request through client, Stripe's
// included unless WithStripeBackends overrides them; nil keeps a default
// client with a ten-second timeout and the Stripe SDK's own.
func WithHTTPClient(client *http.Client) Option {
	return func(r *Registry) { r.httpClient = client }
}

// WithStripeBackends points the Stripe clients at backends; tests use it to
// reach a local fake.
func WithStripeBackends(backends *stripe.Backends) Option {
	return func(r *Registry) { r.stripeBackends = backends }
}

// WithCryptomusBaseURL points the Cryptomus clients at another API host.
// Production never sets it: the invoice query is the authoritative payment
// confirmation, so it must not be redirectable through the configuration
// of a payment method.
func WithCryptomusBaseURL(baseURL string) Option {
	return func(r *Registry) { r.cryptomusBaseURL = baseURL }
}

// NewRegistry builds a registry of the supported gateways.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{cryptomusBaseURL: cryptomusAPI}
	for _, opt := range opts {
		opt(r)
	}
	if r.httpClient == nil {
		r.httpClient = &http.Client{Timeout: defaultTimeout}
	} else if r.stripeBackends == nil {
		r.stripeBackends = stripe.NewBackendsWithConfig(&stripe.BackendConfig{HTTPClient: r.httpClient})
	}
	return r
}

// platformSpec describes one supported gateway platform.
type platformSpec struct {
	open func(r *Registry, method Method) (Gateway, error)
	// normalize validates an administrator's configuration and returns the
	// canonical form stored on the payment method.
	normalize func(raw []byte) (string, error)
	style     CallbackStyle
}

var platforms = map[string]platformSpec{
	PlatformEPay:      {open: openEPay, normalize: normalizeEPay, style: CallbackStyle{UniqueParams: true, TextReply: true}},
	PlatformStripe:    {open: openStripe, normalize: normalizeStripe, style: CallbackStyle{Body: true, SignatureHeader: "Stripe-Signature"}},
	PlatformCryptomus: {open: openCryptomus, normalize: normalizeCryptomus, style: CallbackStyle{Body: true, TextReply: true}},
}

// Open returns the gateway of the payment method.
func (r *Registry) Open(method Method) (Gateway, error) {
	spec, ok := platforms[method.Platform]
	if !ok {
		return nil, ErrNotGateway
	}
	return spec.open(r, method)
}

// CallbackStyle returns how the platform's callbacks are delivered; false
// for a platform no gateway serves.
func (r *Registry) CallbackStyle(platform string) (CallbackStyle, bool) {
	spec, ok := platforms[platform]
	return spec.style, ok
}

// NormalizeConfig validates the JSON configuration a payment method of
// platform is saved with and returns the canonical form to store. Unknown
// fields are dropped.
func (r *Registry) NormalizeConfig(platform string, raw []byte) (string, error) {
	spec, ok := platforms[platform]
	if !ok {
		return "", ErrNotGateway
	}
	return spec.normalize(raw)
}

// Field types: FieldSelect takes one of its Options, FieldTextarea is text of
// several lines; a field without a type is one line of text.
const (
	FieldSelect   = "select"
	FieldTextarea = "textarea"
)

// Field describes one configuration field of a platform for the
// administrator's form.
type Field struct {
	Key string `json:"key"`
	// Label and Description are English source text the frontend
	// translates.
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Type is FieldSelect, FieldTextarea or empty for a text field.
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
	// Secret fields are never returned to the browser.
	Secret bool `json:"secret"`
	// Managed fields are maintained by the server and not edited.
	Managed bool `json:"managed"`
	// Options are the values a FieldSelect field takes, in display order.
	Options []FieldOption `json:"options,omitempty"`
	// When limits the field to configurations whose fields hold these
	// values; the configuration drops it otherwise.
	When map[string]string `json:"when,omitempty"`
}

// FieldOption is one value of a FieldSelect field.
type FieldOption struct {
	Value string `json:"value"`
	// Label is English source text the frontend translates.
	Label string `json:"label"`
}

// PlatformInfo describes a gateway platform for the administrator.
type PlatformInfo struct {
	Platform string  `json:"platform"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Fields   []Field `json:"fields"`
}

// Platforms describes every gateway platform, in display order.
func Platforms() []PlatformInfo {
	return []PlatformInfo{
		{
			Platform: PlatformEPay,
			Name:     "EPay",
			Fields: []Field{
				{Key: "url", Label: "Gateway URL", Required: true},
				{Key: "pid", Label: "Merchant ID", Required: true},
				{Key: "version", Label: "Protocol version", Type: FieldSelect, Required: true, Options: []FieldOption{
					{Value: EPayV1, Label: "EPay V1 (MD5 merchant key)"},
					{Value: EPayV2, Label: "EPay V2 (RSA keys)"},
				}},
				{Key: "checkout", Label: "Checkout mode", Type: FieldSelect, Required: true,
					Description: "An API order is created by the server, which notices a failing gateway before the buyer leaves.",
					Options: []FieldOption{
						{Value: EPayCheckoutAPI, Label: "API order"},
						{Value: EPayCheckoutPage, Label: "Gateway payment page"},
					}},
				{Key: "key", Label: "Merchant key", Required: true, Secret: true, When: map[string]string{"version": EPayV1}},
				{Key: "private_key", Label: "Merchant private key", Required: true, Secret: true, When: map[string]string{"version": EPayV2}},
				{Key: "platform_public_key", Label: "Platform public key", Type: FieldTextarea, Required: true, When: map[string]string{"version": EPayV2}},
				{Key: "type", Label: "Payment type",
					Description: "alipay, wxpay, qqpay and so on. API orders need one; on the payment page an empty type lets the buyer choose."},
			},
		},
		{
			Platform: PlatformStripe,
			Name:     "Stripe",
			URL:      "https://stripe.com",
			Fields: []Field{
				{Key: "public_key", Label: "Publishable key", Required: true},
				{Key: "secret_key", Label: "Secret key", Required: true, Secret: true},
				{Key: "payment_method_type", Label: "Payment method type", Type: FieldSelect, Required: true, Options: []FieldOption{
					{Value: stripeTypeCard, Label: "Card"},
					{Value: stripeTypeAlipay, Label: "Alipay"},
					{Value: stripeTypeWechatPay, Label: "WeChat Pay"},
				}},
				{Key: "webhook_endpoint_id", Label: "Webhook endpoint ID", Managed: true},
				{Key: "webhook_url", Label: "Webhook URL", Managed: true},
				{Key: "webhook_secret", Label: "Webhook signing secret", Secret: true, Managed: true},
			},
		},
		{
			Platform: PlatformCryptomus,
			Name:     "Cryptomus",
			URL:      "https://cryptomus.com",
			Fields: []Field{
				{Key: "merchant_id", Label: "Merchant UUID", Required: true},
				{Key: "api_key", Label: "Payment API key", Required: true, Secret: true},
			},
		},
	}
}
