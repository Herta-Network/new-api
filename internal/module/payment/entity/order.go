// Package entity holds the payment module's tables and their constants.
package entity

// Payment order statuses. A pending order becomes paid when its payment is
// confirmed and finished once it is fulfilled (quota credited or
// subscription created); a paid order whose fulfilment failed stays paid
// with FulfilError until an administrator fulfils it. A closed order can
// still be paid late, then it is settled like a pending one.
const (
	PaymentOrderPending  = 1
	PaymentOrderPaid     = 2
	PaymentOrderClosed   = 3
	PaymentOrderFinished = 5
)

// What a payment order buys.
const (
	PaymentPurposeTopup        = "topup"
	PaymentPurposeSubscription = "subscription"
)

// PaymentOrder is one purchase: a wallet top-up or a subscription plan.
// Amounts count hundredths of their currency. A gateway order is paid
// through its invoices; the balance pays an order itself.
type PaymentOrder struct {
	Id      int    `json:"id" gorm:"index:idx_payment_orders_user,priority:2"`
	OrderNo string `json:"order_no" gorm:"type:varchar(64);not null;uniqueIndex"`
	UserId  int    `json:"user_id" gorm:"not null;index:idx_payment_orders_user,priority:1"`
	Purpose string `json:"purpose" gorm:"type:varchar(16);not null"`
	PlanId  int    `json:"plan_id" gorm:"not null;default:0"`
	Subject string `json:"subject" gorm:"type:varchar(255);not null;default:''"`

	// TopupAmount is the face value bought, in the site currency. QuotaAmount
	// is the quota a top-up credits or a balance purchase deducts, converted
	// at QuotaRate, the site currency to USD rate when the order was made.
	TopupAmount int64  `json:"topup_amount" gorm:"not null;default:0"`
	QuotaAmount int    `json:"quota_amount" gorm:"type:bigint;not null;default:0"`
	QuotaRate   string `json:"quota_rate" gorm:"type:varchar(64);not null;default:''"`

	// Currency is the site currency the price is in. Amount = Price +
	// Adjustment + FeeAmount, where Adjustment is what the group ratio and
	// the amount discount change (negative for a discount).
	Currency   string `json:"currency" gorm:"type:varchar(8);not null"`
	Price      int64  `json:"price" gorm:"not null;default:0"`
	Adjustment int64  `json:"adjustment" gorm:"not null;default:0"`
	FeeAmount  int64  `json:"fee_amount" gorm:"not null;default:0"`
	Amount     int64  `json:"amount" gorm:"not null;default:0"`

	// PaymentOptionId is the payment option the buyer chose.
	PaymentOptionId int `json:"payment_option_id" gorm:"not null;default:0"`
	// The payment method, the payment expectation and the trade show the
	// order's latest invoice, and once the order is paid the invoice that
	// paid it (a manual completion's trade is ManualTradeNo). They are a copy
	// for lists and statistics; checkouts, callbacks and settlement read the
	// invoices. A balance order names the balance method itself.
	PaymentMethodId int    `json:"payment_method_id" gorm:"not null"`
	Platform        string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_payment_orders_platform_trade,priority:1,where:trade_no <> ''"`
	PaymentAmount   int64  `json:"payment_amount" gorm:"not null;default:0"`
	PaymentCurrency string `json:"payment_currency" gorm:"type:varchar(8);not null;default:''"`
	ExchangeRate    string `json:"exchange_rate" gorm:"type:varchar(64);not null;default:''"`
	TradeNo         string `json:"trade_no" gorm:"type:varchar(255);not null;default:'';uniqueIndex:idx_payment_orders_platform_trade,priority:2"`

	Status             int    `json:"status" gorm:"not null;index:idx_payment_orders_status_check,priority:1;index:idx_payment_orders_status_paid,priority:1"`
	FulfilError        string `json:"fulfil_error" gorm:"type:text;not null;default:''"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"not null;default:0"`
	ClientIp           string `json:"-" gorm:"type:varchar(64);not null;default:''"`
	CloseReason        string `json:"close_reason" gorm:"type:varchar(32);not null;default:''"`

	CreatedAt     int64 `json:"created_at" gorm:"bigint"`
	ExpiresAt     int64 `json:"expires_at" gorm:"bigint;not null;default:0"`
	PaidAt        int64 `json:"paid_at" gorm:"bigint;not null;default:0;index:idx_payment_orders_status_paid,priority:2"`
	FinishedAt    int64 `json:"finished_at" gorm:"bigint;not null;default:0"`
	ClosedAt      int64 `json:"closed_at" gorm:"bigint;not null;default:0"`
	NextCheckAt   int64 `json:"-" gorm:"bigint;not null;default:0;index:idx_payment_orders_status_check,priority:2"`
	CheckAttempts int   `json:"-" gorm:"not null;default:0"`
}
