package kiro

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Usage is the plan of a Kiro channel's account and how much of each metered
// resource its current cycle has used.
type Usage struct {
	// PlanTitle is the plan as Kiro shows it, as KIRO PRO+.
	PlanTitle string `json:"plan_title"`
	// PlanType is Kiro's identifier of the plan, as
	// Q_DEVELOPER_STANDALONE_PRO_PLUS.
	PlanType string `json:"plan_type"`
	// ResetAt is when the cycle's usage resets, in Unix seconds; 0 when Kiro
	// did not say.
	ResetAt int64 `json:"reset_at"`
	// OverageEnabled reports whether usage past a resource's limit is billed
	// at its overage rate instead of refused.
	OverageEnabled bool            `json:"overage_enabled"`
	Resources      []UsageResource `json:"resources"`
}

// UsageResource is one resource Kiro meters, such as credits.
type UsageResource struct {
	// Type is Kiro's identifier of the resource, as CREDIT.
	Type string `json:"type"`
	// Name is Kiro's display name of the resource; Type when it gives none.
	Name string `json:"name"`
	// Used is how much of the resource the cycle used, in its units.
	Used float64 `json:"used"`
	// Limit is how much of the resource the plan includes in a cycle.
	Limit float64 `json:"limit"`
	// OverageUsed is the usage past Limit billed as overage.
	OverageUsed float64 `json:"overage_used"`
	// OverageRate is the price of one unit past Limit, in Currency.
	OverageRate float64 `json:"overage_rate"`
	// Currency is the currency of OverageRate, as USD.
	Currency string `json:"currency"`
	// Bonuses are the extra allowances granted on top of Limit.
	Bonuses []UsageBonus `json:"bonuses"`
}

// UsageBonus is an extra allowance of a resource, such as an upgrade bonus or
// the free trial of a new account.
type UsageBonus struct {
	// Name is the bonus as Kiro names it; empty for the free trial.
	Name string `json:"name"`
	// FreeTrial reports the free trial of a new account rather than a bonus.
	FreeTrial bool `json:"free_trial"`
	// Status is ACTIVE, or EXHAUSTED for one used up before it expires.
	Status string `json:"status" enums:"ACTIVE,EXHAUSTED"`
	// Used is how much of the bonus is spent.
	Used float64 `json:"used"`
	// Limit is how much the bonus grants.
	Limit float64 `json:"limit"`
	// ExpiresAt is when the bonus expires, in Unix seconds; 0 when Kiro did
	// not say.
	ExpiresAt int64 `json:"expires_at"`
}

// usageLimits is the part of a GetUsageLimits answer Usage reports. Where a
// figure comes in whole units and WithPrecision, the precise one is taken.
type usageLimits struct {
	NextDateReset    float64 `json:"nextDateReset"`
	SubscriptionInfo struct {
		Title string `json:"subscriptionTitle"`
		Type  string `json:"type"`
	} `json:"subscriptionInfo"`
	OverageConfiguration struct {
		Status string `json:"overageStatus"`
	} `json:"overageConfiguration"`
	Breakdowns []struct {
		ResourceType                 string  `json:"resourceType"`
		DisplayName                  string  `json:"displayName"`
		DisplayNamePlural            string  `json:"displayNamePlural"`
		Currency                     string  `json:"currency"`
		CurrentUsage                 float64 `json:"currentUsage"`
		CurrentUsageWithPrecision    float64 `json:"currentUsageWithPrecision"`
		UsageLimit                   float64 `json:"usageLimit"`
		UsageLimitWithPrecision      float64 `json:"usageLimitWithPrecision"`
		CurrentOverages              float64 `json:"currentOverages"`
		CurrentOveragesWithPrecision float64 `json:"currentOveragesWithPrecision"`
		OverageRate                  float64 `json:"overageRate"`
		FreeTrialInfo                *struct {
			Status                    string  `json:"freeTrialStatus"`
			Expiry                    float64 `json:"freeTrialExpiry"`
			CurrentUsage              float64 `json:"currentUsage"`
			CurrentUsageWithPrecision float64 `json:"currentUsageWithPrecision"`
			UsageLimit                float64 `json:"usageLimit"`
			UsageLimitWithPrecision   float64 `json:"usageLimitWithPrecision"`
		} `json:"freeTrialInfo"`
		Bonuses []struct {
			BonusCode    string  `json:"bonusCode"`
			DisplayName  string  `json:"displayName"`
			Status       string  `json:"status"`
			ExpiresAt    float64 `json:"expiresAt"`
			CurrentUsage float64 `json:"currentUsage"`
			UsageLimit   float64 `json:"usageLimit"`
		} `json:"bonuses"`
	} `json:"usageBreakdownList"`
}

// ChannelUsage reads the plan of a Kiro channel's account and its usage in
// the current cycle from GetUsageLimits, without the account's user
// information. Refusals return a *common.Message for the web console; other
// errors are failures for the server log.
func ChannelUsage(ctx context.Context, channel *model.Channel) (*Usage, error) {
	var answer usageLimits
	err := withAccount(ctx, channel, func(ctx context.Context, a account) error {
		query := url.Values{"origin": {"AI_EDITOR"}, "isEmailRequired": {"false"}, "profileArn": {a.profile}}
		payload := map[string]any{"origin": "AI_EDITOR", "isEmailRequired": false, "profileArn": a.profile}
		return rpc(ctx, a.client, managementURL(a.runtime)+"?"+query.Encode(), "AmazonCodeWhispererService.GetUsageLimits", a.token, payload, &answer)
	})
	var refused statusError
	switch {
	case err == nil:
	case errors.Is(err, ErrRejected), errors.Is(err, ErrCredential), statusOf(err) == http.StatusUnauthorized:
		return nil, common.NewMessage("Kiro rejected the channel credential")
	case errors.Is(err, errProfileUnresolved):
		return nil, common.NewMessage("The Kiro account has no single profile; set profileArn in the channel credential")
	case errors.Is(err, errUnavailable), errors.Is(err, context.DeadlineExceeded):
		return nil, common.NewMessage("Kiro is unavailable. Please try again later")
	case errors.As(err, &refused) && (refused.denied || refused.status == http.StatusForbidden):
		// Kiro refuses an account whose plan reports no usage
		// (FEATURE_NOT_SUPPORTED) or that is suspended.
		return nil, common.NewMessage("The Kiro account is not allowed to query its usage")
	case errors.As(err, &refused):
		return nil, common.NewMessage("Upstream returned status {{status}}", map[string]any{"status": refused.status})
	default:
		return nil, err
	}

	usage := &Usage{
		PlanTitle:      answer.SubscriptionInfo.Title,
		PlanType:       answer.SubscriptionInfo.Type,
		ResetAt:        int64(answer.NextDateReset),
		OverageEnabled: answer.OverageConfiguration.Status == "ENABLED",
		Resources:      make([]UsageResource, 0, len(answer.Breakdowns)),
	}
	for _, b := range answer.Breakdowns {
		resource := UsageResource{
			Type:        b.ResourceType,
			Name:        cmp.Or(b.DisplayNamePlural, b.DisplayName, b.ResourceType),
			Used:        cmp.Or(b.CurrentUsageWithPrecision, b.CurrentUsage),
			Limit:       cmp.Or(b.UsageLimitWithPrecision, b.UsageLimit),
			OverageUsed: cmp.Or(b.CurrentOveragesWithPrecision, b.CurrentOverages),
			OverageRate: b.OverageRate,
			Currency:    b.Currency,
			Bonuses:     []UsageBonus{},
		}
		// As Kiro's IDE shows them: an active free trial, and the bonuses that
		// are active or used up but not expired.
		if trial := b.FreeTrialInfo; trial != nil && trial.Status == "ACTIVE" {
			resource.Bonuses = append(resource.Bonuses, UsageBonus{
				FreeTrial: true,
				Status:    trial.Status,
				Used:      cmp.Or(trial.CurrentUsageWithPrecision, trial.CurrentUsage),
				Limit:     cmp.Or(trial.UsageLimitWithPrecision, trial.UsageLimit),
				ExpiresAt: int64(trial.Expiry),
			})
		}
		for _, bonus := range b.Bonuses {
			if bonus.Status != "ACTIVE" && bonus.Status != "EXHAUSTED" {
				continue
			}
			resource.Bonuses = append(resource.Bonuses, UsageBonus{
				Name:      cmp.Or(bonus.DisplayName, bonus.BonusCode),
				Status:    bonus.Status,
				Used:      bonus.CurrentUsage,
				Limit:     bonus.UsageLimit,
				ExpiresAt: int64(bonus.ExpiresAt),
			})
		}
		usage.Resources = append(usage.Resources, resource)
	}
	return usage, nil
}
