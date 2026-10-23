package entity

// PaymentExchangeRate is the last accepted rate of a currency pair: the
// price of one unit of FromCurrency in ToCurrency, as a plain decimal.
type PaymentExchangeRate struct {
	FromCurrency string `json:"from_currency" gorm:"type:varchar(8);primaryKey"`
	ToCurrency   string `json:"to_currency" gorm:"type:varchar(8);primaryKey"`
	Rate         string `json:"rate" gorm:"type:varchar(64);not null"`
	Source       string `json:"source" gorm:"type:varchar(32);not null;default:''"`
	FetchedAt    int64  `json:"fetched_at" gorm:"bigint;not null;default:0"`
}
