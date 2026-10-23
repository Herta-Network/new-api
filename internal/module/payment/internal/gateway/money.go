// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/payment/money.go
//   - internal/module/billing/internal/settle/settle.go (ValidateTradeNo)
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: ConvertAmount takes the exchange rate as a decimal
// string and rejects anything but a plain positive decimal, instead of
// reading a float64; ValidateTradeNo moved here from the settlement package.

package gateway

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Money on the order side is always an int64 count of hundredths of its
// currency. Gateways exchange decimal strings; FormatAmount and ParseAmount
// are the only conversions between the two and ConvertAmount is the only
// currency conversion, so the amount recorded as a payment expectation is
// exactly the amount a gateway is asked to collect.

// FormatAmount renders hundredths as the two-decimal string payment gateways
// exchange: 1990 is "19.90" and 5 is "0.05". It never goes through a float.
func FormatAmount(minor int64) string {
	sign := ""
	magnitude := uint64(minor)
	if minor < 0 {
		sign = "-"
		magnitude = uint64(-(minor + 1)) + 1
	}
	cents := strconv.FormatUint(magnitude%100, 10)
	if len(cents) == 1 {
		cents = "0" + cents
	}
	return sign + strconv.FormatUint(magnitude/100, 10) + "." + cents
}

// ParseAmount converts a non-negative decimal amount with at most two
// decimal places into hundredths. Signs, exponents, surrounding spaces and a
// third decimal place are rejected: callback code must never compare floats.
func ParseAmount(value string) (int64, error) {
	if value == "" || len(value) > 20 || strings.TrimSpace(value) != value {
		return 0, errors.New("invalid money format")
	}
	wholePart, fractionalPart, hasFraction := strings.Cut(value, ".")
	if wholePart == "" || !decimalDigits(wholePart) {
		return 0, errors.New("invalid money format")
	}
	if hasFraction && (fractionalPart == "" || len(fractionalPart) > 2 || !decimalDigits(fractionalPart)) {
		return 0, errors.New("invalid money format")
	}
	whole, err := strconv.ParseInt(wholePart, 10, 64)
	if err != nil || whole > math.MaxInt64/100 {
		return 0, errors.New("money amount out of range")
	}
	fraction := int64(0)
	if hasFraction {
		if len(fractionalPart) == 1 {
			fractionalPart += "0"
		}
		fraction, err = strconv.ParseInt(fractionalPart, 10, 64)
		if err != nil {
			return 0, errors.New("invalid money format")
		}
	}
	minor := whole * 100
	if fraction > math.MaxInt64-minor {
		return 0, errors.New("money amount out of range")
	}
	return minor + fraction, nil
}

// ConvertAmount converts hundredths of one currency into hundredths of
// another at rate, the price of one source unit in target units written as
// a plain decimal ("7.2419"). The exact product is rounded to the nearest
// target hundredth with halves rounded away from zero, so the result never
// differs from the exact conversion by more than half a hundredth and the
// same inputs always produce the same charge.
func ConvertAmount(minor int64, rate string) (int64, error) {
	exact, err := parseRate(rate)
	if err != nil {
		return 0, err
	}
	exact.Mul(exact, new(big.Rat).SetInt64(minor))
	numerator, denominator := exact.Num(), exact.Denom()
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if twice := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1); twice.Cmp(denominator) >= 0 {
		if numerator.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	if !quotient.IsInt64() {
		return 0, errors.New("converted amount out of range")
	}
	return quotient.Int64(), nil
}

// parseRate reads an exchange rate: a positive plain decimal such as "7.2"
// or "0.000139". Fractions, exponents and signs are rejected, so a rate is
// always stored and compared in the one form the rate provider published.
func parseRate(rate string) (*big.Rat, error) {
	whole, fraction, hasFraction := strings.Cut(rate, ".")
	if len(rate) > 64 || whole == "" || !decimalDigits(whole) ||
		(hasFraction && (fraction == "" || !decimalDigits(fraction))) {
		return nil, errors.New("invalid exchange rate")
	}
	exact, ok := new(big.Rat).SetString(rate)
	if !ok || exact.Sign() <= 0 {
		return nil, errors.New("invalid exchange rate")
	}
	return exact, nil
}

func decimalDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// ValidateTradeNo rejects malformed or hostile gateway trade numbers: empty,
// longer than 255 bytes, padded with spaces, invalid UTF-8 or containing
// control characters.
func ValidateTradeNo(tradeNo string) error {
	if !validIdentifier(tradeNo) {
		return errors.New("invalid trade number")
	}
	return nil
}

// validateOrderNo applies the trade number rules to the order number a
// callback names; it is the key the order is looked up by.
func validateOrderNo(orderNo string) error {
	if !validIdentifier(orderNo) {
		return errors.New("invalid order number")
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
