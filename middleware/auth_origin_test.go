package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
	router.Use(CORS())
	api := router.Group("/api", gzip.Gzip(gzip.DefaultCompression))
	api.GET("/user/self", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	api.GET("/usage/token/", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.POST("/pg/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

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
		router.ServeHTTP(response, request)
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
		}
	})
}
