// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/billingtest/stripe.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the fake answers automatic-payment-method intents
// with newapi metadata, replays idempotent creations as Stripe does,
// updates webhook endpoints, and refuses customer calls.

// Package gatewaytest provides a fake Stripe API for tests of the payment
// gateway and the payment service.
package gatewaytest

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stripe/stripe-go/v81"
)

// FakeStripe is an in-process Stripe API answering the PaymentIntent and
// webhook endpoint calls the payment gateway makes. Any other call, such as
// creating a customer, fails the test.
type FakeStripe struct {
	t        testing.TB
	Backends *stripe.Backends

	mu      sync.Mutex
	intents map[string]map[string]any
	// replays holds the creation answer of each idempotency key, replayed
	// as Stripe does.
	replays  map[string]map[string]any
	created  []url.Values
	keys     []string
	canceled []string
	webhooks map[string]map[string]any
	// endpoints numbers the webhook endpoints ever created.
	endpoints int
}

// NewFakeStripe starts a fake Stripe API for the test; Backends routes a
// Stripe client to it.
func NewFakeStripe(t testing.TB) *FakeStripe {
	t.Helper()
	fake := &FakeStripe{t: t, intents: map[string]map[string]any{}, replays: map[string]map[string]any{}, webhooks: map[string]map[string]any{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.Backends = stripe.NewBackendsWithConfig(&stripe.BackendConfig{
		URL:               stripe.String(server.URL),
		HTTPClient:        server.Client(),
		MaxNetworkRetries: stripe.Int64(0),
		LeveledLogger:     &stripe.LeveledLogger{Level: stripe.LevelNull},
	})
	return fake
}

func (f *FakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("parse form: %v", err)
	}
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	switch {
	case r.Method == http.MethodPost && path == "payment_intents":
		key := r.Header.Get("Idempotency-Key")
		if replay, ok := f.replays[key]; ok {
			w.Header().Set("Idempotent-Replayed", "true")
			f.write(w, replay)
			return
		}
		f.created = append(f.created, r.PostForm)
		f.keys = append(f.keys, key)
		id := fmt.Sprintf("pi_%d", len(f.created))
		var amount int64
		_, _ = fmt.Sscan(r.PostForm.Get("amount"), &amount)
		intent := Intent(id, amount, r.PostForm.Get("currency"), "requires_payment_method",
			r.PostForm.Get("metadata[newapi_order_no]"), r.PostForm.Get("metadata[newapi_method_id]"))
		f.intents[id] = intent
		f.replays[key] = maps.Clone(intent)
		f.write(w, intent)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "payment_intents/") && strings.HasSuffix(path, "/cancel"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "payment_intents/"), "/cancel")
		// Like Stripe, a succeeded, processing or canceled intent cannot be
		// canceled.
		intent, ok := f.intents[id]
		if !ok || intent["status"] == "succeeded" || intent["status"] == "processing" || intent["status"] == "canceled" {
			w.WriteHeader(http.StatusBadRequest)
			f.write(w, map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "payment_intent_unexpected_state"}})
			return
		}
		intent["status"] = "canceled"
		f.canceled = append(f.canceled, id)
		f.write(w, intent)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "payment_intents/"):
		intent, ok := f.intents[strings.TrimPrefix(path, "payment_intents/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			f.write(w, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "No such payment_intent"}})
			return
		}
		f.write(w, intent)
	case r.Method == http.MethodPost && path == "webhook_endpoints":
		f.endpoints++
		id := fmt.Sprintf("we_%d", f.endpoints)
		endpoint := map[string]any{
			"id": id, "object": "webhook_endpoint", "url": r.PostForm.Get("url"), "api_version": r.PostForm.Get("api_version"),
			"enabled_events": formList(r.PostForm, "enabled_events"), "status": "enabled", "account": account,
		}
		f.webhooks[id] = endpoint
		f.write(w, mergeMaps(endpoint, map[string]any{"secret": "whsec_" + id}))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "webhook_endpoints/"):
		endpoint, ok := f.webhooks[strings.TrimPrefix(path, "webhook_endpoints/")]
		// Like Stripe, an endpoint exists only in the account that created it.
		if !ok || endpoint["account"] != account {
			w.WriteHeader(http.StatusNotFound)
			f.write(w, map[string]any{"error": map[string]any{"type": "invalid_request_error"}})
			return
		}
		endpoint["url"] = r.PostForm.Get("url")
		endpoint["enabled_events"] = formList(r.PostForm, "enabled_events")
		if r.PostForm.Get("disabled") == "false" {
			endpoint["status"] = "enabled"
		}
		f.write(w, endpoint)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "webhook_endpoints/"):
		id := strings.TrimPrefix(path, "webhook_endpoints/")
		if endpoint, ok := f.webhooks[id]; !ok || endpoint["account"] != account {
			w.WriteHeader(http.StatusNotFound)
			f.write(w, map[string]any{"error": map[string]any{"type": "invalid_request_error"}})
			return
		}
		delete(f.webhooks, id)
		f.write(w, map[string]any{"id": id, "object": "webhook_endpoint", "deleted": true})
	default:
		f.t.Errorf("unexpected Stripe request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func Intent(id string, amount int64, currency, status, orderNo, methodID string) map[string]any {
	metadata := map[string]string{}
	if orderNo != "" {
		metadata["newapi_order_no"] = orderNo
		metadata["newapi_method_id"] = methodID
	}
	return map[string]any{
		"id": id, "object": "payment_intent", "amount": amount, "amount_received": 0, "currency": currency,
		"client_secret": id + "_secret", "status": status, "metadata": metadata,
		"automatic_payment_methods": map[string]any{"enabled": true},
	}
}

func formList(form url.Values, name string) []string {
	var values []string
	for i := 0; form.Has(fmt.Sprintf("%s[%d]", name, i)); i++ {
		values = append(values, form.Get(fmt.Sprintf("%s[%d]", name, i)))
	}
	return values
}

func mergeMaps(a, b map[string]any) map[string]any {
	merged := maps.Clone(a)
	maps.Copy(merged, b)
	return merged
}

func (f *FakeStripe) write(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	content, err := common.Marshal(body)
	if err != nil {
		f.t.Errorf("encode response: %v", err)
	}
	_, _ = w.Write(content)
}

// Seed stores an existing PaymentIntent.
func (f *FakeStripe) Seed(intent map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intents[intent["id"].(string)] = intent
}

func (f *FakeStripe) Set(id, field string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intents[id][field] = value
}

func (f *FakeStripe) Status(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, _ := f.intents[id]["status"].(string)
	return status
}

// Webhooks returns a copy of every endpoint by id; "account" is the secret
// key that created it.
func (f *FakeStripe) Webhooks() map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	webhooks := make(map[string]map[string]any, len(f.webhooks))
	for id, endpoint := range f.webhooks {
		webhooks[id] = maps.Clone(endpoint)
	}
	return webhooks
}

// Webhook returns a copy of endpoint id, or nil.
func (f *FakeStripe) Webhook(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.webhooks[id])
}

func (f *FakeStripe) DisableWebhook(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhooks[id]["status"] = "disabled"
}

func (f *FakeStripe) Snapshot() (created []url.Values, keys, canceled []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.created...), append([]string(nil), f.keys...), append([]string(nil), f.canceled...)
}
