// Partly ported from perfect-panel/server at commit 944dc363cc6a:
//   - cases of internal/module/billing/internal/exchangerate/exchange_rate_test.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: testify against a local fake of the rate API.

package exchangerate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExchangeRateFetch(t *testing.T) {
	var answer string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/currency_data/convert", r.URL.Path)
		assert.Equal(t, "access-key", r.Header.Get("apikey"))
		assert.Equal(t, url.Values{"from": {"USD"}, "to": {"CNY"}, "amount": {"1"}}, r.URL.Query())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	rates := NewClient(server.Client(), server.URL, "access-key")
	ctx := context.Background()

	for result, want := range map[string]string{"7.1234": "7.1234", "7.10": "7.1", "1.39e-04": "0.000139", "7": "7"} {
		answer = `{"success":true,"query":{"from":"USD","to":"CNY","amount":1},"info":{"quote":1},"result":` + result + `}`
		got, err := rates.Fetch(ctx, RateQuery{From: "usd", To: "CNY"})
		require.NoError(t, err, result)
		assert.Equal(t, want, got, result)
	}
	for _, result := range []string{"0", "-7.1"} {
		answer = `{"success":true,"result":` + result + `}`
		_, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
		assert.ErrorIs(t, err, ErrImplausibleRate, result)
	}

	// Within the bound of the previous rate the new one is accepted; a jump
	// beyond it is not.
	answer = `{"success":true,"result":7.1234}`
	got, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "7", MaxChangeBP: 500})
	require.NoError(t, err)
	assert.Equal(t, "7.1234", got)
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "5", MaxChangeBP: 500})
	assert.ErrorIs(t, err, ErrImplausibleRate)
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY", Previous: "5"})
	assert.NoError(t, err, "a zero bound disables the check")

	for name, tt := range map[string]struct {
		status int
		answer string
	}{
		"unsuccessful": {http.StatusOK, `{"success":false,"error":{"code":201,"info":"invalid source currency"}}`},
		"unauthorized": {http.StatusUnauthorized, `{"message":"Invalid authentication credentials"}`},
		"not JSON":     {http.StatusOK, `<html></html>`},
		"no result":    {http.StatusOK, `{"success":true}`},
	} {
		status, answer = tt.status, tt.answer
		_, err := rates.Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
		assert.Error(t, err, name)
		assert.NotErrorIs(t, err, ErrImplausibleRate, name)
	}

	_, err = NewClient(server.Client(), server.URL, "").Fetch(ctx, RateQuery{From: "USD", To: "CNY"})
	assert.Error(t, err, "without an API key there is no rate")
	_, err = rates.Fetch(ctx, RateQuery{From: "USD", To: "USD"})
	assert.Error(t, err)
}
