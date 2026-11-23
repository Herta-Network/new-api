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

// errSaveFirst is a model list asked for an unsaved channel whose credential
// would have to be renewed: Kiro may rotate the refresh token, and only a
// saved channel keeps the new one.
var errSaveFirst = errors.New("save the Kiro channel before fetching its models")

// ChannelModels lists the models the account of a Kiro channel offers. A
// saved channel's credential is renewed as a request would renew it.
func ChannelModels(ctx context.Context, channel *model.Channel) ([]string, error) {
	if channel.ChannelInfo.IsMultiKey {
		return nil, ErrCredential
	}
	credential, err := ParseCredential(channel.Key)
	if err != nil {
		return nil, err
	}
	setting := channel.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return nil, err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 2*renewTimeout)
	defer cancel()
	base := channel.GetBaseURL()
	if !credential.Usable(time.Now()) || revoked(credential.AccessToken) {
		if credential.Method == "access_token" {
			return nil, ErrCredential
		}
		if channel.Id <= 0 {
			return nil, errSaveFirst
		}
		line, err := renew(ctx, &noRedirect, channel.Id, base, channel.Key)
		if err != nil {
			logRenewalFailure(channel.Id, err)
			return nil, err
		}
		if credential, err = ParseCredential(line); err != nil {
			return nil, err
		}
	}
	profile := credential.ProfileArn
	runtime := runtimeURL(base, profile, credential.authRegion())
	if profile == "" {
		if profile, err = profileFor(ctx, &noRedirect, runtime, credential.AccessToken); err != nil {
			return nil, err
		}
		runtime = runtimeURL(base, profile, credential.authRegion())
	}
	entry, err := catalogs.list(ctx, &noRedirect, runtime, credential.AccessToken, profile)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entry.models))
	for id := range entry.models {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
