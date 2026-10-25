package entity

import (
	"errors"

	"gorm.io/gorm"
)

// BalancePaymentOptionID is the fixed payment option of the wallet balance,
// which subscriptions can be paid with. The balance payment method is its
// only channel.
const BalancePaymentOptionID = -1

// Fee modes of a payment option. The fee is charged on top of the order
// price and never changes the credited quota.
const (
	PaymentFeeNone         = 0
	PaymentFeeRate         = 1
	PaymentFeeFixed        = 2
	PaymentFeeRateAndFixed = 3
)

var ErrBalancePaymentOptionUndeletable = errors.New("the balance payment option cannot be deleted")

// PaymentOption is a way to pay as buyers see it, such as Alipay or card. Its
// payment methods are the channels that collect it: an order paid with the
// option is invoiced through whichever of them takes its amount, by priority
// and weight. The buyer pays the option's fee whatever channel collects.
type PaymentOption struct {
	Id          int    `json:"id"`
	Name        string `json:"name" gorm:"type:varchar(100);not null;default:''"`
	Icon        string `json:"icon" gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	Sort        int    `json:"sort" gorm:"not null;default:0"`
	Enabled     bool   `json:"enabled" gorm:"not null"`
	// The fee is FeeRateBp basis points of the price and/or FeeFixed
	// hundredths of the site currency, as FeeMode selects.
	FeeMode   int            `json:"fee_mode" gorm:"not null;default:0"`
	FeeRateBp int64          `json:"fee_rate_bp" gorm:"not null;default:0"`
	FeeFixed  int64          `json:"fee_fixed" gorm:"not null;default:0"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (o *PaymentOption) BeforeDelete(*gorm.DB) error {
	if o.Id == BalancePaymentOptionID {
		return ErrBalancePaymentOptionUndeletable
	}
	return nil
}
