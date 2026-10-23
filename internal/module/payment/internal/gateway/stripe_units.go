// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/payment/stripe/units.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: ISK, which Stripe represents in hundredths that must
// be whole krónur, is rounded to whole units as well; the functions are
// unexported.

package gateway

import "strings"

// Stripe counts amounts in each currency's smallest unit, which is not always
// a hundredth: zero-decimal currencies (JPY, KRW, ...) count whole units and
// three-decimal ones (BHD, KWD, ...) count thousandths. The rest of new-api
// counts hundredths of every currency, so amounts are converted here, at the
// edge, in both directions.
var (
	stripeZeroDecimal = map[string]bool{
		"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true,
		"KRW": true, "MGA": true, "PYG": true, "RWF": true, "UGX": true, "VND": true,
		"VUV": true, "XAF": true, "XOF": true, "XPF": true,
	}
	stripeThreeDecimal = map[string]bool{
		"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true,
	}
	// stripeWholeHundredths are currencies Stripe counts in hundredths whose
	// decimal part must be zero.
	stripeWholeHundredths = map[string]bool{"ISK": true}
)

// roundStripeHundredths rounds an amount in hundredths of currency to what
// Stripe can collect: whole units (half up) for a zero-decimal currency or
// ISK, unchanged otherwise. Three-decimal currencies need no rounding: a
// hundredth is ten Stripe units, which keeps the last digit zero as Stripe
// requires.
func roundStripeHundredths(hundredths int64, currency string) int64 {
	if c := strings.ToUpper(currency); stripeZeroDecimal[c] || stripeWholeHundredths[c] {
		return (hundredths + 50) / 100 * 100
	}
	return hundredths
}

// toStripeAmount converts hundredths of currency into Stripe's unit. The
// amount must already be rounded with roundStripeHundredths.
func toStripeAmount(hundredths int64, currency string) int64 {
	switch c := strings.ToUpper(currency); {
	case stripeZeroDecimal[c]:
		return hundredths / 100
	case stripeThreeDecimal[c]:
		return hundredths * 10
	}
	return hundredths
}

// fromStripeAmount converts an amount in Stripe's unit into hundredths of
// currency.
func fromStripeAmount(amount int64, currency string) int64 {
	switch c := strings.ToUpper(currency); {
	case stripeZeroDecimal[c]:
		return amount * 100
	case stripeThreeDecimal[c]:
		return amount / 10
	}
	return amount
}
