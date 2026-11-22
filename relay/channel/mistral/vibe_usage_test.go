package mistral

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchVibeUsage(t *testing.T) {
	// The bodies follow the Vibe client's fixtures of the two billing routes.
	const teamSubscription = `{"plan_name":"TEAM","payg_enabled":true,"vibe_payg_enabled":true,"enterprise":false}`
	resetAt := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name               string
		subscriptionStatus int
		subscription       string
		budgetStatus       int
		budget             string
		want               *VibeUsage
		wantMessage        string
		wantError          bool
	}{
		{
			name:               "a Le Chat plan with its allowance",
			subscriptionStatus: http.StatusOK,
			subscription:       teamSubscription,
			budgetStatus:       http.StatusOK,
			budget:             `{"vibe_budget":{"usage_percentage":42.5,"initial_budget":300.0,"currency":"EUR","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`,
			want: &VibeUsage{PlanName: "TEAM", PlanTitle: "Team", AllowanceStatus: VibeAllowanceOK,
				Allowance: &VibeAllowance{UsedPercent: 42.5, Amount: 300, Currency: "EUR", ResetAt: resetAt}},
		},
		{
			name:               "a used-up allowance without pay-as-you-go",
			subscriptionStatus: http.StatusOK,
			subscription:       `{"plan_name":"INDIVIDUAL","vibe_payg_enabled":false}`,
			budgetStatus:       http.StatusOK,
			budget:             `{"vibe_budget":{"usage_percentage":100,"initial_budget":15,"currency":"eur"}}`,
			want: &VibeUsage{PlanName: "INDIVIDUAL", PlanTitle: "Pro", AllowanceStatus: VibeAllowanceOK,
				Allowance: &VibeAllowance{UsedPercent: 100, Amount: 15, Currency: "EUR"}, LimitReached: true},
		},
		{
			name:               "usage past the allowance billed by the subscription's pay-as-you-go",
			subscriptionStatus: http.StatusOK,
			subscription:       teamSubscription,
			budgetStatus:       http.StatusOK,
			budget:             `{"vibe_budget":{"usage_percentage":130,"initial_budget":300,"currency":"EUR","reset_at":"2026-12-01T00:00:00Z"}}`,
			want: &VibeUsage{PlanName: "TEAM", PlanTitle: "Team", AllowanceStatus: VibeAllowanceOK,
				Allowance: &VibeAllowance{UsedPercent: 130, Amount: 300, Currency: "EUR", ResetAt: resetAt, PaygEnabled: true}},
		},
		{
			name:               "an API plan has no Vibe Code allowance",
			subscriptionStatus: http.StatusOK,
			subscription:       `{"plan_name":"SCALE","payg_enabled":true}`,
			budgetStatus:       http.StatusOK,
			budget:             `{"vibe_budget":null}`,
			want:               &VibeUsage{PlanName: "SCALE", PlanTitle: "SCALE", AllowanceStatus: VibeAllowanceNone},
		},
		{
			name:               "a key whose owner may not see the organization's usage",
			subscriptionStatus: http.StatusOK,
			subscription:       teamSubscription,
			budgetStatus:       http.StatusForbidden,
			want:               &VibeUsage{PlanName: "TEAM", PlanTitle: "Team", AllowanceStatus: VibeAllowanceForbidden},
		},
		{
			name:               "a rejected key",
			subscriptionStatus: http.StatusUnauthorized,
			subscription:       `{"detail":"Unauthorized"}`,
			wantMessage:        "Mistral rejected the API key",
		},
		{
			name:               "a key rejected by the budget route",
			subscriptionStatus: http.StatusOK,
			subscription:       teamSubscription,
			budgetStatus:       http.StatusUnauthorized,
			wantMessage:        "Mistral rejected the API key",
		},
		{
			name:               "an upstream error",
			subscriptionStatus: http.StatusBadGateway,
			subscription:       `oops`,
			wantMessage:        "Upstream returned status {{status}}",
		},
		{
			name:               "a subscription without a plan",
			subscriptionStatus: http.StatusOK,
			subscription:       `{"payg_enabled":true}`,
			wantError:          true,
		},
		{
			name:               "a budget that is not JSON",
			subscriptionStatus: http.StatusOK,
			subscription:       teamSubscription,
			budgetStatus:       http.StatusOK,
			budget:             `<html>`,
			wantError:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "Bearer mistral-key", r.Header.Get("Authorization"))
				switch r.URL.Path {
				case "/v1/billing/subscription":
					w.WriteHeader(tt.subscriptionStatus)
					_, _ = w.Write([]byte(tt.subscription))
				case "/v1/billing/budget":
					w.WriteHeader(tt.budgetStatus)
					_, _ = w.Write([]byte(tt.budget))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()

			usage, err := FetchVibeUsage(context.Background(), upstream.Client(), upstream.URL+"/", " mistral-key ")
			switch {
			case tt.wantMessage != "":
				var message *common.Message
				require.True(t, errors.As(err, &message), "%v", err)
				assert.Equal(t, tt.wantMessage, message.Key)
				assert.Nil(t, usage)
			case tt.wantError:
				require.Error(t, err)
				var message *common.Message
				assert.False(t, errors.As(err, &message), "a payload failure is for the server log")
				assert.Nil(t, usage)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.want, usage)
			}
		})
	}
}
