package common

import (
	"net/url"
	"strings"
)

// RedactSecret replaces every spelling of secret that an error or an upstream
// answer may carry, as it is, query-escaped and path-escaped, with
// [REDACTED]. A blank secret changes nothing.
func RedactSecret(text string, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return text
	}
	for _, spelling := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
		text = strings.ReplaceAll(text, spelling, "[REDACTED]")
	}
	return text
}
