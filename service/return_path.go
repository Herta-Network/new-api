package service

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// ConsoleURL returns the address of a console page, such as a payment's
// return page or the link in an email: suffix on the console address.
func ConsoleURL(suffix string) string {
	return strings.TrimRight(system_setting.ConsoleAddress(), "/") + suffix
}
