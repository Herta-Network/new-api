// Package payment implements payment protocols without owning business settlement.
package payment

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

const Epay = "epay"

var ErrInvalidPayment = errors.New("invalid payment notification")

// Money is denominated in the currency's smallest unit. Epay's contract uses CNY fen.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type Checkout struct {
	OrderNo   string
	Subject   string
	Money     Money
	NotifyURL string
	ReturnURL string
}

type CheckoutResult struct {
	URL    string            `json:"url"`
	Params map[string]string `json:"data"`
}

// Notice contains authenticated provider facts, never a request to grant quota.
type Notice struct {
	OrderNo     string `json:"order_no"`
	TradeNo     string `json:"trade_no"`
	PaymentType string `json:"payment_type"`
	Money       Money  `json:"money"`
	Paid        bool   `json:"paid"`
}

type Gateway interface {
	StartPayment(context.Context, Checkout) (*CheckoutResult, error)
	ParseCallback(context.Context, map[string]string) (*Notice, error)
}

// Open binds a provider implementation to an immutable configuration version.
func Open(provider string, config string) (Gateway, error) {
	switch provider {
	case Epay:
		var settings EpayConfig
		if err := common.UnmarshalJsonStr(config, &settings); err != nil {
			return nil, err
		}
		return NewEpay(settings)
	default:
		return nil, errors.New("unsupported payment provider")
	}
}

var decimalMoney = regexp.MustCompile(`^[0-9]{1,14}(\.[0-9]{1,2})?$`)

// ParseCNY rejects exponent notation, signs, extra precision and overflow.
func ParseCNY(value string) (Money, error) {
	if !decimalMoney.MatchString(value) {
		return Money{}, ErrInvalidPayment
	}
	whole, fraction, _ := strings.Cut(value, ".")
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return Money{}, ErrInvalidPayment
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	minor, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || major > (math.MaxInt64-minor)/100 {
		return Money{}, ErrInvalidPayment
	}
	amount := major*100 + minor
	if amount <= 0 || amount > common.MaxWalletQuota {
		return Money{}, ErrInvalidPayment
	}
	return Money{Amount: amount, Currency: "CNY"}, nil
}

// RoundCNY applies the checkout rounding once, before persisting the snapshot.
func RoundCNY(value decimal.Decimal) (Money, error) {
	return ParseCNY(value.StringFixed(2))
}

func (m Money) Decimal() string {
	return decimal.NewFromInt(m.Amount).Shift(-2).StringFixed(2)
}
