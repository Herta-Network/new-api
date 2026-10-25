package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// useFrontendOrigins trusts origins and the frontend address for one test.
func useFrontendOrigins(t *testing.T, frontendAddress string, origins ...string) {
	t.Helper()
	previousOrigins, previousAddress := common.FrontendOrigins, system_setting.FrontendAddress
	common.FrontendOrigins, system_setting.FrontendAddress = origins, frontendAddress
	t.Cleanup(func() {
		common.FrontendOrigins, system_setting.FrontendAddress = previousOrigins, previousAddress
	})
}

func TestIsConsoleOrigin(t *testing.T) {
	useFrontendOrigins(t, "https://console.example.net/", "https://app.example.net")
	tests := []struct {
		name    string
		origin  string
		referer string
		trusted bool
	}{
		{name: "backend host", origin: "https://api.example.com", trusted: true},
		{name: "backend host behind a TLS proxy", origin: "http://api.example.com", trusted: true},
		{name: "frontend origin", origin: "https://app.example.net", trusted: true},
		{name: "frontend address setting", origin: "https://console.example.net", trusted: true},
		{name: "referer fallback", referer: "https://app.example.net/user/settings", trusted: true},
		{name: "missing both"},
		{name: "null origin", origin: "null"},
		{name: "suffix attack", origin: "https://app.example.net.evil.test"},
		{name: "other port", origin: "https://app.example.net:8443"},
		{name: "other site", origin: "https://evil.example.org"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://api.example.com/api/user/auth/refresh", nil)
			request.Host = "api.example.com"
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.referer != "" {
				request.Header.Set("Referer", test.referer)
			}
			assert.Equal(t, test.trusted, IsConsoleOrigin(request))
		})
	}
}

func TestCORSAdmitsOnlyFrontendOriginsToTheConsoleAPI(t *testing.T) {
	useFrontendOrigins(t, "https://console.example.net", "https://app.example.net")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.HandleMethodNotAllowed = true
	api := router.Group("/api", gzip.Gzip(gzip.DefaultCompression))
	api.GET("/user/self", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	api.GET("/user/denied", func(c *gin.Context) { c.JSON(http.StatusUnauthorized, gin.H{"success": false}) })
	api.GET("/usage/token/", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.POST("/pg/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	handler := CORS(router)

	serve := func(method, path, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "https://api.example.com"+path, nil)
		request.Host = "api.example.com"
		request.Header.Set("Accept-Encoding", "gzip")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if method == http.MethodOptions {
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	t.Run("trusted preflight", func(t *testing.T) {
		for _, origin := range []string{"https://app.example.net", "https://console.example.net"} {
			response := serve(http.MethodOptions, "/api/user/self", origin)
			assert.Equal(t, http.StatusNoContent, response.Code)
			assert.Equal(t, origin, response.Header().Get("Access-Control-Allow-Origin"))
			assert.Contains(t, response.Header().Get("Access-Control-Allow-Headers"), "Authorization")
			assert.Contains(t, response.Header().Get("Access-Control-Allow-Headers"), "X-Security-Proof")
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
			assert.Contains(t, response.Header().Values("Vary"), "Origin")
		}
	})
	t.Run("trusted request keeps Vary Origin through gzip", func(t *testing.T) {
		response := serve(http.MethodGet, "/api/user/self", "https://app.example.net")
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "https://app.example.net", response.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
		assert.ElementsMatch(t, []string{"Accept-Encoding", "Origin"}, response.Header().Values("Vary"))
	})
	t.Run("untrusted origins get no CORS headers", func(t *testing.T) {
		for _, origin := range []string{"https://evil.example.org", "https://app.example.net.evil.test", "null"} {
			preflight := serve(http.MethodOptions, "/api/user/self", origin)
			assert.Equal(t, http.StatusForbidden, preflight.Code)
			assert.Empty(t, preflight.Header().Get("Access-Control-Allow-Origin"))
			request := serve(http.MethodGet, "/api/user/self", origin)
			assert.Empty(t, request.Header().Get("Access-Control-Allow-Origin"))
			playground := serve(http.MethodOptions, "/pg/chat/completions", origin)
			assert.Equal(t, http.StatusForbidden, playground.Code)
		}
	})
	t.Run("same origin needs no CORS", func(t *testing.T) {
		response := serve(http.MethodGet, "/api/user/self", "https://api.example.com")
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	})
	t.Run("API key routes admit any origin", func(t *testing.T) {
		for _, path := range []string{"/v1/chat/completions", "/api/usage/token/"} {
			response := serve(http.MethodOptions, path, "https://third-party.example.org")
			assert.Equal(t, http.StatusNoContent, response.Code, path)
			assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"), path)
			assert.Equal(t, "GET,POST,PUT,DELETE,OPTIONS", response.Header().Get("Access-Control-Allow-Methods"), path)
			assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Headers"), path)
			assert.Equal(t, "43200", response.Header().Get("Access-Control-Max-Age"), path)
			request := serve(http.MethodPost, path, "https://third-party.example.org")
			assert.Equal(t, "*", request.Header().Get("Access-Control-Allow-Origin"), path)
			assert.Equal(t, "true", request.Header().Get("Access-Control-Allow-Credentials"), path)
		}
	})
	// gin answers trailing-slash redirects before any middleware runs; the
	// policy wraps the router, so they carry it like every other answer.
	t.Run("every answer carries the policy, framework answers included", func(t *testing.T) {
		for _, test := range []struct {
			method, path string
			status       int
		}{
			{method: http.MethodGet, path: "/api/user/self", status: http.StatusOK},
			{method: http.MethodGet, path: "/api/user/denied", status: http.StatusUnauthorized},
			{method: http.MethodGet, path: "/api/missing", status: http.StatusNotFound},
			{method: http.MethodPost, path: "/api/user/self", status: http.StatusMethodNotAllowed},
			{method: http.MethodGet, path: "/api/user/self/", status: http.StatusMovedPermanently},
		} {
			trusted := serve(test.method, test.path, "https://app.example.net")
			assert.Equal(t, test.status, trusted.Code, test.path)
			assert.Equal(t, "https://app.example.net", trusted.Header().Get("Access-Control-Allow-Origin"), test.path)
			assert.Contains(t, trusted.Header().Values("Vary"), "Origin", test.path)
			untrusted := serve(test.method, test.path, "https://evil.example.org")
			assert.Equal(t, test.status, untrusted.Code, test.path)
			assert.Empty(t, untrusted.Header().Get("Access-Control-Allow-Origin"), test.path)
		}
		for _, test := range []struct {
			method, path string
			status       int
		}{
			{method: http.MethodPost, path: "/v1/chat/completions/", status: http.StatusTemporaryRedirect},
			{method: http.MethodGet, path: "/v1/chat/completions", status: http.StatusMethodNotAllowed},
			{method: http.MethodGet, path: "/v1/missing", status: http.StatusNotFound},
		} {
			response := serve(test.method, test.path, "https://third-party.example.org")
			assert.Equal(t, test.status, response.Code, test.path)
			assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"), test.path)
		}
	})
}

// Before setup no frontend address is known, so the setup wizard on any
// origin can reach the setup endpoints and the public status; nothing else
// opens up, and setup closes the exception.
func TestCORSAdmitsAnyOriginToSetupUntilTheSystemIsSetUp(t *testing.T) {
	useFrontendOrigins(t, "")
	previousSetup := constant.Setup
	t.Cleanup(func() { constant.Setup = previousSetup })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) }
	router.GET("/api/setup", ok)
	router.POST("/api/setup", ok)
	router.GET("/api/status", ok)
	router.GET("/api/user/self", ok)
	handler := CORS(router)

	serve := func(method, path, requestMethod string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "https://api.example.com"+path, nil)
		request.Host = "api.example.com"
		request.Header.Set("Origin", "https://app.example.com")
		if method == http.MethodOptions {
			request.Header.Set("Access-Control-Request-Method", requestMethod)
			request.Header.Set("Access-Control-Request-Headers", "content-type")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	constant.Setup = false
	preflight := serve(http.MethodOptions, "/api/setup", http.MethodPost)
	assert.Equal(t, http.StatusNoContent, preflight.Code)
	assert.Equal(t, "*", preflight.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, preflight.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Contains(t, preflight.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	for _, path := range []string{"/api/setup", "/api/status"} {
		response := serve(http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, response.Code, path)
		assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"), path)
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"), path)
	}
	other := serve(http.MethodOptions, "/api/user/self", http.MethodGet)
	assert.Equal(t, http.StatusForbidden, other.Code)
	assert.Empty(t, other.Header().Get("Access-Control-Allow-Origin"))

	constant.Setup = true
	afterSetup := serve(http.MethodOptions, "/api/setup", http.MethodPost)
	assert.Equal(t, http.StatusForbidden, afterSetup.Code)
	assert.Empty(t, afterSetup.Header().Get("Access-Control-Allow-Origin"))
	status := serve(http.MethodGet, "/api/status", "")
	assert.Empty(t, status.Header().Get("Access-Control-Allow-Origin"))
}
