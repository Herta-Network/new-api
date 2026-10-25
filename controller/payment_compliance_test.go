package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Confirming the compliance terms saves its five options together, and the
// payment settings reflect them.
func TestConfirmPaymentComplianceSavesEveryOption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pgtest.Reset(t, model.DB)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	paymentSetting := operation_setting.GetPaymentSetting()
	previous := *paymentSetting
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		paymentSetting.ComplianceConfirmed = previous.ComplianceConfirmed
		paymentSetting.ComplianceTermsVersion = previous.ComplianceTermsVersion
		paymentSetting.ComplianceConfirmedAt = previous.ComplianceConfirmedAt
		paymentSetting.ComplianceConfirmedBy = previous.ComplianceConfirmedBy
		paymentSetting.ComplianceConfirmedIP = previous.ComplianceConfirmedIP
	})
	paymentSetting.ComplianceConfirmed = false

	router := gin.New()
	router.POST("/confirm", func(c *gin.Context) {
		c.Set("id", 7)
		ConfirmPaymentCompliance(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(`{"confirmed":true}`))
	request.RemoteAddr = "203.0.113.9:4000"
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response common.ApiResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)

	var options []model.Option
	require.NoError(t, model.DB.Where("key LIKE ?", "payment_setting.compliance_%").Find(&options).Error)
	saved := make(map[string]string, len(options))
	for _, option := range options {
		saved[option.Key] = option.Value
	}
	assert.Equal(t, map[string]string{
		"payment_setting.compliance_confirmed":     "true",
		"payment_setting.compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
		"payment_setting.compliance_confirmed_at":  strconv.FormatInt(paymentSetting.ComplianceConfirmedAt, 10),
		"payment_setting.compliance_confirmed_by":  "7",
		"payment_setting.compliance_confirmed_ip":  "203.0.113.9",
	}, saved)
	assert.True(t, operation_setting.IsPaymentComplianceConfirmed())
	assert.Positive(t, paymentSetting.ComplianceConfirmedAt)
}
