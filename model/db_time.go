package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
func GetDBTimestamp() int64 {
	return getDBTimestamp(DB)
}

func getDBTimestamp(tx *gorm.DB) int64 {
	var ts int64
	err := tx.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	if err != nil || ts <= 0 {
		return common.GetTimestamp()
	}
	return ts
}
