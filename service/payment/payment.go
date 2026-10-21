// Package payment orchestrates new-api payments on top of the gateway
// protocol layer: pricing and quotes, order creation and checkout, gateway
// callbacks, closing and reconciling pending orders, the administrator's
// payment methods and the exchange rates the prices are converted with.
// The orders themselves and their settlement live in model.
package payment

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/payment/gateway"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	// paymentWindow is how long a new order waits for its payment before the
	// reconcile task asks its gateway whether it may close.
	paymentWindow = 15 * time.Minute
	// maxPendingOrders is how many unexpired pending orders a user may hold.
	maxPendingOrders = 5
	// gatewayTimeout bounds every gateway and rate API request.
	gatewayTimeout = 15 * time.Second
)

// gatewayRegistry opens the gateways of payment methods. It is built on
// first use, after the outbound HTTP client is initialized; tests replace
// it with a registry of local fakes.
var gatewayRegistry = sync.OnceValue(func() *gateway.Registry {
	client := &http.Client{Timeout: gatewayTimeout}
	if base := service.GetHttpClient(); base != nil {
		client.Transport = base.Transport
	}
	return gateway.NewRegistry(gateway.WithHTTPClient(client))
})

// methodView is the gateway's view of a payment method.
func methodView(method *model.PaymentMethod) gateway.Method {
	return gateway.Method{ID: int64(method.Id), Platform: method.Platform, Token: method.Token, Domain: method.Domain, Config: method.Config}
}

// orderView is the gateway's view of a payment order.
func orderView(order *model.PaymentOrder) *gateway.Order {
	return &gateway.Order{
		OrderNo:         order.OrderNo,
		Amount:          order.Amount,
		Currency:        order.Currency,
		PaymentAmount:   order.PaymentAmount,
		PaymentCurrency: order.PaymentCurrency,
		TradeNo:         order.TradeNo,
		CreatedAt:       time.Unix(order.CreatedAt, 0),
		ExpiresAt:       time.Unix(order.ExpiresAt, 0),
	}
}

// openGateway opens the gateway of a method; the balance method has none.
func openGateway(method *model.PaymentMethod) (gateway.Gateway, error) {
	return gatewayRegistry().Open(methodView(method))
}

// siteAddress is the public address of the site, which callbacks fall back
// to and return URLs are built on.
func siteAddress() string {
	return strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
}

// newOrderNo returns an order number every gateway accepts: letters and
// digits only, unique and sortable by creation time.
func newOrderNo() (string, error) {
	suffix, err := common.GenerateRandomCharsKey(10)
	if err != nil {
		return "", err
	}
	return "P" + time.Now().UTC().Format("20060102150405") + strings.ToUpper(suffix), nil
}
