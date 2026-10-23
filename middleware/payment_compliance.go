package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// PaymentCompliance refuses a request while the administrator has not
// confirmed the payment compliance terms.
func PaymentCompliance() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !operation_setting.IsPaymentComplianceConfirmed() {
			common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
			c.Abort()
			return
		}
		c.Next()
	}
}
