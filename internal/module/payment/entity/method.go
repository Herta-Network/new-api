package entity

import (
	"errors"

	"gorm.io/gorm"
)

// PaymentPlatformBalance is the platform of the wallet balance method, the
// fixed payment method BalancePaymentMethodID of the balance payment option.
// Every other platform is served by one of the payment module's gateways.
const (
	PaymentPlatformBalance = "balance"
	BalancePaymentMethodID = -1
)

var ErrBalancePaymentMethodUndeletable = errors.New("the balance payment method cannot be deleted")

// PaymentMethod is one channel that collects a payment option: a gateway
// account (an EPay merchant and payment type, a Stripe account, a Cryptomus
// merchant) or the wallet balance. Buyers never see it; its name is for
// administrators. Methods are soft-deleted so a late callback still finds
// the method its notify URL names.
type PaymentMethod struct {
	Id       int    `json:"id"`
	Name     string `json:"name" gorm:"type:varchar(100);not null;default:''"`
	Platform string `json:"platform" gorm:"<-:create;type:varchar(32);not null"`
	// OptionId is the payment option the method collects.
	OptionId int  `json:"option_id" gorm:"not null;default:0;index"`
	Enabled  bool `json:"enabled" gorm:"not null"`
	// The method takes orders whose price, in hundredths of the site
	// currency, is at least MinAmount and at most MaxAmount; 0 leaves that
	// end open.
	MinAmount int64 `json:"min_amount" gorm:"not null;default:0"`
	MaxAmount int64 `json:"max_amount" gorm:"not null;default:0"`
	// Priority orders the methods of an option: the lowest priority that
	// has a method able to take an order is tried first, and its methods
	// share the orders by Weight.
	Priority int `json:"priority" gorm:"not null;default:0"`
	Weight   int `json:"weight" gorm:"not null;default:1"`
	// Domain is the base URL gateway callbacks are sent to; empty selects
	// the site address.
	Domain string `json:"domain" gorm:"type:varchar(255);not null;default:''"`
	// Config is the platform's JSON configuration. It holds secrets and is
	// never serialized as is.
	Config string `json:"-" gorm:"type:text;not null;default:''"`
	// Token is the secret path segment of the method's notify URL.
	Token     string         `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (m *PaymentMethod) BeforeDelete(*gorm.DB) error {
	if m.Id == BalancePaymentMethodID {
		return ErrBalancePaymentMethodUndeletable
	}
	return nil
}
