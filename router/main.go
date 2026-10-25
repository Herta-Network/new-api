package router

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetRouter registers every route on router and returns the handler to serve:
// the router behind the CORS policy, so every answer carries it, and behind
// the trailing-slash matching of the console API.
func SetRouter(router *gin.Engine) http.Handler {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	pluginDispatcher := SetPluginRouter(router)
	SetWebRouter(router, pluginDispatcher)
	return middleware.CORS(matchAPITrailingSlash(router))
}

// matchAPITrailingSlash serves an /api path that misses its route only by a
// trailing slash, such as /api/channel for the route /api/channel/, at that
// route, where gin would redirect to it: the console calls the API from
// another origin, where every redirect costs one more preflighted request.
// The request reaches gin with the registered path, so the route's full path,
// access token rule and audit are the same either way. Other paths keep gin's
// redirect.
func matchAPITrailingSlash(engine *gin.Engine) http.Handler {
	// apiRoutes holds the /api routes with empty handlers, so a lookup in gin's
	// own route tree tells whether a path matches one.
	apiRoutes := gin.New()
	apiRoutes.RedirectTrailingSlash = false
	for _, route := range engine.Routes() {
		if route.Path == "/api" || strings.HasPrefix(route.Path, "/api/") {
			apiRoutes.Handle(route.Method, route.Path, func(*gin.Context) {})
		}
	}
	matches := func(r *http.Request) bool {
		var lookup routeLookup
		apiRoutes.ServeHTTP(&lookup, r)
		return lookup.status != http.StatusNotFound
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if (path != "/api" && !strings.HasPrefix(path, "/api/")) || matches(r) {
			engine.ServeHTTP(w, r)
			return
		}
		alternate := *r.URL
		alternate.RawPath = ""
		if trimmed, ok := strings.CutSuffix(path, "/"); ok {
			alternate.Path = trimmed
		} else {
			alternate.Path = path + "/"
		}
		retry := r.WithContext(r.Context())
		retry.URL = &alternate
		if matches(retry) {
			r = retry
		}
		engine.ServeHTTP(w, r)
	})
}

// routeLookup is the response writer of a lookup in the /api route table; it
// keeps only the status, 404 when no route matches.
type routeLookup struct {
	header http.Header
	status int
}

func (l *routeLookup) Header() http.Header {
	if l.header == nil {
		l.header = http.Header{}
	}
	return l.header
}

func (l *routeLookup) Write(data []byte) (int, error) {
	return len(data), nil
}

func (l *routeLookup) WriteHeader(status int) {
	l.status = status
}
