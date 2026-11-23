package kiro

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

// NormalizeKey checks the key an administrator submits and returns it as the
// channel stores it: one credential, or for a batch one credential per line.
// Members outside the credential, such as an exported account's email, are
// dropped.
func NormalizeKey(key string) (string, error) {
	if credential, err := ParseCredential(key); err == nil {
		return credential.Line()
	}
	var lines []string
	for line := range strings.SplitSeq(key, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		credential, err := ParseCredential(line)
		if err != nil {
			return "", err
		}
		normalized, err := credential.Line()
		if err != nil {
			return "", err
		}
		lines = append(lines, normalized)
	}
	if len(lines) == 0 {
		return "", ErrCredential
	}
	return strings.Join(lines, "\n"), nil
}

// KeySummary describes a stored credential without its secrets, for an
// administrator who asks to see the channel key.
func KeySummary(key string) string {
	credential, err := ParseCredential(key)
	if err != nil {
		return ""
	}
	summary := map[string]any{"method": credential.Method}
	if credential.Region != "" {
		summary["region"] = credential.Region
	}
	if credential.ProfileArn != "" {
		summary["profileArn"] = credential.ProfileArn
	}
	if !credential.ExpiresAt.IsZero() {
		summary["expiresAt"] = credential.ExpiresAt
	}
	raw, err := common.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(raw)
}

// errSaveFirst is a management call for an unsaved channel whose credential
// would have to be renewed: Kiro may rotate the refresh token, and only a
// saved channel keeps the new one.
var errSaveFirst = errors.New("save the Kiro channel before fetching its models")

// account is the account of a Kiro channel as the management service is
// called for it: the access token and its profile.
type account struct {
	client  *http.Client
	runtime string
	token   string
	profile string
}

// withAccount calls the management service as the account of a Kiro
// channel, finding the profile when the credential names none. A saved
// channel's credential is renewed as a request renews it: when it expires,
// and once when Kiro refuses the access token before then.
func withAccount(ctx context.Context, channel *model.Channel, call func(context.Context, account) error) error {
	if channel.ChannelInfo.IsMultiKey {
		return ErrCredential
	}
	credential, err := ParseCredential(channel.Key)
	if err != nil {
		return err
	}
	setting := channel.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 2*renewTimeout)
	defer cancel()
	base, line := channel.GetBaseURL(), channel.Key
	stale := !credential.Usable(time.Now()) || revoked(credential.AccessToken)
	for retried := false; ; retried = true {
		if stale {
			if credential.Method == "access_token" {
				return ErrCredential
			}
			if channel.Id <= 0 {
				return errSaveFirst
			}
			if line, err = renew(ctx, &noRedirect, channel.Id, base, line); err != nil {
				logRenewalFailure(channel.Id, err)
				return err
			}
			if credential, err = ParseCredential(line); err != nil {
				return err
			}
		}
		profile := credential.ProfileArn
		runtime := runtimeURL(base, profile, credential.authRegion())
		if profile == "" {
			profile, err = profileFor(ctx, &noRedirect, runtime, credential.AccessToken)
			runtime = runtimeURL(base, profile, credential.authRegion())
		}
		if err == nil {
			err = call(ctx, account{client: &noRedirect, runtime: runtime, token: credential.AccessToken, profile: profile})
		}
		if statusOf(err) != http.StatusUnauthorized || retried {
			return err
		}
		// Kiro refused the access token before its expiry: renew it once.
		revoke(credential.AccessToken)
		stale = true
	}
}

// ChannelModels lists the models the account of a Kiro channel offers.
func ChannelModels(ctx context.Context, channel *model.Channel) ([]string, error) {
	var ids []string
	err := withAccount(ctx, channel, func(ctx context.Context, a account) error {
		entry, err := catalogs.list(ctx, a.client, a.runtime, a.token, a.profile)
		if err != nil {
			return err
		}
		ids = make([]string, 0, len(entry.models))
		for id := range entry.models {
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	return ids, nil
}
