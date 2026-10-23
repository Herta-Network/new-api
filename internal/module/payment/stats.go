package payment

import "github.com/QuantumNous/new-api/internal/module/payment/internal/store"

// The payment statistics of the admin operations overview. Revenue counts
// what gateways, or administrators by hand, got paid for, at the time it
// was paid; what the wallet balance paid for is reported apart.

type (
	// CurrencyTotal counts records of one currency and sums their amounts,
	// in hundredths.
	CurrencyTotal = store.CurrencyTotal
	// RevenueRow is the paid orders of one bucket, purpose and currency.
	RevenueRow = store.RevenueRow
	// BucketCount is a count in one bucket.
	BucketCount = store.BucketCount
)

// GatewayRevenue sums what gateways collected in [start, end) in currency.
func GatewayRevenue(start, end int64, currency string) (amount int64, orders int64, err error) {
	return store.GatewayRevenue(start, end, currency)
}

// Attention totals per currency the paid orders whose fulfilment failed and
// the open unmatched payments, which an operator has to act on.
func Attention() (unfulfilled []CurrencyTotal, unmatched []CurrencyTotal, err error) {
	return store.Attention()
}

// FirstPaidAt is when the earliest paid order was paid, 0 without one;
// gatewayOnly leaves out what the balance paid for.
func FirstPaidAt(gatewayOnly bool) (int64, error) {
	return store.FirstPaidAt(gatewayOnly)
}

// RevenueByBucket groups the orders paid in the buckets starts delimits:
// bucket i, from 1, spans [starts[i-1], starts[i]).
func RevenueByBucket(starts []int64) ([]RevenueRow, error) {
	return store.RevenueByBucket(starts)
}

// PayersByBucket counts per bucket the users a gateway got paid by and the
// first-time payers among them, and the payers of the whole span.
func PayersByBucket(starts []int64) (payers []BucketCount, firstTimePayers []BucketCount, periodPayers int64, err error) {
	return store.PayersByBucket(starts)
}
