package middleware

import (
	"bufio"
	"net"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// consoleAllowedHeaders are the request headers the console frontend sends.
const consoleAllowedHeaders = "Authorization, Content-Type, Cache-Control, X-Security-Proof, X-Auth-Session, X-OAuth-Nonce"

// CORS answers cross-origin requests for every response of next. It wraps the
// router instead of running as gin middleware, so the answers gin gives
// before any middleware runs, such as its trailing-slash redirects, carry the
// same headers as the routes, their 404s and their errors.
//
// The AI API takes API keys from any client, so it admits every origin. The
// console API (/api and the playground under /pg) is called only by the
// console frontend with a dashboard access token, so it admits only the
// frontend origins: the origin of the frontend address setting and the
// optional FRONTEND_ORIGINS. The token usage and log lookups under /api take
// API keys and follow the AI API. Neither policy relies on cookies: the
// browser sends no credential the server reads by itself.
//
// Before the system is set up no frontend address is known yet, and the
// setup wizard records the console's origin as it. Until then the setup
// endpoints and the public site status answer any origin with "*"; their
// answers carry nothing sensitive (ASVS 5.0 3.4.2).
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host {
			next.ServeHTTP(w, r)
			return
		}
		header := w.Header()
		preflight := r.Method == http.MethodOptions
		path := r.URL.Path
		console := (path == "/api" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/pg/")) &&
			!strings.HasPrefix(path, "/api/usage/token") && !strings.HasPrefix(path, "/api/log/token")
		if !console {
			header.Set("Access-Control-Allow-Origin", "*")
			header.Set("Access-Control-Allow-Credentials", "true")
			if preflight {
				header.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				header.Set("Access-Control-Allow-Headers", "*")
				header.Set("Access-Control-Max-Age", "43200")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		w = &varyOriginWriter{ResponseWriter: w}
		if normalized, err := common.NormalizeOrigin(origin); err != nil || !isFrontendOrigin(normalized) {
			if !constant.Setup && (path == "/api/setup" || path == "/api/status") {
				header.Set("Access-Control-Allow-Origin", "*")
				if preflight {
					header.Set("Access-Control-Allow-Methods", "GET, POST")
					header.Set("Access-Control-Allow-Headers", consoleAllowedHeaders)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			} else if preflight {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Expose-Headers", "Content-Disposition")
		if preflight {
			header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			header.Set("Access-Control-Allow-Headers", consoleAllowedHeaders)
			header.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// varyOriginWriter keeps Origin in the Vary header of a console answer that
// depends on the request's Origin; the gzip middleware later replaces Vary
// with Accept-Encoding. It passes on the optional interfaces gin's writer
// asserts on the writer it wraps.
type varyOriginWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *varyOriginWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		varied := false
		for value := range strings.SplitSeq(strings.Join(w.Header().Values("Vary"), ","), ",") {
			varied = varied || strings.EqualFold(strings.TrimSpace(value), "Origin")
		}
		if !varied {
			w.Header().Add("Vary", "Origin")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *varyOriginWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *varyOriginWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *varyOriginWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *varyOriginWriter) CloseNotify() <-chan bool {
	if notifier, ok := w.ResponseWriter.(http.CloseNotifier); ok {
		return notifier.CloseNotify()
	}
	return nil
}

func (w *varyOriginWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
