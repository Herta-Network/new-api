package middleware

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// IsConsoleOrigin reports whether a browser request comes from the console:
// its Origin, or its Referer when it has no Origin, is on the backend's own
// host or is a frontend origin. Refresh and logout accept the legacy refresh
// cookie only from the console, as the cookie's origin check did.
func IsConsoleOrigin(request *http.Request) bool {
	origin, ok := requestBrowserOrigin(request)
	if !ok {
		return false
	}
	if parsed, err := url.Parse(origin); err == nil && strings.EqualFold(parsed.Host, request.Host) {
		return true
	}
	return isFrontendOrigin(origin)
}

// isFrontendOrigin reports whether a normalized origin serves the console
// frontend: it is in FRONTEND_ORIGINS or is the frontend address setting.
func isFrontendOrigin(origin string) bool {
	if slices.Contains(common.FrontendOrigins, origin) {
		return true
	}
	frontend, err := common.NormalizeOrigin(system_setting.FrontendAddress)
	return err == nil && frontend == origin
}

func requestBrowserOrigin(request *http.Request) (string, bool) {
	originValues := request.Header.Values("Origin")
	if len(originValues) > 1 {
		return "", false
	}
	if len(originValues) == 1 {
		if strings.Contains(originValues[0], ",") {
			return "", false
		}
		origin, err := common.NormalizeOrigin(originValues[0])
		return origin, err == nil
	}
	refererValues := request.Header.Values("Referer")
	if len(refererValues) != 1 {
		return "", false
	}
	referer, err := url.Parse(strings.TrimSpace(refererValues[0]))
	if err != nil || referer.Scheme == "" || referer.Host == "" || referer.User != nil {
		return "", false
	}
	origin, err := common.NormalizeOrigin(referer.Scheme + "://" + referer.Host)
	return origin, err == nil
}
