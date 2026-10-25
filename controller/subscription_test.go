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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A plan is charged in hundredths of the site currency, so a price with more
// decimal places is refused when the plan is saved, not when a buyer orders it.
func TestSubscriptionPlanPriceAllowsTwoDecimalPlaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pgtest.Reset(t, model.DB)

	router := gin.New()
	router.POST("/plans", AdminCreateSubscriptionPlan)
	router.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
	type saveResponse struct {
		Success    bool   `json:"success"`
		MessageKey string `json:"message_key"`
	}
	save := func(method, path, price string) saveResponse {
		recorder := httptest.NewRecorder()
		body := `{"plan":{"title":"Pro","total_amount":1000,"price_amount":` + price + `}}`
		router.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		require.Equal(t, http.StatusOK, recorder.Code)
		var response saveResponse
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		return response
	}
	const refusal = "The plan price must be an amount with at most two decimal places"

	assert.Equal(t, saveResponse{MessageKey: refusal}, save(http.MethodPost, "/plans", "9.999"))
	var count int64
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Count(&count).Error)
	assert.Zero(t, count)

	require.True(t, save(http.MethodPost, "/plans", "9.99").Success)
	var plan model.SubscriptionPlan
	require.NoError(t, model.DB.First(&plan).Error)
	assert.Equal(t, 9.99, plan.PriceAmount)
	planPath := "/plans/" + strconv.Itoa(plan.Id)

	assert.Equal(t, saveResponse{MessageKey: refusal}, save(http.MethodPut, planPath, "19.995"))
	require.NoError(t, model.DB.First(&plan, plan.Id).Error)
	assert.Equal(t, 9.99, plan.PriceAmount)

	require.True(t, save(http.MethodPut, planPath, "19.99").Success)
	require.NoError(t, model.DB.First(&plan, plan.Id).Error)
	assert.Equal(t, 19.99, plan.PriceAmount)
}

// Saving or switching a plan that does not exist fails instead of reporting
// success for an update that changed nothing.
func TestUpdatingMissingSubscriptionPlanFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pgtest.Reset(t, model.DB)

	router := gin.New()
	router.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
	router.PATCH("/plans/:id", AdminUpdateSubscriptionPlanStatus)
	tests := []struct {
		method string
		body   string
	}{
		{http.MethodPut, `{"plan":{"title":"Pro","total_amount":1000,"price_amount":9.99}}`},
		{http.MethodPatch, `{"enabled":false}`},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, "/plans/999", strings.NewReader(test.body)))
			require.Equal(t, http.StatusOK, recorder.Code)
			var response common.ApiResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, "Subscription plan does not exist", response.MessageKey)
		})
	}
}
