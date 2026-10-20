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
//	Kimi For Coding      Moonshot or Anthropic, kimi.com or kimi.ai under /coding,
//	                     or the kimi-coding-plan base
//	GLM Coding Plan      Zhipu or Anthropic, bigmodel.cn or z.ai under /api/coding
//	                     or /api/anthropic, or a glm-coding-plan base
//	MiniMax Coding Plan  MiniMax or Anthropic, any MiniMax host; plan keys use
//	                     the ordinary API endpoints
//
// An empty base URL stands for the channel type's default.
func Detect(channelType int, baseURL string) (Plan, bool) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(channelType)
	}
	if special, ok := constant.ChannelSpecialBases[baseURL]; ok {
		baseURL = special.ClaudeBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Plan{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	origin := parsed.Scheme + "://" + parsed.Host
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
		return Plan{Provider: ProviderKimi, UsageURL: origin + "/coding/v1/usages"}, true
	case (channelType == constant.ChannelTypeZhipu_v4 || viaAnthropic) &&
		onDomain("bigmodel.cn", "z.ai") && (underPath("/api/coding") || underPath("/api/anthropic")):
		return Plan{Provider: ProviderGLM, UsageURL: origin + "/api/monitor/usage/quota/limit"}, true
	case channelType == constant.ChannelTypeMiniMax || viaAnthropic:
		// The usage route is published only on these two API hosts; the CN
		// inference hosts share keys with api.minimaxi.com.
		switch {
		case onDomain("minimax.io"):
			return Plan{Provider: ProviderMiniMax, UsageURL: "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"}, true
		case onDomain("minimaxi.com", "minimax.cn", "minimax.chat"):
			return Plan{Provider: ProviderMiniMax, UsageURL: "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"}, true
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
		err = decodeGLM(document, usage, now)
	case ProviderMiniMax:
		err = decodeMiniMax(document, usage, now)
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

// providerError is a refusal a provider reports inside a 200 response.
func providerError(provider Provider, rejectedKey bool, reason string) *common.Message {
	if rejectedKey {
		return common.NewMessage("{{provider}} rejected the API key", map[string]any{"provider": provider.Name()})
	}
	reason = sanitizeLabel(reason, 120)
	if reason == "" {
		return common.NewMessage("{{provider}} reported an error", map[string]any{"provider": provider.Name()})
	}
	return common.NewMessage("{{provider}} reported an error: {{reason}}", map[string]any{"provider": provider.Name(), "reason": reason})
}

// decodeKimi reads GET /coding/v1/usages:
//
//	{"limits":[{"detail":{"limit","remaining","resetTime"}}],
//	 "usage":{"limit","remaining","resetTime"}}
//
// limits[] is the rolling 5-hour window; usage is the weekly allowance.
func decodeKimi(document map[string]json.RawMessage, usage *Usage, now time.Time) error {
	window := func(fields map[string]json.RawMessage, seconds int64) *opencodego.UsageWindow {
		limit, ok := flexibleNumber(fields["limit"])
		if !ok || limit <= 0 {
			return nil
		}
		remaining, ok := flexibleNumber(fields["remaining"])
		if !ok {
			return nil
		}
		return newWindow(math.Max(limit-remaining, 0)/limit*100, seconds, flexibleTime(fields["resetTime"]), now)
	}
	if raw, ok := document["limits"]; ok && jsonKind(raw) == '[' {
		var limits []struct {
			Detail map[string]json.RawMessage `json:"detail"`
		}
		if err := common.Unmarshal(raw, &limits); err != nil {
			return fmt.Errorf("invalid Kimi For Coding limits: %w", err)
		}
		for _, item := range limits {
			if usage.FiveHour = window(item.Detail, fiveHourSeconds); usage.FiveHour != nil {
				break
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
//	{"success":true,"data":{"level":"pro","limits":[
//	   {"type":"TOKENS_LIMIT","percentage":12,"nextResetTime":<ms>,"unit":3,"number":5},
//	   {"type":"TOKENS_LIMIT","percentage":40,"nextResetTime":<ms>,"unit":6,"number":7}]}}
//
// unit 3 is the 5-hour window and unit 6 the weekly one. Entries without a
// recognised unit fill the empty slots in reset order: no reset first, then
// the earliest.
func decodeGLM(document map[string]json.RawMessage, usage *Usage, now time.Time) error {
	var success bool
	if raw, ok := document["success"]; ok && common.Unmarshal(raw, &success) == nil && !success {
		var code int
		var message string
		_ = common.Unmarshal(document["code"], &code)
		_ = common.Unmarshal(document["msg"], &message)
		// 1000 to 1004 are Zhipu's authentication failures.
		rejected := code == http.StatusUnauthorized || (code >= 1000 && code <= 1004)
		return providerError(ProviderGLM, rejected, message)
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

// decodeMiniMax reads GET /v1/api/openplatform/coding_plan/remains:
//
//	{"base_resp":{"status_code":0},"model_remains":[{"model_name":"general",
//	  "current_interval_remaining_percent":80,"end_time":<ms>,
//	  "current_weekly_status":1,"current_weekly_remaining_percent":55,"weekly_end_time":<ms>}]}
//
// Only the "general" (coding) entry counts; the weekly window exists only
// while current_weekly_status is 1.
func decodeMiniMax(document map[string]json.RawMessage, usage *Usage, now time.Time) error {
	if raw, ok := document["base_resp"]; ok && jsonKind(raw) == '{' {
		var base struct {
			StatusCode *int64 `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		}
		if common.Unmarshal(raw, &base) == nil && base.StatusCode != nil && *base.StatusCode != 0 {
			// 1004 is a failed login and 2049 an invalid API key.
			rejected := *base.StatusCode == 1004 || *base.StatusCode == 2049
			return providerError(ProviderMiniMax, rejected, fmt.Sprintf("%s (code %d)", base.StatusMsg, *base.StatusCode))
		}
	}
	raw, ok := document["model_remains"]
	if !ok || jsonKind(raw) != '[' {
		return nil
	}
	var remains []struct {
		ModelName      string          `json:"model_name"`
		IntervalRemain json.RawMessage `json:"current_interval_remaining_percent"`
		EndTime        json.RawMessage `json:"end_time"`
		WeeklyStatus   *int64          `json:"current_weekly_status"`
		WeeklyRemain   json.RawMessage `json:"current_weekly_remaining_percent"`
		WeeklyEndTime  json.RawMessage `json:"weekly_end_time"`
	}
	if err := common.Unmarshal(raw, &remains); err != nil {
		return fmt.Errorf("invalid MiniMax Coding Plan usage: %w", err)
	}
	for _, item := range remains {
		if !strings.EqualFold(strings.TrimSpace(item.ModelName), "general") {
			continue
		}
		if remain, ok := flexibleNumber(item.IntervalRemain); ok {
			usage.FiveHour = newWindow(100-remain, fiveHourSeconds, flexibleTime(item.EndTime), now)
		}
		if item.WeeklyStatus != nil && *item.WeeklyStatus == 1 {
			if remain, ok := flexibleNumber(item.WeeklyRemain); ok {
				usage.Weekly = newWindow(100-remain, weeklySeconds, flexibleTime(item.WeeklyEndTime), now)
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
