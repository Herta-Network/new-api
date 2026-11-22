package mistral

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// maxVibeUsageResponseBytes bounds a billing response read into memory.
const maxVibeUsageResponseBytes = 64 << 10

// The allowance statuses of a VibeUsage.
const (
	// VibeAllowanceOK: the plan has a monthly Vibe Code allowance, reported in
	// Allowance.
	VibeAllowanceOK = "ok"
	// VibeAllowanceNone: the plan has no Vibe Code allowance, as an API plan.
	VibeAllowanceNone = "none"
	// VibeAllowanceForbidden: Mistral refused to report the allowance to this
	// key, whose owner lacks the permission to see the organization's usage.
	VibeAllowanceForbidden = "forbidden"
)

// vibePlanTitles are the names Mistral shows for the Le Chat plans, keyed by
// the plan name its billing API reports.
var vibePlanTitles = map[string]string{
	"FREE":       "Free",
	"INDIVIDUAL": "Pro",
	"EDU":        "Student",
	"TEAM":       "Team",
}

// VibeAllowance is how much of its monthly Vibe Code allowance a Mistral plan
// has used.
type VibeAllowance struct {
	// UsedPercent is the share of the allowance used; above 100 when
	// pay-as-you-go covers the usage past it.
	UsedPercent float64 `json:"used_percent"`
	// Amount is the monthly allowance in Currency.
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency,omitempty"`
	// ResetAt is when the allowance renews, in Unix seconds; 0 when Mistral
	// did not say.
	ResetAt int64 `json:"reset_at,omitempty"`
	// PaygEnabled reports whether usage past the allowance is billed instead
	// of refused.
	PaygEnabled bool `json:"payg_enabled"`
}

// VibeUsage is the plan of a Mistral key's organization and its Vibe Code
// allowance.
type VibeUsage struct {
	// PlanName is the plan as Mistral's billing names it, as TEAM.
	PlanName string `json:"plan_name"`
	// PlanTitle is the name Mistral shows for the plan, as Team; PlanName when
	// the plan is not a Le Chat plan.
	PlanTitle string `json:"plan_title"`
	// AllowanceStatus is ok, none or forbidden; Allowance is set only for ok.
	AllowanceStatus string         `json:"allowance_status" enums:"ok,none,forbidden"`
	Allowance       *VibeAllowance `json:"allowance"`
	// LimitReached reports a used-up allowance without pay-as-you-go, which
	// Mistral answers with HTTP 402.
	LimitReached bool `json:"limit_reached"`
}

// FetchVibeUsage reads the plan and the Vibe Code allowance of apiKey's
// organization from the billing routes Mistral Vibe reads: GET
// /v1/billing/subscription and GET /v1/billing/budget. Mistral's API reference
// does not document them; the response shapes follow the Vibe client
// (github.com/mistralai/mistral-vibe, vibe/app_server/_billing.py). Upstream
// refusals return a *common.Message for the web console; other errors are
// transport or payload failures for the server log.
func FetchVibeUsage(ctx context.Context, client *http.Client, baseURL string, apiKey string) (*VibeUsage, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Mistral base URL: %q", baseURL)
	}
	apiKey = strings.TrimSpace(apiKey)

	var subscription struct {
		PlanName        string `json:"plan_name"`
		VibePaygEnabled *bool  `json:"vibe_payg_enabled"`
	}
	status, err := readVibeBilling(ctx, client, base+"/v1/billing/subscription", apiKey, &subscription)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, vibeBillingRefusal(status)
	}
	planName := strings.TrimSpace(subscription.PlanName)
	if planName == "" {
		return nil, errors.New("Mistral subscription response has no plan_name")
	}
	usage := &VibeUsage{PlanName: planName, PlanTitle: planName, AllowanceStatus: VibeAllowanceNone}
	if title, ok := vibePlanTitles[strings.ToUpper(planName)]; ok {
		usage.PlanTitle = title
	}

	var budget struct {
		VibeBudget *struct {
			UsagePercentage float64 `json:"usage_percentage"`
			InitialBudget   float64 `json:"initial_budget"`
			Currency        string  `json:"currency"`
			ResetAt         string  `json:"reset_at"`
			PaygEnabled     *bool   `json:"payg_enabled"`
		} `json:"vibe_budget"`
	}
	status, err = readVibeBilling(ctx, client, base+"/v1/billing/budget", apiKey, &budget)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusForbidden:
		usage.AllowanceStatus = VibeAllowanceForbidden
		return usage, nil
	default:
		return nil, vibeBillingRefusal(status)
	}
	detail := budget.VibeBudget
	if detail == nil || detail.InitialBudget <= 0 {
		return usage, nil
	}

	payg := detail.PaygEnabled
	if payg == nil {
		payg = subscription.VibePaygEnabled
	}
	allowance := &VibeAllowance{
		UsedPercent: max(0, detail.UsagePercentage),
		Amount:      detail.InitialBudget,
		Currency:    strings.ToUpper(strings.TrimSpace(detail.Currency)),
		PaygEnabled: payg != nil && *payg,
	}
	if resetAt, err := time.Parse(time.RFC3339Nano, detail.ResetAt); err == nil {
		allowance.ResetAt = resetAt.Unix()
	}
	usage.AllowanceStatus = VibeAllowanceOK
	usage.Allowance = allowance
	usage.LimitReached = allowance.UsedPercent >= 100 && !allowance.PaygEnabled
	return usage, nil
}

// readVibeBilling sends an authenticated GET to a Mistral billing route and
// decodes a 200 response into target. It returns the response status.
func readVibeBilling(ctx context.Context, client *http.Client, requestURL string, apiKey string, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "new-api/"+common.Version)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxVibeUsageResponseBytes))
	if err != nil {
		return 0, err
	}
	if err := common.Unmarshal(body, target); err != nil {
		return 0, fmt.Errorf("invalid Mistral billing response from %s: %w", request.URL.Path, err)
	}
	return http.StatusOK, nil
}

// vibeBillingRefusal is the console message for a billing route's non-200
// status.
func vibeBillingRefusal(status int) error {
	if status == http.StatusUnauthorized {
		return common.NewMessage("Mistral rejected the API key")
	}
	return common.NewMessage("Upstream returned status {{status}}", map[string]any{"status": status})
}
