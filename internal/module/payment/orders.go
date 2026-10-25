package payment

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
)

// The order lookups and records of the HTTP handlers, and the gateway types
// a callback handler builds.

type (
	// OrderFilter selects the orders of ListOrders.
	OrderFilter = store.OrderFilter
	// Notification is a gateway callback as it was delivered.
	Notification = gateway.Notification
)

var (
	ErrOrderNotFound   = store.ErrOrderNotFound
	ErrOrderNotPending = store.ErrOrderNotPending
	// ErrInvalidCallback marks a callback that redelivery cannot fix.
	ErrInvalidCallback = gateway.ErrInvalidCallback
)

// ManualTradeNo is the trade number of an order an administrator marked as
// paid.
func ManualTradeNo(orderNo string) string {
	return store.ManualTradeNo(orderNo)
}

// GetOrder loads an order by its number.
func GetOrder(orderNo string) (*entity.PaymentOrder, error) {
	return store.GetOrderByNo(orderNo)
}

// OrderInvoices lists the invoices of an order in the order they were
// issued.
func OrderInvoices(orderNo string) ([]*entity.PaymentInvoice, error) {
	order, err := store.GetOrderByNo(orderNo)
	if err != nil {
		return nil, err
	}
	return store.ListInvoices(order.Id)
}

// ListOrders lists the orders filter selects, newest first.
func ListOrders(filter OrderFilter, pageInfo *common.PageInfo) ([]*entity.PaymentOrder, int64, error) {
	return store.ListOrders(filter, pageInfo)
}

// FulfilOrder retries the fulfilment of a paid order whose fulfilment
// failed.
func FulfilOrder(orderNo string, clientIp string) (*entity.PaymentOrder, error) {
	return store.FulfilOrder(orderNo, clientIp)
}

// ListUnmatched lists the payments that could not settle their order;
// resolved selects resolved or open records, nil both.
func ListUnmatched(resolved *bool, pageInfo *common.PageInfo) ([]*entity.PaymentUnmatched, int64, error) {
	return store.ListUnmatched(resolved, pageInfo)
}

// ResolveUnmatched marks an open record resolved and reports whether this
// call resolved it.
func ResolveUnmatched(id int, adminId int, note string) (bool, error) {
	return store.ResolveUnmatched(id, adminId, note)
}
