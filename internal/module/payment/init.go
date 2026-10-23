package payment

import (
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/model"
)

// Init runs the payment module's startup data steps on the master node,
// after the database migrations: it seeds the wallet balance method and
// gives every Stripe method a payment method type. Both are idempotent.
func Init() error {
	if err := store.EnsureBalanceMethod(model.DB); err != nil {
		return err
	}
	return store.EnsureStripeMethodTypes(model.DB)
}
