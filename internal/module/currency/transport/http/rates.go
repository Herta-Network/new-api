// Package handler serves the currency module's HTTP API: the root's
// exchange rate settings under /api/payment/settings/rates.
package handler

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/currency"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

type paymentRateOverview struct {
	Currency string              `json:"currency"`
	Pairs    []currency.RatePair `json:"pairs"`
}

// GetPaymentRates lists the currency pairs the site converts with, their
// stored live rates and overrides. The currency query previews another
// site currency before it is saved.
//
//	@Summary		获取汇率
//	@Description	列出站点货币需要的货币对及管理员固定的货币对，带已存的实时汇率。
//	@Tags			支付设置
//	@Param			currency	query		string	false	"站点货币代码，默认当前站点货币；可预览尚未保存的站点货币"
//	@Success		200			{object}	common.ApiResponse{data=paymentRateOverview}
//	@Security		RootAuth
//	@Router			/api/payment/settings/rates [get]
func GetPaymentRates(c *gin.Context) {
	site := strings.ToUpper(strings.TrimSpace(c.Query("currency")))
	if site == "" {
		site = operation_setting.PaymentCurrency()
	}
	if !slices.Contains(operation_setting.PaymentCurrencies, site) {
		common.ApiErrorT(c, "Unsupported site currency")
		return
	}
	pairs, err := currency.RateOverview(site)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, paymentRateOverview{Currency: site, Pairs: pairs})
}

// RefreshPaymentRates fetches the live rates of the pairs the site needs
// now, except pairs with an override.
//
//	@Summary		刷新汇率
//	@Description	拉取当前站点货币所需、没有固定汇率的货币对的实时汇率，再返回与获取汇率相同的内容；有货币对刷新失败时返回失败。
//	@Tags			支付设置
//	@Param			currency	query		string	false	"返回哪个站点货币的汇率，默认当前站点货币；不影响刷新哪些货币对"
//	@Success		200			{object}	common.ApiResponse{data=paymentRateOverview}
//	@Security		RootAuth
//	@Router			/api/payment/settings/rates/refresh [post]
func RefreshPaymentRates(c *gin.Context) {
	if err := currency.RefreshRates(c.Request.Context()); err != nil {
		common.SysError(common.LogText("exchange rate refresh failed: %v", err))
		common.ApiErrorT(c, "Some exchange rates could not be refreshed. See the server log for details.")
		return
	}
	GetPaymentRates(c)
}
