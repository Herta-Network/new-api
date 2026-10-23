package entity

import (
	"errors"

	"gorm.io/gorm"
)

// PaymentPlatformBalance is the platform of the wallet balance method, the
// fixed payment method BalancePaymentMethodID that subscriptions can be paid
// with. Every other platform is served by one of the payment module's
// gateways.
const (
	PaymentPlatformBalance = "balance"
	BalancePaymentMethodID = -1
)

// Fee modes of a payment method. The fee is charged on top of the order
// price and never changes the credited quota.
const (
	PaymentFeeNone         = 0
	PaymentFeeRate         = 1
	PaymentFeeFixed        = 2
	PaymentFeeRateAndFixed = 3
)

var ErrBalancePaymentMethodUndeletable = errors.New("the balance payment method cannot be deleted")

// PaymentMethod is one configured way to pay: a gateway account (an EPay
// merchant and payment type, a Stripe account, a Cryptomus merchant) or the
// wallet balance. Methods are soft-deleted so a late callback still finds the
// method its notify URL names.
type PaymentMethod struct {
	Id          int    `json:"id"`
	Name        string `json:"name" gorm:"type:varchar(100);not null;default:''"`
	Platform    string `json:"platform" gorm:"<-:create;type:varchar(32);not null"`
	Icon        string `json:"icon" gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	Sort        int    `json:"sort" gorm:"not null;default:0"`
	Enabled     bool   `json:"enabled" gorm:"not null"`
	// Domain is the base URL gateway callbacks are sent to; empty selects
	// the site address.
	Domain string `json:"domain" gorm:"type:varchar(255);not null;default:''"`
	// Config is the platform's JSON configuration. It holds secrets and is
	// never serialized as is.
	Config string `json:"-" gorm:"type:text;not null;default:''"`
	// The fee is FeeRateBp basis points of the price and/or FeeFixed
	// hundredths of the site currency, as FeeMode selects.
	FeeMode   int   `json:"fee_mode" gorm:"not null;default:0"`
	FeeRateBp int64 `json:"fee_rate_bp" gorm:"not null;default:0"`
	FeeFixed  int64 `json:"fee_fixed" gorm:"not null;default:0"`
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
