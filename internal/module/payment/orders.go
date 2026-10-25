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

// BuyerOrder is an order as its buyer sees it: what was bought, the payment
// option it was paid with and what was charged, never the channel that
// collected it.
type BuyerOrder struct {
	OrderNo     string `json:"order_no"`
	Purpose     string `json:"purpose"`
	PlanId      int    `json:"plan_id"`
	Subject     string `json:"subject"`
	TopupAmount int64  `json:"topup_amount"`
	QuotaAmount int    `json:"quota_amount"`
	Currency    string `json:"currency"`
	Price       int64  `json:"price"`
	Adjustment  int64  `json:"adjustment"`
	FeeAmount   int64  `json:"fee_amount"`
	Amount      int64  `json:"amount"`
	// PaymentOption names the payment option; Balance marks an order the
	// wallet balance pays.
	PaymentOption string `json:"payment_option"`
	Balance       bool   `json:"balance"`
	// PaymentAmount and PaymentCurrency are what the buyer is charged, once
	// a checkout started.
	PaymentAmount   int64  `json:"payment_amount"`
	PaymentCurrency string `json:"payment_currency"`
	Status          int    `json:"status"`
	FulfilError     string `json:"fulfil_error"`
	CloseReason     string `json:"close_reason"`
	CreatedAt       int64  `json:"created_at"`
	ExpiresAt       int64  `json:"expires_at"`
	PaidAt          int64  `json:"paid_at"`
	FinishedAt      int64  `json:"finished_at"`
	ClosedAt        int64  `json:"closed_at"`
}

// BuyerOrders is how the buyer sees orders.
func BuyerOrders(orders []*entity.PaymentOrder) ([]*BuyerOrder, error) {
	ids := make([]int, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.PaymentOptionId)
	}
	names, err := store.OptionNames(ids)
	if err != nil {
		return nil, err
	}
	views := make([]*BuyerOrder, 0, len(orders))
	for _, order := range orders {
		views = append(views, &BuyerOrder{
			OrderNo: order.OrderNo, Purpose: order.Purpose, PlanId: order.PlanId, Subject: order.Subject,
			TopupAmount: order.TopupAmount, QuotaAmount: order.QuotaAmount, Currency: order.Currency, Price: order.Price,
			Adjustment: order.Adjustment, FeeAmount: order.FeeAmount, Amount: order.Amount,
			PaymentOption: names[order.PaymentOptionId], Balance: order.Platform == entity.PaymentPlatformBalance,
			PaymentAmount: order.PaymentAmount, PaymentCurrency: order.PaymentCurrency, Status: order.Status,
			FulfilError: order.FulfilError, CloseReason: order.CloseReason, CreatedAt: order.CreatedAt, ExpiresAt: order.ExpiresAt,
			PaidAt: order.PaidAt, FinishedAt: order.FinishedAt, ClosedAt: order.ClosedAt,
		})
	}
	return views, nil
}

// BuyerOrderOf is how the buyer sees an order.
func BuyerOrderOf(order *entity.PaymentOrder) (*BuyerOrder, error) {
	views, err := BuyerOrders([]*entity.PaymentOrder{order})
	if err != nil {
		return nil, err
	}
	return views[0], nil
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
