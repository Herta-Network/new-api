// Ported from perfect-panel/server at commit 944dc363cc6a:
//   - internal/module/billing/internal/gateway/notifyurl.go
//
// The original is licensed under GPL-3.0 and is redistributed here under the
// AGPL-3.0 of new-api, as GPL-3.0 section 13 permits.
//
// Changed in new-api: the path is /api/payment/notify/{platform}/{token};
// the method's domain is validated like the site address instead of being
// used as given; the site address is a single URL rather than a list of
// hosts; *.localhost names are rejected as well.

package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrNotifyAddress reports that a payment method has no callback base URL a
// provider can reach.
var ErrNotifyAddress = errors.New("payment notify address is not configured")

// NotifyURL builds the callback URL of a payment method; it carries the
// method's secret token in its path. The base is the method's Domain, else
// siteAddress, the public address of the site. The request Host header is
// never used: the client controls it, and deriving the callback from it
// would let a caller send the notification, token included, to a server of
// its choice. A base that a provider cannot reach (localhost, a loopback or
// wildcard address), one with user information, a query or a fragment, or
// one that is not http(s) is rejected; a bare host is taken as https.
func NotifyURL(method Method, siteAddress string) (string, error) {
	if method.Platform == "" || method.Token == "" {
		return "", errors.New("payment method has no platform or token")
	}
	raw := strings.TrimSpace(method.Domain)
	if raw == "" {
		raw = strings.TrimSpace(siteAddress)
	}
	if raw == "" {
		return "", ErrNotifyAddress
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: %q is not a public http(s) URL", ErrNotifyAddress, raw)
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return "", fmt.Errorf("%w: %q is a local address", ErrNotifyAddress, raw)
	}
	if ip := net.ParseIP(hostname); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		return "", fmt.Errorf("%w: %q is a local address", ErrNotifyAddress, raw)
	}
	base := strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host+parsed.EscapedPath(), "/")
	return base + "/api/payment/notify/" + url.PathEscape(method.Platform) + "/" + url.PathEscape(method.Token), nil
}
