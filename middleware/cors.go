package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// consoleAllowedHeaders are the request headers the console frontend sends.
const consoleAllowedHeaders = "Authorization, Content-Type, Cache-Control, X-Security-Proof, X-Auth-Session, X-OAuth-Nonce"

// CORS answers cross-origin requests on every route. The AI API takes API
// keys from any client, so it admits every origin. The console API (/api and
// the playground under /pg) is called only by the console frontend with a
// dashboard access token, so it admits only the frontend origins:
// FRONTEND_ORIGINS and the origin of the frontend address setting. The token
// usage and log lookups under /api take API keys and follow the AI API.
// Neither policy relies on cookies: the browser sends no credential the
// server reads by itself.
func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	relayCORS := cors.New(config)

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		console := (path == "/api" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/pg/")) &&
			!strings.HasPrefix(path, "/api/usage/token") && !strings.HasPrefix(path, "/api/log/token")
		if !console {
			relayCORS(c)
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" || origin == "http://"+c.Request.Host || origin == "https://"+c.Request.Host {
			return
		}
		c.Writer = varyOriginWriter{c.Writer}
		if normalized, err := common.NormalizeOrigin(origin); err != nil || !isFrontendOrigin(normalized) {
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
			}
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Expose-Headers", "Content-Disposition")
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			c.Header("Access-Control-Allow-Headers", consoleAllowedHeaders)
			c.Header("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
		}
	}
}

// varyOriginWriter keeps Origin in the Vary header of a console answer that
// depends on the request's Origin; the gzip middleware later replaces Vary
// with Accept-Encoding.
type varyOriginWriter struct {
	gin.ResponseWriter
}

func (w varyOriginWriter) varyOrigin() {
	if w.Written() {
		return
	}
	for value := range strings.SplitSeq(strings.Join(w.Header().Values("Vary"), ","), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "Origin") {
			return
		}
	}
	w.Header().Add("Vary", "Origin")
}

func (w varyOriginWriter) WriteHeaderNow() {
	w.varyOrigin()
	w.ResponseWriter.WriteHeaderNow()
}

func (w varyOriginWriter) Write(data []byte) (int, error) {
	w.varyOrigin()
	return w.ResponseWriter.Write(data)
}

func (w varyOriginWriter) WriteString(s string) (int, error) {
	w.varyOrigin()
	return w.ResponseWriter.WriteString(s)
}

func (w varyOriginWriter) Flush() {
	w.varyOrigin()
	w.ResponseWriter.Flush()
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
