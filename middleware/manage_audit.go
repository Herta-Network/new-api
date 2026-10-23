package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// RecordManageAudit records an administrator's operation as an audit log
// owned by the operator, for the handlers of internal/module, which cannot
// use the helpers of package controller. content is the English text of
// the operation; params are the language-neutral parameters the web
// console renders it with. The request is marked logged, so the admin
// audit fallback skips it.
func RecordManageAudit(c *gin.Context, action string, content string, params map[string]any) {
	operatorId := c.GetInt("id")
	operatorRole := c.GetInt("role")
	adminInfo := &model.AuditAdminInfo{
		AdminID:       operatorId,
		AdminUsername: c.GetString("username"),
		AdminRole:     operatorRole,
		AuthMethod:    auditAuthMethod(c),
	}
	model.RecordOperationAuditLog(operatorId, operatorRole, content, c.ClientIP(), action, params, adminInfo, nil, c)
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
}
