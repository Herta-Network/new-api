package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// ConversationSessionID returns the conversation session of the request, or ""
// when the conversation module's middleware did not run for it, recognition is
// off, the route has no conversation, or the body cannot be read. The module
// recognizes the session on the first call and later calls reuse it. It never
// fails the request.
func ConversationSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	resolve, ok := common.GetContextKeyType[func(*gin.Context) string](c, constant.ContextKeyConversationSessionID)
	if !ok || resolve == nil {
		return ""
	}
	return resolve(c)
}
