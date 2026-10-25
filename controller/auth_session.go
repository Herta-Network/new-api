package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// @Summary		续期登录
// @Description	凭 new_api_refresh Cookie（HttpOnly、SameSite=Strict、Path=/api/user/auth）轮换 Refresh Token 并签发新的访问令牌；Cookie 安全模式下还校验请求的 Origin 或 Referer。
// @Description	X-Auth-Session 与 Cookie 所属会话不一致时返回 409 AUTH_SESSION_MISMATCH；Refresh Token 无效或会话已撤销时返回 401 并清除 Cookie。
// @Tags			认证
// @Param			X-Auth-Session	header		string	false	"客户端内存中的会话 sid"
// @Success		200				{object}	common.ApiResponse{data=loginResult}
// @Failure		401				{object}	common.ApiResponse
// @Failure		409				{object}	common.ApiResponse
// @Router			/api/user/auth/refresh [post]
func RefreshAuth(c *gin.Context) {
	setAuthNoStore(c)
	rawRefreshToken, err := c.Cookie(service.RefreshCookieName)
	if err != nil || rawRefreshToken == "" {
		service.ClearRefreshCookie(c)
		writeAuthSessionError(c, service.ErrRefreshTokenInvalid)
		return
	}
	bundle, user, err := service.RefreshLoginSession(rawRefreshToken, c.GetHeader("X-Auth-Session"), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, service.ErrRefreshTokenInvalid) || errors.Is(err, service.ErrLoginSessionRevoked) {
			service.ClearRefreshCookie(c)
		}
		writeAuthSessionError(c, err)
		return
	}
	service.WriteRefreshCookie(c, bundle.RefreshToken)
	common.ApiSuccess(c, loginResult{AuthBundle: bundle, User: buildSelfUserData(user)})
}

type logoutResult struct {
	RevokedSID    string `json:"revoked_sid"`
	CookieCleared bool   `json:"cookie_cleared"`
}

// @Summary		登出
// @Description	撤销 new_api_refresh Cookie 所属的登录会话并清除 Cookie；Cookie 安全模式下还校验请求的 Origin 或 Referer。
// @Description	同时带有效的 Authorization: Bearer <访问令牌> 时撤销该令牌所属会话，data 为被撤销的 sid 和是否清除了 Cookie；否则不返回 data。X-Auth-Session 与 Cookie 或访问令牌的会话不一致时返回 409 AUTH_SESSION_MISMATCH。
// @Tags			认证
// @Param			X-Auth-Session	header		string	false	"客户端内存中的会话 sid"
// @Success		200				{object}	common.ApiResponse{data=logoutResult}
// @Failure		409				{object}	common.ApiResponse
// @Router			/api/user/auth/logout [post]
func AuthLogout(c *gin.Context) {
	setAuthNoStore(c)
	expectedSID := strings.TrimSpace(c.GetHeader("X-Auth-Session"))
	rawRefreshToken, cookieErr := c.Cookie(service.RefreshCookieName)
	cookieSID, hasCookieSID := service.RefreshTokenSID(rawRefreshToken)
	if expectedSID != "" && cookieErr == nil && hasCookieSID && cookieSID != expectedSID {
		writeAuthSessionError(c, service.ErrLoginSessionMismatch)
		return
	}

	if rawAccessToken, ok := dashboardBearer(c.GetHeader("Authorization")); ok {
		if identity, err := service.ParseAccessToken(rawAccessToken); err == nil {
			if expectedSID != "" && expectedSID != identity.SessionID {
				writeAuthSessionError(c, service.ErrLoginSessionMismatch)
				return
			}
			if _, err := model.RevokeUserSession(identity.UserID, identity.SessionID, "logout"); err != nil {
				writeAuthSessionError(c, err)
				return
			}
			cookieCleared := false
			if cookieErr == nil && hasCookieSID && cookieSID == identity.SessionID {
				if err := service.RevokeByRefreshToken(rawRefreshToken, identity.SessionID, "logout"); err != nil {
					writeAuthSessionError(c, err)
					return
				}
				service.ClearRefreshCookie(c)
				cookieCleared = true
			}
			common.ApiSuccess(c, logoutResult{RevokedSID: identity.SessionID, CookieCleared: cookieCleared})
			return
		}
	}
	if cookieErr != nil || rawRefreshToken == "" {
		service.ClearRefreshCookie(c)
		c.JSON(http.StatusOK, common.ApiResponse{Success: true})
		return
	}
	if err := service.RevokeByRefreshToken(rawRefreshToken, expectedSID, "logout"); err != nil {
		writeAuthSessionError(c, err)
		return
	}
	service.ClearRefreshCookie(c)
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
}

// @Summary		获取登录会话
// @Description	列出当前鉴权版本的有效登录会话，当前会话在前，最多 100 条；仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
// @Tags			登录会话
// @Success		200	{object}	common.ApiResponse{data=[]service.LoginSessionView}
// @Security		UserAuth
// @Router			/api/user/sessions [get]
func GetLoginSessions(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	sessions, err := service.ListLoginSessions(identity.UserID, identity.SessionID)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	common.ApiSuccess(c, sessions)
}

type revokedLoginSession struct {
	RevokedSID string `json:"revoked_sid"`
	// Current reports that the revoked session is the caller's own.
	Current bool `json:"current"`
}

// @Summary		撤销登录会话
// @Description	可以撤销当前会话；撤销的是 Refresh Cookie 所属会话时同时清除 Cookie。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
// @Tags			登录会话
// @Param			sid	path		string	true	"会话 sid"
// @Success		200	{object}	common.ApiResponse{data=revokedLoginSession}
// @Failure		404	{object}	common.ApiResponse
// @Security		UserAuth
// @Router			/api/user/sessions/{sid} [delete]
func DeleteLoginSession(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	sid := strings.TrimSpace(c.Param("sid"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "AUTH_SESSION_ID_REQUIRED", "message": "session id is required"})
		return
	}
	revoked, err := model.RevokeUserSession(identity.UserID, sid, "user_revoked")
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	if !revoked {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "AUTH_SESSION_NOT_FOUND", "message": "session not found"})
		return
	}
	if rawRefreshToken, cookieErr := c.Cookie(service.RefreshCookieName); cookieErr == nil {
		cookieSID, ok := service.RefreshTokenSID(rawRefreshToken)
		if ok && cookieSID == sid {
			service.ClearRefreshCookie(c)
		}
	}
	common.ApiSuccess(c, revokedLoginSession{RevokedSID: sid, Current: sid == identity.SessionID})
}

type revokedLoginSessions struct {
	RevokedCount int64 `json:"revoked_count"`
}

// @Summary		撤销其他登录会话
// @Description	保留当前会话，撤销其余会话，包括旧鉴权版本残留的会话；仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
// @Tags			登录会话
// @Success		200	{object}	common.ApiResponse{data=revokedLoginSessions}
// @Security		UserAuth
// @Router			/api/user/sessions/revoke-others [post]
func RevokeOtherLoginSessions(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	count, err := model.RevokeOtherUserSessions(identity.UserID, identity.SessionID, "user_revoked_others")
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	common.ApiSuccess(c, revokedLoginSessions{RevokedCount: count})
}

func requireBrowserSession(c *gin.Context) (service.AuthIdentity, bool) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "AUTH_SESSION_REQUIRED",
			"message": "a dashboard login session is required",
		})
		return service.AuthIdentity{}, false
	}
	return identity, true
}

func writeAuthSessionError(c *gin.Context, err error) {
	status, code := service.AuthSessionErrorCode(err)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status, code = http.StatusUnauthorized, "AUTH_UNAUTHORIZED"
	}
	if status == http.StatusInternalServerError {
		// The response body only carries the generic AUTH_INTERNAL_ERROR
		// code; without this log the underlying Redis/database/session
		// failure is indistinguishable from the client side.
		logger.LogError(c.Request.Context(), common.LogText("auth session internal error (%s %s): %v", c.Request.Method, c.Request.URL.Path, err))
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": http.StatusText(status)})
}

func setAuthNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
}

// sessionRotation carries the access token and login session that replace
// the current ones after a security change. It is empty when the change came
// through an access token, which has no browser session to rotate.
type sessionRotation struct {
	*service.AuthBundle
}

func dashboardBearer(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
