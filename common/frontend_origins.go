package common

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
)

// FrontendOrigins are the browser origins, other than the backend's own and
// the Frontend Address setting's, that serve the console frontend: the
// optional FRONTEND_ORIGINS, plus the deprecated SESSION_COOKIE_TRUSTED_URL.
// The console API admits them in CORS.
var FrontendOrigins []string

// NormalizeOrigin validates and canonicalizes a browser origin. Only an exact
// scheme/host/effective-port match is meaningful; paths and wildcards are not
// accepted.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || strings.ContainsAny(raw, "\r\n") {
		return "", fmt.Errorf("origin is empty or invalid")
	}
	parsedURL, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("origin scheme must be http or https")
	}
	if parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Path != "" && parsedURL.Path != "/") {
		return "", fmt.Errorf("origin must contain only scheme and host")
	}
	hostname := strings.ToLower(parsedURL.Hostname())
	if hostname == "" || strings.Contains(hostname, "*") {
		return "", fmt.Errorf("origin host is empty")
	}
	port := parsedURL.Port()
	normalizedHost := hostname
	if strings.Contains(hostname, ":") {
		normalizedHost = "[" + hostname + "]"
	}
	if port == "" || (parsedURL.Scheme == "http" && port == "80") || (parsedURL.Scheme == "https" && port == "443") {
		return parsedURL.Scheme + "://" + normalizedHost, nil
	}
	return parsedURL.Scheme + "://" + net.JoinHostPort(hostname, port), nil
}

// InitFrontendOrigins reads FRONTEND_ORIGINS and the deprecated
// SESSION_COOKIE_TRUSTED_URL, each a comma-separated list of exact origins.
// SESSION_COOKIE_SECURE no longer has an effect: the browser keeps the
// refresh token itself instead of a cookie.
func InitFrontendOrigins() error {
	FrontendOrigins = nil
	for _, name := range []string{"FRONTEND_ORIGINS", "SESSION_COOKIE_TRUSTED_URL"} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		for value := range strings.SplitSeq(raw, ",") {
			origin, err := NormalizeOrigin(value)
			if err != nil {
				return fmt.Errorf("invalid %s: %w", name, err)
			}
			if !slices.Contains(FrontendOrigins, origin) {
				FrontendOrigins = append(FrontendOrigins, origin)
			}
		}
	}
	for _, name := range []string{"SESSION_COOKIE_SECURE", "SESSION_COOKIE_TRUSTED_URL"} {
		if os.Getenv(name) != "" {
			SysLog(LogText("%s is deprecated: the console no longer uses a refresh cookie; list frontend origins in FRONTEND_ORIGINS", name))
		}
	}
	if os.Getenv("FRONTEND_BASE_URL") != "" {
		SysLog(LogText("FRONTEND_BASE_URL is no longer used: console paths on the backend redirect to the Frontend Address setting"))
	}
	return nil
}
