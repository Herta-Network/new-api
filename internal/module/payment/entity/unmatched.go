package entity

// PaymentUnmatched records money a gateway confirmed that could not settle
// its order: a second payment of a paid order (also through another of its
// invoices), an amount that differs from the expectation, a fulfilment that
// failed. The callback is still
// acknowledged so the gateway stops retrying; an operator refunds or
// fulfils by hand and resolves the record. One payment is recorded once.
type PaymentUnmatched struct {
	Id       int    `json:"id"`
	Platform string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_payment_unmatched_payment,priority:1"`
	TradeNo  string `json:"trade_no" gorm:"type:varchar(255);not null;uniqueIndex:idx_payment_unmatched_payment,priority:2"`
	OrderNo  string `json:"order_no" gorm:"type:varchar(64);not null;uniqueIndex:idx_payment_unmatched_payment,priority:3"`
	// InvoiceNo is the invoice the payment was made for; empty for a manual
	// completion.
	InvoiceNo       string `json:"invoice_no" gorm:"type:varchar(64);not null;default:''"`
	PaymentMethodId int    `json:"payment_method_id" gorm:"not null;default:0"`
	UserId          int    `json:"user_id" gorm:"not null;default:0"`
	Amount          int64  `json:"amount" gorm:"not null;default:0"`
	Currency        string `json:"currency" gorm:"type:varchar(8);not null;default:''"`
	Reason          string `json:"reason" gorm:"type:text;not null;default:''"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint"`
	ResolvedAt      int64  `json:"resolved_at" gorm:"bigint;not null;default:0;index"`
	ResolvedBy      int    `json:"resolved_by" gorm:"not null;default:0"`
	ResolveNote     string `json:"resolve_note" gorm:"type:text;not null;default:''"`
}

func (PaymentUnmatched) TableName() string {
	return "payment_unmatched"
}
