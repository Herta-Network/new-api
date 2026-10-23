package currency

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/module/currency/entity"
	"github.com/QuantumNous/new-api/internal/module/currency/internal/exchangerate"
	"github.com/QuantumNous/new-api/internal/module/currency/internal/store"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/shopspring/decimal"
)

const (
	// rateMaxAge is how long a stored rate may be used. An older rate is
	// fetched again before it prices anything, and an order needing a rate
	// that cannot be fetched is refused.
	rateMaxAge = 72 * time.Hour
	// rateCacheTTL is how long a node keeps a rate it read before reading
	// the database again; the master node refreshes rates there.
	rateCacheTTL = 5 * time.Minute
	// rateMaxChangeBP bounds how far, in basis points, a refreshed rate may
	// move from the stored one; a larger move is treated as a bad answer
	// and the stored rate is kept.
	rateMaxChangeBP = 2000

	// RateSourceLive marks a rate fetched from apilayer, RateSourceManual
	// one an administrator pinned in payment_setting.exchange_rate_overrides.
	RateSourceLive   = "apilayer"
	RateSourceManual = "manual"
)

// Uses of a currency pair in the rate settings.
const (
	RateUseQuota          = "quota"
	RateUseEPay           = "epay"
	RateUseChannelBalance = "channel_balance"
)

// ErrRateUnavailable reports a currency pair without a usable rate.
var ErrRateUnavailable = errors.New("exchange rate unavailable")

type cachedRate struct {
	rate     string
	fetched  time.Time
	cachedAt time.Time
}

var rateCache = struct {
	sync.Mutex
	rates map[string]cachedRate
}{rates: map[string]cachedRate{}}

// ResolvedRate is the rate of a currency pair: the price of one From in To.
type ResolvedRate struct {
	From string
	To   string
	// Rate is a decimal string; "1" for the same currency.
	Rate string
	// Source is RateSourceManual or RateSourceLive; empty for the same
	// currency.
	Source string
	// FetchedAt is when a live rate was fetched.
	FetchedAt int64
	// reverse is the pinned price of one To in From when the override was
	// set for the reverse pair; conversions divide by it to stay exact.
	reverse decimal.Decimal
}

// Convert converts an amount of From into To.
func (r ResolvedRate) Convert(amount decimal.Decimal) decimal.Decimal {
	if r.reverse.IsPositive() {
		return amount.Div(r.reverse)
	}
	return amount.Mul(decimal.RequireFromString(r.Rate))
}

// Inverse is the price of one To in From; empty when Rate is not a
// positive decimal.
func (r ResolvedRate) Inverse() string {
	if r.reverse.IsPositive() {
		return r.reverse.String()
	}
	rate, err := decimal.NewFromString(r.Rate)
	if err != nil || !rate.IsPositive() {
		return ""
	}
	return decimal.NewFromInt(1).Div(rate).String()
}

// ResolveRate is the one place a currency pair gets its rate, for payments,
// displayed amounts and upstream balances alike: an administrator's
// override first, then a live rate at most rateMaxAge old, fetched again
// when the stored one is older or missing. A pair with neither is an error
// matching ErrRateUnavailable.
func ResolveRate(ctx context.Context, from, to string) (ResolvedRate, error) {
	resolved, err := resolveRate(ctx, from, to, true)
	if err != nil {
		return ResolvedRate{}, rateUnavailable(resolved.From, resolved.To, err)
	}
	return resolved, nil
}

// resolveRate resolves the pair; without fetch it only reads stored rates.
// The returned rate names the normalized pair even on error.
func resolveRate(ctx context.Context, from, to string, fetch bool) (ResolvedRate, error) {
	resolved := ResolvedRate{From: strings.ToUpper(from), To: strings.ToUpper(to), Rate: "1"}
	if resolved.From == resolved.To {
		return resolved, nil
	}
	if pinned, reversed, ok := operation_setting.ExchangeRateOverride(resolved.From, resolved.To); ok {
		value, err := decimal.NewFromString(pinned)
		if err != nil || !value.IsPositive() {
			return resolved, fmt.Errorf("invalid override %q", pinned)
		}
		resolved.Source, resolved.Rate = RateSourceManual, value.String()
		if reversed {
			resolved.reverse = value
			resolved.Rate = decimal.NewFromInt(1).Div(value).String()
		}
		publishIfSiteRate(resolved)
		return resolved, nil
	}
	rate, fetched, err := liveRate(ctx, resolved.From, resolved.To, fetch)
	if err != nil {
		return resolved, err
	}
	resolved.Rate, resolved.Source, resolved.FetchedAt = rate, RateSourceLive, fetched.Unix()
	publishIfSiteRate(resolved)
	return resolved, nil
}

// liveRate is the stored rate of the pair while it is at most rateMaxAge
// old, or, with fetch, a freshly fetched one.
func liveRate(ctx context.Context, from, to string, fetch bool) (string, time.Time, error) {
	key := from + "/" + to
	now := time.Now()
	rateCache.Lock()
	cached, ok := rateCache.rates[key]
	rateCache.Unlock()
	if ok && now.Sub(cached.cachedAt) < rateCacheTTL && now.Sub(cached.fetched) < rateMaxAge {
		return cached.rate, cached.fetched, nil
	}
	stored, err := store.GetExchangeRate(from, to)
	if err != nil {
		return "", time.Time{}, err
	}
	if stored != nil && now.Sub(time.Unix(stored.FetchedAt, 0)) < rateMaxAge {
		if value, err := decimal.NewFromString(stored.Rate); err == nil && value.IsPositive() {
			fetched := time.Unix(stored.FetchedAt, 0)
			cacheRate(key, stored.Rate, fetched)
			return stored.Rate, fetched, nil
		}
	}
	if !fetch {
		return "", time.Time{}, errors.New("no fresh stored rate")
	}
	return refreshRate(ctx, from, to, nil)
}

// rateUnavailable is the web console message of a missing rate, which also
// matches ErrRateUnavailable; the cause stays in the server log.
func rateUnavailable(from, to string, cause error) error {
	common.SysError(common.LogText("exchange rate %s/%s unavailable: %v", from, to, cause))
	return unavailableRateError{message: common.NewMessage("The exchange rate from {{from}} to {{to}} is unavailable. Try again later.", map[string]any{"from": from, "to": to})}
}

type unavailableRateError struct{ message *common.Message }

func (e unavailableRateError) Error() string   { return e.message.Error() }
func (e unavailableRateError) Unwrap() []error { return []error{ErrRateUnavailable, e.message} }

func cacheRate(key, rate string, fetched time.Time) {
	rateCache.Lock()
	defer rateCache.Unlock()
	rateCache.rates[key] = cachedRate{rate: rate, fetched: fetched, cachedAt: time.Now()}
}

// refreshRate fetches the pair from the rate API and stores it. A previous
// rate that is still fresh bounds how far the new one may move.
func refreshRate(ctx context.Context, from, to string, previous *entity.ExchangeRate) (string, time.Time, error) {
	apiKey := strings.TrimSpace(operation_setting.GetPaymentSetting().ExchangeRateAPIKey)
	if apiKey == "" {
		return "", time.Time{}, errors.New("the exchange rate API key is not configured")
	}
	query := exchangerate.RateQuery{From: from, To: to}
	if previous != nil && time.Since(time.Unix(previous.FetchedAt, 0)) < rateMaxAge {
		query.Previous, query.MaxChangeBP = previous.Rate, rateMaxChangeBP
	}
	rate, err := exchangerate.NewClient(rateHTTPClient(), rateAPIURL, apiKey).Fetch(ctx, query)
	if err != nil {
		return "", time.Time{}, err
	}
	fetched := time.Now()
	if err := store.SaveExchangeRate(&entity.ExchangeRate{
		FromCurrency: from, ToCurrency: to, Rate: rate, Source: RateSourceLive, FetchedAt: fetched.Unix(),
	}); err != nil {
		return "", time.Time{}, err
	}
	cacheRate(from+"/"+to, rate, fetched)
	return rate, fetched, nil
}

// publishIfSiteRate publishes a resolved site currency to USD rate as the
// rate amounts are shown at.
func publishIfSiteRate(resolved ResolvedRate) {
	if resolved.To != "USD" || resolved.From != operation_setting.PaymentCurrency() {
		return
	}
	operation_setting.PublishSiteCurrencyRate(operation_setting.SiteCurrencyRate{
		Currency: resolved.From, USDRate: resolved.Inverse(), Source: resolved.Source, FetchedAt: resolved.FetchedAt,
	})
}

// PublishSiteCurrencyRate publishes the rate amounts are shown at from the
// override or the stored live rate of the site currency, without fetching.
// Every node runs it every minute; the settings API runs it after a change.
func PublishSiteCurrencyRate() {
	site := operation_setting.PaymentCurrency()
	if site == "USD" {
		operation_setting.PublishSiteCurrencyRate(operation_setting.SiteCurrencyRate{Currency: site, USDRate: "1"})
		return
	}
	if _, err := resolveRate(context.Background(), site, "USD", false); err != nil {
		operation_setting.PublishSiteCurrencyRate(operation_setting.SiteCurrencyRate{Currency: site})
	}
}

// Pair is a currency pair the site converts with: From into To, for Use.
type Pair struct {
	From, To, Use string
}

// pairSources list the pairs other modules convert with; see
// RegisterPairSource.
var pairSources []func(site string) ([]Pair, error)

// RegisterPairSource adds the pairs another module converts with in a site
// of currency site to the pairs the rate task refreshes and the rate
// settings list. A module that converts with these rates calls it from its
// init function, because this module cannot import it.
func RegisterPairSource(source func(site string) ([]Pair, error)) {
	pairSources = append(pairSources, source)
}

// requiredRatePairs are the pairs a site in currency site converts with:
// the site currency to USD for quota and displayed amounts, the pairs other
// modules registered (the site currency to CNY when an enabled EPay method
// collects CNY), and CNY to USD for the balances of Moonshot and DeepSeek
// channels.
func requiredRatePairs(site string) ([]Pair, error) {
	var pairs []Pair
	if site != "USD" {
		pairs = append(pairs, Pair{site, "USD", RateUseQuota})
	}
	for _, source := range pairSources {
		registered, err := source(site)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, registered...)
	}
	for _, channelType := range []int{constant.ChannelTypeMoonshot, constant.ChannelTypeDeepSeek} {
		count, err := model.CountChannelsByType(channelType)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			pairs = append(pairs, Pair{"CNY", "USD", RateUseChannelBalance})
			break
		}
	}
	return pairs, nil
}

// RefreshRates refreshes every pair the site needs that has no override.
// It is the daily rate task's work; a failed pair keeps its stored rate.
func RefreshRates(ctx context.Context) error {
	pairs, err := requiredRatePairs(operation_setting.PaymentCurrency())
	if err != nil {
		return err
	}
	var errs []error
	refreshed := map[string]bool{}
	for _, pair := range pairs {
		key := pair.From + "/" + pair.To
		if _, _, pinned := operation_setting.ExchangeRateOverride(pair.From, pair.To); pinned || refreshed[key] {
			continue
		}
		refreshed[key] = true
		previous, err := store.GetExchangeRate(pair.From, pair.To)
		if err == nil {
			_, _, err = refreshRate(ctx, pair.From, pair.To, previous)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	PublishSiteCurrencyRate()
	return errors.Join(errs...)
}

// Live rate statuses of a pair in the rate settings: a fresh live rate for
// every use, one older than rateMaxAge, or none. An override is reported
// apart, so the settings can show what the pair falls back to without it.
const (
	RateStatusLive    = "live"
	RateStatusStale   = "stale"
	RateStatusMissing = "missing"
)

// RatePair is a currency pair in the rate settings, written as the price of
// one Base in Quote: USD first when the pair has it, else the site currency.
type RatePair struct {
	Base  string `json:"base"`
	Quote string `json:"quote"`
	// Uses are what the site converts with the pair; empty for an override
	// nothing uses any more.
	Uses []string `json:"uses"`
	// Live is the newest stored live rate, nil when none was fetched.
	Live *LiveRate `json:"live"`
	// Override is the pinned rate of one Base in Quote, empty when there is
	// none.
	Override string `json:"override"`
	// Status is the state of the live rate: RateStatusLive, RateStatusStale
	// or RateStatusMissing.
	Status string `json:"status"`
}

// LiveRate is a stored live rate of one Base in Quote.
type LiveRate struct {
	Rate      string `json:"rate"`
	FetchedAt int64  `json:"fetched_at"`
}

// RateOverview lists the pairs a site in currency site converts with, and
// every pair that has an override, with their stored live rates.
func RateOverview(site string) ([]RatePair, error) {
	needed, err := requiredRatePairs(site)
	if err != nil {
		return nil, err
	}
	for pair := range operation_setting.GetPaymentSetting().ExchangeRateOverrides {
		from, to, _ := strings.Cut(pair, "/")
		needed = append(needed, Pair{From: from, To: to})
	}
	var rows []RatePair
	index := map[string]int{}
	for _, need := range needed {
		base, quote := need.From, need.To
		if quote == "USD" || (quote == site && base != "USD") {
			base, quote = quote, base
		}
		key := base + "/" + quote
		position, seen := index[key]
		if !seen {
			position = len(rows)
			index[key] = position
			rows = append(rows, RatePair{Base: base, Quote: quote, Uses: []string{}, Status: RateStatusLive})
		}
		row := &rows[position]
		if need.Use == "" {
			continue
		}
		if !slices.Contains(row.Uses, need.Use) {
			row.Uses = append(row.Uses, need.Use)
		}
		stored, err := store.GetExchangeRate(need.From, need.To)
		if err != nil {
			return nil, err
		}
		switch {
		case stored == nil:
			row.Status = RateStatusMissing
		case time.Since(time.Unix(stored.FetchedAt, 0)) >= rateMaxAge && row.Status != RateStatusMissing:
			row.Status = RateStatusStale
		}
		if row.Live == nil && stored != nil {
			row.Live = &LiveRate{Rate: stored.Rate, FetchedAt: stored.FetchedAt}
			if need.From != base {
				row.Live.Rate = ResolvedRate{Rate: stored.Rate}.Inverse()
			}
		}
	}
	for i := range rows {
		row := &rows[i]
		if pinned, reversed, ok := operation_setting.ExchangeRateOverride(row.Base, row.Quote); ok {
			row.Override = pinned
			if reversed {
				row.Override = ResolvedRate{Rate: pinned}.Inverse()
			}
		}
		if row.Live != nil {
			continue
		}
		live, err := storedLiveRate(row.Base, row.Quote)
		if err != nil {
			return nil, err
		}
		row.Live = live
		if len(row.Uses) > 0 {
			continue
		}
		switch {
		case live == nil:
			row.Status = RateStatusMissing
		case time.Since(time.Unix(live.FetchedAt, 0)) >= rateMaxAge:
			row.Status = RateStatusStale
		}
	}
	return rows, nil
}

// storedLiveRate is the newest stored rate of one base in quote, taken from
// either direction of the pair, for a pair no use has a stored rate for.
func storedLiveRate(base, quote string) (*LiveRate, error) {
	direct, err := store.GetExchangeRate(base, quote)
	if err != nil {
		return nil, err
	}
	reverse, err := store.GetExchangeRate(quote, base)
	if err != nil {
		return nil, err
	}
	if reverse != nil && (direct == nil || reverse.FetchedAt > direct.FetchedAt) {
		return &LiveRate{Rate: ResolvedRate{Rate: reverse.Rate}.Inverse(), FetchedAt: reverse.FetchedAt}, nil
	}
	if direct != nil {
		return &LiveRate{Rate: direct.Rate, FetchedAt: direct.FetchedAt}, nil
	}
	return nil, nil
}
