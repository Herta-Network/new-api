package currency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/internal/module/currency/entity"
	"github.com/QuantumNous/new-api/internal/module/currency/internal/exchangerate"
	"github.com/QuantumNous/new-api/internal/module/currency/internal/store"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRates answers apilayer's convert API with result.
type fakeRates struct {
	mu     sync.Mutex
	calls  int
	result string
}

func (f *fakeRates) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	_, _ = w.Write([]byte(`{"success":true,"result":` + f.result + `}`))
}

func (f *fakeRates) answer(result string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
	return f.calls
}

// setupRates gives the test an empty database, a USD site without
// overrides or a rate API key, an empty rate cache and a fake rate API.
func setupRates(t *testing.T) *fakeRates {
	t.Helper()
	pgtest.Reset(t, model.DB)
	t.Cleanup(func() { pgtest.Reset(t, model.DB) })

	settings := operation_setting.GetPaymentSetting()
	savedSettings := *settings
	t.Cleanup(func() { *settings = savedSettings })
	settings.Currency, settings.ExchangeRateAPIKey = "USD", ""
	settings.ExchangeRateOverrides = map[string]string{}
	rateCache.Lock()
	rateCache.rates = map[string]cachedRate{}
	rateCache.Unlock()

	rates := &fakeRates{result: "7.2419"}
	server := httptest.NewServer(rates)
	t.Cleanup(server.Close)
	savedURL := rateAPIURL
	rateAPIURL = server.URL
	t.Cleanup(func() { rateAPIURL = savedURL })
	return rates
}

func storeRate(t *testing.T, from, to, rate string, fetched time.Time) {
	t.Helper()
	require.NoError(t, store.SaveExchangeRate(&entity.ExchangeRate{FromCurrency: from, ToCurrency: to, Rate: rate, Source: RateSourceLive, FetchedAt: fetched.Unix()}))
}

// A pair whose stored rate is missing or older than 72 hours has no rate
// until the rate API answers; the answer is stored and a fresh rate is not
// fetched again. The daily refresh keeps the stored rate when the API jumps
// too far.
func TestResolveRateFetchesStaleRates(t *testing.T) {
	rates := setupRates(t)
	ctx := context.Background()
	settings := operation_setting.GetPaymentSetting()
	settings.Currency = "CNY"
	storeRate(t, "CNY", "USD", "0.1381", time.Now().Add(-80*time.Hour))

	_, err := ResolveRate(ctx, "CNY", "USD")
	assert.ErrorIs(t, err, ErrRateUnavailable)

	settings.ExchangeRateAPIKey = "rate-key"
	calls := rates.answer("0.14")
	resolved, err := ResolveRate(ctx, "cny", "usd")
	require.NoError(t, err)
	assert.Equal(t, "0.14", resolved.Rate)
	assert.Equal(t, RateSourceLive, resolved.Source)
	stored, err := store.GetExchangeRate("CNY", "USD")
	require.NoError(t, err)
	assert.Equal(t, "0.14", stored.Rate)
	assert.WithinDuration(t, time.Now(), time.Unix(stored.FetchedAt, 0), time.Minute)
	_, err = ResolveRate(ctx, "CNY", "USD")
	require.NoError(t, err)
	assert.Equal(t, calls+1, rates.answer("0.14"), "a fresh rate is not fetched again")

	rates.answer("0.30")
	assert.ErrorIs(t, RefreshRates(ctx), exchangerate.ErrImplausibleRate)
	stored, err = store.GetExchangeRate("CNY", "USD")
	require.NoError(t, err)
	assert.Equal(t, "0.14", stored.Rate)
}

// An override wins over the live rate in both directions of its pair and is
// not fetched; without one a fresh live rate is used, and a stale one
// without an API key leaves the pair without a rate, shown in USD. The site
// currency's resolved rate is the one amounts are shown at.
func TestResolveRatePrecedence(t *testing.T) {
	rates := setupRates(t)
	ctx := context.Background()
	settings := operation_setting.GetPaymentSetting()
	settings.Currency = "CNY"
	storeRate(t, "CNY", "USD", "0.1381", time.Now())

	resolved, err := ResolveRate(ctx, "CNY", "USD")
	require.NoError(t, err)
	assert.Equal(t, "0.1381", resolved.Rate)
	assert.Equal(t, RateSourceLive, operation_setting.GetSiteCurrencyRate().Source)
	amount, shownIn := operation_setting.QuotaDisplayAmount(6_905_000)
	assert.Equal(t, "CNY", shownIn)
	assert.Equal(t, "100.00", amount.StringFixed(2))

	settings.ExchangeRateOverrides = map[string]string{"USD/CNY": "7.3"}
	resolved, err = ResolveRate(ctx, "CNY", "USD")
	require.NoError(t, err)
	assert.Equal(t, RateSourceManual, resolved.Source)
	assert.Equal(t, "10", resolved.Convert(decimal.NewFromInt(73)).String())
	PublishSiteCurrencyRate()
	shown := operation_setting.GetSiteCurrencyRate()
	assert.Equal(t, "7.3", shown.USDRate)
	assert.Equal(t, RateSourceManual, shown.Source)
	pairs, err := RateOverview("CNY")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, RatePair{Base: "USD", Quote: "CNY", Uses: []string{RateUseQuota}, Override: "7.3", Status: RateStatusLive,
		Live: &LiveRate{Rate: "7.2411296162201303", FetchedAt: pairs[0].Live.FetchedAt}}, pairs[0], "the live rate is the one the quota pair converts with")
	settings.ExchangeRateAPIKey = "rate-key"
	calls := rates.answer("0.2")
	require.NoError(t, RefreshRates(ctx))
	assert.Equal(t, calls, rates.answer("0.2"), "a pinned pair is not fetched")

	settings.ExchangeRateOverrides, settings.ExchangeRateAPIKey = map[string]string{}, ""
	storeRate(t, "CNY", "USD", "0.1381", time.Now().Add(-73*time.Hour))
	rateCache.Lock()
	rateCache.rates = map[string]cachedRate{}
	rateCache.Unlock()
	_, err = ResolveRate(ctx, "CNY", "USD")
	assert.ErrorIs(t, err, ErrRateUnavailable)
	pairs, err = RateOverview("CNY")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, RateStatusStale, pairs[0].Status)
	PublishSiteCurrencyRate()
	assert.Empty(t, operation_setting.GetSiteCurrencyRate().USDRate)
	amount, shownIn = operation_setting.QuotaDisplayAmount(500_000)
	assert.Equal(t, "USD", shownIn)
	assert.Equal(t, "1", amount.String())
}

// The pairs another module registers are refreshed and listed with their
// use.
func TestRegisteredPairsAreRefreshed(t *testing.T) {
	rates := setupRates(t)
	savedSources := pairSources
	t.Cleanup(func() { pairSources = savedSources })
	RegisterPairSource(func(site string) ([]Pair, error) {
		return []Pair{{From: site, To: "CNY", Use: RateUseEPay}}, nil
	})
	operation_setting.GetPaymentSetting().ExchangeRateAPIKey = "rate-key"
	calls := rates.answer("7.2419")

	require.NoError(t, RefreshRates(context.Background()))
	assert.Equal(t, calls+1, rates.answer("7.2419"))
	pairs, err := RateOverview("USD")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, RatePair{Base: "USD", Quote: "CNY", Uses: []string{RateUseEPay}, Status: RateStatusLive,
		Live: &LiveRate{Rate: "7.2419", FetchedAt: pairs[0].Live.FetchedAt}}, pairs[0])
}
