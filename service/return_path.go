package service

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Console pages that emails link to.
const (
	// AccountSecurityPath is the user's account security settings.
	AccountSecurityPath = "/user/settings/security"
	// WalletOrdersPath lists the user's payment orders.
	WalletOrdersPath = "/user/wallet/orders"
)

// ConsoleURL returns the address of a console page, such as a payment's
// return page or the link in an email: suffix on the console address.
func ConsoleURL(suffix string) string {
	return strings.TrimRight(system_setting.ConsoleAddress(), "/") + suffix
}
