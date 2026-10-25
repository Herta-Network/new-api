package router

import (
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine) {
	// Registered before every route so preflights, which match none, get it too.
	router.Use(middleware.CORS())
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	pluginDispatcher := SetPluginRouter(router)
	SetWebRouter(router, pluginDispatcher)
}
