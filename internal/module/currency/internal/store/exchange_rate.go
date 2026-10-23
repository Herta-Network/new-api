// Package store keeps the currency module's table: the stored live
// exchange rates.
package store

import (
	"errors"

	"github.com/QuantumNous/new-api/internal/module/currency/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetExchangeRate returns the stored rate of the pair, or nil when
// there is none.
func GetExchangeRate(from, to string) (*entity.ExchangeRate, error) {
	var rate entity.ExchangeRate
	err := model.DB.Where("from_currency = ? AND to_currency = ?", from, to).First(&rate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rate, nil
}

// SaveExchangeRate stores the rate of its pair, replacing the
// previous one.
func SaveExchangeRate(rate *entity.ExchangeRate) error {
	return model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "from_currency"}, {Name: "to_currency"}},
		DoUpdates: clause.AssignmentColumns([]string{"rate", "source", "fetched_at"}),
	}).Create(rate).Error
}
