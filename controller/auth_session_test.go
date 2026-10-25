package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAuthSessionControllerTest gives each test a clean database with one
// enabled user and a fixed signing secret.
func setupAuthSessionControllerTest(t *testing.T) *model.User {
	t.Helper()
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	previousOrigins := common.FrontendOrigins
	pgtest.Reset(t, model.DB)
	common.RedisEnabled = false
	common.SessionSecret = "auth-session-controller-test-secret"
	common.FrontendOrigins = []string{"https://app.example.net"}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		common.FrontendOrigins = previousOrigins
	})
	user := &model.User{
		Username: "auth-session-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

type authSessionResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		RevokedSID   string `json:"revoked_sid"`
		Session      struct {
			SID string `json:"sid"`
		} `json:"session"`
	} `json:"data"`
}

// authSessionRequest calls handler the way the console does: body is the
// JSON body, and headers are set as given.
func authSessionRequest(t *testing.T, handler gin.HandlerFunc, path, body string, headers map[string]string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, authSessionResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://api.example.com"+path, strings.NewReader(body))
	c.Request.Host = "api.example.com"
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		c.Request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		c.Request.AddCookie(cookie)
	}
	handler(c)
	var response authSessionResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	return recorder, response
}

func refreshBody(token string) string {
	return `{"refresh_token":"` + token + `"}`
}

func TestRefreshAuthRotatesTheRefreshTokenTheConsoleKeeps(t *testing.T) {
	user := setupAuthSessionControllerTest(t)
	login, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent")
	require.NoError(t, err)

	recorder, first := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(login.RefreshToken), map[string]string{"X-Auth-Session": login.Session.SID})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, first.Success)
	assert.NotEmpty(t, first.Data.AccessToken)
	assert.NotEmpty(t, first.Data.RefreshToken)
	assert.NotEqual(t, login.RefreshToken, first.Data.RefreshToken)
	assert.Equal(t, login.Session.SID, first.Data.Session.SID)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	assert.Empty(t, recorder.Header().Values("Set-Cookie"), "the refresh token never travels in a cookie")

	recorder, second := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(first.Data.RefreshToken), nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.NotEqual(t, first.Data.RefreshToken, second.Data.RefreshToken)

	// A token two rotations old is no longer accepted.
	recorder, stale := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(login.RefreshToken), nil)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, "AUTH_UNAUTHORIZED", stale.Code)
	assert.Empty(t, stale.Data.RefreshToken)

	recorder, mismatch := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(second.Data.RefreshToken), map[string]string{"X-Auth-Session": "another-session"})
	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", mismatch.Code)
}

func TestRefreshAuthReplayOfARotatedTokenRevokesTheSession(t *testing.T) {
	user := setupAuthSessionControllerTest(t)
	login, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent")
	require.NoError(t, err)
	_, rotated := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(login.RefreshToken), nil)
	require.True(t, rotated.Success)
	// Leave the window in which a concurrent refresh may present the
	// previous token, as a stolen token replayed later would.
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", login.Session.SID).Update("previous_valid_until", time.Now().Unix()-1).Error)

	recorder, replayed := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(login.RefreshToken), nil)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, "AUTH_SESSION_REVOKED", replayed.Code)
	_, revoked := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", refreshBody(rotated.Data.RefreshToken), nil)
	assert.Equal(t, "AUTH_SESSION_REVOKED", revoked.Code, "the whole session ends, including the newest token")
}

func TestRefreshAuthRejectsMissingExpiredAndSignedOutTokens(t *testing.T) {
	user := setupAuthSessionControllerTest(t)
	expired, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", expired.Session.SID).Update("expires_at", time.Now().Unix()-1).Error)
	signedOut, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent")
	require.NoError(t, err)
	recorder, logout := authSessionRequest(t, AuthLogout, "/api/user/auth/logout", refreshBody(signedOut.RefreshToken), nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, logout.Success)

	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "no token", body: "", code: "AUTH_UNAUTHORIZED"},
		{name: "empty token", body: refreshBody(""), code: "AUTH_UNAUTHORIZED"},
		{name: "forged secret", body: refreshBody(expired.Session.SID + ".forged"), code: "AUTH_SESSION_REVOKED"},
		{name: "expired session", body: refreshBody(expired.RefreshToken), code: "AUTH_SESSION_REVOKED"},
		{name: "signed out", body: refreshBody(signedOut.RefreshToken), code: "AUTH_SESSION_REVOKED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, response := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", test.body, nil)
			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
			assert.Equal(t, test.code, response.Code)
			assert.Empty(t, response.Data.AccessToken)
		})
	}
}

func TestRefreshAuthMigratesTheLegacyCookieOnlyFromTheConsole(t *testing.T) {
	user := setupAuthSessionControllerTest(t)
	legacyCookie := func(token string) []*http.Cookie {
		return []*http.Cookie{
			{Name: service.LegacyRefreshCookieName, Value: token},
			{Name: "new_api_has_session", Value: "1"},
		}
	}
	tests := []struct {
		name     string
		origin   string
		migrated bool
	}{
		{name: "backend host", origin: "https://api.example.com", migrated: true},
		{name: "frontend origin", origin: "https://app.example.net", migrated: true},
		{name: "other site", origin: "https://evil.example.org"},
		{name: "no origin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			login, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent")
			require.NoError(t, err)
			headers := map[string]string{}
			if test.origin != "" {
				headers["Origin"] = test.origin
			}
			recorder, response := authSessionRequest(t, RefreshAuth, "/api/user/auth/refresh", "", headers, legacyCookie(login.RefreshToken)...)
			cleared := strings.Join(recorder.Header().Values("Set-Cookie"), "\n")
			assert.Contains(t, cleared, service.LegacyRefreshCookieName+"=; Path=/api/user/auth; Expires=")
			assert.Contains(t, cleared, "new_api_has_session=; Path=/; Expires=")
			if !test.migrated {
				assert.Equal(t, http.StatusUnauthorized, recorder.Code)
				assert.Empty(t, response.Data.RefreshToken)
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.Equal(t, login.Session.SID, response.Data.Session.SID)
			assert.NotEmpty(t, response.Data.RefreshToken)
			assert.NotEqual(t, login.RefreshToken, response.Data.RefreshToken)
		})
	}
}

func TestAuthLogoutRevokesTheSessionOfTheRefreshToken(t *testing.T) {
	user := setupAuthSessionControllerTest(t)
	sessionA, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-a")
	require.NoError(t, err)
	sessionB, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-b")
	require.NoError(t, err)

	recorder, mismatch := authSessionRequest(t, AuthLogout, "/api/user/auth/logout", refreshBody(sessionB.RefreshToken), map[string]string{
		"Authorization": "Bearer " + sessionA.AccessToken, "X-Auth-Session": sessionA.Session.SID,
	})
	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", mismatch.Code)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusActive, stored.Status)
	}

	recorder, byAccessToken := authSessionRequest(t, AuthLogout, "/api/user/auth/logout", refreshBody(sessionA.RefreshToken), map[string]string{
		"Authorization": "Bearer " + sessionA.AccessToken, "X-Auth-Session": sessionA.Session.SID,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, sessionA.Session.SID, byAccessToken.Data.RevokedSID)

	recorder, byRefreshToken := authSessionRequest(t, AuthLogout, "/api/user/auth/logout", refreshBody(sessionB.RefreshToken), nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.True(t, byRefreshToken.Success)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	}
	assert.Empty(t, recorder.Header().Values("Set-Cookie"))
}

func TestWriteAuthSessionErrorMapsSessionGrowthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "active session limit",
			err:            model.ErrUserSessionLimit,
			expectedStatus: http.StatusConflict,
			expectedCode:   "AUTH_SESSION_LIMIT",
		},
		{
			name:           "issuance limit",
			err:            model.ErrUserSessionIssuanceLimit,
			expectedStatus: http.StatusTooManyRequests,
			expectedCode:   "AUTH_SESSION_ISSUANCE_LIMIT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeAuthSessionError(c, test.err)

			assert.Equal(t, test.expectedStatus, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedCode, response.Code)
		})
	}
}

func TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful(t *testing.T) {
	previousRedis := common.RedisEnabled
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	pgtest.Reset(t, model.DB)
	db := model.DB
	common.RedisEnabled = false
	common.UserSessionActiveLimit = 1
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})

	const previousLastLoginAt = int64(123)
	user := &model.User{
		Username: "rejected-login-audit-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{
		SID: "existing-active-session", UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	setupLogin(user, nil, c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, stored.LastLoginAt)
}
