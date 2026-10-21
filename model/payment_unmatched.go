package model

import (
	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm/clause"
)

// PaymentUnmatched records money a gateway confirmed that could not settle
// its order: a second payment of a paid order, an amount that differs from
// the expectation, a fulfilment that failed. The callback is still
// acknowledged so the gateway stops retrying; an operator refunds or
// fulfils by hand and resolves the record. One payment is recorded once.
type PaymentUnmatched struct {
	Id              int    `json:"id"`
	Platform        string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_payment_unmatched_payment,priority:1"`
	TradeNo         string `json:"trade_no" gorm:"type:varchar(255);not null;uniqueIndex:idx_payment_unmatched_payment,priority:2"`
	OrderNo         string `json:"order_no" gorm:"type:varchar(64);not null;uniqueIndex:idx_payment_unmatched_payment,priority:3"`
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

// RecordPaymentUnmatched keeps the record of a payment, once.
func RecordPaymentUnmatched(record *PaymentUnmatched) error {
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error
}

// ListPaymentUnmatched lists the records, newest first; resolved selects
// resolved or open records, nil both.
func ListPaymentUnmatched(resolved *bool, pageInfo *common.PageInfo) ([]*PaymentUnmatched, int64, error) {
	query := DB.Model(&PaymentUnmatched{})
	if resolved != nil {
		if *resolved {
			query = query.Where("resolved_at > 0")
		} else {
			query = query.Where("resolved_at = 0")
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []*PaymentUnmatched
	err := query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&records).Error
	return records, total, err
}

// ResolvePaymentUnmatched marks an open record resolved and reports whether
// this call resolved it.
func ResolvePaymentUnmatched(id int, adminId int, note string) (bool, error) {
	result := DB.Model(&PaymentUnmatched{}).
		Where("id = ? AND resolved_at = 0", id).
		Updates(map[string]any{"resolved_at": common.GetTimestamp(), "resolved_by": adminId, "resolve_note": note})
	return result.RowsAffected == 1, result.Error
}
