package store

import (
	"strings"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

// Revenue counts orders a gateway (or an administrator, by hand) got paid
// for, at the time they were paid. What the wallet balance paid for is
// reported apart: the top-up that funded it was the revenue. Amounts are
// hundredths of a currency and are only added within one currency.

// paidTimeSQL is when an order was paid; an order that reached Finished
// without a paid_at counts from its finish.
const paidTimeSQL = "CASE WHEN paid_at > 0 THEN paid_at ELSE finished_at END"

// paidInRangeSQL selects orders paid in [start, end); it takes the bounds
// twice, and keeps paid_at alone on one side so the index on
// (status, paid_at) serves it.
const paidInRangeSQL = "((paid_at >= ? AND paid_at < ?) OR (paid_at = 0 AND finished_at >= ? AND finished_at < ?))"

// paidStatuses are the statuses of an order whose payment arrived.
var paidStatuses = []int{entity.PaymentOrderPaid, entity.PaymentOrderFinished}

// CurrencyTotal counts records of one currency and sums their amounts.
type CurrencyTotal struct {
	Currency string
	Count    int64
	Amount   int64
}

// RevenueRow is the paid orders of one bucket, purpose and currency, apart
// for the balance and for the orders an administrator completed by hand.
type RevenueRow struct {
	Bucket   int
	Purpose  string
	Currency string
	Balance  bool
	Manual   bool
	Orders   int64
	Amount   int64
}

// BucketCount is a count in one bucket.
type BucketCount struct {
	Bucket int
	Count  int64
}

// gatewayPaidOrders selects the paid orders that are revenue: every
// platform but the wallet balance.
func gatewayPaidOrders() *gorm.DB {
	return model.DB.Model(&entity.PaymentOrder{}).Where("status IN ? AND platform <> ?", paidStatuses, entity.PaymentPlatformBalance)
}

// bucketIndex is the SQL bucket number, from 1, of a column holding a unix
// time; bucket i spans [starts[i], starts[i+1]).
func bucketIndex(column string, starts []int64) any {
	thresholds := make([]any, len(starts))
	for i, start := range starts {
		thresholds[i] = start
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(thresholds)), ",")
	return gorm.Expr("width_bucket("+column+", ARRAY["+placeholders+"]::bigint[])", thresholds...)
}

// GatewayRevenue sums what gateways collected in [start, end) in currency.
func GatewayRevenue(start, end int64, currency string) (amount int64, orders int64, err error) {
	var revenue struct {
		Amount int64
		Orders int64
	}
	err = gatewayPaidOrders().
		Select("coalesce(sum(amount), 0) AS amount, count(*) AS orders").
		Where("upper(currency) = ?", currency).
		Where(paidInRangeSQL, start, end, start, end).
		Scan(&revenue).Error
	return revenue.Amount, revenue.Orders, err
}

// Attention totals per currency the paid orders whose fulfilment failed and
// the open unmatched payments that are not one of those orders.
func Attention() (unfulfilled []CurrencyTotal, unmatched []CurrencyTotal, err error) {
	if err := model.DB.Model(&entity.PaymentOrder{}).Select("currency, count(*) AS count, coalesce(sum(amount), 0) AS amount").
		Where("status = ?", entity.PaymentOrderPaid).Group("currency").Scan(&unfulfilled).Error; err != nil {
		return nil, nil, err
	}
	if err := model.DB.Model(&entity.PaymentUnmatched{}).Select("currency, count(*) AS count, coalesce(sum(amount), 0) AS amount").
		Where("resolved_at = 0").
		Where("NOT EXISTS (SELECT 1 FROM payment_orders WHERE payment_orders.order_no = payment_unmatched.order_no AND payment_orders.trade_no = payment_unmatched.trade_no AND payment_orders.status = ?)", entity.PaymentOrderPaid).
		Group("currency").Scan(&unmatched).Error; err != nil {
		return nil, nil, err
	}
	return unfulfilled, unmatched, nil
}

// FirstPaidAt is when the earliest paid order was paid, 0 without one;
// gatewayOnly leaves out what the balance paid for.
func FirstPaidAt(gatewayOnly bool) (int64, error) {
	query := model.DB.Model(&entity.PaymentOrder{}).Where("status IN ?", paidStatuses)
	if gatewayOnly {
		query = gatewayPaidOrders()
	}
	var first int64
	err := query.Select("coalesce(min(" + paidTimeSQL + "), 0)").Scan(&first).Error
	return first, err
}

// RevenueByBucket groups the orders paid in the buckets that starts
// delimits.
func RevenueByBucket(starts []int64) ([]RevenueRow, error) {
	var rows []RevenueRow
	start, end := starts[0], starts[len(starts)-1]
	err := model.DB.Model(&entity.PaymentOrder{}).
		Select("? AS bucket, purpose, currency, platform = ? AS balance, trade_no LIKE ? AS manual, count(*) AS orders, coalesce(sum(amount), 0) AS amount",
			bucketIndex(paidTimeSQL, starts), entity.PaymentPlatformBalance, ManualTradeNo("")+"%").
		Where("status IN ?", paidStatuses).
		Where(paidInRangeSQL, start, end, start, end).
		Group("bucket, purpose, currency, balance, manual").
		Scan(&rows).Error
	return rows, err
}

// PayersByBucket counts, in the buckets that starts delimits, the users a
// gateway got paid by, and among them the users who paid for the first
// time; periodPayers counts the users who paid in the whole span.
func PayersByBucket(starts []int64) (payers []BucketCount, firstTimePayers []BucketCount, periodPayers int64, err error) {
	start, end := starts[0], starts[len(starts)-1]
	if err := gatewayPaidOrders().Select("? AS bucket, count(DISTINCT user_id) AS count", bucketIndex(paidTimeSQL, starts)).
		Where(paidInRangeSQL, start, end, start, end).
		Group("bucket").Scan(&payers).Error; err != nil {
		return nil, nil, 0, err
	}
	// A payer of the period who paid nothing before it made their first
	// payment at their earliest one in the period. Only the period's orders
	// are grouped, and the earlier orders are looked up per payer.
	firstPaid := gatewayPaidOrders().Select("user_id, min("+paidTimeSQL+") AS first_paid").
		Where(paidInRangeSQL, start, end, start, end).
		Group("user_id")
	paidBefore := model.DB.Table("payment_orders AS earlier").Select("1").
		Where("earlier.user_id = firsts.user_id AND earlier.status IN ? AND earlier.platform <> ?", paidStatuses, entity.PaymentPlatformBalance).
		Where("((earlier.paid_at > 0 AND earlier.paid_at < ?) OR (earlier.paid_at = 0 AND earlier.finished_at < ?))", start, start)
	if err := model.DB.Table("(?) AS firsts", firstPaid).Select("? AS bucket, count(*) AS count", bucketIndex("first_paid", starts)).
		Where("NOT EXISTS (?)", paidBefore).
		Group("bucket").Scan(&firstTimePayers).Error; err != nil {
		return nil, nil, 0, err
	}
	if err := gatewayPaidOrders().Where(paidInRangeSQL, start, end, start, end).
		Distinct("user_id").Count(&periodPayers).Error; err != nil {
		return nil, nil, 0, err
	}
	return payers, firstTimePayers, periodPayers, nil
}
