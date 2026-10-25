package router

import (
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// SetWebRouter answers the paths no API or plugin route claims. The backend
// serves no console: the console is deployed apart from it (new-api-frontend).
// A browser that opens a console path on the backend, such as an old email
// link or an OAuth callback registered at the backend's address, is sent to
// the console at FRONTEND_BASE_URL, or else the Frontend Address setting,
// with the path and query kept. Unknown API paths, and every path when no
// console address is set, get the relay's not-found answer.
func SetWebRouter(router *gin.Engine, pluginDispatcher gin.HandlerFunc) {
	frontendBaseUrl := strings.TrimSuffix(os.Getenv("FRONTEND_BASE_URL"), "/")
	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(),
		func(c *gin.Context) {
			consoleAddress := frontendBaseUrl
			if consoleAddress == "" {
				consoleAddress = strings.TrimSuffix(strings.TrimSpace(system_setting.FrontendAddress), "/")
			}
			uri := c.Request.RequestURI
			if consoleAddress == "" || strings.HasPrefix(uri, "/v1") || strings.HasPrefix(uri, "/api") || strings.HasPrefix(uri, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Redirect(http.StatusFound, consoleAddress+uri)
		},
	)
}
