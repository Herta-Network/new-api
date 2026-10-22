package operation_setting

import (
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/shopspring/decimal"
)

// Quota display modes: amounts in the site currency, or raw quota.
const (
	QuotaDisplayModeCurrency = "currency"
	QuotaDisplayModeTokens   = "tokens"
)

type GeneralSetting struct {
	DocsLink            string `json:"docs_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// QuotaDisplayMode shows quota as an amount in the site currency or as
	// raw quota (QuotaDisplayModeTokens).
	QuotaDisplayMode string `json:"quota_display_mode"`
	// BillingAPIUsesTokenQuota makes the OpenAI-compatible billing endpoints
	// report the calling API key's quota instead of the user's wallet.
	BillingAPIUsesTokenQuota bool `json:"billing_api_uses_token_quota"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:                 "https://docs.newapi.pro",
	PingIntervalEnabled:      false,
	PingIntervalSeconds:      60,
	QuotaDisplayMode:         QuotaDisplayModeCurrency,
	BillingAPIUsesTokenQuota: true,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("general_setting", &generalSetting)
}

func GetGeneralSetting() *GeneralSetting {
	return &generalSetting
}

// IsTokensDisplay reports whether quota is shown as raw quota instead of an
// amount of money.
func IsTokensDisplay() bool {
	return generalSetting.QuotaDisplayMode == QuotaDisplayModeTokens
}

// SiteCurrencyRate is the rate amounts are shown at: one USD in the site
// currency. The payment rate resolver publishes it on every node, so the
// logger and the status API show quota at the rate payments convert with.
type SiteCurrencyRate struct {
	Currency string
	// USDRate is one USD in Currency as a decimal string; empty when the
	// site currency has no usable rate.
	USDRate string
	// Source is "manual" or "apilayer"; empty for USD itself or without a
	// rate.
	Source    string
	FetchedAt int64
}

var siteCurrencyRate atomic.Pointer[SiteCurrencyRate]

// PublishSiteCurrencyRate replaces the rate amounts are shown at.
func PublishSiteCurrencyRate(rate SiteCurrencyRate) {
	siteCurrencyRate.Store(&rate)
}

// GetSiteCurrencyRate returns the published rate of the current site
// currency. A rate published for another currency, before the site currency
// changed, is not used.
func GetSiteCurrencyRate() SiteCurrencyRate {
	site := PaymentCurrency()
	if published := siteCurrencyRate.Load(); published != nil && published.Currency == site {
		return *published
	}
	if site == "USD" {
		return SiteCurrencyRate{Currency: site, USDRate: "1"}
	}
	return SiteCurrencyRate{Currency: site}
}

// QuotaDisplayAmount converts quota into the amount it is shown as: in the
// site currency at its published rate, or in USD while the site currency
// has no usable rate. It returns the amount and its currency.
func QuotaDisplayAmount(quota int64) (decimal.Decimal, string) {
	usd := decimal.NewFromInt(quota)
	if common.QuotaPerUnit > 0 {
		usd = usd.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	published := GetSiteCurrencyRate()
	rate, err := decimal.NewFromString(published.USDRate)
	if err != nil || !rate.IsPositive() {
		return usd, "USD"
	}
	return usd.Mul(rate), published.Currency
}
