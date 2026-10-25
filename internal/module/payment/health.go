package payment

import (
	"sync"
	"time"
)

// A channel whose checkouts failed breakerThreshold times in a row is
// tripped for breakerCooldown: it is tried only after every other channel
// that takes the order. Once the cooldown passes it takes its place again,
// and a single failure trips it again until a checkout through it starts.
// Each server keeps its own view of the channels.
const (
	breakerThreshold = 3
	breakerCooldown  = 5 * time.Minute
)

var (
	healthMu sync.Mutex
	health   = map[int]*ChannelHealth{}
	// healthNow is the breaker's clock; tests move it.
	healthNow = time.Now
)

// ChannelHealth is what this server saw of a channel's checkouts since the
// last one that started.
type ChannelHealth struct {
	// Failures counts the checkouts that failed in a row and count towards
	// the breaker; a refusal, for one, does not.
	Failures int `json:"failures"`
	// TrippedUntil ends the channel's cooldown, in Unix seconds; 0 when it
	// was not tripped.
	TrippedUntil int64 `json:"tripped_until"`
	// LastError and LastFailedAt describe the latest failed checkout,
	// counted or not.
	LastError    string `json:"last_error"`
	LastFailedAt int64  `json:"last_failed_at"`
}

// channelTripped reports whether a channel is cooling down.
func channelTripped(methodID int) bool {
	healthMu.Lock()
	defer healthMu.Unlock()
	state := health[methodID]
	return state != nil && state.TrippedUntil > healthNow().Unix()
}

// checkoutStarted forgets a channel's failures.
func checkoutStarted(methodID int) {
	healthMu.Lock()
	defer healthMu.Unlock()
	delete(health, methodID)
}

// checkoutFailed records a failed checkout of a channel. One that counts is
// counted towards the breaker, which trips the channel at the threshold.
func checkoutFailed(methodID int, err error, counts bool) {
	healthMu.Lock()
	defer healthMu.Unlock()
	state := health[methodID]
	if state == nil {
		state = &ChannelHealth{}
		health[methodID] = state
	}
	now := healthNow()
	state.LastError, state.LastFailedAt = err.Error(), now.Unix()
	if !counts {
		return
	}
	state.Failures++
	if state.Failures >= breakerThreshold {
		state.TrippedUntil = now.Add(breakerCooldown).Unix()
	}
}

// channelHealth returns this server's view of a channel; nil when nothing
// failed since its last checkout.
func channelHealth(methodID int) *ChannelHealth {
	healthMu.Lock()
	defer healthMu.Unlock()
	state := health[methodID]
	if state == nil {
		return nil
	}
	view := *state
	return &view
}
