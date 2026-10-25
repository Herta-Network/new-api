package controller

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The setup wizard runs on the console, which calls the backend from its own
// origin; setup records that origin as the Frontend Address. A fresh system
// has no passkeys, so the default server address is not kept as a trusted
// passkey domain the way a later change of the setting keeps the old one.
func TestSetupRecordsTheConsoleOriginAsTheFrontendAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	previousSetup := constant.Setup
	previousServerAddress, previousFrontendAddress := system_setting.ServerAddress, system_setting.FrontendAddress
	previousPasskeys := *system_setting.GetPasskeySettings()
	previousSelfUse, previousDemo := operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		system_setting.ServerAddress, system_setting.FrontendAddress = previousServerAddress, previousFrontendAddress
		common.OptionMapRWMutex.Unlock()
		constant.Setup = previousSetup
		*system_setting.GetPasskeySettings() = previousPasskeys
		operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = previousSelfUse, previousDemo
	})

	for _, test := range []struct {
		name            string
		origin          string
		frontendAddress string
	}{
		{name: "browser origin", origin: "https://App.example.com/", frontendAddress: "https://app.example.com"},
		{name: "no origin", origin: "", frontendAddress: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			pgtest.Reset(t, model.DB)
			constant.Setup = false
			system_setting.ServerAddress, system_setting.FrontendAddress = "http://localhost:3000", ""
			*system_setting.GetPasskeySettings() = system_setting.PasskeySettings{}
			router := gin.New()
			router.POST("/api/setup", PostSetup)

			request := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"username":"root","password":"setup-password-123","confirmPassword":"setup-password-123"}`))
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			require.Contains(t, response.Body.String(), `"success":true`)
			assert.True(t, constant.Setup)
			assert.Equal(t, test.frontendAddress, system_setting.FrontendAddress)
			var stored []model.Option
			require.NoError(t, model.DB.Where("key = ?", "FrontendAddress").Find(&stored).Error)
			if test.frontendAddress == "" {
				assert.Empty(t, stored)
				return
			}
			require.Len(t, stored, 1)
			assert.Equal(t, test.frontendAddress, stored[0].Value)
			settings := system_setting.PasskeySettingsSnapshot()
			assert.Equal(t, test.frontendAddress, settings.Origins)
			assert.Equal(t, []string{"app.example.com"}, settings.RelyingPartyIDs())
		})
	}
}
