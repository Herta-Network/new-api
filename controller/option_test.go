package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useInitialOptions loads the options a site starts with from an empty
// database and puts the previous ones back when the test ends.
func useInitialOptions(t *testing.T) {
	t.Helper()
	pgtest.Reset(t, model.DB)
	common.OptionMapRWMutex.RLock()
	previous := common.OptionMap
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	model.InitOptionMap()
}

func TestGetOptionsReportsWhetherEachSecretIsStored(t *testing.T) {
	paymentSetting := operation_setting.GetPaymentSetting()
	previousRateKey := paymentSetting.ExchangeRateAPIKey
	paymentSetting.ExchangeRateAPIKey = ""
	t.Cleanup(func() { paymentSetting.ExchangeRateAPIKey = previousRateKey })
	useInitialOptions(t)
	stored := map[string]string{
		"TurnstileSecretKey":               "turnstile-private-secret",
		"GitHubClientSecret":               "github-private-secret",
		"oidc.client_secret":               "oidc-private-secret",
		"TelegramBotToken":                 "telegram-private-token",
		"WorkerValidKey":                   "worker-private-key",
		"SMTPToken":                        " \t ",
		"TurnstileSiteKey":                 "turnstile-public-site-key",
		"GitHubClientSecret.configured":    "false",
		"PaymentExchangeRateKeyConfigured": "true",
	}
	common.OptionMapRWMutex.Lock()
	for key, value := range stored {
		common.OptionMap[key] = value
	}
	common.OptionMapRWMutex.Unlock()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	GetOptions(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    []model.Option `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	options := make(map[string]string, len(response.Data))
	for _, option := range response.Data {
		_, repeated := options[option.Key]
		assert.False(t, repeated, "%s is listed twice", option.Key)
		options[option.Key] = option.Value
	}

	for key, configured := range map[string]string{
		"TurnstileSecretKey":                    "true",
		"GitHubClientSecret":                    "true",
		"oidc.client_secret":                    "true",
		"TelegramBotToken":                      "true",
		"WorkerValidKey":                        "true",
		"SMTPToken":                             "false",
		"discord.client_secret":                 "false",
		"telegram.client_secret":                "false",
		"LinuxDOClientSecret":                   "false",
		"WeChatServerToken":                     "false",
		"ResendApiKey":                          "false",
		"CloudflareEmailApiToken":               "false",
		"payment_setting.exchange_rate_api_key": "false",
	} {
		assert.Equal(t, configured, options[key+".configured"], key)
		assert.NotContains(t, options, key, "the secret itself is never listed")
	}
	assert.Equal(t, "false", options["PaymentExchangeRateKeyConfigured"], "a stored status is ignored")
	assert.Equal(t, "turnstile-public-site-key", options["TurnstileSiteKey"])
	assert.NotContains(t, options, "TurnstileSiteKey.configured")
	for _, secret := range []string{"turnstile-private-secret", "github-private-secret", "oidc-private-secret", "telegram-private-token", "worker-private-key"} {
		assert.NotContains(t, recorder.Body.String(), secret)
	}
}

func TestSecretStatusOptionsAreReadOnly(t *testing.T) {
	useInitialOptions(t)
	for _, key := range []string{"GitHubClientSecret.configured", "telegram.client_secret.configured", "Unknown.configured", "PaymentExchangeRateKeyConfigured"} {
		t.Run(key, func(t *testing.T) {
			body, err := common.Marshal(OptionUpdateRequest{Key: key, Value: true})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body)))
			UpdateOption(context)

			assert.Contains(t, recorder.Body.String(), `"success":false`)
			assert.Contains(t, recorder.Body.String(), `"message_key":"Secret configuration status is read-only"`)
			var rows int64
			require.NoError(t, model.DB.Model(&model.Option{}).Where(&model.Option{Key: key}).Count(&rows).Error)
			assert.Zero(t, rows)
			common.OptionMapRWMutex.RLock()
			_, inMemory := common.OptionMap[key]
			common.OptionMapRWMutex.RUnlock()
			assert.False(t, inMemory)
		})
	}
}
