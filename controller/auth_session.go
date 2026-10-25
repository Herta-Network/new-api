package controller

import (
	"errors"
	"io"
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

// authRefreshRequest carries the refresh token the console keeps.
type authRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// requestRefreshToken returns the refresh token of a refresh or logout
// request: the one in the body, or the legacy refresh cookie of a session
// signed in before the console kept the token itself. The cookie is accepted
// only from the console's origin, like its origin check did, and deleted from
// the browser either way.
func requestRefreshToken(c *gin.Context) (string, error) {
	var request authRefreshRequest
	if c.Request.ContentLength != 0 {
		if err := common.DecodeJson(c.Request.Body, &request); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	legacyToken := service.LegacyRefreshToken(c)
	service.ClearLegacyRefreshCookies(c)
	if token := strings.TrimSpace(request.RefreshToken); token != "" {
		return token, nil
	}
	if legacyToken != "" && middleware.IsConsoleOrigin(c.Request) {
		return legacyToken, nil
	}
	return "", nil
}

// @Summary		续期登录
// @Description	用请求体里的 refresh_token 换取新的访问令牌和新的 refresh_token；旧的 refresh_token 随即失效，30 秒后再次使用会撤销整个会话。前端须保存返回的 refresh_token 供下次续期。
// @Description	升级前登录的会话可以不带请求体，改用旧的 new_api_refresh Cookie 续期一次（仅限控制台来源的请求），响应会删除该 Cookie。X-Auth-Session 与 refresh_token 所属会话不一致时返回 409 AUTH_SESSION_MISMATCH；refresh_token 无效或会话已撤销时返回 401。
// @Tags			认证
// @Param			X-Auth-Session	header		string				false	"客户端内存中的会话 sid"
// @Param			body			body		authRefreshRequest	false	"refresh_token"
// @Success		200				{object}	common.ApiResponse{data=loginResult}
// @Failure		401				{object}	common.ApiResponse
// @Failure		409				{object}	common.ApiResponse
// @Router			/api/user/auth/refresh [post]
func RefreshAuth(c *gin.Context) {
	setAuthNoStore(c)
	rawRefreshToken, err := requestRefreshToken(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if rawRefreshToken == "" {
		writeAuthSessionError(c, service.ErrRefreshTokenInvalid)
		return
	}
	bundle, user, err := service.RefreshLoginSession(rawRefreshToken, c.GetHeader("X-Auth-Session"), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	common.ApiSuccess(c, loginResult{AuthBundle: bundle, User: buildSelfUserData(user)})
}

type logoutResult struct {
	RevokedSID string `json:"revoked_sid"`
}

// @Summary		登出
// @Description	撤销 Authorization: Bearer <访问令牌> 所属的登录会话；没有有效访问令牌时改为撤销请求体里 refresh_token 所属的会话。前端随后应删除自己保存的令牌。
// @Description	撤销了访问令牌所属会话时 data 为被撤销的 sid，否则不返回 data。X-Auth-Session 与访问令牌或 refresh_token 的会话不一致时返回 409 AUTH_SESSION_MISMATCH。升级前登录的会话也可以凭旧的 new_api_refresh Cookie 登出（仅限控制台来源的请求），响应会删除该 Cookie。
// @Tags			认证
// @Param			X-Auth-Session	header		string				false	"客户端内存中的会话 sid"
// @Param			body			body		authRefreshRequest	false	"refresh_token"
// @Success		200				{object}	common.ApiResponse{data=logoutResult}
// @Failure		409				{object}	common.ApiResponse
// @Router			/api/user/auth/logout [post]
func AuthLogout(c *gin.Context) {
	setAuthNoStore(c)
	expectedSID := strings.TrimSpace(c.GetHeader("X-Auth-Session"))
	rawRefreshToken, err := requestRefreshToken(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	refreshSID, hasRefreshSID := service.RefreshTokenSID(rawRefreshToken)
	if expectedSID != "" && hasRefreshSID && refreshSID != expectedSID {
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
			common.ApiSuccess(c, logoutResult{RevokedSID: identity.SessionID})
			return
		}
	}
	if rawRefreshToken == "" {
		c.JSON(http.StatusOK, common.ApiResponse{Success: true})
		return
	}
	if err := service.RevokeByRefreshToken(rawRefreshToken, expectedSID, "logout"); err != nil {
		writeAuthSessionError(c, err)
		return
	}
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
// @Description	可以撤销当前会话，此时 data.current 为 true，前端应删除自己保存的令牌。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
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
