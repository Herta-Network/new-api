// Package currency is the facade of the currency module: the rate a
// currency pair converts at (an administrator's override, then a fresh
// live rate), the refresh of the live rates the site needs, the rate
// amounts are shown at, and the administrator's overview of the rates. Its
// exported functions and types are the module's API; internal/exchangerate
// is the rate provider and internal/store keeps the stored live rates.
package currency

import (
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/internal/module/currency/internal/exchangerate"
	"github.com/QuantumNous/new-api/service"
)

// rateAPITimeout bounds every rate API request.
const rateAPITimeout = 15 * time.Second

// rateAPIURL is the rate API host; tests point it at a local fake.
var rateAPIURL = exchangerate.APILayerURL

// rateHTTPClient carries the rate API requests. It is built on first use,
// after the outbound HTTP client is initialized.
var rateHTTPClient = sync.OnceValue(func() *http.Client {
	client := &http.Client{Timeout: rateAPITimeout}
	if base := service.GetHttpClient(); base != nil {
		client.Transport = base.Transport
	}
	return client
})
