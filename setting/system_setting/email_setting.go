package system_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// EmailSupportEmailOptionKey is the option of the support address, which
// must be an email address when it is set.
const EmailSupportEmailOptionKey = "email_setting.support_email"

// EmailSetting is what the site's emails say about the site beyond their
// own content. The layout shows each text in its footer only when it is
// set.
type EmailSetting struct {
	// CompanyName and CompanyAddress are the legal name and postal address
	// of the site's operator.
	CompanyName    string `json:"company_name"`
	CompanyAddress string `json:"company_address"`
	// SupportEmail is where readers write for help, since nobody reads
	// replies to the site's emails.
	SupportEmail string `json:"support_email"`
	// TopupReceiptEnabled sends the buyer a receipt when a top-up order is
	// credited.
	TopupReceiptEnabled bool `json:"topup_receipt_enabled"`
}

var emailSetting = EmailSetting{
	TopupReceiptEnabled: true,
}

func init() {
	config.GlobalConfig.Register("email_setting", &emailSetting)
}

func GetEmailSetting() *EmailSetting {
	return &emailSetting
}

// ValidateEmailSupportEmail checks the support address before it is saved:
// empty, or one email address.
func ValidateEmailSupportEmail(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := common.Validate.Var(value, "email"); err != nil {
		return common.NewMessage("Invalid email address")
	}
	return nil
}
