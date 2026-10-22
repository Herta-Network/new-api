package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/payment/gateway"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The Currency & Display settings the site currency replaced: a typed CNY
// per USD rate, a display currency of their own (USD, CNY, TOKENS, or
// CUSTOM with its own symbol and rate) and two legacy toggles.
const (
	retiredUSDExchangeRateKey      = "USDExchangeRate"
	retiredDisplayInCurrencyKey    = "DisplayInCurrencyEnabled"
	retiredDisplayTokenStatKey     = "DisplayTokenStatEnabled"
	retiredQuotaDisplayTypeKey     = "general_setting.quota_display_type"
	retiredCustomCurrencySymbolKey = "general_setting.custom_currency_symbol"
	retiredCustomCurrencyRateKey   = "general_setting.custom_currency_exchange_rate"
	// retiredUSDExchangeRateDefault was USDExchangeRate without a stored row.
	retiredUSDExchangeRateDefault = "7.3"
	// liveRateMaxAge matches the age up to which payments use a stored rate.
	liveRateMaxAge = 72 * time.Hour
)

var retiredCurrencyOptionKeys = []string{
	retiredUSDExchangeRateKey, retiredDisplayInCurrencyKey, retiredDisplayTokenStatKey,
	retiredQuotaDisplayTypeKey, retiredCustomCurrencySymbolKey, retiredCustomCurrencyRateKey,
}

// migrateRetiredCurrencyOptions maps the retired Currency & Display options
// onto the site currency and deletes them, in one transaction:
//
//   - Display type TOKENS, or DisplayInCurrencyEnabled=false without a
//     display type, becomes the tokens display mode. USD, CNY and CUSTOM
//     show amounts in the site currency; a custom symbol and rate are
//     dropped.
//   - USDExchangeRate becomes the USD/CNY override where something that
//     goes on using the pair converted with it, and no live rate can replace
//     it (no exchange rate API key and no fresh stored CNY/USD rate):
//     amounts on a CNY site that displayed CNY, so they keep their value;
//     or the CNY balances of Moonshot and DeepSeek channels, unless payments
//     also price with the pair (a CNY site's top-ups, a USD site's EPay
//     charges). Those payments were refused without a live rate, and a rate
//     the operator typed for display must not start pricing them unseen. An
//     existing USD/CNY override is kept.
//   - DisplayTokenStatEnabled becomes general_setting.billing_api_uses_token_quota.
//
// New options are only created, never overwritten. Once the retired rows are
// gone it finds nothing to do.
func migrateRetiredCurrencyOptions(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []Option
		if err := tx.Where(commonKeyCol+" IN ?", retiredCurrencyOptionKeys).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		retired := make(map[string]string, len(rows))
		for _, row := range rows {
			retired[row.Key] = strings.TrimSpace(row.Value)
		}

		displayType, typed := retired[retiredQuotaDisplayTypeKey]
		displayType = strings.ToUpper(displayType)
		if !typed && retired[retiredDisplayInCurrencyKey] == "false" {
			displayType = "TOKENS"
		}
		if displayType == "TOKENS" {
			if err := createOptionIfMissing(tx, "general_setting.quota_display_mode", "tokens"); err != nil {
				return err
			}
		}
		if displayType == "CUSTOM" {
			common.SysLog(common.LogText("the custom display currency %q is retired; amounts are shown in the site currency", retired[retiredCustomCurrencySymbolKey]))
		}
		if value, ok := retired[retiredDisplayTokenStatKey]; ok {
			if err := createOptionIfMissing(tx, "general_setting.billing_api_uses_token_quota", value); err != nil {
				return err
			}
		}

		var siteOption Option
		if err := tx.Where(commonKeyCol+" = ?", "payment_setting.currency").Limit(1).Find(&siteOption).Error; err != nil {
			return err
		}
		site := strings.ToUpper(strings.TrimSpace(siteOption.Value))
		if site == "" {
			site = "USD"
		}
		cnyRateUsed := displayType == "CNY" && site == "CNY"
		if !cnyRateUsed {
			paymentsUsePair := site == "CNY"
			if site == "USD" {
				var epayMethods int64
				err := tx.Model(&PaymentMethod{}).Where("platform = ? AND enabled = ?", gateway.PlatformEPay, true).Count(&epayMethods).Error
				if err != nil {
					return err
				}
				paymentsUsePair = epayMethods > 0
			}
			var balanceChannels int64
			err := tx.Model(&Channel{}).Where("type IN ?", []int{constant.ChannelTypeMoonshot, constant.ChannelTypeDeepSeek}).Count(&balanceChannels).Error
			if err != nil {
				return err
			}
			cnyRateUsed = balanceChannels > 0 && !paymentsUsePair
		}
		if cnyRateUsed {
			rate, ok := retired[retiredUSDExchangeRateKey]
			if !ok {
				rate = retiredUSDExchangeRateDefault
			}
			if err := keepRetiredCNYRate(tx, rate); err != nil {
				return err
			}
		}

		if err := tx.Where(commonKeyCol+" IN ?", retiredCurrencyOptionKeys).Delete(&Option{}).Error; err != nil {
			return fmt.Errorf("delete retired currency options: %w", err)
		}
		common.SysLog(common.LogText("migrated %d retired currency display options", len(rows)))
		return nil
	})
}

// keepRetiredCNYRate stores rate, the retired CNY per USD rate, as the
// USD/CNY override unless a live rate or an override already covers the
// pair.
func keepRetiredCNYRate(tx *gorm.DB, rate string) error {
	value, err := decimal.NewFromString(rate)
	if err != nil || !value.IsPositive() {
		common.SysLog(common.LogText("the retired USDExchangeRate %q is not a positive rate; no USD/CNY override is stored", rate))
		return nil
	}
	var apiKey Option
	err = tx.Where(commonKeyCol+" = ?", "payment_setting.exchange_rate_api_key").Limit(1).Find(&apiKey).Error
	if err != nil {
		return err
	}
	if strings.TrimSpace(apiKey.Value) != "" {
		return nil
	}
	var fresh int64
	err = tx.Model(&PaymentExchangeRate{}).
		Where("((from_currency = ? AND to_currency = ?) OR (from_currency = ? AND to_currency = ?)) AND fetched_at > ?",
			"CNY", "USD", "USD", "CNY", time.Now().Add(-liveRateMaxAge).Unix()).
		Count(&fresh).Error
	if err != nil {
		return err
	}
	if fresh > 0 {
		return nil
	}

	var stored Option
	const overridesKey = "payment_setting.exchange_rate_overrides"
	if err := tx.Where(commonKeyCol+" = ?", overridesKey).Limit(1).Find(&stored).Error; err != nil {
		return err
	}
	overrides := map[string]string{}
	if strings.TrimSpace(stored.Value) != "" {
		if err := common.UnmarshalJsonStr(stored.Value, &overrides); err != nil {
			return fmt.Errorf("read %s: %w", overridesKey, err)
		}
	}
	if _, ok := overrides["USD/CNY"]; ok {
		return nil
	}
	if _, ok := overrides["CNY/USD"]; ok {
		return nil
	}
	overrides["USD/CNY"] = value.Round(12).String()
	encoded, err := common.Marshal(overrides)
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&Option{Key: overridesKey, Value: string(encoded)}).Error
	if err != nil {
		return err
	}
	common.SysLog(common.LogText("kept the retired USDExchangeRate %s as the USD/CNY exchange rate override", overrides["USD/CNY"]))
	return nil
}

// createOptionIfMissing stores an option unless a row already holds it.
func createOptionIfMissing(tx *gorm.DB, key, value string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: key, Value: value}).Error
}
