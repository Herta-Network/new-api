package middleware

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	ginGzip "github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// precompressedFile is the gzip encoding of one frontend build file.
type precompressedFile struct {
	once        sync.Once
	body        []byte
	contentType string
	err         error
}

// ServeFrontendFiles serves files from the embedded frontend build. The build
// never changes while the process runs, so each file is gzip-compressed once
// and the bytes are reused, instead of recompressing multi-megabyte chunks on
// every request. The cache holds at most one entry per build file. Clients
// without gzip, the image types the gzip middleware skips, directories, and
// the index.html redirect keep using http.FileServer.
func ServeFrontendFiles(frontendFS static.ServeFileSystem) gin.HandlerFunc {
	fileServer := http.FileServer(frontendFS)
	var cache sync.Map // URL path -> *precompressedFile
	return func(c *gin.Context) {
		urlPath := c.Request.URL.Path
		if !frontendFS.Exists("/", urlPath) {
			return
		}
		c.Abort()
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") ||
			ginGzip.DefaultExcludedExtentions.Contains(path.Ext(urlPath)) ||
			strings.HasSuffix(urlPath, "/index.html") {
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		value, ok := cache.Load(urlPath)
		if !ok {
			value, _ = cache.LoadOrStore(urlPath, &precompressedFile{})
		}
		file := value.(*precompressedFile)
		file.once.Do(func() {
			f, err := frontendFS.Open(urlPath)
			if err != nil {
				file.err = err
				return
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				file.err = err
				return
			}
			if info.IsDir() {
				file.err = errors.New("directory")
				return
			}
			raw, err := io.ReadAll(f)
			if err != nil {
				file.err = err
				return
			}
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			if _, err := writer.Write(raw); err != nil {
				file.err = err
				return
			}
			if err := writer.Close(); err != nil {
				file.err = err
				return
			}
			// Match http.FileServer: extension first, then content sniffing.
			file.contentType = mime.TypeByExtension(path.Ext(urlPath))
			if file.contentType == "" {
				file.contentType = http.DetectContentType(raw)
			}
			file.body = bytes.Clone(compressed.Bytes())
		})
		if file.err != nil {
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		// Range requests get the whole body; a server may ignore Range.
		header := c.Writer.Header()
		header.Set("Content-Encoding", "gzip")
		header.Set("Vary", "Accept-Encoding")
		header.Set("Content-Type", file.contentType)
		header.Set("Content-Length", strconv.Itoa(len(file.body)))
		c.Writer.WriteHeader(http.StatusOK)
		if c.Request.Method != http.MethodHead {
			_, _ = c.Writer.Write(file.body)
		}
	}
}

// consoleScriptSources are where console scripts may come from: the console's
// own origin and the third parties it loads, the Telegram login widget,
// Cloudflare Turnstile and Stripe.js. The build emits no inline script.
const consoleScriptSources = "'self' https://telegram.org https://challenges.cloudflare.com https://js.stripe.com https://*.js.stripe.com"

// consoleOtherDirectives complete the policy. Components inject styles, so
// inline styles stay allowed. Images, media, frames and connections may reach
// any host: administrators configure logos, home and about page iframes,
// chat links, OIDC discovery and API addresses, and a console served apart
// calls the backend on another origin. The page cannot be framed by other
// sites, carry plugins, change its base URL, or post forms elsewhere.
const consoleOtherDirectives = "style-src 'self' 'unsafe-inline'; img-src * data: blob:; media-src * data: blob:; font-src 'self' data:; connect-src * data: blob:; frame-src *; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"

// ConsoleContentSecurityPolicy is the Content-Security-Policy of the console
// page. A console served apart from the backend should send it from its own
// host; the backend sends it, extended by ConsolePage, with the console it
// serves.
const ConsoleContentSecurityPolicy = "default-src 'self'; script-src " + consoleScriptSources + "; " + consoleOtherDirectives

var htmlScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
var htmlSrcAttributePattern = regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"']+)["']`)

// ConsolePage serves the console's index page for every path the frontend
// routes: never cached, with ConsoleContentSecurityPolicy extended for the
// analytics the backend injected into indexPage, its inline scripts by hash
// and its external scripts by origin.
func ConsolePage(indexPage []byte) gin.HandlerFunc {
	sources := strings.Fields(consoleScriptSources)
	for _, script := range htmlScriptPattern.FindAllSubmatch(indexPage, -1) {
		source := ""
		if src := htmlSrcAttributePattern.FindSubmatch(script[1]); src != nil {
			parsed, err := url.Parse(string(src[1]))
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				continue
			}
			source = parsed.Scheme + "://" + parsed.Host
		} else {
			sum := sha256.Sum256(script[2])
			source = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	policy := "default-src 'self'; script-src " + strings.Join(sources, " ") + "; " + consoleOtherDirectives
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Header("Content-Security-Policy", policy)
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
	}
}
