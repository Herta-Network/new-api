package payment

import (
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/model"
)

// Init runs the payment module's startup data steps on the master node,
// after the database migrations: it seeds the wallet balance method, gives
// every Stripe method a payment method type and every EPay method a
// protocol version and checkout mode. All are idempotent.
func Init() error {
	if err := store.EnsureBalanceMethod(model.DB); err != nil {
		return err
	}
	if err := store.EnsureStripeMethodTypes(model.DB); err != nil {
		return err
	}
	return store.EnsureEPayProtocols(model.DB)
}
