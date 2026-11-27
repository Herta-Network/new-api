// Package codingplan reads the plan usage of Kimi For Coding, GLM Coding Plan
// and MiniMax Coding Plan keys. These plans have no channel type of their own:
// a Moonshot, Zhipu, MiniMax or Anthropic channel is one when its base URL
// points at the plan's endpoint.
//
// The usage routes, request headers and payload decoding are adapted from
// AstrLink (https://github.com/Calcium-Ion/AstrLink),
// core/internal/codingplan/usage.go at commit a544b1ffa, licensed under the
// Apache License 2.0. Changed for new-api: channels are recognised by type and
// base URL, windows use the shape of the OpenCode Go usage windows, and
// upstream refusals become web console messages.
package codingplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/opencodego"
)

// Provider names the vendor of a coding plan.
type Provider string

const (
	ProviderKimi    Provider = "kimi"
	ProviderGLM     Provider = "glm"
	ProviderMiniMax Provider = "minimax"
)

// Name is the product name shown in web console messages.
func (provider Provider) Name() string {
	switch provider {
	case ProviderKimi:
		return "Kimi For Coding"
	case ProviderGLM:
		return "GLM Coding Plan"
	case ProviderMiniMax:
		return "MiniMax Coding Plan"
	default:
		return string(provider)
	}
}

const (
	fiveHourSeconds int64 = 5 * 60 * 60
	weeklySeconds   int64 = 7 * 24 * 60 * 60
	// maxUsageResponseBytes bounds the usage response read into memory.
	maxUsageResponseBytes = 64 << 10
)

// Plan is a recognised coding plan channel and the route its usage is read
// from.
type Plan struct {
	Provider Provider
	UsageURL string
}

// Detect recognises a coding plan channel by its type and base URL:
//
//	Kimi For Coding      Moonshot or Anthropic, kimi.com or kimi.ai under /coding
//	GLM Coding Plan      Zhipu or Anthropic, bigmodel.cn or z.ai under /api/coding
//	                     or /api/anthropic
//	MiniMax Coding Plan  MiniMax or Anthropic, any MiniMax host; plan keys use
//	                     the ordinary API endpoints
//
// The plan presets (kimi-coding-plan, glm-coding-plan,
// glm-coding-plan-international) count on the Moonshot and Zhipu types, whose
// adaptors resolve them; the Anthropic adaptor sends a preset name as it is.
// An empty base URL stands for the channel type's default.
func Detect(channelType int, baseURL string) (Plan, bool) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(channelType)
	}
	if special, ok := constant.ChannelSpecialBases[baseURL]; ok {
		if channelType != constant.ChannelTypeMoonshot && channelType != constant.ChannelTypeZhipu_v4 {
			return Plan{}, false
		}
		// Recognise the preset's plan whichever of the two types carries it.
		switch baseURL {
		case "kimi-coding-plan":
			channelType = constant.ChannelTypeMoonshot
		case "glm-coding-plan", "glm-coding-plan-international":
			channelType = constant.ChannelTypeZhipu_v4
		default:
			return Plan{}, false
		}
		baseURL = special.ClaudeBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Plan{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimRight(parsed.Path, "/")
	underPath := func(prefix string) bool {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	onDomain := func(domains ...string) bool {
		return slices.ContainsFunc(domains, func(domain string) bool {
			return host == domain || strings.HasSuffix(host, "."+domain)
		})
	}
	viaAnthropic := channelType == constant.ChannelTypeAnthropic

	switch {
	case (channelType == constant.ChannelTypeMoonshot || viaAnthropic) &&
		onDomain("kimi.com", "kimi.ai") && underPath("/coding"):
		// The usage routes below are all on the providers' own hosts, so they
		// are asked over https whatever scheme the base URL names: the key
		// travels in a header.
		return Plan{Provider: ProviderKimi, UsageURL: "https://" + host + "/coding/v1/usages"}, true
	case (channelType == constant.ChannelTypeZhipu_v4 || viaAnthropic) &&
		onDomain("bigmodel.cn", "z.ai") && (underPath("/api/coding") || underPath("/api/anthropic")):
		// Zhipu's usage plugin asks the API host it is configured with,
		// open.bigmodel.cn, dev.bigmodel.cn or api.z.ai
		// (github.com/zai-org/zai-coding-plugins, glm-plan-usage); another host
		// of either site asks that site's API host.
		apiHost := host
		switch {
		case host == "open.bigmodel.cn" || host == "dev.bigmodel.cn" || host == "api.z.ai":
		case onDomain("bigmodel.cn"):
			apiHost = "open.bigmodel.cn"
		default:
			apiHost = "api.z.ai"
		}
		return Plan{Provider: ProviderGLM, UsageURL: "https://" + apiHost + "/api/monitor/usage/quota/limit"}, true
	case channelType == constant.ChannelTypeMiniMax || viaAnthropic:
		// MiniMax's CLI reads the plan at /v1/token_plan/remains on the base
		// URL's host (github.com/MiniMax-AI/cli, src/client/endpoints.ts). Its
		// regions are api.minimax.io and the China site's api.minimax.cn,
		// which was api.minimaxi.com until 2026-09-28 and still counts as China
		// (src/config/schema.ts, src/errors/api.ts), so api.minimaxi.com is
		// that site under its earlier name rather than a third one. A channel
		// on one of these API hosts asks its own host; the older inference
		// hosts share keys with their region's.
		const route = "/v1/token_plan/remains"
		switch {
		case host == "api.minimax.io" || host == "api.minimax.cn" || host == "api.minimaxi.com":
			return Plan{Provider: ProviderMiniMax, UsageURL: "https://" + host + route}, true
		case onDomain("minimax.io"):
			return Plan{Provider: ProviderMiniMax, UsageURL: "https://api.minimax.io" + route}, true
		case onDomain("minimaxi.com", "minimax.cn", "minimax.chat", "minimaxi.chat"):
			return Plan{Provider: ProviderMiniMax, UsageURL: "https://api.minimax.cn" + route}, true
		}
	}
	return Plan{}, false
}

// Usage is how much of each window a coding plan key has used.
type Usage struct {
	Provider Provider `json:"provider"`
	// Plan is the subscription tier the provider reports, such as GLM's "pro".
	Plan         string                  `json:"plan,omitempty"`
	FiveHour     *opencodego.UsageWindow `json:"five_hour,omitempty"`
	Weekly       *opencodego.UsageWindow `json:"weekly,omitempty"`
	LimitReached bool                    `json:"limit_reached"`
}

// FetchUsage reads how much of each window apiKey has used. Upstream refusals
// and answers without plan usage return a *common.Message for the web console;
// other errors are transport or payload failures for the server log.
func (plan Plan) FetchUsage(ctx context.Context, client *http.Client, apiKey string, now time.Time) (*Usage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.UsageURL, nil)
	if err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if plan.Provider == ProviderGLM {
		// Zhipu's monitor route takes the raw key, not a Bearer token.
		request.Header.Set("Authorization", apiKey)
		request.Header.Set("Accept-Language", "en-US,en")
	} else {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "new-api/"+common.Version)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	provider := map[string]any{"provider": plan.Provider.Name()}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, common.NewMessage("{{provider}} rejected the API key", provider)
	case http.StatusForbidden:
		return nil, common.NewMessage("This API key cannot read {{provider}} usage. Check that it belongs to a Coding Plan", provider)
	case http.StatusTooManyRequests:
		return nil, common.NewMessage("{{provider}} is rate limiting usage queries. Please try again later", provider)
	default:
		return nil, common.NewMessage("Upstream returned status {{status}}", map[string]any{"status": response.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUsageResponseBytes))
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := common.Unmarshal(bytes.TrimSpace(body), &document); err != nil || document == nil {
		return nil, fmt.Errorf("invalid %s usage response", plan.Provider.Name())
	}

	usage := &Usage{Provider: plan.Provider}
	switch plan.Provider {
	case ProviderKimi:
		err = decodeKimi(document, usage, now)
	case ProviderGLM:
		err = decodeGLM(document, usage, now, apiKey)
	case ProviderMiniMax:
		err = decodeMiniMax(document, usage, now, apiKey)
	default:
		err = fmt.Errorf("unknown coding plan provider %q", plan.Provider)
	}
	if err != nil {
		return nil, err
	}
	if usage.FiveHour == nil && usage.Weekly == nil {
		return nil, common.NewMessage("{{provider}} reported no plan usage for this API key", provider)
	}
	usage.LimitReached = (usage.FiveHour != nil && usage.FiveHour.UsedPercent >= 100) ||
		(usage.Weekly != nil && usage.Weekly.UsedPercent >= 100)
	return usage, nil
}

// providerError is a refusal a provider reports inside a 200 response. The
// reason is shown without the key and without text that may carry an email.
func providerError(provider Provider, rejectedKey bool, reason string, apiKey string) *common.Message {
	if rejectedKey {
		return common.NewMessage("{{provider}} rejected the API key", map[string]any{"provider": provider.Name()})
	}
	reason = sanitizeLabel(common.RedactSecret(strings.Join(strings.Fields(reason), " "), apiKey), 120)
	if reason == "" {
		return common.NewMessage("{{provider}} reported an error", map[string]any{"provider": provider.Name()})
	}
	return common.NewMessage("{{provider}} reported an error: {{reason}}", map[string]any{"provider": provider.Name(), "reason": reason})
}

// decodeKimi reads GET /coding/v1/usages as Kimi Code CLI's /usage command
// does (github.com/MoonshotAI/kimi-cli, src/kimi_cli/ui/shell/usage.py):
//
//	{"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
//	            "detail":{"limit","used" or "remaining","resetTime"}}],
//	 "usage":{"limit","used" or "remaining","resetTime"}}
//
// limits[] are the rolling windows, each sized by its window, and usage is
// the weekly allowance. The 5-hour window is the entry whose window lasts 5
// hours, else the first one.
func decodeKimi(document map[string]json.RawMessage, usage *Usage, now time.Time) error {
	window := func(fields map[string]json.RawMessage, seconds int64) *opencodego.UsageWindow {
		limit, ok := flexibleNumber(fields["limit"])
		if !ok || limit <= 0 {
			return nil
		}
		used, ok := flexibleNumber(fields["used"])
		if !ok {
			remaining, ok := flexibleNumber(fields["remaining"])
			if !ok {
				return nil
			}
			used = limit - remaining
		}
		return newWindow(math.Max(used, 0)/limit*100, seconds, flexibleTime(fields["resetTime"]), now)
	}
	if raw, ok := document["limits"]; ok && jsonKind(raw) == '[' {
		var limits []struct {
			Window   map[string]json.RawMessage `json:"window"`
			Duration json.RawMessage            `json:"duration"`
			TimeUnit string                     `json:"timeUnit"`
			Detail   map[string]json.RawMessage `json:"detail"`
		}
		if err := common.Unmarshal(raw, &limits); err != nil {
			return fmt.Errorf("invalid Kimi For Coding limits: %w", err)
		}
		for _, item := range limits {
			duration, unit := item.Window["duration"], ""
			_ = common.Unmarshal(item.Window["timeUnit"], &unit)
			if duration == nil {
				duration, unit = item.Duration, item.TimeUnit
			}
			seconds := fiveHourSeconds
			if amount, ok := flexibleNumber(duration); ok && amount > 0 {
				// The CLI reads a duration without a known unit as seconds.
				switch unit = strings.ToUpper(unit); {
				case strings.Contains(unit, "MINUTE"):
					seconds = int64(amount * 60)
				case strings.Contains(unit, "HOUR"):
					seconds = int64(amount * 3600)
				case strings.Contains(unit, "DAY"):
					seconds = int64(amount * 86400)
				default:
					seconds = int64(amount)
				}
			}
			current := window(item.Detail, seconds)
			if current == nil {
				continue
			}
			if usage.FiveHour == nil || (usage.FiveHour.LimitWindowSeconds != fiveHourSeconds && seconds == fiveHourSeconds) {
				usage.FiveHour = current
			}
		}
	}
	if raw, ok := document["usage"]; ok && jsonKind(raw) == '{' {
		var weekly map[string]json.RawMessage
		if err := common.Unmarshal(raw, &weekly); err != nil {
			return fmt.Errorf("invalid Kimi For Coding usage: %w", err)
		}
		usage.Weekly = window(weekly, weeklySeconds)
	}
	return nil
}

// decodeGLM reads GET /api/monitor/usage/quota/limit:
//
//	{"code":200,"success":true,"data":{"level":"lite","limits":[
//	   {"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":2000,"currentValue":15,
//	    "remaining":1984,"percentage":1,"nextResetTime":<ms>},
//	   {"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":10000,"currentValue":873,
//	    "remaining":9126,"percentage":8,"nextResetTime":<ms>}]}}
//
// percentage is the used share. Credit plans report CREDIT_LIMIT and older
// plans TOKENS_LIMIT; TIME_LIMIT is the monthly tool allowance. unit 3
// (hours) is the 5-hour window and unit 6 (weeks) the weekly one, whose number
// is 1 or 7. Entries without a recognised unit fill the empty slots in reset
// order: no reset first, then the earliest. A key whose account has no plan
// answers success false with code 500.
func decodeGLM(document map[string]json.RawMessage, usage *Usage, now time.Time, apiKey string) error {
	var success bool
	if raw, ok := document["success"]; ok && common.Unmarshal(raw, &success) == nil && !success {
		var code int
		var message string
		_ = common.Unmarshal(document["code"], &code)
		_ = common.Unmarshal(document["msg"], &message)
		// 1000 to 1004 are Zhipu's authentication failures.
		rejected := code == http.StatusUnauthorized || (code >= 1000 && code <= 1004)
		return providerError(ProviderGLM, rejected, message, apiKey)
	}
	raw, ok := document["data"]
	if !ok || jsonKind(raw) != '{' {
		return nil
	}
	var data struct {
		Level  string `json:"level"`
		Limits []struct {
			Type          string          `json:"type"`
			Percentage    json.RawMessage `json:"percentage"`
			NextResetTime json.RawMessage `json:"nextResetTime"`
			Unit          *int64          `json:"unit"`
		} `json:"limits"`
	}
	if err := common.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("invalid GLM Coding Plan usage: %w", err)
	}
	type entry struct {
		percent float64
		reset   *time.Time
	}
	var fiveHour, weekly *entry
	var unclassified []entry
	for _, item := range data.Limits {
		kind := strings.ToUpper(strings.TrimSpace(item.Type))
		if kind != "TOKENS_LIMIT" && kind != "CREDIT_LIMIT" {
			continue
		}
		percent, _ := flexibleNumber(item.Percentage)
		current := entry{percent: percent, reset: flexibleTime(item.NextResetTime)}
		switch {
		case item.Unit != nil && *item.Unit == 3 && fiveHour == nil:
			fiveHour = &current
		case item.Unit != nil && *item.Unit == 6 && weekly == nil:
			weekly = &current
		default:
			unclassified = append(unclassified, current)
		}
	}
	slices.SortStableFunc(unclassified, func(a, b entry) int {
		switch {
		case a.reset == nil && b.reset == nil:
			return 0
		case a.reset == nil:
			return -1
		case b.reset == nil:
			return 1
		default:
			return a.reset.Compare(*b.reset)
		}
	})
	for index := range unclassified {
		switch {
		case fiveHour == nil:
			fiveHour = &unclassified[index]
		case weekly == nil:
			weekly = &unclassified[index]
		}
	}
	usage.Plan = sanitizeLabel(data.Level, 64)
	if fiveHour != nil {
		usage.FiveHour = newWindow(fiveHour.percent, fiveHourSeconds, fiveHour.reset, now)
	}
	if weekly != nil {
		usage.Weekly = newWindow(weekly.percent, weeklySeconds, weekly.reset, now)
	}
	return nil
}

// decodeMiniMax reads GET /v1/token_plan/remains, typed and rendered by
// MiniMax's CLI (github.com/MiniMax-AI/cli, src/types/api.ts and
// src/output/quota-table.ts):
//
//	{"base_resp":{"status_code":0},"model_remains":[{"model_name":"general",
//	  "start_time":<ms>,"end_time":<ms>,"current_interval_status":1,
//	  "current_interval_remaining_percent":80,"current_interval_total_count":0,
//	  "weekly_start_time":<ms>,"weekly_end_time":<ms>,"current_weekly_status":1,
//	  "current_weekly_remaining_percent":55,"current_weekly_total_count":0}]}
//
// Only the "general" (text) entry counts. The statuses are the CLI's enum
// (src/types/api.ts since commit 83dd37f449): 1 a normal, limited window
// read from its remaining percent, 2 an exhausted one whatever its percent
// says, 3 an unlimited one; both windows at 3 with no totals is a model
// outside the plan. A weekly window exists only under one of these statuses,
// as it did when only 1 was read: a plan without a weekly limit reports
// none. The 5-hour window, which older answers sent without a status, is
// read from its percent when the status is absent. The remaining percents
// are authoritative; the counts are ambiguous between used and remaining.
func decodeMiniMax(document map[string]json.RawMessage, usage *Usage, now time.Time, apiKey string) error {
	if raw, ok := document["base_resp"]; ok && jsonKind(raw) == '{' {
		var base struct {
			StatusCode *int64 `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		}
		if common.Unmarshal(raw, &base) == nil && base.StatusCode != nil && *base.StatusCode != 0 {
			// 1004 is a failed login and 2049 an invalid API key.
			rejected := *base.StatusCode == 1004 || *base.StatusCode == 2049
			return providerError(ProviderMiniMax, rejected, fmt.Sprintf("%s (code %d)", base.StatusMsg, *base.StatusCode), apiKey)
		}
	}
	raw, ok := document["model_remains"]
	if !ok || jsonKind(raw) != '[' {
		return nil
	}
	var remains []struct {
		ModelName       string          `json:"model_name"`
		StartTime       json.RawMessage `json:"start_time"`
		EndTime         json.RawMessage `json:"end_time"`
		IntervalStatus  *int64          `json:"current_interval_status"`
		IntervalRemain  json.RawMessage `json:"current_interval_remaining_percent"`
		IntervalTotal   json.RawMessage `json:"current_interval_total_count"`
		WeeklyStartTime json.RawMessage `json:"weekly_start_time"`
		WeeklyEndTime   json.RawMessage `json:"weekly_end_time"`
		WeeklyStatus    *int64          `json:"current_weekly_status"`
		WeeklyRemain    json.RawMessage `json:"current_weekly_remaining_percent"`
		WeeklyTotal     json.RawMessage `json:"current_weekly_total_count"`
	}
	if err := common.Unmarshal(raw, &remains); err != nil {
		return fmt.Errorf("invalid MiniMax Coding Plan usage: %w", err)
	}
	const (
		statusNormal    int64 = 1
		statusExhausted int64 = 2
		statusUnlimited int64 = 3
	)
	window := func(status *int64, remainRaw, startRaw, endRaw json.RawMessage, seconds int64) *opencodego.UsageWindow {
		start, end := flexibleTime(startRaw), flexibleTime(endRaw)
		if start != nil && end != nil && end.After(*start) {
			seconds = int64(end.Sub(*start).Seconds())
		}
		switch {
		case status == nil || *status == statusNormal:
			remain, ok := flexibleNumber(remainRaw)
			if !ok {
				return nil
			}
			return newWindow(100-remain, seconds, end, now)
		case *status == statusExhausted:
			return newWindow(100, seconds, end, now)
		case *status == statusUnlimited:
			return &opencodego.UsageWindow{Status: opencodego.UsageStatusUnlimited, LimitWindowSeconds: seconds}
		default:
			return nil
		}
	}
	for _, item := range remains {
		if !strings.EqualFold(strings.TrimSpace(item.ModelName), "general") {
			continue
		}
		intervalTotal, _ := flexibleNumber(item.IntervalTotal)
		weeklyTotal, _ := flexibleNumber(item.WeeklyTotal)
		outsidePlan := item.IntervalStatus != nil && *item.IntervalStatus == statusUnlimited &&
			item.WeeklyStatus != nil && *item.WeeklyStatus == statusUnlimited &&
			intervalTotal == 0 && weeklyTotal == 0
		if !outsidePlan {
			usage.FiveHour = window(item.IntervalStatus, item.IntervalRemain, item.StartTime, item.EndTime, fiveHourSeconds)
			if item.WeeklyStatus != nil {
				usage.Weekly = window(item.WeeklyStatus, item.WeeklyRemain, item.WeeklyStartTime, item.WeeklyEndTime, weeklySeconds)
			}
		}
		break
	}
	return nil
}

// newWindow rounds the used share to two decimals and keeps the reset only
// while it is ahead of now.
func newWindow(percent float64, seconds int64, reset *time.Time, now time.Time) *opencodego.UsageWindow {
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return nil
	}
	window := &opencodego.UsageWindow{
		UsedPercent:        min(max(math.Round(percent*100)/100, 0), 1000),
		LimitWindowSeconds: seconds,
	}
	if reset != nil && reset.After(now) {
		window.ResetAt = reset.Unix()
		window.ResetAfterSeconds = int64(reset.Sub(now).Seconds())
	}
	return window
}

// jsonKind is the first byte of a JSON value: '{' for an object, '[' for an
// array.
func jsonKind(raw json.RawMessage) byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	return trimmed[0]
}

// flexibleNumber accepts JSON numbers and numeric strings ("100").
func flexibleNumber(raw json.RawMessage) (float64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}
	var number json.Number
	if common.Unmarshal(trimmed, &number) == nil {
		if value, err := number.Float64(); err == nil {
			return value, true
		}
	}
	var text string
	if common.Unmarshal(trimmed, &text) == nil {
		if value, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return value, true
		}
	}
	return 0, false
}

// flexibleTime accepts RFC 3339 strings and unix seconds or milliseconds, as
// numbers or strings. Zero and negative timestamps (MiniMax's "no active
// window") are absent.
func flexibleTime(raw json.RawMessage) *time.Time {
	var text string
	if common.Unmarshal(raw, &text) == nil {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil {
			return &parsed
		}
	}
	value, ok := flexibleNumber(raw)
	if !ok || value <= 0 {
		return nil
	}
	var parsed time.Time
	if value < 1_000_000_000_000 {
		parsed = time.Unix(int64(value), 0)
	} else {
		parsed = time.UnixMilli(int64(value))
	}
	return &parsed
}

// sanitizeLabel trims provider text shown in the web console, dropping text
// that may carry an account email or spans lines.
func sanitizeLabel(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "@") || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	if runes := []rune(value); len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}
