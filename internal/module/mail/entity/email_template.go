package entity

// EmailTemplate is an administrator's version of one email in one backend
// language. Without a row the email uses its built-in template.
type EmailTemplate struct {
	Id        int    `json:"id"`
	Kind      string `json:"kind" gorm:"type:varchar(32);not null;uniqueIndex:idx_email_templates_kind_language,priority:1"`
	Language  string `json:"language" gorm:"type:varchar(16);not null;uniqueIndex:idx_email_templates_kind_language,priority:2"`
	Subject   string `json:"subject" gorm:"type:text;not null"`
	Body      string `json:"body" gorm:"type:text;not null"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}
