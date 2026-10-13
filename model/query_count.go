package model

import "gorm.io/gorm"

// countMatchingRowsUpTo limits the rows fed to the aggregate, not its single
// result row. NewDB preserves the transaction/context without leaking filters
// onto the outer count or mutating the caller's subsequent list query.
func countMatchingRowsUpTo(query *gorm.DB, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	matches := query.Session(&gorm.Session{}).Select("1").Limit(limit)
	var count int64
	err := query.Session(&gorm.Session{NewDB: true}).Table("(?) AS matched_rows", matches).Count(&count).Error
	return count, err
}
