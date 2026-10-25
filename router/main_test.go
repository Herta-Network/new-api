package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(string) error {
		common.IsMasterNode = true
		common.RedisEnabled = false
		if err := model.InitDB(); err != nil {
			return err
		}
		return model.InitLogDB()
	}))
}

// The console on another origin calls /api routes registered with a trailing
// slash without it. Both forms reach the route itself, with its access token
// rule and the CORS headers, instead of a redirect that carried none.
func TestSetRouterServesAPIPathsWithOrWithoutTrailingSlash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pgtest.Reset(t, model.DB)
	require.NoError(t, authz.Init(model.DB))
	previousAddress := system_setting.FrontendAddress
	system_setting.FrontendAddress = "https://console.example.com"
	t.Cleanup(func() { system_setting.FrontendAddress = previousAddress })
	const console = "https://console.example.com"

	root := &model.User{Username: "slash-root", Password: "placeholder", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "slash-root"}
	require.NoError(t, model.DB.Create(root).Error)
	suffix, err := common.GenerateRandomCharsKey(43)
	require.NoError(t, err)
	token := model.AccessTokenPrefix + suffix
	accessToken := &model.UserAccessToken{Name: "slash token", TokenHash: model.AccessTokenFingerprint(token), TokenHint: model.AccessTokenHint(token)}
	require.NoError(t, accessToken.SetScopes([]string{"channel:read", "log:read", "group:read"}))
	require.NoError(t, model.CreateUserAccessToken(root.Id, accessToken, service.AccessTokenMaxPerUser))

	handler := SetRouter(gin.New())
	serve := func(method, path, origin, credential string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		request.Host = "api.example.com"
		request.Header.Set("Origin", origin)
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	for _, route := range []string{"/api/channel/", "/api/log/", "/api/data/", "/api/group/", "/api/prefill_group/", "/api/mj/"} {
		for _, path := range []string{route, strings.TrimSuffix(route, "/")} {
			t.Run(path, func(t *testing.T) {
				response := serve(http.MethodGet, path+"?p=1", console, token)
				assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), `"success":true`)
				assert.Empty(t, response.Header().Get("Location"))
				assert.Equal(t, console, response.Header().Get("Access-Control-Allow-Origin"))

				anonymous := serve(http.MethodGet, path, console, "")
				assert.Equal(t, http.StatusUnauthorized, anonymous.Code)
				assert.Equal(t, console, anonymous.Header().Get("Access-Control-Allow-Origin"))

				stranger := serve(http.MethodGet, path, "https://evil.example.org", token)
				assert.Empty(t, stranger.Header().Get("Access-Control-Allow-Origin"))
			})
		}
	}

	t.Run("writes match without the slash too", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			response := serve(method, "/api/channel", console, "")
			assert.Equal(t, http.StatusUnauthorized, response.Code, method)
			assert.Equal(t, console, response.Header().Get("Access-Control-Allow-Origin"), method)
		}
	})
	t.Run("routes without a slash match with one", func(t *testing.T) {
		response := serve(http.MethodGet, "/api/log/self/", console, "")
		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Empty(t, response.Header().Get("Location"))
	})
	t.Run("unknown API paths answer 404 with the headers", func(t *testing.T) {
		response := serve(http.MethodGet, "/api/missing", console, token)
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, console, response.Header().Get("Access-Control-Allow-Origin"))
	})
	t.Run("the AI API keeps its redirect and admits any origin", func(t *testing.T) {
		response := serve(http.MethodPost, "/v1/chat/completions/", "https://third-party.example.org", "")
		assert.Equal(t, http.StatusTemporaryRedirect, response.Code)
		assert.Equal(t, "/v1/chat/completions", response.Header().Get("Location"))
		assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"))
	})
}
