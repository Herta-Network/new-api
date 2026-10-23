package controller

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
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

// Moonshot and DeepSeek balances in CNY convert at the rate payments
// resolve: an override first, then a fresh live rate; with neither the
// balance update fails instead of guessing.
func TestCNYBalanceToUSDUsesTheResolvedRate(t *testing.T) {
	pgtest.Reset(t, model.DB)
	t.Cleanup(func() { pgtest.Reset(t, model.DB) })
	useExchangeRateOverrides(t, map[string]string{})
	ctx := context.Background()

	_, err := cnyBalanceToUSD(ctx, 73)
	assert.ErrorIs(t, err, payment.ErrRateUnavailable)

	require.NoError(t, model.DB.Create(&entity.PaymentExchangeRate{
		FromCurrency: "CNY", ToCurrency: "USD", Rate: "0.14", Source: payment.RateSourceLive, FetchedAt: time.Now().Unix(),
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
