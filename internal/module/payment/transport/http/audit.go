package handler

import (
	"fmt"
	"os"

	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// auditContentTemplates are the English texts of the payment audit
// actions; ${key} takes the parameter of that name.
var auditContentTemplates = map[string]string{
	"payment.order_complete":    "Marked payment order ${order_no} as paid: ${note}",
	"payment.unmatched_resolve": "Resolved unmatched payment ${id}: ${note}",
	"payment.method_create":     "Created payment method ${name} (${platform}, ID: ${id})",
	"payment.method_update":     "Updated payment method ${name} (${platform}, ID: ${id})",
}

// recordAudit records an administrator's payment operation with its
// parameters.
func recordAudit(c *gin.Context, action string, params map[string]any) {
	content := os.Expand(auditContentTemplates[action], func(key string) string {
		if value, ok := params[key]; ok {
			return fmt.Sprintf("%v", value)
		}
		return ""
	})
	middleware.RecordManageAudit(c, action, content, params)
}
