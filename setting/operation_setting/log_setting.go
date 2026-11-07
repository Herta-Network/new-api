package operation_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	LogMaxRangeDaysOptionKey  = "log_setting.max_range_days"
	LogRetentionDaysOptionKey = "log_setting.retention_days"

	// maxLogRetentionDays bounds the setting so the cutoff it gives never
	// overflows a Unix timestamp.
	maxLogRetentionDays = 36500
)

// LogSetting limits how far regular users can reach into usage logs.
type LogSetting struct {
	// UserExportEnabled lets regular users export their own usage logs.
	// Administrators can always export.
	UserExportEnabled bool `json:"user_export_enabled"`
	// MaxRangeDays is the longest time range a user can search in their logs
	// and dashboard, and the longest range any export can cover.
	MaxRangeDays int `json:"max_range_days"`
	// RetentionDays is how many days usage logs and finished async task
	// records are kept. Once a day the master node deletes the ones older
	// than that; 0 keeps them until an administrator deletes them.
	RetentionDays int `json:"retention_days"`
}

var logSetting = LogSetting{
	UserExportEnabled: true,
	MaxRangeDays:      365,
	RetentionDays:     0,
}

func init() {
	config.GlobalConfig.Register("log_setting", &logSetting)
}

func GetLogSetting() *LogSetting {
	return &logSetting
}

func (s *LogSetting) MaxRangeSeconds() int64 {
	return int64(max(s.MaxRangeDays, 1)) * 24 * 60 * 60
}

func ValidateLogMaxRangeDays(value string) error {
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 {
		return common.NewMessage("The longest log time range must be at least 1 day")
	}
	return nil
}

func ValidateLogRetentionDays(value string) error {
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > maxLogRetentionDays {
		return common.NewMessage("Log retention must be a whole number of days from 0 to 36500")
	}
	return nil
}
