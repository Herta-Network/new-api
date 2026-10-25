package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

//	@Summary		Telegram 旧版登录（已停用）
//	@Description	始终返回 410 TELEGRAM_LEGACY_AUTH_REMOVED；Telegram 登录改用 POST /api/oauth/state 和 GET /api/oauth/telegram。
//	@Tags			第三方登录
//	@Failure		410	{object}	common.ApiResponse
//	@Router			/api/oauth/telegram/login [get]

//	@Summary		开始绑定 Telegram（已停用）
//	@Description	始终返回 410 TELEGRAM_LEGACY_AUTH_REMOVED；Telegram 绑定改用 POST /api/oauth/state 和 GET /api/oauth/telegram。
//	@Tags			第三方登录
//	@Failure		410	{object}	common.ApiResponse
//	@Security		UserAuth
//	@Router			/api/oauth/telegram/bind/start [post]

//	@Summary		Telegram 旧版绑定回调（已停用）
//	@Description	始终返回 410 TELEGRAM_LEGACY_AUTH_REMOVED。
//	@Tags			第三方登录
//	@Param			flow_token	path		string	true	"绑定流程的 flow_token"
//	@Failure		410			{object}	common.ApiResponse
//	@Router			/api/oauth/telegram/bind/{flow_token} [get]

// TelegramLegacyAuth retires the unsigned-flow widget endpoints. Existing
// Telegram bindings are used by the unified OAuth provider instead.
func TelegramLegacyAuth(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"success": false,
		"code":    "TELEGRAM_LEGACY_AUTH_REMOVED",
		"message": "Telegram login has changed. Reload the page and start Telegram OAuth again.",
	})
}
