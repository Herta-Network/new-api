package controller

import (
	"bytes"
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/pkg/payment"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func confirmPaymentComplianceForTest(t *testing.T) {
	t.Helper()
	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed := paymentSetting.ComplianceConfirmed
	originalTermsVersion := paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = originalConfirmed
		paymentSetting.ComplianceTermsVersion = originalTermsVersion
	})
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
}

type paymentFixture struct {
	db     *gorm.DB
	router *gin.Engine
	method model.PaymentMethod
	config payment.Config
}

func newPaymentFixture(t *testing.T) *paymentFixture {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	db, err := testutil.OpenDB(t, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	originalDB, originalLogDB := model.DB, model.LOG_DB
	originalRedis, originalQuota := common.RedisEnabled, common.QuotaPerUnit
	originalPrice, originalRate, originalMin := operation_setting.Price, operation_setting.USDExchangeRate, operation_setting.MinTopUp
	originalAddress, originalCallback := system_setting.ServerAddress, operation_setting.CustomCallbackAddress
	model.DB, model.LOG_DB, common.RedisEnabled, common.QuotaPerUnit = db, db, false, 100
	operation_setting.Price, operation_setting.USDExchangeRate, operation_setting.MinTopUp = 7.3, 7.3, 1
	system_setting.ServerAddress, operation_setting.CustomCallbackAddress = "https://gateway.example.com", ""
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.QuotaPerUnit = originalDB, originalLogDB, originalRedis, originalQuota
		operation_setting.Price, operation_setting.USDExchangeRate, operation_setting.MinTopUp = originalPrice, originalRate, originalMin
		system_setting.ServerAddress, operation_setting.CustomCallbackAddress = originalAddress, originalCallback
		model.InvalidateSubscriptionPlanCache(1)
	})
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.PaymentMethod{}, &model.PaymentMethodVersion{}, &model.PaymentOrder{}, &model.PaymentEvent{}, &model.PaymentEventAction{}, &model.PaymentCustomer{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{}, &model.Log{}))
	}
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "payment-test", Group: "default", Status: common.UserStatusEnabled}).Error)
	config := payment.Config{Endpoint: "https://pay.example.com", MerchantID: "10001", Key: "test-merchant-key", PaymentType: "alipay"}
	method := model.PaymentMethod{Name: "Alipay", Provider: payment.Epay, Icon: "SiAlipay", Enabled: true}
	require.NoError(t, model.SavePaymentMethod(&method, config))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", 1); c.Next() })
	router.POST("/pay", RequestPayment)
	router.POST("/amount", RequestAmount)
	router.POST("/subscription/pay", SubscriptionRequestPayment)
	router.GET("/info", GetTopUpInfo)
	router.GET("/methods", ListPaymentMethods)
	router.POST("/methods", SavePaymentMethod)
	router.PUT("/methods/:id", SavePaymentMethod)
	router.DELETE("/methods/:id", DeletePaymentMethod)
	router.Any("/notify/:provider/:version_id", PaymentNotify)
	router.POST("/stripe/webhook/:method_id", PaymentNotify)
	router.GET("/return/:order_no", PaymentReturn)
	return &paymentFixture{db: db, router: router, method: method, config: config}
}

func (f *paymentFixture) request(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func (f *paymentFixture) checkout(t *testing.T, subscription bool) map[string]string {
	t.Helper()
	path := "/pay"
	body := map[string]any{"amount": 2, "payment_method": f.method.PublicType()}
	if subscription {
		path = "/subscription/pay"
		body["plan_id"] = 1
	}
	response := f.request(t, http.MethodPost, path, body)
	var result struct {
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
		URL     string            `json:"url"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
	require.Equal(t, "success", result.Message, response.Body.String())
	require.Equal(t, "https://pay.example.com/submit.php", result.URL)
	require.Equal(t, "https://gateway.example.com/api/payments/epay/notify/"+strconv.Itoa(f.method.VersionID), result.Data["notify_url"])
	return result.Data
}

func (f *paymentFixture) callback(t *testing.T, checkout map[string]string, changes map[string]string) url.Values {
	t.Helper()
	params := map[string]string{
		"pid": f.config.MerchantID, "type": f.config.PaymentType, "money": checkout["money"], "name": "Payment",
		"out_trade_no": checkout["out_trade_no"], "trade_no": "TRADE" + checkout["out_trade_no"], "trade_status": "TRADE_SUCCESS",
	}
	for key, value := range changes {
		params[key] = value
	}
	signed, err := epay.GenerateParamsWithError(params, f.config.Key)
	require.NoError(t, err)
	values := url.Values{}
	for key, value := range signed {
		values.Set(key, value)
	}
	return values
}

func (f *paymentFixture) notify(orderNo string, values url.Values) *httptest.ResponseRecorder {
	order, _, _ := model.GetPaymentOrder(context.Background(), orderNo)
	request := httptest.NewRequest(http.MethodPost, "/notify/epay/"+strconv.Itoa(order.VersionID), strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func TestPaymentGatewayRejectsIncorrectPaymentFacts(t *testing.T) {
	f := newPaymentFixture(t)
	checkout := f.checkout(t, false)
	require.Equal(t, "14.60", checkout["money"])
	assert.Equal(t, "alipay", model.GetTopUpByTradeNo(checkout["out_trade_no"]).PaymentMethod)
	for _, tc := range []struct {
		name    string
		changes map[string]string
	}{
		{"underpaid", map[string]string{"money": "0.01"}},
		{"overpaid", map[string]string{"money": "14.61"}},
		{"precision", map[string]string{"money": "14.600"}},
		{"exponent", map[string]string{"money": "1.46e1"}},
		{"negative", map[string]string{"money": "-14.60"}},
		{"zero", map[string]string{"money": "0.00"}},
		{"overflow", map[string]string{"money": "99999999999999999999"}},
		{"merchant", map[string]string{"pid": "other"}},
		{"payment type", map[string]string{"type": "wxpay"}},
		{"currency", map[string]string{"currency": "USD"}},
		{"order binding", map[string]string{"out_trade_no": "different"}},
		{"missing trade", map[string]string{"trade_no": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := f.notify(checkout["out_trade_no"], f.callback(t, checkout, tc.changes))
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, "fail", response.Body.String())
		})
	}
	for _, mode := range []string{"missing signature", "invalid signature", "duplicate", "ambiguous value"} {
		t.Run(mode, func(t *testing.T) {
			params := f.callback(t, checkout, nil)
			switch mode {
			case "missing signature":
				params.Del("sign")
			case "invalid signature":
				params.Set("sign", "bad")
			case "duplicate":
				params.Add("money", "0.01")
			case "ambiguous value":
				params.Set("name", "item&out_trade_no=different")
			}
			assert.Equal(t, http.StatusBadRequest, f.notify(checkout["out_trade_no"], params).Code)
		})
	}
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Zero(t, user.Quota)
	var events []model.PaymentEvent
	require.NoError(t, f.db.Find(&events).Error)
	require.Len(t, events, 2)
	for _, event := range events {
		assert.Equal(t, model.PaymentEventRejected, event.Status)
		assert.Equal(t, "verification_failed", event.LastError)
	}
	pending := f.callback(t, checkout, map[string]string{"trade_status": "WAIT_BUYER_PAY"})
	assert.Equal(t, http.StatusOK, f.notify(checkout["out_trade_no"], pending).Code)
	var pendingEvent model.PaymentEvent
	require.NoError(t, f.db.Where("status = ?", model.PaymentEventIgnored).First(&pendingEvent).Error)
	assert.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(checkout["out_trade_no"]).Status)
	// A subsequent correctly signed amount is not poisoned by earlier rejections.
	assert.Equal(t, http.StatusOK, f.notify(checkout["out_trade_no"], f.callback(t, checkout, nil)).Code)
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 200, user.Quota)
}

func TestPaymentGatewaySnapshotsAndConcurrentCallbacks(t *testing.T) {
	f := newPaymentFixture(t)
	checkout := f.checkout(t, false)
	original := f.method.VersionID
	rotated := f.config
	rotated.Key = "rotated-key"
	require.NoError(t, model.SavePaymentMethod(&f.method, rotated))
	require.NotEqual(t, original, f.method.VersionID)
	require.NoError(t, model.ArchivePaymentMethod(f.method.ID))
	common.QuotaPerUnit, operation_setting.Price = 500, 99
	response := f.request(t, http.MethodPost, "/pay", map[string]any{"amount": 2, "payment_method": f.method.PublicType()})
	assert.Contains(t, response.Body.String(), `"message":"error"`)
	params := f.callback(t, checkout, nil)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() { statuses <- f.notify(checkout["out_trade_no"], params).Code })
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		assert.Equal(t, http.StatusOK, status)
	}
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 200, user.Quota)
	var count int64
	require.NoError(t, f.db.Model(&model.PaymentEvent{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	var order model.PaymentOrder
	require.NoError(t, f.db.First(&order, "order_no = ?", checkout["out_trade_no"]).Error)
	assert.Equal(t, original, order.VersionID)
	assert.EqualValues(t, 1460, order.Amount)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)
	// Another authenticated provider trade cannot claim the already paid purchase.
	assert.Equal(t, http.StatusBadRequest, f.notify(order.OrderNo, f.callback(t, checkout, map[string]string{"trade_no": "SECOND"})).Code)
}

func TestPaymentGatewaySubscriptionSnapshotAndAmount(t *testing.T) {
	f := newPaymentFixture(t)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	// Settlement must use its transaction connection, including the DB timestamp.
	sqlDB.SetMaxOpenConns(1)
	plan := model.SubscriptionPlan{Id: 1, Title: "Original plan", PriceAmount: 10, Currency: "USD", DurationUnit: "month", DurationValue: 1, TotalAmount: 1234, Enabled: true}
	require.NoError(t, f.db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(1)
	checkout := f.checkout(t, true)
	require.Equal(t, "73.00", checkout["money"])
	require.NoError(t, f.db.Model(&plan).Updates(map[string]any{"price_amount": 99, "total_amount": 9999, "enabled": false}).Error)
	operation_setting.USDExchangeRate = 9
	model.InvalidateSubscriptionPlanCache(1)
	assert.Equal(t, http.StatusBadRequest, f.notify(checkout["out_trade_no"], f.callback(t, checkout, map[string]string{"money": "10.00"})).Code)
	var count int64
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.Zero(t, count)
	params := f.callback(t, checkout, nil)
	for range 2 {
		require.Equal(t, http.StatusOK, f.notify(checkout["out_trade_no"], params).Code)
	}
	var subscriptions []model.UserSubscription
	require.NoError(t, f.db.Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.EqualValues(t, 1234, subscriptions[0].AmountTotal)
	assert.Equal(t, common.TopUpStatusSuccess, model.GetSubscriptionOrderByTradeNo(checkout["out_trade_no"]).Status)
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Zero(t, user.Quota)
}

func TestPaymentGatewayDurableRecoveryAfterDatabaseFailure(t *testing.T) {
	f := newPaymentFixture(t)
	checkout := f.checkout(t, false)
	require.NoError(t, f.db.Exec(`CREATE FUNCTION reject_payment_credit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected payment failure'; END $$`).Error)
	require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_payment_credit BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_payment_credit()`).Error)
	response := f.notify(checkout["out_trade_no"], f.callback(t, checkout, nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.First(&event).Error)
	assert.Equal(t, model.PaymentEventPending, event.Status)
	assert.Equal(t, "verification_unavailable", event.LastError)
	assert.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(checkout["out_trade_no"]).Status)
	require.NoError(t, f.db.Exec(`DROP TRIGGER reject_payment_credit ON users`).Error)
	require.NoError(t, f.db.Model(&event).Update("next_attempt_at", 0).Error)
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	require.NoError(t, f.db.First(&event).Error)
	assert.Equal(t, model.PaymentEventCompleted, event.Status)
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 200, user.Quota)
}

func TestPaymentGatewayMethodManagementAndManualCompletion(t *testing.T) {
	f := newPaymentFixture(t)
	response := f.request(t, http.MethodGet, "/methods", nil)
	assert.NotContains(t, response.Body.String(), f.config.Key)
	assert.Contains(t, response.Body.String(), `"has_key":true`)
	previous := f.method
	changed := f.config
	changed.Key = ""
	require.NoError(t, model.SavePaymentMethod(&f.method, changed))
	require.ErrorIs(t, model.SavePaymentMethod(&previous, changed), model.ErrPaymentConfigChanged)
	checkout := f.checkout(t, false)
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(&model.PaymentMethod{}, &model.PaymentMethodVersion{}, &model.PaymentOrder{}, &model.PaymentEvent{}))
	}
	// Browser return cannot grant money, even when provided a valid signed callback.
	request := httptest.NewRequest(http.MethodGet, "/return/"+checkout["out_trade_no"]+"?"+f.callback(t, checkout, nil).Encode(), nil)
	returned := httptest.NewRecorder()
	f.router.ServeHTTP(returned, request)
	assert.Equal(t, http.StatusFound, returned.Code)
	assert.Contains(t, returned.Header().Get("Location"), "pay=pending")
	assert.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(checkout["out_trade_no"]).Status)
	require.NoError(t, model.ManualCompleteTopUp(checkout["out_trade_no"], "127.0.0.1"))
	require.Equal(t, http.StatusOK, f.notify(checkout["out_trade_no"], f.callback(t, checkout, nil)).Code)
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 200, user.Quota)
	// A trade cannot pay two distinct orders of the same merchant.
	other := f.checkout(t, false)
	params := f.callback(t, other, map[string]string{"trade_no": "TRADE" + checkout["out_trade_no"]})
	assert.Equal(t, http.StatusBadRequest, f.notify(other["out_trade_no"], params).Code)
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 200, user.Quota)
}

func TestPaymentGatewayAdminAuthorization(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PaymentMethod{}, &model.PaymentMethodVersion{}, &model.PaymentEvent{}))
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleRootUser).Error)
	readToken, _ := createScopedAccessToken(t, user.Id, 0, "option:read")
	writeToken, _ := createScopedAccessToken(t, user.Id, 0, "option:write")
	expiredToken, _ := createScopedAccessToken(t, user.Id, common.GetTimestamp()-10, "option:write")
	router := gin.New()
	group := router.Group("/api/option", middleware.RootAuth())
	group.GET("/payment-methods", ListPaymentMethods)
	group.POST("/payment-methods", SavePaymentMethod)
	group.GET("/payment-events", ListPaymentEvents)
	body := `{"name":"Test","provider":"epay","enabled":true,"config":{"endpoint":"https://pay.example.com","merchant_id":"10001","key":"must-not-appear-in-audit","payment_type":"alipay"}}`
	for _, tc := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"anonymous", "GET", "/payment-methods", "", http.StatusUnauthorized},
		{"read settings", "GET", "/payment-methods", readToken, http.StatusOK},
		{"read events", "GET", "/payment-events", readToken, http.StatusOK},
		{"read token cannot write", "POST", "/payment-methods", readToken, http.StatusForbidden},
		{"expired token", "POST", "/payment-methods", expiredToken, http.StatusUnauthorized},
		{"write settings", "POST", "/payment-methods", writeToken, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/option"+tc.path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, tc.status, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "must-not-appear-in-audit")
		})
	}
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleCommonUser).Error)
	request := httptest.NewRequest("GET", "/api/option/payment-methods", nil)
	request.Header.Set("Authorization", "Bearer "+readToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	var logs []model.AuditLog
	require.NoError(t, model.DB.Find(&logs).Error)
	require.NotEmpty(t, logs)
	encoded, err := common.Marshal(logs)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "must-not-appear-in-audit")
	assert.NotContains(t, string(encoded), writeToken)
}

func TestPaymentGatewaySeparatesMerchantAccounts(t *testing.T) {
	f := newPaymentFixture(t)
	first := f.checkout(t, false)
	require.Equal(t, http.StatusOK, f.notify(first["out_trade_no"], f.callback(t, first, map[string]string{"trade_no": "PROVIDER123"})).Code)
	oldKey := f.config.Key
	f.config.MerchantID, f.config.Key = "10002", "another-merchant-key"
	f.method.ID, f.method.VersionID, f.method.Name = 0, 0, "Second merchant"
	require.NoError(t, model.SavePaymentMethod(&f.method, f.config))
	second := f.checkout(t, false)
	f.config.Key = oldKey
	wrongKey := f.callback(t, second, map[string]string{"trade_no": "PROVIDER123"})
	assert.Equal(t, http.StatusBadRequest, f.notify(second["out_trade_no"], wrongKey).Code)
	f.config.Key = "another-merchant-key"
	// Provider trade numbers are unique within the merchant account, not globally.
	require.Equal(t, http.StatusOK, f.notify(second["out_trade_no"], f.callback(t, second, map[string]string{"trade_no": "PROVIDER123"})).Code)
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 400, user.Quota)
	methods, err := model.ListPaymentMethods(true)
	require.NoError(t, err)
	require.Len(t, methods, 2)
	assert.NotEqual(t, methods[0].PublicType(), methods[1].PublicType())
}
