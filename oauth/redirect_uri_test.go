package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The redirect_uri of a token exchange must be the console callback the
// authorization request named: the configured console address, never the
// API request's Host, and without a doubled slash when the setting ends in one.
func TestTokenExchangeNamesTheConsoleCallback(t *testing.T) {
	var redirects []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		redirects = append(redirects, r.PostForm.Get("redirect_uri"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token"}`))
	}))
	defer server.Close()

	frontendAddress, serverAddress := system_setting.FrontendAddress, system_setting.ServerAddress
	oidc := system_setting.GetOIDCSettings()
	tokenEndpoint := oidc.TokenEndpoint
	t.Cleanup(func() {
		system_setting.FrontendAddress, system_setting.ServerAddress = frontendAddress, serverAddress
		oidc.TokenEndpoint = tokenEndpoint
	})
	system_setting.FrontendAddress = "https://console.example/"
	system_setting.ServerAddress = "https://api.example"
	oidc.TokenEndpoint = server.URL
	t.Setenv("LINUX_DO_TOKEN_ENDPOINT", server.URL)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "http://api.example/api/oauth/linuxdo?code=c", nil)

	_, err := (&LinuxDOProvider{}).ExchangeToken(context.Background(), "c", c)
	require.NoError(t, err)
	_, err = (&OIDCProvider{}).ExchangeToken(context.Background(), "c", c)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://console.example/oauth/linuxdo",
		"https://console.example/oauth/oidc",
	}, redirects)
}
