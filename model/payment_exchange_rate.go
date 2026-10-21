package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentExchangeRate is the last accepted rate of a currency pair: the
// price of one unit of FromCurrency in ToCurrency, as a plain decimal.
type PaymentExchangeRate struct {
	FromCurrency string `json:"from_currency" gorm:"type:varchar(8);primaryKey"`
	ToCurrency   string `json:"to_currency" gorm:"type:varchar(8);primaryKey"`
	Rate         string `json:"rate" gorm:"type:varchar(64);not null"`
	Source       string `json:"source" gorm:"type:varchar(32);not null;default:''"`
	FetchedAt    int64  `json:"fetched_at" gorm:"bigint;not null;default:0"`
}

// GetPaymentExchangeRate returns the stored rate of the pair, or nil when
// there is none.
func GetPaymentExchangeRate(from, to string) (*PaymentExchangeRate, error) {
	var rate PaymentExchangeRate
	err := DB.Where("from_currency = ? AND to_currency = ?", from, to).First(&rate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rate, nil
}

// SavePaymentExchangeRate stores the rate of its pair, replacing the
// previous one.
func SavePaymentExchangeRate(rate *PaymentExchangeRate) error {
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "from_currency"}, {Name: "to_currency"}},
		DoUpdates: clause.AssignmentColumns([]string{"rate", "source", "fetched_at"}),
	}).Create(rate).Error
}
