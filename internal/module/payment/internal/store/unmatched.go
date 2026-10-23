package store

import (
	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm/clause"
)

// RecordUnmatched keeps the record of a payment, once.
func RecordUnmatched(record *entity.PaymentUnmatched) error {
	return model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error
}

// ListUnmatched lists the records, newest first; resolved selects
// resolved or open records, nil both.
func ListUnmatched(resolved *bool, pageInfo *common.PageInfo) ([]*entity.PaymentUnmatched, int64, error) {
	query := model.DB.Model(&entity.PaymentUnmatched{})
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
	var records []*entity.PaymentUnmatched
	err := query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&records).Error
	return records, total, err
}

// ResolveUnmatched marks an open record resolved and reports whether
// this call resolved it.
func ResolveUnmatched(id int, adminId int, note string) (bool, error) {
	result := model.DB.Model(&entity.PaymentUnmatched{}).
		Where("id = ? AND resolved_at = 0", id).
		Updates(map[string]any{"resolved_at": common.GetTimestamp(), "resolved_by": adminId, "resolve_note": note})
	return result.RowsAffected == 1, result.Error
}
