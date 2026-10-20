package middleware

import (
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ConversationSession lets chat, responses, messages and Gemini requests have
// a conversation session that channel affinity and channels can ask for, and
// stores what each successful response teaches about it. It must run before
// Distribute, which selects the channel. With recognition off it only checks
// the setting.
func ConversationSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !service.BeginConversationSession(c) {
			c.Next()
			return
		}
		c.Next()
		service.FinishConversationSession(c)
	}
}
