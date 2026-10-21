package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/payment/gateway"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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
	rateSource      = "apilayer"
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

// Rate returns the price of one unit of from in units of to as a decimal
// string: "1" for the same currency, otherwise the stored rate while it is
// at most rateMaxAge old, or a freshly fetched one.
func Rate(ctx context.Context, from, to string) (string, error) {
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	if from == to {
		return "1", nil
	}
	key := from + "/" + to
	now := time.Now()
	rateCache.Lock()
	cached, ok := rateCache.rates[key]
	rateCache.Unlock()
	if ok && now.Sub(cached.cachedAt) < rateCacheTTL && now.Sub(cached.fetched) < rateMaxAge {
		return cached.rate, nil
	}
	stored, err := model.GetPaymentExchangeRate(from, to)
	if err != nil {
		return "", err
	}
	if stored != nil && now.Sub(time.Unix(stored.FetchedAt, 0)) < rateMaxAge {
		cacheRate(key, stored.Rate, time.Unix(stored.FetchedAt, 0))
		return stored.Rate, nil
	}
	rate, err := refreshRate(ctx, from, to, nil)
	if err != nil {
		return "", rateUnavailable(from, to, err)
	}
	return rate, nil
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
func refreshRate(ctx context.Context, from, to string, previous *model.PaymentExchangeRate) (string, error) {
	apiKey := strings.TrimSpace(operation_setting.GetPaymentSetting().ExchangeRateAPIKey)
	if apiKey == "" {
		return "", errors.New("the exchange rate API key is not configured")
	}
	query := gateway.RateQuery{From: from, To: to}
	if previous != nil && time.Since(time.Unix(previous.FetchedAt, 0)) < rateMaxAge {
		query.Previous, query.MaxChangeBP = previous.Rate, rateMaxChangeBP
	}
	rate, err := gatewayRegistry().ExchangeRates(apiKey).Fetch(ctx, query)
	if err != nil {
		return "", err
	}
	fetched := time.Now()
	if err := model.SavePaymentExchangeRate(&model.PaymentExchangeRate{
		FromCurrency: from, ToCurrency: to, Rate: rate, Source: rateSource, FetchedAt: fetched.Unix(),
	}); err != nil {
		return "", err
	}
	cacheRate(from+"/"+to, rate, fetched)
	return rate, nil
}

// requiredRatePairs are the pairs the site prices with: the site currency
// to USD for quota, and to CNY when an enabled EPay method collects CNY.
func requiredRatePairs() ([][2]string, error) {
	site := operation_setting.PaymentCurrency()
	var pairs [][2]string
	if site != "USD" {
		pairs = append(pairs, [2]string{site, "USD"})
	}
	if site != "CNY" {
		methods, err := model.ListPaymentMethods()
		if err != nil {
			return nil, err
		}
		for _, method := range methods {
			if method.Enabled && method.Platform == gateway.PlatformEPay {
				pairs = append(pairs, [2]string{site, "CNY"})
				break
			}
		}
	}
	return pairs, nil
}

// RefreshRates refreshes every pair the site needs. It is the daily rate
// task's work; a failed pair keeps its stored rate.
func RefreshRates(ctx context.Context) error {
	pairs, err := requiredRatePairs()
	if err != nil {
		return err
	}
	var errs []error
	for _, pair := range pairs {
		previous, err := model.GetPaymentExchangeRate(pair[0], pair[1])
		if err == nil {
			_, err = refreshRate(ctx, pair[0], pair[1], previous)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", pair[0], pair[1], err))
		}
	}
	return errors.Join(errs...)
}
