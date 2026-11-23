package kiro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/sync/singleflight"
)

// Model is one entry of an account's model catalog. Its request schema and
// output ceiling decide how a request's options are fitted.
type Model struct {
	ID string `json:"modelId"`
	// Schema is the JSON Schema of additionalModelRequestFields; a model
	// without one takes none of them.
	Schema json.RawMessage `json:"additionalModelRequestFieldsSchema"`
	Limits struct {
		Output int64 `json:"maxOutputTokens"`
	} `json:"tokenLimits"`
	// SystemPrompt reports whether the profile may send a system prompt of its
	// own (GenerateAssistantResponse systemPrompt, CreateResponse instructions).
	SystemPrompt bool `json:"-"`
}

// TakesOption reports whether the model's request schema lists a top-level
// option, such as max_tokens or temperature.
func (m Model) TakesOption(name string) bool {
	return gjson.GetBytes(m.Schema, "properties."+gjson.Escape(name)).IsObject()
}

// TakesOutputLimit reports whether GenerateAssistantResponse enforces a
// per-request output cap (max_tokens) for the model. Elsewhere Kiro has none
// and the model's ceiling applies.
func (m Model) TakesOutputLimit() bool { return m.TakesOption("max_tokens") }

// OutputLimit fits a requested GenerateAssistantResponse cap into the bounds
// of the model's schema.
func (m Model) OutputLimit(requested int64) int64 {
	bounds := gjson.GetBytes(m.Schema, "properties.max_tokens")
	limit := max(requested, bounds.Get("minimum").Int())
	if maximum := bounds.Get("maximum"); maximum.Exists() {
		limit = min(limit, maximum.Int())
	}
	return limit
}

// catalogTTL is how long an account's catalog and feature answer are reused.
const catalogTTL = 10 * time.Minute

type catalogEntry struct {
	models       map[string]Model
	systemPrompt bool
	expires      time.Time
}

// systemPromptFeature is the GetFeatureConfiguration key of the
// system_field_injection feature: the hex SHA-256 of the client's salt and the
// feature name. Without it Kiro refuses both system prompt fields.
var systemPromptFeature = func() string {
	sum := sha256.Sum256([]byte("kiro-feature-config-key-salt-3e9b1d7a" + "system_field_injection"))
	return hex.EncodeToString(sum[:])
}()

// systemPromptEnabled asks the Runtime whether the profile has
// system_field_injection. A failed lookup reads as not enabled, which only
// moves the system prompt into the first user message.
func systemPromptEnabled(ctx context.Context, client *http.Client, runtime, token, profile string) bool {
	var answer struct {
		Configuration map[string]bool `json:"configuration"`
	}
	payload := map[string]string{"profileArn": profile, "origin": "AI_EDITOR", "version": "1.4.215"}
	if err := rpc(ctx, client, runtime+"/", "AmazonCodeWhispererStreamingService.GetFeatureConfiguration", token, payload, &answer); err != nil {
		return false
	}
	return answer.Configuration[systemPromptFeature]
}

// managementURL is the management service of a Runtime: the same region on
// management.<region>.kiro.dev, or a custom base URL itself.
func managementURL(runtime string) string {
	u, err := url.Parse(runtime)
	if err == nil && u.Scheme == "https" && strings.HasPrefix(u.Host, "runtime.") && strings.HasSuffix(u.Host, ".kiro.dev") &&
		regionPattern.MatchString(strings.TrimSuffix(strings.TrimPrefix(u.Host, "runtime."), ".kiro.dev")) {
		u.Host = "management." + strings.TrimPrefix(u.Host, "runtime.")
		return strings.TrimRight(u.String(), "/") + "/"
	}
	return strings.TrimRight(runtime, "/") + "/"
}

// errProfileUnresolved is an account with no single profile to use; the
// administrator names one in the credential.
var errProfileUnresolved = errors.New("the Kiro account has no single profile; set profileArn in the credential")

// resolveProfile finds the account's profile when the credential names none.
func resolveProfile(ctx context.Context, client *http.Client, runtime, token string) (string, error) {
	var page struct {
		Profiles []struct {
			Arn string `json:"arn"`
		} `json:"profiles"`
	}
	if err := rpc(ctx, client, managementURL(runtime), "AmazonCodeWhispererService.ListAvailableProfiles", token, struct{}{}, &page); err != nil {
		return "", err
	}
	if len(page.Profiles) != 1 || !profilePattern.MatchString(page.Profiles[0].Arn) {
		return "", errProfileUnresolved
	}
	return page.Profiles[0].Arn, nil
}

// catalog caches each account's model catalog by Runtime, profile and access
// token.
type catalog struct {
	mu      sync.Mutex
	entries map[[32]byte]catalogEntry
	fetches singleflight.Group
}

var catalogs = &catalog{}

// errModelUnavailable is a model the account's catalog does not list.
var errModelUnavailable = errors.New("the Kiro account does not offer this model")

// list returns the account's catalog, from the cache or fetched. Requests
// that find it missing at the same time share one fetch, which does not end
// when one of them gives up.
func (c *catalog) list(ctx context.Context, client *http.Client, runtime, token, profile string) (catalogEntry, error) {
	key := sha256.Sum256([]byte(runtime + "\x00" + profile + "\x00" + token))
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached, nil
	}
	flight := c.fetches.DoChan(hex.EncodeToString(key[:]), func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prepareTimeout)
		defer cancel()
		entry, err := fetchCatalog(fetchCtx, client, runtime, token, profile)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[[32]byte]catalogEntry)
		}
		if len(c.entries) >= 128 {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
		c.entries[key] = entry
		c.mu.Unlock()
		return entry, nil
	})
	select {
	case result := <-flight:
		if result.Err != nil {
			return catalogEntry{}, result.Err
		}
		return result.Val.(catalogEntry), nil
	case <-ctx.Done():
		return catalogEntry{}, ctx.Err()
	}
}

// fetchCatalog reads every page of the account's catalog and its
// system_field_injection feature.
func fetchCatalog(ctx context.Context, client *http.Client, runtime, token, profile string) (catalogEntry, error) {
	found := map[string]Model{}
	next := ""
	seen := map[string]bool{}
	for range 40 {
		payload := map[string]string{"origin": "AI_EDITOR", "profileArn": profile}
		if next != "" {
			payload["nextToken"] = next
		}
		var page struct {
			Models []Model `json:"models"`
			Next   string  `json:"nextToken"`
		}
		if err := rpc(ctx, client, managementURL(runtime), "KiroControlPlaneBearerService.ListAvailableModels", token, payload, &page); err != nil {
			return catalogEntry{}, err
		}
		for _, m := range page.Models {
			found[m.ID] = m
		}
		if len(found) > 2048 || seen[page.Next] {
			break
		}
		if page.Next == "" {
			return catalogEntry{models: found, systemPrompt: systemPromptEnabled(ctx, client, runtime, token, profile), expires: time.Now().Add(catalogTTL)}, nil
		}
		seen[page.Next] = true
		next = page.Next
	}
	return catalogEntry{}, errors.New("invalid Kiro model catalog")
}

func (c *catalog) lookup(ctx context.Context, client *http.Client, runtime, token, profile, id string) (Model, error) {
	entry, err := c.list(ctx, client, runtime, token, profile)
	if err != nil {
		return Model{}, err
	}
	m, ok := entry.models[id]
	if !ok {
		return Model{}, errModelUnavailable
	}
	m.SystemPrompt = entry.systemPrompt
	return m, nil
}
