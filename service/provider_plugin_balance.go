package service

import (
	"context"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"

	"github.com/shopspring/decimal"
)

// CNYToUSDRate is the price of one CNY in USD, the rate the currency module
// resolves (an administrator's override, then a fresh live rate, which it may
// fetch). The kernel cannot import the module, so the module sets it from
// its init function.
var CNYToUSDRate func(ctx context.Context) (decimal.Decimal, error)

// PluginBalanceUSD is a balance result in USD for the balance column: USD as
// it is, CNY at the site's CNY to USD rate. ok is false for another currency,
// an unlimited result, one without an amount, or a CNY result while that
// rate is unavailable.
func PluginBalanceUSD(result jsplugin.BalanceResult) (balance float64, ok bool) {
	return pluginBalanceUSD(result, pluginBalanceCNYRate([]jsplugin.BalanceResult{result}))
}

// pluginBalanceCNYRate resolves the CNY to USD rate when one of results is a
// CNY amount, zero otherwise or while the rate is unavailable. It may fetch
// the rate, so it runs before any locked write.
func pluginBalanceCNYRate(results []jsplugin.BalanceResult) decimal.Decimal {
	if CNYToUSDRate == nil || !slices.ContainsFunc(results, func(result jsplugin.BalanceResult) bool { return result.Currency == "CNY" }) {
		return decimal.Zero
	}
	rate, err := CNYToUSDRate(context.Background())
	if err != nil {
		common.SysError(common.LogText("plugin balance: CNY to USD rate unavailable: %s", err.Error()))
		return decimal.Zero
	}
	return rate
}

func pluginBalanceUSD(result jsplugin.BalanceResult, cnyRate decimal.Decimal) (float64, bool) {
	if result.Amount == nil || result.Unlimited {
		return 0, false
	}
	switch result.Currency {
	case "USD":
		return *result.Amount, true
	case "CNY":
		if !cnyRate.IsPositive() || math.IsNaN(*result.Amount) || math.IsInf(*result.Amount, 0) {
			return 0, false
		}
		return decimal.NewFromFloat(*result.Amount).Mul(cnyRate).InexactFloat64(), true
	}
	return 0, false
}

// UpdatePluginKeyBalances merges results into the channel's per-key balance
// records and updates the USD balance when the results have one.
func UpdatePluginKeyBalances(channel *model.Channel, balances map[int]model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, error) {
	results := make([]jsplugin.BalanceResult, 0, len(balances))
	for _, result := range balances {
		if result.Error == "" {
			results = append(results, result.BalanceResult)
		}
	}
	cnyRate := pluginBalanceCNYRate(results)
	return model.UpdateChannelBalanceDetail(channel.Id, func(stored *model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64) {
		if stored == nil {
			stored = &model.ChannelBalanceDetail{}
		}
		succeeded, failure := false, ""
		for _, index := range slices.Sorted(maps.Keys(balances)) {
			result := balances[index]
			if result.Error == "" {
				succeeded = true
			} else if failure == "" {
				failure = result.Error
			}
		}
		if !channel.ChannelInfo.IsMultiKey {
			result, ok := balances[0]
			if !ok {
				return nil, nil
			}
			if result.Error != "" {
				stored.Error = result.Error
				return stored, nil
			}
			stored = &result
		} else {
			keyCount := len(channel.GetKeys())
			keys := make(map[string]model.ChannelBalanceDetail, keyCount)
			for index, detail := range stored.Keys {
				if position, err := strconv.Atoi(index); err == nil && position >= 0 && position < keyCount {
					keys[index] = detail
				}
			}
			for index, result := range balances {
				if index < 0 || index >= keyCount {
					continue
				}
				key := strconv.Itoa(index)
				if result.Error != "" {
					detail := keys[key]
					if detail.Windows == nil {
						detail.Windows = []jsplugin.BalanceWindow{}
					}
					detail.Error = result.Error
					keys[key] = detail
				} else {
					keys[key] = result
				}
			}
			stored.Keys = keys
			stored.BalanceResult = sumPluginKeyBalances(keys)
			stored.Error = ""
			if !succeeded {
				stored.Error = failure
			}
		}
		if balance, ok := pluginBalanceUSD(stored.BalanceResult, cnyRate); ok && succeeded {
			return stored, &balance
		}
		return stored, nil
	})
}

// sumPluginKeyBalances is a multi-key channel's own result from its keys': the sum
// of their amounts when they share one currency, unlimited when every key
// with a result is unlimited.
func sumPluginKeyBalances(keys map[string]model.ChannelBalanceDetail) jsplugin.BalanceResult {
	result := jsplugin.BalanceResult{Windows: []jsplugin.BalanceWindow{}}
	var sum float64
	counted, results, unlimited := 0, 0, 0
	for _, key := range keys {
		if key.Amount == nil && !key.Unlimited && len(key.Windows) == 0 {
			continue
		}
		results++
		if key.Unlimited {
			unlimited++
			continue
		}
		if key.Amount == nil {
			continue
		}
		if counted > 0 && key.Currency != result.Currency {
			return jsplugin.BalanceResult{Windows: []jsplugin.BalanceWindow{}}
		}
		result.Currency = key.Currency
		sum += *key.Amount
		counted++
	}
	if results > 0 && unlimited == results {
		result.Unlimited = true
		return result
	}
	if counted > 0 {
		result.Amount = &sum
	}
	return result
}
