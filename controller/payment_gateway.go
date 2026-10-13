package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ListPaymentMethods(c *gin.Context) {
	methods, err := model.ListPaymentMethods(false)
	if err != nil {
		common.ApiErrorT(c, "Failed to load payment methods")
		return
	}
	type item struct {
		model.PaymentMethodConfig
		WebhookURL string `json:"webhook_url"`
	}
	result := make([]item, 0, len(methods))
	for _, method := range methods {
		endpoint := strings.TrimRight(service.GetCallbackAddress(), "/") + "/api/payments/" + method.Provider + "/notify/" + strconv.Itoa(method.VersionID)
		if method.Provider == payment.Stripe {
			endpoint = strings.TrimRight(service.GetCallbackAddress(), "/") + "/api/payments/stripe/webhook/" + strconv.Itoa(method.ID)
		}
		result = append(result, item{PaymentMethodConfig: method, WebhookURL: endpoint})
	}

	common.ApiSuccess(c, result)
}

func SavePaymentMethod(c *gin.Context) {
	var input model.PaymentMethodConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if c.Request.Method == http.MethodPost {
		input.ID, input.VersionID = 0, 0
	} else {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 || input.ID != id {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	if err := model.SavePaymentMethod(&input.PaymentMethod, input.Config); err != nil {
		if errors.Is(err, model.ErrPaymentConfigChanged) {
			common.ApiErrorT(c, "Payment configuration changed. Reload and try again.")
			return
		}
		common.ApiErrorT(c, "Invalid payment configuration")
		return
	}
	common.ApiSuccess(c, input.PaymentMethod)
}

func DeletePaymentMethod(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.ArchivePaymentMethod(id); err != nil {
		common.ApiErrorT(c, "Failed to delete payment method")
		return
	}
	common.ApiSuccess(c, nil)
}

func ListPaymentEvents(c *gin.Context) {
	page := common.GetPageQuery(c)
	var events []model.PaymentEvent
	query := model.DB.Order("id DESC")
	if orderNo := c.Query("order_no"); orderNo != "" {
		query = query.Where("order_no = ?", orderNo)
	}
	if err := query.Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&events).Error; err != nil {
		common.ApiErrorT(c, "Failed to load payment events")
		return
	}
	common.ApiSuccess(c, events)
}

func PaymentNotify(c *gin.Context) {
	provider := c.Param("provider")
	identifier := c.Param("version_id")
	stableStripe := c.Param("method_id") != ""
	if stableStripe {
		provider = payment.Stripe
		identifier = c.Param("method_id")
	} else if provider == payment.Stripe {
		c.String(http.StatusNotFound, "fail")
		return
	}
	versionID, err := strconv.Atoi(identifier)
	if err != nil || versionID <= 0 {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	callback := payment.Callback{}
	if provider == payment.Epay {
		if err := c.Request.ParseForm(); err != nil {
			c.String(http.StatusBadRequest, "fail")
			return
		}
		callback.Params = make(map[string]string, len(c.Request.Form))
		for key, values := range c.Request.Form {
			if len(values) != 1 {
				c.String(http.StatusBadRequest, "fail")
				return
			}
			callback.Params[key] = values[0]
		}
		if len(callback.Params) == 0 {
			c.String(http.StatusBadRequest, "fail")
			return
		}
	} else {
		if c.Request.Method != http.MethodPost {
			c.String(http.StatusMethodNotAllowed, "fail")
			return
		}
		callback.Body, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.String(http.StatusBadRequest, "fail")
			return
		}
		callback.Signature = c.GetHeader("Stripe-Signature")
	}
	if stableStripe {
		err = service.HandleStripePaymentCallback(c.Request.Context(), versionID, callback, c.ClientIP())
	} else {
		err = service.HandlePaymentCallback(c.Request.Context(), provider, versionID, callback, c.ClientIP())
	}
	if errors.Is(err, payment.ErrInvalidPayment) {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if err != nil {
		c.String(http.StatusServiceUnavailable, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}

// A browser redirect never proves payment. Only the authenticated notification
// and its durable event can settle the purchase.
func PaymentReturn(c *gin.Context) {
	c.Redirect(http.StatusFound, paymentReturnPath("/wallet?pay=pending"))
}
