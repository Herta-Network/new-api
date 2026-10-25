package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
)

func TestConsoleURLUsesTheFrontendAddressWhenSet(t *testing.T) {
	previousAddress, previousFrontendAddress := system_setting.ServerAddress, system_setting.FrontendAddress
	t.Cleanup(func() {
		system_setting.ServerAddress, system_setting.FrontendAddress = previousAddress, previousFrontendAddress
	})
	system_setting.ServerAddress, system_setting.FrontendAddress = "https://dashboard.example.com/", ""
	assert.Equal(t, "https://dashboard.example.com/user/wallet", ConsoleURL("/user/wallet"))

	system_setting.FrontendAddress = "https://app.example.net/"
	assert.Equal(t, "https://app.example.net/user/wallet", ConsoleURL("/user/wallet"))
	system_setting.FrontendAddress = "  "
	assert.Equal(t, "https://dashboard.example.com/user/wallet", ConsoleURL("/user/wallet"))
}
