package entity

// Payment invoice statuses. A pending invoice becomes paid when its gateway
// confirms the payment, or closed once its gateway confirms that none is
// coming or another invoice replaced it. A closed invoice can still be paid
// late; the payment then settles its order if the order is not paid yet.
const (
	PaymentInvoicePending = 1
	PaymentInvoicePaid    = 2
	PaymentInvoiceClosed  = 3
)

// PaymentInvoice asks one payment method to collect a payment order. An
// order is not bound to a method: when an invoice cannot be paid another one
// is issued, and the first invoice that is paid settles the order. InvoiceNo
// is the order number the gateway is given; the first invoice of an order
// takes the order's own number. The payment expectation (what the gateway is
// asked to collect) and the gateway payment bound to the invoice are written
// once.
type PaymentInvoice struct {
	Id              int    `json:"id" gorm:"index:idx_payment_invoices_order,priority:2"`
	InvoiceNo       string `json:"invoice_no" gorm:"type:varchar(64);not null;uniqueIndex"`
	OrderId         int    `json:"order_id" gorm:"not null;index:idx_payment_invoices_order,priority:1"`
	PaymentMethodId int    `json:"payment_method_id" gorm:"not null;index:idx_payment_invoices_method_status,priority:1"`
	Platform        string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_payment_invoices_platform_trade,priority:1,where:trade_no <> ''"`

	// An empty PaymentCurrency means the checkout of the invoice has not
	// started. ExchangeRate converted the order's amount into it.
	PaymentAmount   int64  `json:"payment_amount" gorm:"not null;default:0"`
	PaymentCurrency string `json:"payment_currency" gorm:"type:varchar(8);not null;default:''"`
	ExchangeRate    string `json:"exchange_rate" gorm:"type:varchar(64);not null;default:''"`

	// TradeNo is the gateway payment bound to the invoice; one payment binds
	// at most one invoice of its platform.
	TradeNo string `json:"trade_no" gorm:"type:varchar(255);not null;default:'';uniqueIndex:idx_payment_invoices_platform_trade,priority:2"`

	Status      int    `json:"status" gorm:"not null;index:idx_payment_invoices_method_status,priority:2"`
	CloseReason string `json:"close_reason" gorm:"type:varchar(32);not null;default:''"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
	PaidAt      int64  `json:"paid_at" gorm:"bigint;not null;default:0"`
	ClosedAt    int64  `json:"closed_at" gorm:"bigint;not null;default:0"`
}
