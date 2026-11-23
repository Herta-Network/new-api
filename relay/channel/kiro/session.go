package kiro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"golang.org/x/sync/singleflight"
)

// renewTimeout bounds one renewal: the wait for another node's renewal of the
// channel, and the refresh call itself.
const renewTimeout = 12 * time.Second

var renewals singleflight.Group

// renew refreshes the credential line a request used and returns the line to
// use from now on: the refreshed one, or the usable one another node stored
// meanwhile. Requests of this node that need the same line share one
// renewal, and the channel's credential refresh lock serializes the nodes, so
// a refresh token Kiro rotates is spent once. The renewal outlives a caller
// that gives up, because a rotated refresh token must still be saved.
func renew(ctx context.Context, client *http.Client, channelID int, base, used string) (string, error) {
	sum := sha256.Sum256([]byte(used))
	flight := renewals.DoChan(strconv.Itoa(channelID)+":"+hex.EncodeToString(sum[:]), func() (any, error) {
		return renewLine(client, channelID, base, used)
	})
	select {
	case result := <-flight:
		if result.Err != nil {
			return "", result.Err
		}
		return result.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func renewLine(client *http.Client, channelID int, base, used string) (string, error) {
	lockCtx, cancelLock := context.WithTimeout(context.Background(), renewTimeout)
	defer cancelLock()
	var current, stored string
	err := model.WithPluginCredentialRefreshLock(lockCtx, channelID, func() error {
		channel, err := model.GetChannelById(channelID, true)
		if err != nil {
			return err
		}
		if channel.Type != constant.ChannelTypeKiro || channel.ChannelInfo.IsMultiKey {
			return ErrCredential
		}
		stored = channel.Key
		credential, err := ParseCredential(channel.Key)
		if err != nil {
			return err
		}
		if channel.Key != used && credential.Usable(time.Now()) && !revoked(credential.AccessToken) {
			// Another node renewed it, or an administrator replaced it.
			current = channel.Key
			return nil
		}
		if credential.Method == "access_token" {
			return ErrCredential
		}
		ctx, cancel := context.WithTimeout(context.Background(), renewTimeout)
		defer cancel()
		next, refreshErr := credential.Refresh(ctx, client)
		if refreshErr == nil && next.ProfileArn == "" {
			// Saved with the credential, so the profile is looked up once.
			if profile, err := resolveProfile(ctx, client, runtimeURL(base, "", next.authRegion()), next.AccessToken); err == nil {
				next.ProfileArn = profile
			}
		}
		// Kiro has spent the old refresh token once it answers with a new one,
		// so the new one is saved even when the rest of the answer is unusable.
		rotated := next.RefreshToken != "" && next.RefreshToken != credential.RefreshToken && next.Validate() == nil
		if refreshErr != nil && !rotated {
			return refreshErr
		}
		line, err := next.Line()
		if err != nil {
			return err
		}
		saved, err := model.UpdateChannelLocked(channelID, func(channel *model.Channel) error {
			if channel.Key != stored {
				return model.ErrChannelKeyChanged
			}
			channel.Key = line
			return nil
		})
		if err != nil {
			if rotated {
				// The stored refresh token is spent and the new one is lost.
				common.SysError(common.LogText("kiro refresh token was rotated but could not be saved: channel_id=%d; sign in to Kiro again and save a new credential", channelID))
			}
			return err
		}
		stored = saved.Key
		if refreshErr != nil {
			return refreshErr
		}
		current = line
		return nil
	})
	if stored != "" && stored != used {
		// This node's cache may still hold the line the request used.
		model.CacheUpdateChannelKey(channelID, stored)
	}
	if err != nil {
		return "", err
	}
	return current, nil
}

// revokedTokens holds access tokens the Runtime refused before they expired,
// so the next request renews the credential instead of sending them again.
var revokedTokens = struct {
	sync.Mutex
	until map[[32]byte]time.Time
}{until: map[[32]byte]time.Time{}}

func revoke(token string) {
	if token == "" {
		return
	}
	revokedTokens.Lock()
	defer revokedTokens.Unlock()
	now := time.Now()
	for k, until := range revokedTokens.until {
		if !now.Before(until) {
			delete(revokedTokens.until, k)
		}
	}
	revokedTokens.until[sha256.Sum256([]byte(token))] = now.Add(2 * time.Hour)
}

func revoked(token string) bool {
	revokedTokens.Lock()
	defer revokedTokens.Unlock()
	until, ok := revokedTokens.until[sha256.Sum256([]byte(token))]
	return ok && time.Now().Before(until)
}

// profiles caches the profile found for an access token whose credential
// names none and is never renewed here (an access_token credential).
var profiles = struct {
	sync.Mutex
	arn map[[32]byte]string
}{arn: map[[32]byte]string{}}

func profileFor(ctx context.Context, client *http.Client, runtime, token string) (string, error) {
	key := sha256.Sum256([]byte(runtime + "\x00" + token))
	profiles.Lock()
	arn, ok := profiles.arn[key]
	profiles.Unlock()
	if ok {
		return arn, nil
	}
	arn, err := resolveProfile(ctx, client, runtime, token)
	if err != nil {
		return "", err
	}
	profiles.Lock()
	if len(profiles.arn) >= 1024 {
		clear(profiles.arn)
	}
	profiles.arn[key] = arn
	profiles.Unlock()
	return arn, nil
}

// Cooldowns after a credential failure. A refused or unusable credential
// waits for an administrator, so its channel sits out a fixed while; a
// transient failure backs off from transientCooldown up to permanentCooldown.
const (
	permanentCooldown = 5 * time.Minute
	transientCooldown = 30 * time.Second
)

type credentialFailure struct {
	until     time.Time
	permanent bool
	streak    int
	err       error
}

// failures keeps, per channel and credential line, the failure that put it
// in cooldown, so the requests of this node fail fast instead of sending the
// credential to Kiro again. A new line, such as one an administrator saves,
// starts clean.
var failures = struct {
	sync.Mutex
	byLine map[[32]byte]credentialFailure
}{byLine: map[[32]byte]credentialFailure{}}

func failureKey(channelID int, line string) [32]byte {
	return sha256.Sum256([]byte(strconv.Itoa(channelID) + "\x00" + line))
}

func currentFailure(channelID int, line string) (credentialFailure, bool) {
	failures.Lock()
	defer failures.Unlock()
	f, ok := failures.byLine[failureKey(channelID, line)]
	return f, ok && time.Now().Before(f.until)
}

// recordFailure starts the cooldown of a credential line and returns it. A
// failure while the line is already cooling down is the same failure seen by
// another request, such as one renewal shared by many, so it changes nothing;
// the backoff grows only when the line fails again after a cooldown.
func recordFailure(channelID int, line string, err error, permanent bool) credentialFailure {
	failures.Lock()
	defer failures.Unlock()
	now := time.Now()
	for k, f := range failures.byLine {
		if !now.Before(f.until.Add(permanentCooldown)) {
			delete(failures.byLine, k)
		}
	}
	key := failureKey(channelID, line)
	f := failures.byLine[key]
	if now.Before(f.until) {
		return f
	}
	f.streak++
	f.permanent, f.err = permanent, err
	wait := permanentCooldown
	if !permanent {
		wait = min(transientCooldown<<min(f.streak-1, 4), permanentCooldown)
	}
	f.until = now.Add(wait)
	failures.byLine[key] = f
	return f
}

func clearFailure(channelID int, line string) {
	failures.Lock()
	defer failures.Unlock()
	delete(failures.byLine, failureKey(channelID, line))
}

// permanentFailure reports a failure only an administrator can resolve.
func permanentFailure(err error) bool {
	return errors.Is(err, ErrRejected) || errors.Is(err, ErrCredential) || errors.Is(err, errProfileUnresolved)
}

// logRenewalFailure records why a renewal failed without the credential.
func logRenewalFailure(channelID int, err error) {
	reason := "transient"
	switch {
	case errors.Is(err, ErrRejected):
		reason = "rejected"
	case errors.Is(err, ErrCredential):
		reason = "unusable"
	case errors.Is(err, errProfileUnresolved):
		reason = "profile_unresolved"
	case errors.Is(err, model.ErrChannelKeyChanged):
		reason = "key_changed"
	case statusOf(err) != 0:
		reason = "status_" + strconv.Itoa(statusOf(err))
	}
	common.SysLog(common.LogText("kiro credential renewal failed: channel_id=%d reason=%s", channelID, reason))
}
