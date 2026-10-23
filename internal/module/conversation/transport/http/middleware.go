// Package handler wires conversation session recognition into the relay
// routes.
package handler

import (
	"github.com/QuantumNous/new-api/internal/module/conversation"
	"github.com/gin-gonic/gin"
)

// ConversationSession lets chat, responses, messages and Gemini requests have
// a conversation session that channel affinity and channels can ask for
// through service.ConversationSessionID, and stores what each successful
// response teaches about it. It must run before Distribute, which selects the
// channel. With recognition off it only checks the setting.
func ConversationSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !conversation.Begin(c) {
			c.Next()
			return
		}
		c.Next()
		conversation.Finish(c)
	}
}
