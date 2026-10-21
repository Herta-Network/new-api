package operation_setting

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/shopspring/decimal"
)

type PaymentSetting struct {
	AmountOptions  []int           `json:"amount_options"`
	AmountDiscount map[int]float64 `json:"amount_discount"` // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠

	ComplianceConfirmed    bool   `json:"compliance_confirmed"`
	ComplianceTermsVersion string `json:"compliance_terms_version"`
	ComplianceConfirmedAt  int64  `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy  int    `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP  string `json:"compliance_confirmed_ip"`

	// Currency is the site currency: every price and top-up amount is in it.
	Currency string `json:"currency"`
	// MinTopup is the smallest top-up amount in the site currency, a decimal
	// with at most two decimal places.
	MinTopup string `json:"min_topup"`
	// ExchangeRateAPIKey authenticates the apilayer exchange-rate API. The
	// option list never returns it (its key ends in api_key).
	ExchangeRateAPIKey string `json:"exchange_rate_api_key"`
}

// PaymentCurrencies are the currencies a site may price in. Each counts
// hundredths, the unit every order amount is stored in.
var PaymentCurrencies = []string{
	"USD", "EUR", "GBP", "CNY", "HKD", "TWD", "JPY", "KRW", "SGD", "AUD", "CAD", "CHF", "RUB", "INR", "BRL",
}

const CurrentComplianceTermsVersion = "v1"

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:  []int{10, 20, 50, 100, 200, 500},
	AmountDiscount: map[int]float64{},
	Currency:       "USD",
	MinTopup:       "1",
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}

// PaymentCurrency is the site currency.
func PaymentCurrency() string {
	return strings.ToUpper(strings.TrimSpace(paymentSetting.Currency))
}

// ValidatePaymentOption checks a payment_setting option before it is saved:
// the site currency must be supported and the minimum top-up a positive
// amount with at most two decimal places.
func ValidatePaymentOption(key, value string) error {
	switch key {
	case "payment_setting.currency":
		if !slices.Contains(PaymentCurrencies, strings.ToUpper(strings.TrimSpace(value))) {
			return common.NewMessage("Unsupported site currency")
		}
	case "payment_setting.min_topup":
		amount, err := decimal.NewFromString(strings.TrimSpace(value))
		if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
			return common.NewMessage("The minimum top-up must be a positive amount with at most two decimal places")
		}
	}
	return nil
}
