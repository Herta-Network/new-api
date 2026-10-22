package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useFrontendOptionMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	truncateTables(t)
	return DB
}

func requireOptionValue(t *testing.T, db *gorm.DB, key string) string {
	t.Helper()
	var option Option
	require.NoError(t, db.Where(&Option{Key: key}).First(&option).Error)
	return option.Value
}

func requireOptionMissing(t *testing.T, db *gorm.DB, key string) {
	t.Helper()
	var option Option
	assert.ErrorIs(t, db.Where(&Option{Key: key}).First(&option).Error, gorm.ErrRecordNotFound)
}

func TestMigrateRetiredFrontendOptionsMigratesValidValuesIdempotently(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	legacy := []Option{
		{Key: retiredThemeOptionKey, Value: "classic"},
		{Key: "ApiInfo", Value: `[{"url":"https://api.example.com","route":"primary","description":"API","color":"blue"}]`},
		{Key: "Announcements", Value: `[{"content":"maintenance","publishDate":"2026-07-20T00:00:00Z","type":"warning"}]`},
		{Key: "FAQ", Value: `[{"title":"Question","content":"Answer"}]`},
		{Key: "UptimeKumaUrl", Value: "https://status.example.com"},
		{Key: "UptimeKumaSlug", Value: "status"},
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, MigrateRetiredFrontendOptions())
	assert.Equal(t, "default", requireOptionValue(t, db, retiredThemeOptionKey))
	assert.JSONEq(t, legacy[1].Value, requireOptionValue(t, db, "console_setting.api_info"))
	assert.Equal(t, legacy[2].Value, requireOptionValue(t, db, "console_setting.announcements"))
	assert.JSONEq(t, `[{"question":"Question","answer":"Answer"}]`, requireOptionValue(t, db, "console_setting.faq"))
	assert.JSONEq(t, `[{
		"id":1,"categoryName":"old","url":"https://status.example.com","slug":"status","description":""
	}]`, requireOptionValue(t, db, "console_setting.uptime_kuma_groups"))
	for _, key := range []string{"ApiInfo", "Announcements", "FAQ", "UptimeKumaUrl", "UptimeKumaSlug"} {
		requireOptionMissing(t, db, key)
	}

	before, err := AllOption()
	require.NoError(t, err)
	require.NoError(t, MigrateRetiredFrontendOptions())
	after, err := AllOption()
	require.NoError(t, err)
	assert.ElementsMatch(t, before, after)
}

func TestLegacyConsoleListMigrationCapsAPIInfoAndFAQ(t *testing.T) {
	apiInfo := make([]map[string]any, 51)
	faq := make([]map[string]any, 51)
	for i := range apiInfo {
		apiInfo[i] = map[string]any{
			"url":         fmt.Sprintf("https://api-%d.example.com", i),
			"route":       fmt.Sprintf("route-%d", i),
			"description": "API",
			"color":       "blue",
		}
		faq[i] = map[string]any{"title": fmt.Sprintf("Question %d", i), "content": "Answer"}
	}
	apiBytes, err := common.Marshal(apiInfo)
	require.NoError(t, err)
	faqBytes, err := common.Marshal(faq)
	require.NoError(t, err)

	migratedAPI, err := transformLegacyAPIInfo(string(apiBytes))
	require.NoError(t, err)
	migratedFAQ, err := transformLegacyFAQ(string(faqBytes))
	require.NoError(t, err)
	var apiResult []map[string]any
	require.NoError(t, common.UnmarshalJsonStr(migratedAPI, &apiResult))
	var faqResult []map[string]any
	require.NoError(t, common.UnmarshalJsonStr(migratedFAQ, &faqResult))
	assert.Len(t, apiResult, 50)
	assert.Len(t, faqResult, 50)
}

func TestMigrateRetiredFrontendOptionsPreservesMalformedValuesAndContinues(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	legacy := []Option{
		{Key: "ApiInfo", Value: `{invalid`},
		{Key: "FAQ", Value: `[{"question":"Question","answer":"Answer"}]`},
		{Key: "UptimeKumaUrl", Value: "https://status.example.com"},
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, MigrateRetiredFrontendOptions())
	assert.Equal(t, `{invalid`, requireOptionValue(t, db, "ApiInfo"))
	requireOptionMissing(t, db, "console_setting.api_info")
	requireOptionMissing(t, db, "FAQ")
	assert.JSONEq(t, legacy[1].Value, requireOptionValue(t, db, "console_setting.faq"))
	assert.Equal(t, "https://status.example.com", requireOptionValue(t, db, "UptimeKumaUrl"))
	requireOptionMissing(t, db, "console_setting.uptime_kuma_groups")
}

func TestMigrateRetiredFrontendOptionsPreservesMixedInvalidFAQ(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	legacyFAQ := `[{"question":"Valid question","answer":"Valid answer"},{"question":"Missing answer"}]`
	require.NoError(t, db.Create(&Option{Key: "FAQ", Value: legacyFAQ}).Error)

	require.NoError(t, MigrateRetiredFrontendOptions())
	assert.Equal(t, legacyFAQ, requireOptionValue(t, db, "FAQ"))
	requireOptionMissing(t, db, "console_setting.faq")
}

func TestMigrateRetiredFrontendOptionsKeepsAuthoritativeTargets(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	options := []Option{
		{Key: "ApiInfo", Value: `{invalid`},
		{Key: "console_setting.api_info", Value: `[{"url":"https://new.example.com"}]`},
		{Key: "UptimeKumaUrl", Value: "https://old.example.com"},
		{Key: "UptimeKumaSlug", Value: "old"},
		{Key: "console_setting.uptime_kuma_groups", Value: `[{"url":"https://new.example.com"}]`},
	}
	require.NoError(t, db.Create(&options).Error)

	require.NoError(t, MigrateRetiredFrontendOptions())
	assert.Equal(t, options[1].Value, requireOptionValue(t, db, "console_setting.api_info"))
	assert.Equal(t, options[4].Value, requireOptionValue(t, db, "console_setting.uptime_kuma_groups"))
	for _, key := range []string{"ApiInfo", "UptimeKumaUrl", "UptimeKumaSlug"} {
		requireOptionMissing(t, db, key)
	}
}

func TestMigrateRetiredFrontendOptionsKeepsEmptyAuthoritativeTargets(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	options := []Option{
		{Key: "ApiInfo", Value: `[{"url":"https://old.example.com"}]`},
		{Key: "console_setting.api_info", Value: ""},
		{Key: "UptimeKumaUrl", Value: "https://old.example.com"},
		{Key: "UptimeKumaSlug", Value: "old"},
		{Key: "console_setting.uptime_kuma_groups", Value: ""},
	}
	require.NoError(t, db.Create(&options).Error)

	require.NoError(t, MigrateRetiredFrontendOptions())
	assert.Empty(t, requireOptionValue(t, db, "console_setting.api_info"))
	assert.Empty(t, requireOptionValue(t, db, "console_setting.uptime_kuma_groups"))
	for _, key := range []string{"ApiInfo", "UptimeKumaUrl", "UptimeKumaSlug"} {
		requireOptionMissing(t, db, key)
	}
}

func TestRetiredThemeOptionIsPersistedButNotPublished(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	previousMap := common.OptionMap
	t.Cleanup(func() { common.OptionMap = previousMap })
	common.OptionMap = map[string]string{}

	require.NoError(t, UpdateOption(retiredThemeOptionKey, "default"))
	assert.Equal(t, "default", requireOptionValue(t, db, retiredThemeOptionKey))
	_, published := common.OptionMap[retiredThemeOptionKey]
	assert.False(t, published)
}

// The retired Currency & Display options map onto the site currency: the
// display mode, the billing API toggle, and, where the typed CNY rate goes on
// being used and no live rate replaces it, a USD/CNY override that never
// starts pricing a payment the missing rate refused. A second run changes
// nothing.
func TestMigrateRetiredCurrencyOptions(t *testing.T) {
	const overridesKey = "payment_setting.exchange_rate_overrides"
	const siteKey = "payment_setting.currency"
	tests := []struct {
		name          string
		options       map[string]string
		storedRate    bool
		moonshot      bool
		epay          bool
		wantMode      string
		wantTokenStat string
		wantOverrides string
	}{
		{
			name: "CNY display on a CNY site without a live rate keeps the typed rate",
			options: map[string]string{
				siteKey: "CNY", retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.1",
				retiredDisplayInCurrencyKey: "true", retiredDisplayTokenStatKey: "false",
			},
			wantTokenStat: "false",
			wantOverrides: `{"USD/CNY":"7.1"}`,
		},
		{
			name:          "CNY display on a CNY site without a stored rate row keeps the old default",
			options:       map[string]string{siteKey: "CNY", retiredQuotaDisplayTypeKey: "CNY"},
			wantOverrides: `{"USD/CNY":"7.3"}`,
		},
		{
			name: "CNY display with an exchange rate API key uses live rates",
			options: map[string]string{
				siteKey: "CNY", retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.1",
				"payment_setting.exchange_rate_api_key": "key",
			},
		},
		{
			name:       "CNY display with a fresh stored rate uses it",
			options:    map[string]string{siteKey: "CNY", retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.1"},
			storedRate: true,
		},
		{
			name: "an existing override of the pair is kept",
			options: map[string]string{
				siteKey: "CNY", retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.1",
				overridesKey: `{"CNY/USD":"0.14"}`,
			},
			wantOverrides: `{"CNY/USD":"0.14"}`,
		},
		{
			name:     "tokens display becomes the tokens mode",
			options:  map[string]string{retiredQuotaDisplayTypeKey: "TOKENS", retiredUSDExchangeRateKey: "7.1"},
			wantMode: "tokens",
		},
		{
			name:     "a disabled currency display without a display type becomes the tokens mode",
			options:  map[string]string{retiredDisplayInCurrencyKey: "false"},
			wantMode: "tokens",
		},
		{
			name: "a custom display currency gives way to the site currency",
			options: map[string]string{
				retiredQuotaDisplayTypeKey: "CUSTOM", retiredCustomCurrencySymbolKey: "€",
				retiredCustomCurrencyRateKey: "0.9", retiredUSDExchangeRateKey: "7.1",
			},
		},
		{
			name:          "USD display keeps the rate a Moonshot channel balance converts with",
			options:       map[string]string{retiredQuotaDisplayTypeKey: "USD", retiredUSDExchangeRateKey: "7.2"},
			moonshot:      true,
			wantOverrides: `{"USD/CNY":"7.2"}`,
		},
		{
			name:    "CNY display on a USD site drops a rate amounts no longer use",
			options: map[string]string{retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.2"},
		},
		{
			name:          "CNY display on a USD site keeps the rate for Moonshot balances",
			options:       map[string]string{retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.2"},
			moonshot:      true,
			wantOverrides: `{"USD/CNY":"7.2"}`,
		},
		{
			name:     "a USD site with EPay keeps EPay refused rather than pricing it at the typed rate",
			options:  map[string]string{retiredQuotaDisplayTypeKey: "CNY", retiredUSDExchangeRateKey: "7.2"},
			moonshot: true,
			epay:     true,
		},
		{
			name:     "a CNY site that displayed USD keeps top-ups refused rather than pricing them at the typed rate",
			options:  map[string]string{siteKey: "CNY", retiredQuotaDisplayTypeKey: "USD", retiredUSDExchangeRateKey: "7.2"},
			moonshot: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useFrontendOptionMigrationDB(t)
			for key, value := range test.options {
				require.NoError(t, db.Create(&Option{Key: key, Value: value}).Error)
			}
			if test.storedRate {
				require.NoError(t, SavePaymentExchangeRate(&PaymentExchangeRate{FromCurrency: "CNY", ToCurrency: "USD", Rate: "0.14", FetchedAt: common.GetTimestamp()}))
			}
			if test.moonshot {
				require.NoError(t, db.Create(&Channel{Name: "moonshot", Type: constant.ChannelTypeMoonshot, Key: "sk"}).Error)
			}
			if test.epay {
				require.NoError(t, db.Create(&PaymentMethod{Name: "Alipay", Platform: "epay", Enabled: true, Token: "epay-token"}).Error)
			}

			require.NoError(t, migrateRetiredCurrencyOptions(db))
			for _, key := range retiredCurrencyOptionKeys {
				requireOptionMissing(t, db, key)
			}
			if test.wantMode == "" {
				requireOptionMissing(t, db, "general_setting.quota_display_mode")
			} else {
				assert.Equal(t, test.wantMode, requireOptionValue(t, db, "general_setting.quota_display_mode"))
			}
			if test.wantTokenStat == "" {
				requireOptionMissing(t, db, "general_setting.billing_api_uses_token_quota")
			} else {
				assert.Equal(t, test.wantTokenStat, requireOptionValue(t, db, "general_setting.billing_api_uses_token_quota"))
			}
			if test.wantOverrides == "" {
				requireOptionMissing(t, db, overridesKey)
			} else {
				assert.JSONEq(t, test.wantOverrides, requireOptionValue(t, db, overridesKey))
			}

			before, err := AllOption()
			require.NoError(t, err)
			require.NoError(t, migrateRetiredCurrencyOptions(db))
			after, err := AllOption()
			require.NoError(t, err)
			assert.ElementsMatch(t, before, after)
		})
	}
}
