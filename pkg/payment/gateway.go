// Package payment implements provider protocols without owning business settlement.
package payment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

const (
	Epay      = "epay"
	Stripe    = "stripe"
	Cryptomus = "cryptomus"
)

var ErrInvalidPayment = errors.New("invalid payment notification")

// Config is normalized by provider before it is stored. Unused fields are removed.
type Config struct {
	Endpoint       string `json:"endpoint,omitempty"`
	MerchantID     string `json:"merchant_id,omitempty"`
	Key            string `json:"key"`
	PaymentType    string `json:"payment_type,omitempty"`
	WebhookSecret  string `json:"webhook_secret,omitempty"`
	PriceID        string `json:"price_id,omitempty"`
	PromotionCodes bool   `json:"promotion_codes,omitempty"`
	UnitPrice      string `json:"unit_price,omitempty"`
	MinTopUp       int64  `json:"min_topup,omitempty"`
}

type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type Checkout struct {
	OrderNo        string `json:"order_no"`
	Subject        string `json:"subject"`
	Money          Money  `json:"money"`
	NotifyURL      string `json:"-"`
	ReturnURL      string `json:"-"`
	CancelURL      string `json:"-"`
	Email          string `json:"-"`
	CustomerID     string `json:"customer_id,omitempty"`
	CheckoutID     string `json:"checkout_id,omitempty"`
	PriceID        string `json:"price_id,omitempty"`
	Quantity       int64  `json:"quantity,omitempty"`
	Subscription   bool   `json:"subscription,omitempty"`
	PromotionCodes bool   `json:"promotion_codes,omitempty"`
}

type CheckoutResult struct {
	URL        string            `json:"url"`
	Params     map[string]string `json:"data,omitempty"`
	CheckoutID string            `json:"checkout_id,omitempty"`
}

type Callback struct {
	Body      []byte
	Signature string
	Params    map[string]string
}

// Notice contains authenticated provider facts. Confirmation re-reads paid states
// before the host grants anything; the original notice stays in the durable inbox.
type Notice struct {
	OrderNo        string `json:"order_no"`
	TradeNo        string `json:"trade_no"`
	EventID        string `json:"event_id,omitempty"`
	PaymentType    string `json:"payment_type"`
	Money          Money  `json:"money"`
	Subtotal       int64  `json:"subtotal"`
	Paid           bool   `json:"paid"`
	State          string `json:"state"`
	CheckoutID     string `json:"checkout_id,omitempty"`
	CustomerID     string `json:"customer_id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	InvoiceID      string `json:"invoice_id,omitempty"`
	Recurring      bool   `json:"recurring,omitempty"`
	PeriodStart    int64  `json:"period_start,omitempty"`
	PeriodEnd      int64  `json:"period_end,omitempty"`
}

type Gateway interface {
	AccountKey(context.Context) (string, error)
	PaymentType() string
	Quote(context.Context, Checkout) (Money, error)
	StartPayment(context.Context, Checkout) (*CheckoutResult, error)
	ParseCallback(context.Context, Callback) (*Notice, error)
	Confirm(context.Context, Checkout, Notice) (*Notice, error)
}

// Runtime injects only network boundaries for deterministic provider tests.
type Runtime struct {
	HTTPClient   *http.Client
	StripeURL    string
	CryptomusURL string
}

func Open(provider string, raw string) (Gateway, error) {
	return OpenWithRuntime(provider, raw, Runtime{})
}
func OpenWithRuntime(provider, raw string, runtime Runtime) (Gateway, error) {
	var config Config
	if err := common.UnmarshalJsonStr(raw, &config); err != nil {
		return nil, err
	}
	if runtime.HTTPClient == nil {
		runtime.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	switch provider {
	case Epay:
		return NewEpay(config)
	case Stripe:
		return newStripe(config, runtime)
	case Cryptomus:
		return newCryptomus(config, runtime)
	default:
		return nil, errors.New("unsupported payment provider")
	}
}

func NormalizeConfig(provider string, config Config) (Config, error) {
	config.Key = strings.TrimSpace(config.Key)
	if config.Key == "" || len(config.Key) > 512 || config.MinTopUp < 0 || config.MinTopUp > 10000 {
		return Config{}, errors.New("invalid payment credentials")
	}
	switch provider {
	case Epay:
		config = Config{Endpoint: strings.TrimRight(strings.TrimSpace(config.Endpoint), "/"), MerchantID: strings.TrimSpace(config.MerchantID), Key: config.Key, PaymentType: strings.TrimSpace(config.PaymentType), MinTopUp: config.MinTopUp}
		_, err := NewEpay(config)
		return config, err
	case Stripe:
		config = Config{Key: config.Key, WebhookSecret: strings.TrimSpace(config.WebhookSecret), PriceID: strings.TrimSpace(config.PriceID), PromotionCodes: config.PromotionCodes, MinTopUp: config.MinTopUp}
		if (!strings.HasPrefix(config.Key, "sk_") && !strings.HasPrefix(config.Key, "rk_")) || (config.WebhookSecret != "" && !strings.HasPrefix(config.WebhookSecret, "whsec_")) || (config.PriceID != "" && !strings.HasPrefix(config.PriceID, "price_")) {
			return Config{}, errors.New("invalid Stripe configuration")
		}
		return config, nil
	case Cryptomus:
		unitPrice := strings.TrimSpace(config.UnitPrice)
		if unitPrice == "" {
			unitPrice = "1"
		}
		price, err := decimal.NewFromString(unitPrice)
		if err != nil || !price.IsPositive() || price.GreaterThan(decimal.NewFromInt(10000)) {
			return Config{}, errors.New("invalid unit price")
		}
		config = Config{Key: config.Key, MerchantID: strings.TrimSpace(config.MerchantID), UnitPrice: price.String(), MinTopUp: config.MinTopUp}
		if !protocolIdentifier.MatchString(config.MerchantID) {
			return Config{}, errors.New("invalid merchant ID")
		}
		return config, nil
	default:
		return Config{}, errors.New("unsupported payment provider")
	}
}

func AccountFingerprint(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

var decimalMoney = regexp.MustCompile(`^[0-9]{1,14}(\.[0-9]{1,8})?$`)

func CurrencyDigits(currency string) int32 {
	switch strings.ToUpper(currency) {
	case "BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF":
		return 0
	case "BHD", "JOD", "KWD", "OMR", "TND":
		return 3
	default:
		return 2
	}
}

// ParseMoney permits provider formatting with trailing zeros, but never rounds
// a received payment. Quotes alone round at the currency's minor unit boundary.
func ParseMoney(value, currency string) (Money, error) {
	if !decimalMoney.MatchString(value) || len(currency) != 3 {
		return Money{}, ErrInvalidPayment
	}
	amount, err := decimal.NewFromString(value)
	if err != nil {
		return Money{}, ErrInvalidPayment
	}
	minor := amount.Shift(CurrencyDigits(currency))
	if minor.IsNegative() || !minor.Equal(minor.Truncate(0)) || minor.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return Money{}, ErrInvalidPayment
	}
	return Money{Amount: minor.IntPart(), Currency: strings.ToUpper(currency)}, nil
}
func ParseCNY(value string) (Money, error) {
	// Epay only defines two decimal places, including in signed notifications.
	if _, fraction, ok := strings.Cut(value, "."); ok && len(fraction) > 2 {
		return Money{}, ErrInvalidPayment
	}
	money, err := ParseMoney(value, "CNY")
	if err != nil || money.Amount <= 0 {
		return Money{}, ErrInvalidPayment
	}
	return money, nil
}
func RoundMoney(value decimal.Decimal, currency string) (Money, error) {
	money, err := ParseMoney(value.StringFixed(CurrencyDigits(currency)), currency)
	if err != nil || money.Amount <= 0 {
		return Money{}, ErrInvalidPayment
	}
	return money, nil
}
func RoundCNY(value decimal.Decimal) (Money, error) { return RoundMoney(value, "CNY") }
func (m Money) Decimal() string {
	return decimal.NewFromInt(m.Amount).Shift(-CurrencyDigits(m.Currency)).StringFixed(CurrencyDigits(m.Currency))
}
