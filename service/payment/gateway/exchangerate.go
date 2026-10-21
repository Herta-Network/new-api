// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/exchangerate/exchange_rate.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the rate is returned as the decimal string the
// provider published instead of a float64; it is checked for plausibility
// (positive, and within a bound of the previous rate); the client uses the
// registry's HTTP client instead of resty and has no cache, since rates are
// stored by the payment service; JSON goes through the common wrappers.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// apilayerAPI is the production apilayer API.
const apilayerAPI = "https://api.apilayer.com"

// ErrImplausibleRate reports a rate the provider answered with that is not
// plausible: zero, negative, or too far from the previous rate.
var ErrImplausibleRate = errors.New("implausible exchange rate")

// ExchangeRateClient asks apilayer's currency_data/convert API for rates.
type ExchangeRateClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// ExchangeRates returns the rate client that authenticates with apiKey.
func (r *Registry) ExchangeRates(apiKey string) *ExchangeRateClient {
	return &ExchangeRateClient{apiKey: apiKey, baseURL: r.exchangeRateBaseURL, httpClient: r.httpClient}
}

// RateQuery asks for the price of one unit of From in units of To.
type RateQuery struct {
	From string
	To   string
	// Previous is the last accepted rate of the pair as a decimal string;
	// empty when there is none.
	Previous string
	// MaxChangeBP bounds how far, in basis points of Previous, the new rate
	// may move from it; zero disables the bound.
	MaxChangeBP int64
}

// Fetch returns the current rate of the pair as a plain decimal string
// ("7.2419"). A rate that is zero or negative, or that moved more than
// MaxChangeBP away from Previous, is rejected with ErrImplausibleRate so
// the caller keeps its previous rate.
func (c *ExchangeRateClient) Fetch(ctx context.Context, q RateQuery) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("the exchange rate API key is not configured")
	}
	from, to := strings.ToUpper(strings.TrimSpace(q.From)), strings.ToUpper(strings.TrimSpace(q.To))
	if len(from) != 3 || len(to) != 3 || from == to {
		return "", fmt.Errorf("invalid currency pair %q/%q", q.From, q.To)
	}
	endpoint := strings.TrimSuffix(c.baseURL, "/") + "/currency_data/convert?" +
		url.Values{"from": {from}, "to": {to}, "amount": {"1"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create exchange rate request: %w", err)
	}
	req.Header.Set("apikey", c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange rate request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read exchange rate response: %w", err)
	}
	if len(body) > maxQueryResponseSize {
		return "", errors.New("exchange rate response is too large")
	}
	var response struct {
		Success bool        `json:"success"`
		Result  json.Number `json:"result"`
		Message string      `json:"message"`
		Error   *struct {
			Code int    `json:"code"`
			Info string `json:"info"`
		} `json:"error"`
	}
	decodeErr := common.Unmarshal(body, &response)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || decodeErr != nil || !response.Success {
		message := response.Message
		if response.Error != nil {
			message = fmt.Sprintf("%d %s", response.Error.Code, response.Error.Info)
		}
		return "", fmt.Errorf("exchange rate API failed: http=%d message=%q", resp.StatusCode, message)
	}
	// The result is a JSON number that may use an exponent; it is read
	// exactly and rendered as the plain decimal it denotes.
	rate, ok := new(big.Rat).SetString(response.Result.String())
	if !ok {
		return "", fmt.Errorf("exchange rate API answered an invalid rate %q", response.Result)
	}
	if rate.Sign() <= 0 {
		return "", fmt.Errorf("%w: %s/%s is %s", ErrImplausibleRate, from, to, response.Result)
	}
	precision, _ := rate.FloatPrec()
	formatted := rate.FloatString(precision)
	if _, err := parseRate(formatted); err != nil {
		return "", fmt.Errorf("exchange rate API answered an unusable rate %q", response.Result)
	}
	if q.Previous != "" && q.MaxChangeBP > 0 {
		previous, err := parseRate(q.Previous)
		if err != nil {
			return "", fmt.Errorf("previous rate: %w", err)
		}
		// |rate - previous| × 10000 > previous × MaxChangeBP
		change := new(big.Rat).Sub(rate, previous)
		change.Abs(change).Mul(change, big.NewRat(10000, 1))
		bound := new(big.Rat).Mul(previous, big.NewRat(q.MaxChangeBP, 1))
		if change.Cmp(bound) > 0 {
			return "", fmt.Errorf("%w: %s/%s moved from %s to %s", ErrImplausibleRate, from, to, q.Previous, formatted)
		}
	}
	return formatted, nil
}
