// Package kiro relays requests to Kiro, AWS's subscription coding assistant,
// through its Runtime service: Anthropic Messages over
// GenerateAssistantResponse and OpenAI Responses over CreateResponse.
package kiro

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/tidwall/gjson"
)

// userAgent is the client Kiro's auth and Runtime services accept: they answer
// UnknownOperationException to a client without a KiroIDE version. Follow
// the IDE's version if the services start to require a newer one.
const userAgent = "new-api/kiro KiroIDE-0.11.107"

// DefaultRuntime is the channel base URL; the Runtime region then follows the
// profile.
const DefaultRuntime = "https://runtime.us-east-1.kiro.dev"

var (
	regionPattern  = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`)
	profilePattern = regexp.MustCompile(`^arn:aws(-us-gov)?:codewhisperer:([a-z]{2}(-[a-z0-9]+)+-[0-9]+):[0-9]{12}:profile/[A-Za-z0-9+=,.@_-]+$`)
)

// ErrCredential is a credential that cannot be used: malformed, expired
// without a way to renew, or renewed into an unusable answer.
var ErrCredential = errors.New("invalid Kiro credential")

// ErrRejected is the auth service refusing to renew a grant, such as a
// revoked refresh token. Only an administrator can resolve it.
var ErrRejected = errors.New("Kiro refused to renew the credential")

// errUnavailable is a Kiro service that could not answer: unreachable, timed
// out, throttling or failing.
var errUnavailable = errors.New("Kiro service unavailable")

// statusError is another answer a Kiro service refused a call with.
type statusError struct{ status int }

func (e statusError) Error() string {
	return "Kiro service answered " + http.StatusText(e.status)
}

// tokenRefused reports an answer refusing the access token itself. Kiro
// answers an invalid or revoked token with an AccessDeniedException about the
// token, as a 400 from the management service and a 403 from the Runtime,
// where a 403 otherwise refuses the account (its allowance, its access).
func tokenRefused(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status != http.StatusBadRequest && status != http.StatusForbidden {
		return false
	}
	kind := gjson.GetBytes(body, "__type").Str
	message := strings.ToLower(gjson.GetBytes(body, "message").Str)
	return strings.HasSuffix(kind, "AccessDeniedException") && strings.Contains(message, "token")
}

// statusOf is the HTTP status a Kiro service refused a call with, 0 for any
// other failure; a refused access token reads as 401.
func statusOf(err error) int {
	var refused statusError
	if errors.As(err, &refused) {
		return refused.status
	}
	return 0
}

// Credential is the channel key of a Kiro channel, a JSON object.
type Credential struct {
	// Method is social (Google or GitHub sign-in), builder_id or iam_sso
	// (AWS OIDC, with a client registration), or access_token (a token an
	// administrator renews).
	Method       string    `json:"method"`
	Region       string    `json:"region,omitempty"`
	ProfileArn   string    `json:"profileArn,omitempty"`
	AccessToken  string    `json:"accessToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitzero"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	ClientSecret string    `json:"clientSecret,omitempty"`
}

// ParseCredential reads and validates a channel key.
func ParseCredential(key string) (Credential, error) {
	var c Credential
	if err := common.UnmarshalJsonStr(strings.TrimSpace(key), &c); err != nil {
		return Credential{}, ErrCredential
	}
	if err := c.Validate(); err != nil {
		return Credential{}, err
	}
	return c, nil
}

// Validate checks that the credential has what its method needs and nothing
// another method uses.
func (c Credential) Validate() error {
	for _, v := range []string{c.AccessToken, c.RefreshToken, c.ClientID, c.ClientSecret} {
		if len(v) > 32768 || strings.IndexFunc(v, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
			return ErrCredential
		}
	}
	if c.Region != "" && (len(c.Region) > 40 || !regionPattern.MatchString(c.Region)) || c.ProfileArn != "" && (len(c.ProfileArn) > 512 || !profilePattern.MatchString(c.ProfileArn)) {
		return ErrCredential
	}
	switch c.Method {
	case "access_token":
		if c.AccessToken != "" && c.RefreshToken == "" && c.ClientID == "" && c.ClientSecret == "" {
			return nil
		}
	case "social":
		if c.RefreshToken != "" && c.ClientID == "" && c.ClientSecret == "" && (c.Region == "" || c.Region == "us-east-1") {
			return nil
		}
	case "builder_id", "iam_sso":
		if c.RefreshToken != "" && c.ClientID != "" && c.ClientSecret != "" {
			return nil
		}
	}
	return ErrCredential
}

// Line is the credential as the channel stores it.
func (c Credential) Line() (string, error) {
	raw, err := common.Marshal(c)
	return string(raw), err
}

// refreshMargin is how long before its expiry an access token is renewed, so
// a request never starts on a token about to lapse.
const refreshMargin = time.Minute

// Usable reports whether the access token stays valid past the refresh
// margin. An access_token credential without an expiry is taken as valid
// until Kiro refuses it.
func (c Credential) Usable(now time.Time) bool {
	if c.AccessToken == "" {
		return false
	}
	if c.ExpiresAt.IsZero() {
		return c.Method == "access_token"
	}
	return now.Add(refreshMargin).Before(c.ExpiresAt)
}

func (c Credential) authRegion() string {
	if c.Region != "" {
		return c.Region
	}
	return "us-east-1"
}

// runtimeURL is the Runtime a request goes to: the channel's base URL, or
// for the default base the region of the profile.
func runtimeURL(base, profile, region string) string {
	base = strings.TrimRight(base, "/")
	if base != "" && base != DefaultRuntime {
		return base
	}
	if match := profilePattern.FindStringSubmatch(profile); match != nil {
		region = match[2]
	}
	if !regionPattern.MatchString(region) {
		return DefaultRuntime
	}
	return "https://runtime." + region + ".kiro.dev"
}

// authURL is the service that renews a credential: Kiro's own for a social
// sign-in, AWS OIDC in the registration's region otherwise.
var authURL = func(c Credential) string {
	if c.Method == "social" {
		return "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"
	}
	return "https://oidc." + c.authRegion() + ".amazonaws.com/token"
}

// Refresh exchanges the refresh token for an access token. Kiro may rotate
// the refresh token on every refresh, so once the service has answered, the
// returned credential carries the new refresh token even with an error (then
// without an access token), and the caller must save it in place of the
// spent one.
func (c Credential) Refresh(ctx context.Context, client *http.Client) (Credential, error) {
	payload := map[string]string{"refreshToken": c.RefreshToken}
	if c.Method != "social" {
		payload["grantType"] = "refresh_token"
		payload["clientId"] = c.ClientID
		payload["clientSecret"] = c.ClientSecret
	}
	var result struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ProfileArn   string `json:"profileArn"`
		ExpiresIn    int    `json:"expiresIn"`
	}
	if err := rpc(ctx, client, authURL(c), "", "", payload, &result); err != nil {
		// A grant the auth service refuses (invalid_grant and the like) waits
		// for an administrator; a timeout, a throttle or an outage does not.
		switch statusOf(err) {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			return Credential{}, ErrRejected
		}
		return Credential{}, err
	}
	if result.RefreshToken != "" {
		c.RefreshToken = result.RefreshToken
	}
	// A malformed profile in the answer is not used; the grant stays valid.
	if len(result.ProfileArn) <= 512 && profilePattern.MatchString(result.ProfileArn) {
		c.ProfileArn = result.ProfileArn
	}
	c.AccessToken, c.ExpiresAt = result.AccessToken, time.Now().UTC().Add(time.Duration(result.ExpiresIn)*time.Second).Truncate(time.Second)
	if result.AccessToken == "" || result.ExpiresIn <= 0 || result.ExpiresIn > 7*86400 || c.Validate() != nil {
		c.AccessToken, c.ExpiresAt = "", time.Time{}
		return c, ErrCredential
	}
	return c, nil
}

// rpc posts payload as JSON to a Kiro or AWS service and decodes its answer.
// operation names an awsJson operation (X-Amz-Target); token authorizes it.
func rpc(ctx context.Context, client *http.Client, target, operation, token string, payload any, result any) error {
	body, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Kiro endpoint")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Amz-User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	if operation != "" {
		req.Header.Set("X-Amz-Target", operation)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		// A caller that gave up is not a Kiro failure.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errUnavailable
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusOK:
	case res.StatusCode == http.StatusRequestTimeout || res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return errUnavailable
	default:
		raw, _ := service.ReadUpstreamErrorBody(res.Body)
		if tokenRefused(res.StatusCode, raw) {
			return statusError{http.StatusUnauthorized}
		}
		return statusError{res.StatusCode}
	}
	raw, err := service.ReadUpstreamBody(res.Body)
	if err != nil || common.Unmarshal(raw, result) != nil {
		return errors.New("invalid Kiro service response")
	}
	return nil
}
