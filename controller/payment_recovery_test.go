package controller

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createRecoveryPlan(t *testing.T, f *providerFixture, limit int) {
	t.Helper()
	plan := model.SubscriptionPlan{Id: 1, Title: "Recurring", Currency: "USD", PriceAmount: 10, StripePriceId: "price_plan", DurationUnit: "month", DurationValue: 1, TotalAmount: 1000, MaxPurchasePerUser: limit, Enabled: true}
	require.NoError(t, f.db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(1)
}

func TestPaymentRecoveryPurchaseReservation(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	createRecoveryPlan(t, f, 1)
	f.start(t, true)
	first := f.orderNo
	f.start(t, true)
	assert.Equal(t, first, f.orderNo, "retry must resume the reserved checkout")
	var count int64
	require.NoError(t, f.db.Model(&model.PaymentOrder{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	_, err := model.AdminBindSubscription(1, 1, "must not consume a reserved slot")
	require.Error(t, err)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_reserved", "checkout.session.completed").Code)
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestPaymentRecoveryPaidOrderHonorsCheckout(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	createRecoveryPlan(t, f, 1)
	f.start(t, true)
	// A later administrative change must not turn a valid paid checkout into an
	// unfulfillable order. This also covers a late payment after a released slot.
	require.NoError(t, f.db.Create(&model.UserSubscription{UserId: 1, PlanId: 1, Status: "active", Source: "admin"}).Error)
	f.paid = true
	assert.Equal(t, http.StatusOK, f.webhook(t, "evt_late", "checkout.session.completed").Code)
	order, _, err := model.GetPaymentOrder(context.Background(), f.orderNo)
	require.NoError(t, err)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)
}

func TestPaymentRecoveryRenewalSurvivesDeletedPeriod(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	createRecoveryPlan(t, f, 1)
	f.start(t, true)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_first", "checkout.session.completed").Code)
	order, _, err := model.GetPaymentOrder(context.Background(), f.orderNo)
	require.NoError(t, err)
	_, err = model.AdminDeleteUserSubscription(order.UserSubscriptionID)
	require.NoError(t, err)
	f.recurring, f.invoiceID = true, "in_after_delete"
	assert.Equal(t, http.StatusOK, f.webhook(t, "evt_after_delete", "invoice.paid").Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.Where("trade_no = ?", f.invoiceID).First(&event).Error)
	assert.Equal(t, model.PaymentEventCompleted, event.Status)
}

func TestPaymentRecoveryInvalidatingRenewalStopsSeries(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	createRecoveryPlan(t, f, 1)
	f.start(t, true)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_first", "checkout.session.completed").Code)
	f.recurring, f.invoiceID = true, "in_second"
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_second", "invoice.paid").Code)
	var order model.PaymentOrder
	require.NoError(t, f.db.Where("provider_trade_no = ?", f.invoiceID).First(&order).Error)
	_, err := model.AdminInvalidateUserSubscription(order.UserSubscriptionID)
	require.NoError(t, err)
	_, err = model.AdminDeleteUserSubscription(order.UserSubscriptionID)
	require.NoError(t, err)
	f.invoiceID = "in_third"
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_third", "invoice.paid").Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.Where("trade_no = ?", f.invoiceID).First(&event).Error)
	assert.Equal(t, model.PaymentEventReview, event.Status)
	assert.Equal(t, "subscription_cancelled", event.LastError)
}

func TestPaymentRecoveryConcurrentCheckoutAndWalletReservation(t *testing.T) {
	f := newPaymentFixture(t)
	require.NoError(t, f.db.Create(&model.SubscriptionPlan{Id: 1, Title: "Limited", Currency: "USD", PriceAmount: 10, DurationUnit: "month", DurationValue: 1, Enabled: true, MaxPurchasePerUser: 1}).Error)
	var group sync.WaitGroup
	orders := make(chan string, 2)
	for range 2 {
		group.Go(func() {
			response := f.request(t, "POST", "/subscription/pay", map[string]any{"plan_id": 1, "payment_method": f.method.PublicType()})
			var result struct {
				Data map[string]string `json:"data"`
			}
			if err := common.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Error(err)
				return
			}
			orders <- result.Data["out_trade_no"]
		})
	}
	group.Wait()
	close(orders)
	var created []string
	for order := range orders {
		created = append(created, order)
	}
	require.Len(t, created, 2)
	require.NotEmpty(t, created[0])
	assert.Equal(t, created[0], created[1])
	other := model.PaymentMethod{Name: "Other merchant", Provider: payment.Epay, Enabled: true}
	require.NoError(t, model.SavePaymentMethod(&other, f.config))
	blocked := f.request(t, "POST", "/subscription/pay", map[string]any{"plan_id": 1, "payment_method": other.PublicType()})
	assert.Contains(t, blocked.Body.String(), "A payment for this plan is pending")
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 1).Update("quota", 10000).Error)
	require.Error(t, model.PurchaseSubscriptionWithBalance(1, 1))
	var user model.User
	require.NoError(t, f.db.First(&user, 1).Error)
	assert.Equal(t, 10000, user.Quota)
}

func TestPaymentRecoveryProviderFailureReleasesReservation(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	createRecoveryPlan(t, f, 1)
	f.start(t, true)
	first := f.orderNo
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_failed_checkout", "checkout.session.async_payment_failed").Code)
	f.start(t, true)
	assert.NotEqual(t, first, f.orderNo)
	f.paid = true
	require.Equal(t, http.StatusOK, f.webhook(t, "evt_replacement_checkout", "checkout.session.completed").Code)
	var count int64
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestPaymentRecoveryReviewedRetryConfirmsProviderAndCreditsOnce(t *testing.T) {
	f := newProviderFixture(t, payment.Cryptomus)
	f.start(t, false)
	f.cryptoStatus = "wrong_amount"
	require.Equal(t, http.StatusOK, f.webhook(t, "", "").Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.First(&event).Error)
	require.Equal(t, model.PaymentEventReview, event.Status)
	require.NoError(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "retry", "Provider investigation completed"))
	require.ErrorIs(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "resolve", "stale action"), model.ErrPaymentEventConflict)
	// Retrying a review does not assume payment; the provider still reports a problem.
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	assert.Zero(t, f.quota(t))
	require.NoError(t, f.db.First(&event, event.ID).Error)
	require.Equal(t, model.PaymentEventReview, event.Status)
	f.cryptoStatus = "paid"
	require.NoError(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "retry", "Provider now confirms full payment"))
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	require.NoError(t, service.ProcessPaymentEvent(context.Background(), event.ID))
	assert.Equal(t, 200, f.quota(t))
	require.NoError(t, f.db.First(&event, event.ID).Error)
	require.ErrorIs(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "retry", "already credited"), model.ErrPaymentEventConflict)
	var actions []model.PaymentEventAction
	require.NoError(t, f.db.Order("id ASC").Find(&actions).Error)
	require.Len(t, actions, 2)
	assert.Equal(t, 1, actions[0].ActorID)
	assert.Equal(t, "Provider now confirms full payment", actions[1].Reason)
}

func TestPaymentRecoveryResolveAndHoldPreventStaleWorkerSettlement(t *testing.T) {
	for _, action := range []string{"hold", "resolve"} {
		t.Run(action, func(t *testing.T) {
			f := newProviderFixture(t, payment.Stripe)
			f.start(t, false)
			f.paid, f.fail = true, true
			require.Equal(t, http.StatusServiceUnavailable, f.webhook(t, "evt_pending", "checkout.session.completed").Code)
			var event model.PaymentEvent
			require.NoError(t, f.db.First(&event).Error)
			require.NoError(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, action, "Handled by operator"))
			f.fail = false
			require.NoError(t, model.ProcessPaymentEvent(context.Background(), event.ID, &payment.Notice{OrderNo: f.orderNo, Paid: true, Subtotal: 300, Money: payment.Money{Amount: 300, Currency: "USD"}}))
			require.NoError(t, service.RetryPaymentEvents(context.Background()))
			assert.Zero(t, f.quota(t))
		})
	}
}

func TestPaymentRecoveryAutomaticRetriesAreBounded(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	f.start(t, false)
	f.paid, f.fail = true, true
	require.Equal(t, http.StatusServiceUnavailable, f.webhook(t, "evt_failure", "checkout.session.completed").Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.First(&event).Error)
	require.NoError(t, f.db.Model(&event).Update("attempts", 11).Error)
	require.Error(t, service.ProcessPaymentEvent(context.Background(), event.ID))
	require.NoError(t, f.db.First(&event, event.ID).Error)
	assert.Equal(t, model.PaymentEventReview, event.Status)
	assert.Equal(t, "retry_exhausted", event.LastError)
	assert.Equal(t, 12, event.Attempts)
}

func TestPaymentRecoveryRetryCannotOverrideInvalidPaymentFacts(t *testing.T) {
	f := newProviderFixture(t, payment.Stripe)
	f.start(t, false)
	f.paid, f.wrongPrice = true, true
	require.Equal(t, http.StatusBadRequest, f.webhook(t, "evt_wrong_price", "checkout.session.completed").Code)
	var event model.PaymentEvent
	require.NoError(t, f.db.First(&event).Error)
	require.Equal(t, model.PaymentEventRejected, event.Status)
	require.ErrorIs(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "retry", " "), model.ErrPaymentEventAction)
	require.NoError(t, model.ReviewPaymentEvent(context.Background(), event.ID, event.Revision, 1, "retry", "Recheck provider transaction"))
	require.NoError(t, service.RetryPaymentEvents(context.Background()))
	require.NoError(t, f.db.First(&event, event.ID).Error)
	assert.Equal(t, model.PaymentEventRejected, event.Status)
	assert.Zero(t, f.quota(t))
}

func TestPaymentRecoveryReviewAuthorizationAndProofBinding(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.PaymentEvent{}, &model.PaymentEventAction{}))
	event := model.PaymentEvent{EventKey: "review-auth", OrderNo: "review-order", Payload: "null", Status: model.PaymentEventReview}
	require.NoError(t, model.DB.Create(&event).Error)
	router := gin.New()
	router.POST("/api/option/payment-events/:id/review", middleware.RootAuth(), ReviewPaymentEvent)
	path := fmt.Sprintf("/api/option/payment-events/%d/review", event.ID)
	read, _ := createScopedAccessToken(t, user.Id, 0, "option:read")
	write, token := createScopedAccessToken(t, user.Id, 0, "option:write")
	expired, _ := createScopedAccessToken(t, user.Id, common.GetTimestamp()-1, "option:write")
	context, err := common.Marshal(service.PaymentEventReviewContext{EventID: event.ID, Revision: event.Revision, Action: "resolve", Reason: "External resolution ticket 123"})
	require.NoError(t, err)
	body := `{"revision":0,"action":"resolve","reason":"External resolution ticket 123"}`
	assert.Equal(t, http.StatusUnauthorized, accessTokenRequest(router, "POST", path, "", "", body).Code)
	assert.Equal(t, http.StatusForbidden, accessTokenRequest(router, "POST", path, read, "", body).Code)
	assert.Equal(t, http.StatusUnauthorized, accessTokenRequest(router, "POST", path, expired, "", body).Code)
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, "", body).Body.String(), "SECURITY_PROOF_REQUIRED")
	identity := service.AuthIdentity{UserID: user.Id, SessionID: model.AccessTokenSessionID(token.Id), UserAuthVersion: user.AuthVersion, SessionVersion: model.AccessTokenSessionVersion}
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopePaymentEventReview, Context: context}, service.VerificationMethodPassword)
	changed := `{"revision":0,"action":"retry","reason":"External resolution ticket 123"}`
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, proof, changed).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	changed = `{"revision":1,"action":"resolve","reason":"External resolution ticket 123"}`
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, proof, changed).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	changed = `{"revision":0,"action":"resolve","reason":"different decision"}`
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, proof, changed).Body.String(), "SECURITY_PROOF_CONTEXT_MISMATCH")
	response := accessTokenRequest(router, "POST", path, write, proof, body)
	assert.Contains(t, response.Body.String(), `"success":true`)
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, proof, body).Body.String(), "SECURITY_PROOF_CONSUMED")
	var actions []model.PaymentEventAction
	require.NoError(t, model.DB.Find(&actions).Error)
	require.Len(t, actions, 1)
	assert.Equal(t, user.Id, actions[0].ActorID)
	assert.NotContains(t, actions[0].Reason, proof)
	proof = issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopePaymentEventReview, Context: context}, service.VerificationMethodPassword)
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ? AND consumed_at IS NULL", model.AuthFlowPurposeSecurityProof).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	assert.Contains(t, accessTokenRequest(router, "POST", path, write, proof, body).Body.String(), "SECURITY_PROOF_EXPIRED")
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
	_, err = service.RequireVerificationMethod(identity, service.VerificationScopePaymentEventReview, service.VerificationMethodPassword)
	require.ErrorIs(t, err, service.ErrProofMethod)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleCommonUser).Error)
	assert.Equal(t, http.StatusForbidden, accessTokenRequest(router, "POST", path, write, "", body).Code)
}
