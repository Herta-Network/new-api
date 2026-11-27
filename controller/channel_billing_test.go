package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/module/currency"
	"github.com/QuantumNous/new-api/internal/module/currency/entity"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDeepSeekBalanceUSD(t *testing.T) {
	useExchangeRateOverrides(t, map[string]string{"USD/CNY": "7.3"})
	tests := []struct {
		name            string
		responseJSON    string
		want            float64
		wantErrContains string
	}{
		{
			name:         "prefers USD when USD precedes CNY",
			responseJSON: `{"balance_infos":[{"currency":"USD","total_balance":"12.50"},{"currency":"CNY","total_balance":"73.00"}]}`,
			want:         12.5,
		},
		{
			name:         "prefers USD when CNY precedes USD",
			responseJSON: `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"},{"currency":"USD","total_balance":"12.50"}]}`,
			want:         12.5,
		},
		{
			name:         "converts CNY when USD is absent",
			responseJSON: `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"}]}`,
			want:         10,
		},
		{
			name:         "converts CNY when USD balance is zero",
			responseJSON: `{"balance_infos":[{"currency":"USD","total_balance":"0.00"},{"currency":"CNY","total_balance":"73.00"}]}`,
			want:         10,
		},
		{
			name:         "converts CNY when zero USD follows CNY",
			responseJSON: `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"},{"currency":"USD","total_balance":"0.00"}]}`,
			want:         10,
		},
		{
			name:         "returns zero when USD balance is zero and CNY is absent",
			responseJSON: `{"balance_infos":[{"currency":"USD","total_balance":"0.00"}]}`,
			want:         0,
		},
		{
			name:            "returns error when USD and CNY are absent",
			responseJSON:    `{"balance_infos":[{"currency":"EUR","total_balance":"10.00"}]}`,
			wantErrContains: "currency USD or CNY not found",
		},
		{
			name:            "returns USD parse error instead of falling back to CNY",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"invalid"},{"currency":"CNY","total_balance":"73.00"}]}`,
			wantErrContains: "invalid syntax",
		},
		{
			name:            "rejects NaN USD balance",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"NaN"}]}`,
			wantErrContains: "USD balance must be finite",
		},
		{
			name:            "rejects negative USD balance",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"-1.00"}]}`,
			wantErrContains: "USD balance must be non-negative",
		},
		{
			name:            "rejects positive infinity CNY balance",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"+Inf"}]}`,
			wantErrContains: "CNY balance must be finite",
		},
		{
			name:            "rejects negative CNY balance",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"-7.30"}]}`,
			wantErrContains: "CNY balance must be non-negative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response DeepSeekUsageResponse
			require.NoError(t, common.Unmarshal([]byte(test.responseJSON), &response))

			balance, err := getDeepSeekBalanceUSD(context.Background(), response)
			if test.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, test.want, balance, 1e-12)
		})
	}
}

// Moonshot and DeepSeek balances in CNY convert at the rate the currency
// module resolves: an override first, then a fresh live rate; with neither
// the balance update fails instead of guessing.
func TestCNYBalanceToUSDUsesTheResolvedRate(t *testing.T) {
	pgtest.Reset(t, model.DB)
	t.Cleanup(func() { pgtest.Reset(t, model.DB) })
	useExchangeRateOverrides(t, map[string]string{})
	ctx := context.Background()

	_, err := cnyBalanceToUSD(ctx, 73)
	assert.ErrorIs(t, err, currency.ErrRateUnavailable)

	require.NoError(t, model.DB.Create(&entity.ExchangeRate{
		FromCurrency: "CNY", ToCurrency: "USD", Rate: "0.14", Source: currency.RateSourceLive, FetchedAt: time.Now().Unix(),
	}).Error)
	balance, err := cnyBalanceToUSD(ctx, 73)
	require.NoError(t, err)
	assert.InDelta(t, 10.22, balance, 1e-12)

	// Moonshot reports a negative balance while the account owes money.
	useExchangeRateOverrides(t, map[string]string{"USD/CNY": "7.3"})
	balance, err = cnyBalanceToUSD(ctx, -7.3)
	require.NoError(t, err)
	assert.InDelta(t, -1, balance, 1e-12)

	useExchangeRateOverrides(t, map[string]string{"USD/CNY": "0.000001"})
	_, err = cnyBalanceToUSD(ctx, math.MaxFloat64)
	assert.EqualError(t, err, "converted USD balance must be finite")
}

// useExchangeRateOverrides pins exchange rates for the test without an
// exchange rate API key.
func useExchangeRateOverrides(t *testing.T, overrides map[string]string) {
	t.Helper()
	settings := operation_setting.GetPaymentSetting()
	saved := *settings
	t.Cleanup(func() { *settings = saved })
	settings.ExchangeRateOverrides, settings.ExchangeRateAPIKey = overrides, ""
}

// SiliconFlow's international site reports the account balance in USD at
// /v1/user/info under the channel's base URL; an overdue account's negative
// balance is stored as it is, and a failed query leaves the stored balance.
func TestUpdateChannelSiliconFlowBalance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    float64
		wantErr string
	}{
		{name: "balance in USD", status: http.StatusOK, body: `{"code":20000,"message":"OK","status":true,"data":{"id":"userid","balance":"0.88","chargeBalance":"88.00","totalBalance":"88.88","status":"normal"}}`, want: 88.88},
		{name: "overdue account", status: http.StatusOK, body: `{"code":20000,"message":"OK","status":true,"data":{"balance":"0.00","chargeBalance":"-1.50","totalBalance":"-1.50","status":"normal"}}`, want: -1.5},
		{name: "upstream reports an error", status: http.StatusOK, body: `{"code":50000,"message":"internal error","status":false,"data":{}}`, wantErr: "The upstream reported an error: internal error (code 50000)"},
		{name: "upstream rejects the key", status: http.StatusUnauthorized, body: `"Invalid token"`, wantErr: "Upstream returned status 401"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v1/user/info", r.URL.Path)
				assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			baseURL := server.URL + "/"
			channel := &model.Channel{Type: constant.ChannelTypeSiliconFlow, BaseURL: &baseURL, Key: "sk-upstream", Name: "siliconflow", Balance: 9}
			require.NoError(t, model.DB.Create(channel).Error)

			result, err := updateChannelBalance(channel)
			stored := storedGapsChannel(t, channel.Id)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				assert.InDelta(t, 9.0, stored.Balance, 1e-9)
				assert.Zero(t, stored.BalanceUpdatedTime)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tc.want, result.Balance, 1e-9)
			assert.InDelta(t, tc.want, stored.Balance, 1e-9)
			detail := stored.GetBalanceDetail()
			require.NotNil(t, detail)
			assert.Equal(t, "USD", detail.Currency)
		})
	}
}

// SiliconFlow's China site stopped serving /user/info without a replacement,
// so a channel there, including one left at the default base URL, answers a
// console message saying so, contacts no upstream, and keeps its balance.
func TestUpdateChannelSiliconFlowBalanceRejectsTheChinaSite(t *testing.T) {
	setupSub2APIBalanceTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })
	// The channels send their requests through this proxy, so a query that
	// reached for SiliconFlow would fail here instead of leaving the machine.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream request: %s %s", r.Method, r.Host)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	setting := `{"proxy":"` + proxy.URL + `"}`

	for _, baseURL := range []string{"", "https://api.siliconflow.cn", "https://api-st.siliconflow.cn/"} {
		t.Run("base URL "+baseURL, func(t *testing.T) {
			channel := &model.Channel{Type: constant.ChannelTypeSiliconFlow, Key: "sk-upstream", Name: "siliconflow-cn", Balance: 9, Setting: &setting}
			if baseURL != "" {
				channel.BaseURL = &baseURL
			}
			require.NoError(t, model.DB.Create(channel).Error)

			response, raw := callChannelAPI(t, UpdateChannelBalance, http.MethodGet, "/api/channel/update_balance", "", idParam(channel.Id))

			assert.False(t, response.Success, raw)
			assert.Equal(t, "SiliconFlow no longer offers balance queries for siliconflow.cn accounts. Check the balance in the SiliconFlow console.", response.Message)
			stored := storedGapsChannel(t, channel.Id)
			assert.InDelta(t, 9.0, stored.Balance, 1e-9)
			assert.Zero(t, stored.BalanceUpdatedTime)
			assert.Nil(t, stored.GetBalanceDetail())
		})
	}
}

func TestUpdateChannelNewAPIBalanceUsesTheSmallerRemainingQuota(t *testing.T) {
	for _, tc := range []struct {
		name    string
		usage   string
		want    float64
		wantErr string
	}{
		{name: "user quota below the key quota", usage: `{"code":true,"data":{"total_available":2000000,"unlimited_quota":false,"user_quota":{"total_available":500000}}}`, want: 0.5},
		{name: "key quota below the user quota", usage: `{"code":true,"data":{"total_available":500000,"unlimited_quota":false,"user_quota":{"total_available":2000000}}}`, want: 0.5},
		{name: "unlimited key uses the user quota", usage: `{"code":true,"data":{"total_available":0,"unlimited_quota":true,"user_quota":{"total_available":3000000}}}`, want: 3},
		{name: "key quota alone without user quota", usage: `{"code":true,"data":{"total_available":1500000,"unlimited_quota":false}}`, want: 1.5},
		{name: "unlimited key without user quota", usage: `{"code":true,"data":{"total_available":0,"unlimited_quota":true}}`, wantErr: "Allow querying user quota"},
		{name: "upstream rejects the key", usage: `{"success":false,"message":"invalid token"}`, wantErr: "invalid token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/usage/token/":
					assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(tc.usage))
				case "/api/status":
					assert.Empty(t, r.Header.Get("Authorization"), "the key is not sent to the public status endpoint")
					_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":1000000}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			baseURL := server.URL + "/"
			channel := &model.Channel{Type: constant.ChannelTypeNewAPI, BaseURL: &baseURL, Key: "sk-upstream", Name: "new-api-upstream"}
			require.NoError(t, model.DB.Create(channel).Error)

			result, err := updateChannelBalance(channel)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tc.want, result.Balance, 1e-9)
			assert.InDelta(t, tc.want, channel.Balance, 1e-9)
		})
	}
}

// A channel with no balance its key can read answers a console message
// saying why, contacts no upstream, and leaves the stored balance as it was:
// types without such an endpoint (Mistral, Azure, Zhipu), OpenAI's own API,
// which answers billing reads only to its dashboard, and coding plan channels,
// which report usage.
func TestUpdateChannelBalanceRejectsChannelsWithoutABalanceQuery(t *testing.T) {
	setupSub2APIBalanceTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })
	// The channels send their requests through this proxy, so a query that
	// reached for an upstream would fail here instead of leaving the machine.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream request: %s %s", r.Method, r.Host)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	setting := `{"proxy":"` + proxy.URL + `"}`

	const (
		unsupported = "This channel type does not support balance queries"
		openAI      = "OpenAI answers balance queries only in its web dashboard, not to API keys. Check the balance in the OpenAI dashboard."
		codingPlan  = "This channel is on a coding plan, which reports its usage instead of a balance"
	)
	for _, tc := range []struct {
		name        string
		channelType int
		baseURL     string
		message     string
	}{
		{name: "Mistral", channelType: constant.ChannelTypeMistral, baseURL: "https://api.mistral.ai", message: unsupported},
		{name: "Azure", channelType: constant.ChannelTypeAzure, baseURL: "https://example.openai.azure.com", message: unsupported},
		{name: "Zhipu at its default base", channelType: constant.ChannelTypeZhipu_v4, message: unsupported},
		{name: "Z.ai open platform", channelType: constant.ChannelTypeZhipu_v4, baseURL: "https://api.z.ai", message: unsupported},
		{name: "OpenAI at its default base", channelType: constant.ChannelTypeOpenAI, message: openAI},
		{name: "OpenAI at its API host", channelType: constant.ChannelTypeOpenAI, baseURL: "https://api.openai.com/", message: openAI},
		{name: "Kimi For Coding on Moonshot", channelType: constant.ChannelTypeMoonshot, baseURL: "kimi-coding-plan", message: codingPlan},
		{name: "GLM Coding Plan on Zhipu", channelType: constant.ChannelTypeZhipu_v4, baseURL: "glm-coding-plan-international", message: codingPlan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &model.Channel{Type: tc.channelType, Key: "sk-upstream", Name: "no-balance", Balance: 9, Setting: &setting}
			if tc.baseURL != "" {
				channel.BaseURL = &tc.baseURL
			}
			require.NoError(t, model.DB.Create(channel).Error)

			response, raw := callChannelAPI(t, UpdateChannelBalance, http.MethodGet, "/api/channel/update_balance", "", idParam(channel.Id))

			assert.False(t, response.Success, raw)
			assert.Equal(t, tc.message, response.Message)
			stored := storedGapsChannel(t, channel.Id)
			assert.InDelta(t, 9.0, stored.Balance, 1e-9)
			assert.Zero(t, stored.BalanceUpdatedTime)
			assert.Nil(t, stored.GetBalanceDetail())
		})
	}
}

// Balance queries read each provider's documented answer, and a failed one
// reports what the upstream said, without the key, and keeps the stored
// balance. The channel names the provider's host in its base URL and reaches
// this fake upstream through its proxy.
func TestUpdateChannelBalanceReadsProviderAnswers(t *testing.T) {
	useExchangeRateOverrides(t, map[string]string{"USD/CNY": "7.3"})
	// https://platform.kimi.ai/docs/api/balance (the China site's answer has
	// the same shape, in CNY).
	const moonshotBalance = `{"code":0,"data":{"available_balance":49.58894,"voucher_balance":46.58893,"cash_balance":3.00001},"scode":"0x0","status":true}`
	type answer struct {
		status int
		body   string
	}
	for _, tc := range []struct {
		name        string
		channelType int
		baseURL     string
		answers     map[string]answer
		want        float64
		wantErr     string
	}{
		{
			// The remaining amount of the budget that blocks the key first
			// (https://openrouter.ai/docs/api-reference/api-keys/get-current-key).
			name: "OpenRouter key under a budget", channelType: constant.ChannelTypeOpenRouter, baseURL: "http://openrouter.ai/api",
			answers: map[string]answer{"/api/v1/key": {200, `{"data":{"label":"sk-or-v1-au7...890","limit":100,"limit_remaining":74.5,"limit_reset":"monthly","usage":25.5,"is_free_tier":false,"is_management_key":false,"budgets":[{"scope":"member","limit_usd":50,"spend_usd":32.5,"remaining_usd":17.5,"reset_interval":"weekly"}],"effective_budget":{"scope":"member","remaining_usd":17.5}}}`}},
			want:    17.5,
		},
		{
			name: "OpenRouter key with a credit limit", channelType: constant.ChannelTypeOpenRouter, baseURL: "http://openrouter.ai/api",
			answers: map[string]answer{"/api/v1/key": {200, `{"data":{"label":"sk-or-v1-au7...890","limit":100,"limit_remaining":74.5,"usage":25.5,"effective_budget":null}}`}},
			want:    74.5,
		},
		{
			name: "OpenRouter key without a limit", channelType: constant.ChannelTypeOpenRouter, baseURL: "http://openrouter.ai/api",
			answers: map[string]answer{"/api/v1/key": {200, `{"data":{"label":"sk-or-v1-au7...890","limit":null,"limit_remaining":null,"usage":25.5,"effective_budget":null}}`}},
			wantErr: "This OpenRouter key has no credit limit, so OpenRouter does not report what it can spend. Check the account's credits in the OpenRouter dashboard.",
		},
		{
			name: "Moonshot international site in USD", channelType: constant.ChannelTypeMoonshot, baseURL: "http://api.moonshot.ai",
			answers: map[string]answer{"/v1/users/me/balance": {200, moonshotBalance}},
			want:    49.58894,
		},
		{
			name: "Moonshot China site in CNY", channelType: constant.ChannelTypeMoonshot, baseURL: "http://api.moonshot.cn",
			answers: map[string]answer{"/v1/users/me/balance": {200, strings.Replace(moonshotBalance, "49.58894", "73", 1)}},
			want:    10,
		},
		{
			name: "Moonshot rejects the key", channelType: constant.ChannelTypeMoonshot, baseURL: "http://api.moonshot.ai",
			answers: map[string]answer{"/v1/users/me/balance": {401, `{"error":{"message":"Invalid Authentication: sk-upstream","type":"invalid_authentication_error"}}`}},
			wantErr: "Upstream returned status 401: Invalid Authentication: [REDACTED]",
		},
		{
			// An OpenAI-compatible relay such as new-api (controller/billing.go):
			// total_usage is in hundredths.
			name: "OpenAI-compatible relay", channelType: constant.ChannelTypeOpenAI, baseURL: "http://relay.example.com",
			answers: map[string]answer{
				"/v1/dashboard/billing/subscription": {200, `{"object":"billing_subscription","has_payment_method":true,"soft_limit_usd":30,"hard_limit_usd":30,"system_hard_limit_usd":30,"access_until":0}`},
				"/v1/dashboard/billing/usage":        {200, `{"object":"list","total_usage":1250}`},
			},
			want: 17.5,
		},
		{
			// new-api answers a failed read with 200 and an OpenAI error body,
			// which is no empty balance.
			name: "OpenAI-compatible relay that failed", channelType: constant.ChannelTypeOpenAI, baseURL: "http://relay.example.com",
			answers: map[string]answer{"/v1/dashboard/billing/subscription": {200, `{"error":{"message":"record not found","type":"upstream_error"}}`}},
			wantErr: "The upstream reported an error: record not found",
		},
		{
			name: "OpenAI-compatible relay that answers a web page", channelType: constant.ChannelTypeOpenAI, baseURL: "http://relay.example.com",
			answers: map[string]answer{"/v1/dashboard/billing/subscription": {200, `<!doctype html><title>Parked</title>`}},
			wantErr: "The upstream's balance answer could not be read",
		},
		{
			// github.com/Wei-Shaw/sub2api, backend/internal/server/middleware.
			name: "Sub2API rejects the key", channelType: constant.ChannelTypeSub2API, baseURL: "http://sub2api.example.com",
			answers: map[string]answer{"/v1/usage": {401, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`}},
			wantErr: "Upstream returned status 401: Invalid API key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			host, err := url.Parse(tc.baseURL)
			require.NoError(t, err)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, host.Host, r.Host)
				assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
				reply, ok := tc.answers[r.URL.Path]
				if !ok {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(reply.status)
				_, _ = w.Write([]byte(reply.body))
			}))
			defer upstream.Close()
			setting := `{"proxy":"` + upstream.URL + `"}`
			channel := &model.Channel{Type: tc.channelType, BaseURL: &tc.baseURL, Key: "sk-upstream", Name: "balance", Balance: 9, Setting: &setting}
			require.NoError(t, model.DB.Create(channel).Error)

			result, err := updateChannelBalance(channel)
			stored := storedGapsChannel(t, channel.Id)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				var message *common.Message
				assert.True(t, errors.As(err, &message), "the failure is a console message")
				assert.InDelta(t, 9.0, stored.Balance, 1e-9)
				assert.Zero(t, stored.BalanceUpdatedTime)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tc.want, result.Balance, 1e-9)
			assert.InDelta(t, tc.want, stored.Balance, 1e-9)
		})
	}

	t.Run("an unreachable upstream", func(t *testing.T) {
		setupSub2APIBalanceTest(t)
		closed := httptest.NewServer(http.NotFoundHandler())
		baseURL := closed.URL
		closed.Close()
		channel := &model.Channel{Type: constant.ChannelTypeSub2API, BaseURL: &baseURL, Key: "sk-upstream", Name: "balance"}
		require.NoError(t, model.DB.Create(channel).Error)

		_, err := updateChannelBalance(channel)
		var message *common.Message
		require.True(t, errors.As(err, &message), "err = %v", err)
		assert.Equal(t, "Could not reach the upstream: {{reason}}", message.Key)
		assert.Contains(t, message.Params["reason"], "connection refused")
		assert.NotContains(t, message.Params["reason"], baseURL, "the reason leaves out the request URL")
	})
}

// A Codex usage read the upstream refused carries a console message, while
// data keeps the upstream body; without a refresh token nothing is renewed.
func TestCodexUsageRefusalCarriesAConsoleMessage(t *testing.T) {
	setupSub2APIBalanceTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/backend-api/wham/usage", r.URL.Path)
		assert.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
		assert.Equal(t, "account-1", r.Header.Get("chatgpt-account-id"))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Unauthorized"}`))
	}))
	defer upstream.Close()
	baseURL := upstream.URL
	channel := &model.Channel{Type: constant.ChannelTypeCodex, BaseURL: &baseURL, Key: `{"access_token":"access-token","account_id":"account-1"}`, Name: "codex"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, raw := callChannelAPI(t, GetCodexChannelUsage, http.MethodGet, "/api/channel/codex/usage", "", idParam(channel.Id))

	var response struct {
		Success        bool           `json:"success"`
		Message        string         `json:"message"`
		MessageKey     string         `json:"message_key"`
		MessageParams  map[string]any `json:"message_params"`
		UpstreamStatus int            `json:"upstream_status"`
		Data           map[string]any `json:"data"`
	}
	require.NoError(t, common.UnmarshalJsonStr(raw, &response))
	assert.False(t, response.Success)
	assert.Equal(t, "ChatGPT rejected the channel credential (status {{status}})", response.MessageKey)
	assert.Equal(t, map[string]any{"status": float64(http.StatusUnauthorized)}, response.MessageParams)
	assert.Equal(t, "ChatGPT rejected the channel credential (status 401)", response.Message)
	assert.Equal(t, http.StatusUnauthorized, response.UpstreamStatus)
	assert.Equal(t, map[string]any{"detail": "Unauthorized"}, response.Data)
}

// A failure quotes only the error a JSON answer states, and never the channel
// key in any spelling: raw, query-escaped or path-escaped, in a 200 answer
// that names its failure as in a refusal. A plain-text or HTML answer is
// shown by its status alone: the base URL could name a service whose content
// must not reach the console.
func TestBalanceFailuresQuoteNoKeyAndNoPageContent(t *testing.T) {
	const key = "sk-a/b+c"
	spellings := key + " " + url.QueryEscape(key) + " " + url.PathEscape(key)
	for _, tc := range []struct {
		name        string
		channelType int
		baseURL     string
		status      int
		body        string
		wantErr     string
	}{
		{
			name: "SiliconFlow names its failure in a 200 answer", channelType: constant.ChannelTypeSiliconFlow, baseURL: "http://api.siliconflow.com",
			status: 200, body: `{"code":40001,"message":"Api key ` + spellings + ` is invalid","status":false,"data":{}}`,
			wantErr: "The upstream reported an error: Api key [REDACTED] [REDACTED] [REDACTED] is invalid (code 40001)",
		},
		{
			name: "Moonshot names its failure in a 200 answer", channelType: constant.ChannelTypeMoonshot, baseURL: "http://api.moonshot.ai",
			status: 200, body: `{"code":1,"data":{},"scode":"` + url.QueryEscape(key) + `","status":false}`,
			wantErr: "The upstream reported an error: status false, code 1, scode [REDACTED]",
		},
		{
			name: "a refusal quoting the key", channelType: constant.ChannelTypeSub2API, baseURL: "http://sub2api.example.com",
			status: 401, body: `{"error":{"message":"key ` + url.PathEscape(key) + ` revoked"}}`,
			wantErr: "Upstream returned status 401: key [REDACTED] revoked",
		},
		{
			name: "a plain-text answer", channelType: constant.ChannelTypeSub2API, baseURL: "http://sub2api.example.com",
			status: 500, body: "internal admin page: db password hunter2",
			wantErr: "Upstream returned status 500",
		},
		{
			name: "a JSON string answer", channelType: constant.ChannelTypeSiliconFlow, baseURL: "http://api.siliconflow.com",
			status: 401, body: `"Invalid token"`,
			wantErr: "Upstream returned status 401",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			setting := `{"proxy":"` + upstream.URL + `"}`
			channel := &model.Channel{Type: tc.channelType, BaseURL: &tc.baseURL, Key: key, Name: "balance", Setting: &setting}
			require.NoError(t, model.DB.Create(channel).Error)

			_, err := updateChannelBalance(channel)
			require.EqualError(t, err, tc.wantErr)
			var message *common.Message
			require.True(t, errors.As(err, &message))
			for _, value := range message.Params {
				assert.NotContains(t, fmt.Sprint(value), "b+c")
				assert.NotContains(t, fmt.Sprint(value), "%2F")
			}
		})
	}
}

// An update of all balances disables a channel whose balance is used up, here
// an OpenRouter key whose weekly limit is spent, and leaves a channel whose
// query failed as it was. With automatic enabling on, it queries the channel
// it disabled again and enables it once the limit has reset, as a passing
// channel test would; with automatic enabling off it leaves it disabled. A
// failure is stored with its console message for the console to translate.
func TestUpdateAllChannelsBalanceDisablesAndRestoresForTheBalance(t *testing.T) {
	setupSub2APIBalanceTest(t)
	previousMemoryCache, previousEnable := common.MemoryCacheEnabled, common.AutomaticEnableChannelEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.AutomaticEnableChannelEnabled = previousMemoryCache, previousEnable
	})
	// https://openrouter.ai/docs/api-reference/limits: limit_reset names
	// the period after which limit_remaining is whole again.
	answers := map[string]string{
		"openrouter.ai":     `{"data":{"label":"sk-or-v1-au7...890","limit":100,"limit_remaining":0,"limit_reset":"weekly","usage":100,"effective_budget":null}}`,
		"relay.example.com": `{"error":{"message":"record not found","type":"upstream_error"}}`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answers[r.Host]))
	}))
	defer upstream.Close()
	setting := `{"proxy":"` + upstream.URL + `"}`
	autoBan := 1
	newChannel := func(channelType int, baseURL string) *model.Channel {
		channel := &model.Channel{Type: channelType, BaseURL: &baseURL, Key: "sk-upstream", Name: baseURL, Status: common.ChannelStatusEnabled, Balance: 9, AutoBan: &autoBan, Setting: &setting}
		require.NoError(t, model.DB.Create(channel).Error)
		return channel
	}
	openRouter := newChannel(constant.ChannelTypeOpenRouter, "http://openrouter.ai/api")
	relay := newChannel(constant.ChannelTypeOpenAI, "http://relay.example.com")
	// The relay answered once, so it keeps a detail its failure is stored in.
	_, err := model.UpdateChannelBalanceDetail(relay.Id, func(*model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64) {
		balance := 9.0
		return &model.ChannelBalanceDetail{BalanceResult: jsplugin.BalanceResult{Amount: &balance, Currency: "USD"}}, nil
	})
	require.NoError(t, err)
	run := func() {
		_, err := updateAllChannelsBalance(context.Background(), func(int, int) {})
		require.NoError(t, err)
	}

	common.AutomaticEnableChannelEnabled = true
	run()
	stored := storedGapsChannel(t, openRouter.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Equal(t, insufficientBalanceReason, stored.GetOtherInfo()["status_reason"])
	assert.InDelta(t, 0.0, stored.Balance, 1e-9)
	stored = storedGapsChannel(t, relay.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status, "a failed query disables nothing")
	assert.InDelta(t, 9.0, stored.Balance, 1e-9, "a failed query keeps the balance")
	detail := stored.GetBalanceDetail()
	require.NotNil(t, detail)
	assert.Equal(t, "The upstream reported an error: record not found", detail.Error)
	assert.Equal(t, "The upstream reported an error: {{reason}}", detail.ErrorKey)
	assert.Equal(t, map[string]any{"reason": "record not found"}, detail.ErrorParams)

	// Still spent: the disabled channel stays as it is.
	run()
	assert.Equal(t, common.ChannelStatusAutoDisabled, storedGapsChannel(t, openRouter.Id).Status)

	// The week is over, but automatic enabling is off.
	answers["openrouter.ai"] = `{"data":{"limit":100,"limit_remaining":100,"limit_reset":"weekly","usage":0,"effective_budget":null}}`
	common.AutomaticEnableChannelEnabled = false
	run()
	stored = storedGapsChannel(t, openRouter.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.InDelta(t, 0.0, stored.Balance, 1e-9, "a channel waiting for its balance is queried only with automatic enabling on")

	common.AutomaticEnableChannelEnabled = true
	run()
	stored = storedGapsChannel(t, openRouter.Id)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.InDelta(t, 100.0, stored.Balance, 1e-9)
	assert.Equal(t, common.ChannelStatusEnabled, storedGapsChannel(t, relay.Id).Status)
}
