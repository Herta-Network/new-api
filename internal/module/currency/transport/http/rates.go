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

// GetPaymentRates lists the currency pairs the site converts with, their
// stored live rates and overrides. The currency query previews another
// site currency before it is saved.
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
	common.ApiSuccess(c, gin.H{"currency": site, "pairs": pairs})
}

// RefreshPaymentRates fetches the live rates of the pairs the site needs
// now, except pairs with an override.
func RefreshPaymentRates(c *gin.Context) {
	if err := currency.RefreshRates(c.Request.Context()); err != nil {
		common.SysError(common.LogText("exchange rate refresh failed: %v", err))
		common.ApiErrorT(c, "Some exchange rates could not be refreshed. See the server log for details.")
		return
	}
	GetPaymentRates(c)
}
