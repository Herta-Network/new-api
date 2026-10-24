package billing_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// ResolveBillingModelName returns the name whose price bills a request for
// origin: origin itself when it carries no @ modifiers, then its canonical
// billing names, then the base model. The first candidate with a configured
// price, ratio, or tiered mode wins; with none, the base model bills.
func ResolveBillingModelName(origin string) string {
	var candidates []string
	if !reasoning.ParseModelModifiers(origin).HasModifiers() {
		candidates = append(candidates, origin)
	}
	candidates = append(candidates, hostreasoning.CanonicalBillingModelNames(origin)...)
	base := hostreasoning.BaseModelName(origin)
	// DeepSeek thinking tails (-none/-low/-max) bill at the base model's price,
	// with or without @ modifiers after them.
	if deepSeekBase, _, _, ok := reasoning.ParseDeepSeekV4ThinkingSuffix(base); ok && !model_setting.ShouldPreserveThinkingSuffix(base) {
		candidates = append(candidates, deepSeekBase)
	}
	candidates = append(candidates, base)

	seen := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if hasPriceOrRatioEntry(name) {
			return name
		}
	}
	return base
}

// hasPriceOrRatioEntry reports whether name has a configured price, ratio, or
// tiered billing-mode entry after a single wildcard normalization. Self-use
// fallback does not count as a configured ratio.
func hasPriceOrRatioEntry(name string) bool {
	formatted := ratio_setting.FormatMatchingModelName(name)
	if _, ok := ratio_setting.GetModelPrice(formatted, false); ok {
		return true
	}
	if ratio_setting.HasConfiguredModelRatio(formatted) {
		return true
	}
	return GetBillingMode(formatted) == BillingModeTieredExpr
}

// ModelBillable reports whether requests for model are billed from a
// configured price: a per-request price, a ratio, or a tiered expression
// under its billing name, or a task plugin's expression for it. Without one,
// a request is refused unless self-use mode or the user's opt-in accepts the
// default ratio, so every model list a user sees applies this test.
func ModelBillable(model string) bool {
	name := ResolveBillingModelName(model)
	if _, ok := ratio_setting.GetModelPrice(name, false); ok {
		return true
	}
	if ratio_setting.HasConfiguredModelRatio(name) {
		return true
	}
	if GetBillingMode(name) == BillingModeTieredExpr {
		expr, ok := GetBillingExpr(name)
		return ok && strings.TrimSpace(expr) != ""
	}
	for key, expr := range billingSetting.PluginBillingExpr {
		if _, pluginModel, ok := SplitPluginBillingExprKey(key); ok && pluginModel == model && strings.TrimSpace(expr) != "" {
			return true
		}
	}
	return false
}
