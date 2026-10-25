// Package store keeps the administrators' email templates.
package store

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/mail/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Templates returns the administrators' templates of lang among kinds,
// keyed by kind.
func Templates(db *gorm.DB, lang string, kinds ...string) (map[string]entity.EmailTemplate, error) {
	var rows []entity.EmailTemplate
	if err := db.Where("language = ? AND kind IN ?", lang, kinds).Find(&rows).Error; err != nil {
		return nil, err
	}
	templates := make(map[string]entity.EmailTemplate, len(rows))
	for _, row := range rows {
		templates[row.Kind] = row
	}
	return templates, nil
}

// AllTemplates returns every administrator's template.
func AllTemplates(db *gorm.DB) ([]entity.EmailTemplate, error) {
	var rows []entity.EmailTemplate
	err := db.Order("kind, language").Find(&rows).Error
	return rows, err
}

// SaveTemplate creates or replaces the template of kind in lang.
func SaveTemplate(db *gorm.DB, kind, lang, subject, body string) error {
	row := entity.EmailTemplate{Kind: kind, Language: lang, Subject: subject, Body: body, UpdatedAt: common.GetTimestamp()}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}, {Name: "language"}},
		DoUpdates: clause.AssignmentColumns([]string{"subject", "body", "updated_at"}),
	}).Create(&row).Error
}

// DeleteTemplate removes the template of kind in lang, so the built-in one
// applies again.
func DeleteTemplate(db *gorm.DB, kind, lang string) error {
	return db.Where("kind = ? AND language = ?", kind, lang).Delete(&entity.EmailTemplate{}).Error
}
