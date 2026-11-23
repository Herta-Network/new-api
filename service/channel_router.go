package service

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ChannelRouter routes the attempts of one request. It walks the request's
// groups in order (one group, or the Auto groups), each group's priority
// tiers from the highest, and within a tier ranks the candidates by weighted
// rendezvous hashing of the request's routing seed (route_seed.go): each
// candidate scores -ln(U)/weight with U drawn from the seed and the channel,
// and the lowest score wins. The key of a multi-key channel is ranked the
// same way. A failed attempt excludes its key; any failure but a key-level
// one also passes its channel over until every other candidate of the group
// was tried, and only then are the channel's other keys used. The next
// attempt takes the best remaining candidate. A tier is left only when none
// of its candidates is left, and RetryTimes only caps the attempts in a
// group.
//
// The same seed always ranks the candidates the same way on every instance,
// so one conversation stays on one channel and key, and an added or removed
// candidate moves only the seeds that ranked it first. Ranking by a random
// seed is a weighted random draw. A channel cooling down after a 429 is
// passed over while another candidate is ready, and used when every one is
// cooling.
type ChannelRouter struct {
	request         routeRequest
	inspected       bool
	modelName       string
	tokenGroup      string
	groups          []string
	groupsResolved  bool
	crossGroup      bool
	groupIdx        int
	firstGroupIdx   int
	groupTries      int
	locked          *model.Channel
	triedChannels   map[int]bool
	triedKeys       map[int]map[string]bool
	attempts        []routeAttempt
	templateApplied bool
	// mode is the failover mode the request runs under: "strict" only for a
	// strict session rule and a request that carries earlier turns.
	mode       string
	modeReason string
	pinWarned  bool
	// seedBody, when set, is the request body the seed is read from.
	seedBody []byte
	// candidates are the ones Next last listed, for group candidatesGroup;
	// Remaining reuses them.
	candidates      []model.RouteCandidate
	candidatesGroup int
}

// RouteChoice is the channel and key one attempt uses, and the group it was
// routed in ("" for a pinned or locked channel).
type RouteChoice struct {
	Channel *model.Channel
	Key     model.ChannelKey
	Group   string
}

type routeAttempt struct {
	N            int           `json:"n"`
	Group        string        `json:"group,omitempty"`
	Priority     int64         `json:"priority"`
	ChannelID    int           `json:"channel_id"`
	KeyIndex     int           `json:"key_index"`
	Rank         int           `json:"rank,omitempty"`
	TierSize     int           `json:"tier_size,omitempty"`
	Reason       string        `json:"reason"`
	TierDown     bool          `json:"tier_down,omitempty"`
	GroupAdvance bool          `json:"group_advance,omitempty"`
	Skipped      *routeSkipped `json:"skipped,omitempty"`
	Failure      *routeFailure `json:"failure,omitempty"`
}

// routeSkipped lists the candidates of the chosen tier that ranked higher
// than the chosen one, by why they were passed over.
type routeSkipped struct {
	Tried   []int `json:"tried,omitempty"`
	Cooling []int `json:"cooling,omitempty"`
}

// routeFailure is how an attempt failed and what the failure excluded: the
// key alone or the whole channel.
type routeFailure struct {
	Status int    `json:"status"`
	Code   string `json:"error_code,omitempty"`
	Scope  string `json:"scope"`
}

const ginKeyChannelRouter = "channel_router"

// RouterFor returns the router of the request on c, starting one when there
// is none.
func RouterFor(c *gin.Context) *ChannelRouter {
	if router := currentRouter(c); router != nil {
		return router
	}
	return NewChannelRouter(c)
}

// NewChannelRouter starts routing a new request on c, replacing any earlier
// router. A Responses WebSocket connection starts one for each
// response.create until a create locks the connection to its channel.
func NewChannelRouter(c *gin.Context) *ChannelRouter {
	router := &ChannelRouter{
		request:       routeRequest{seed: newRandomRouteSeed(), history: routeHistoryNone},
		triedChannels: map[int]bool{},
		triedKeys:     map[int]map[string]bool{},
		mode:          "prefer",
	}
	if c != nil {
		c.Set(ginKeyChannelRouter, router)
	}
	return router
}

func currentRouter(c *gin.Context) *ChannelRouter {
	if c == nil {
		return nil
	}
	value, ok := c.Get(ginKeyChannelRouter)
	if !ok {
		return nil
	}
	router, _ := value.(*ChannelRouter)
	return router
}

// IsStrictRoute reports a request whose failed attempt must not move to
// another channel: one of a strict session that carries earlier turns, which
// another account could not continue.
func IsStrictRoute(c *gin.Context) bool {
	router := currentRouter(c)
	return router != nil && router.mode == "strict"
}

// Lock keeps every attempt on channel, as a task continuing an earlier task
// must run where that task ran.
func (r *ChannelRouter) Lock(channel *model.Channel) {
	r.locked = channel
}

// SetSeedBody makes the router read the request's seed and earlier turns
// from body rather than the request's body storage: a Responses WebSocket
// create, whose payload is not the connection's HTTP request.
func (r *ChannelRouter) SetSeedBody(body []byte) {
	r.seedBody = body
}

// Next chooses the channel and key of the request's next attempt. A pinned
// or locked channel is the only candidate.
func (r *ChannelRouter) Next(c *gin.Context, modelName string) (RouteChoice, *ChannelSelectError) {
	return r.route(c, modelName, true)
}

// Peek returns the choice Next would make now, without recording an attempt,
// keeping what it read from the request, or moving a polling key cursor. The
// task plugin endpoint uses it to decide whether a plugin or an ordinary
// relay channel serves the request before the rest of the request's
// middleware, such as conversation recognition, has run; the distributor
// routes the request afterwards.
func (r *ChannelRouter) Peek(c *gin.Context, modelName string) (RouteChoice, *ChannelSelectError) {
	probe := NewChannelRouter(nil)
	probe.request = inspectRouteRequest(c, modelName, r.seedBody)
	probe.inspected = true
	probe.mode, probe.modeReason = routeMode(probe.request)
	return probe.route(c, modelName, false)
}

// route chooses the next attempt; commit records it, as Next does.
func (r *ChannelRouter) route(c *gin.Context, modelName string, commit bool) (RouteChoice, *ChannelSelectError) {
	r.modelName = modelName
	constraints := GetChannelConstraints(c)
	if pin, found, overridden := constraints.ResolvedPin(); found {
		if !r.pinWarned {
			r.pinWarned = true
			for _, lost := range overridden {
				logger.LogWarn(c, common.LogText(
					"channel pin overridden: winning_source=%s winning_channel_id=%d overridden_source=%s overridden_channel_id=%d",
					pin.Source, pin.ChannelId, lost.Source, lost.ChannelId,
				))
			}
		}
		channel, err := model.CacheGetChannel(pin.ChannelId)
		if err != nil {
			return RouteChoice{}, pinnedChannelUnavailable(pin, http.StatusBadRequest, i18n.MsgDistributorInvalidChannelId)
		}
		if channel.Status != common.ChannelStatusEnabled {
			return RouteChoice{}, pinnedChannelUnavailable(pin, http.StatusForbidden, i18n.MsgDistributorChannelDisabled)
		}
		if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
			return RouteChoice{}, &ChannelSelectError{
				StatusCode: http.StatusBadRequest, Code: types.ErrorCode(kind), MessageID: i18n.MsgDistributorNoAvailableChannel,
				Params:     map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName},
				FilterKind: kind, Channel: channel,
			}
		}
		return r.nextOnChannel(c, channel, "pinned", commit)
	}
	if r.locked != nil {
		return r.nextOnChannel(c, r.locked, "locked", commit)
	}

	if !r.inspected {
		r.inspect(c, modelName)
	}
	if !r.groupsResolved {
		if selectErr := r.resolveGroups(c, modelName); selectErr != nil {
			return RouteChoice{}, selectErr
		}
	}
	cooldowns := model.ChannelCooldownsFor(modelName)
	budget := common.RetryTimes + 1
	for r.groupIdx < len(r.groups) {
		group := r.groups[r.groupIdx]
		if r.groupTries >= budget {
			if !r.crossGroup {
				return RouteChoice{}, r.noChannel(c, modelName)
			}
			r.advanceGroup()
			continue
		}
		candidates, err := model.ListRouteCandidates(group, modelName, constraints.Filters)
		if err != nil {
			return RouteChoice{}, r.selectionFailed(group, modelName, err)
		}
		if commit {
			r.candidates, r.candidatesGroup = candidates, r.groupIdx
		}
		// A group without any candidate is skipped, with or without
		// cross-group retry, so an Auto request starts in the first group that
		// serves it. Only cross-group retry moves on from a group whose
		// candidates were all tried.
		if len(candidates) == 0 {
			r.advanceGroup()
			continue
		}
		choice, attempt, ok := r.pick(group, candidates, cooldowns, commit)
		if !ok {
			if !r.crossGroup {
				return RouteChoice{}, r.noChannel(c, modelName)
			}
			r.advanceGroup()
			continue
		}
		if !commit {
			return choice, nil
		}
		if len(r.attempts) == 0 {
			r.firstGroupIdx = r.groupIdx
		}
		attempt.GroupAdvance = r.groupIdx > r.firstGroupIdx
		if r.tokenGroup == "auto" {
			common.SetContextKey(c, constant.ContextKeyAutoGroup, group)
		}
		r.record(attempt)
		return choice, nil
	}
	return RouteChoice{}, r.noChannel(c, modelName)
}

// Fail records that the attempt on channelID failed with err. The key the
// attempt used is read from c and is not chosen again. A key-level error
// (401, 403, 429, an invalid key) leaves the other keys of a multi-key
// channel eligible; any other error passes the channel over until the
// group's other candidates were tried.
func (r *ChannelRouter) Fail(c *gin.Context, channelID int, err *types.NewAPIError) {
	if channelID <= 0 || err == nil {
		return
	}
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	if key != "" {
		if r.triedKeys[channelID] == nil {
			r.triedKeys[channelID] = map[string]bool{}
		}
		r.triedKeys[channelID][key] = true
	}
	scope := "channel"
	if key != "" && common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) && isKeyLevelFailure(err) {
		scope = "key"
	} else {
		r.triedChannels[channelID] = true
	}
	r.groupTries++
	failure := &routeFailure{Status: err.StatusCode, Code: string(err.GetErrorCode()), Scope: scope}
	if n := len(r.attempts); n > 0 && r.attempts[n-1].ChannelID == channelID && r.attempts[n-1].Failure == nil {
		r.attempts[n-1].Failure = failure
		return
	}
	// An attempt routing did not choose: a task continuing on its origin
	// channel, which resolved its key before routing ran.
	reason := "preselected"
	if r.locked != nil {
		reason = "locked"
	}
	r.attempts = append(r.attempts, routeAttempt{
		N: len(r.attempts) + 1, ChannelID: channelID, KeyIndex: common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		Reason: reason, Failure: failure,
	})
}

func isKeyLevelFailure(err *types.NewAPIError) bool {
	switch err.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return err.GetErrorCode() == types.ErrorCodeChannelInvalidKey
}

// Remaining is how many more attempts the request may make: the attempts
// left in the current group's budget while it has an untried candidate,
// else a fresh budget when cross-group retry reaches a later group with one.
// It follows the same walk as Next without choosing.
func (r *ChannelRouter) Remaining(c *gin.Context) int {
	budget := common.RetryTimes + 1
	constraints := GetChannelConstraints(c)
	pin, pinned, _ := constraints.ResolvedPin()
	if pinned || r.locked != nil {
		channel := r.locked
		if pinned {
			channel, _ = model.CacheGetChannel(pin.ChannelId)
		}
		if channel == nil || channel.Status != common.ChannelStatusEnabled || len(channel.EnabledKeys()) == 0 {
			return 0
		}
		return max(budget-r.groupTries, 0)
	}
	if !r.groupsResolved {
		return max(budget-r.groupTries, 0)
	}
	tries := r.groupTries
	for idx := r.groupIdx; idx < len(r.groups); idx, tries = idx+1, 0 {
		if tries >= budget {
			if !r.crossGroup {
				return 0
			}
			continue
		}
		candidates := r.candidates
		if idx != r.candidatesGroup || candidates == nil {
			var err error
			if candidates, err = model.ListRouteCandidates(r.groups[idx], r.modelName, constraints.Filters); err != nil {
				return 0
			}
		}
		if len(candidates) == 0 {
			continue
		}
		if slices.ContainsFunc(candidates, func(candidate model.RouteCandidate) bool { return len(r.untriedKeys(candidate.Channel)) > 0 }) {
			return budget - tries
		}
		if !r.crossGroup {
			return 0
		}
	}
	return 0
}

func (r *ChannelRouter) inspect(c *gin.Context, modelName string) {
	r.inspected = true
	r.request = inspectRouteRequest(c, modelName, r.seedBody)
	r.mode, r.modeReason = routeMode(r.request)
	rule := r.request.rule
	if rule == nil {
		return
	}
	state := RequestPolicy(c)
	state.SessionMode, state.SessionModeSource, state.RuleName = r.mode, rule.modeSource, rule.name
	state.AddEvent(PolicyEvent{Decision: PolicyDecision{Action: "match", Reason: "session_rule_matched", Source: "session_rule"}})
}

// routeMode is the failover mode of a request: strict only for a strict
// session rule and a request that carries, or may carry, earlier turns.
// Nothing earlier ties a first turn to one account, so it fails over.
func routeMode(request routeRequest) (mode, reason string) {
	if request.rule == nil || request.rule.mode != "strict" {
		return "prefer", ""
	}
	if request.history == routeHistoryNone {
		return "prefer", "no_history"
	}
	return "strict", ""
}

func (r *ChannelRouter) resolveGroups(c *gin.Context, modelName string) *ChannelSelectError {
	r.tokenGroup = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if r.tokenGroup != "auto" {
		r.groups, r.groupsResolved = []string{r.tokenGroup}, true
		return nil
	}
	r.groups = GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	if len(r.groups) == 0 {
		return &ChannelSelectError{
			StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGetChannelFailed,
			Params: map[string]any{"Group": "auto", "Model": modelName, "Error": "auto groups is not enabled"},
		}
	}
	r.crossGroup = common.GetContextKeyBool(c, constant.ContextKeyTokenCrossGroupRetry)
	r.groupsResolved = true
	return nil
}

func (r *ChannelRouter) advanceGroup() {
	r.groupIdx++
	r.groupTries = 0
}

// nextOnChannel chooses the key of the next attempt on a pinned or locked
// channel. Its keys are tried in turn, and start over once each was tried,
// since no other channel can take the request; the budget still caps the
// attempts.
func (r *ChannelRouter) nextOnChannel(c *gin.Context, channel *model.Channel, reason string, commit bool) (RouteChoice, *ChannelSelectError) {
	if r.groupTries >= common.RetryTimes+1 {
		return RouteChoice{}, r.noChannel(c, r.modelName)
	}
	keys := channel.EnabledKeys()
	if untried := slices.DeleteFunc(slices.Clone(keys), func(key model.ChannelKey) bool { return r.triedKeys[channel.Id][key.Value] }); len(untried) > 0 {
		keys = untried
	}
	if len(keys) == 0 {
		return RouteChoice{}, &ChannelSelectError{
			StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeChannelNoAvailableKey, MessageID: i18n.MsgDistributorNoAvailableChannel,
			Params: map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": r.modelName},
		}
	}
	key := r.pickKey(channel, keys, commit)
	if commit {
		r.record(routeAttempt{ChannelID: channel.Id, Priority: channel.GetPriority(), KeyIndex: key.Index, Reason: reason})
	}
	return RouteChoice{Channel: channel, Key: key}, nil
}

// routeEntry is one candidate of a group as one attempt sees it.
type routeEntry struct {
	candidate model.RouteCandidate
	score     float64
	// zero is a candidate of weight 0, ranked after every positive weight.
	zero bool
	// failed is a channel an attempt failed on for a reason other than its
	// key; it is used again only once no other candidate is left.
	failed bool
	// keys are the enabled keys not tried yet; ready leaves out the cooling.
	keys  []model.ChannelKey
	ready []model.ChannelKey
}

func (entry routeEntry) before(other routeEntry) bool {
	if entry.zero != other.zero {
		return !entry.zero
	}
	if entry.score != other.score {
		return entry.score < other.score
	}
	return entry.candidate.Channel.Id < other.candidate.Channel.Id
}

// pick ranks the candidates of group and chooses the best one with a key
// left. It prefers candidates no attempt failed on and that are not cooling
// down: the first pass passes over cooling ones, and when only cooling ones
// are left the second pass uses them, so a cooldown never fails a request on
// its own. The last pass returns to channels an attempt failed on for their
// untried keys. A strict request does not route around a cooldown.
func (r *ChannelRouter) pick(group string, candidates []model.RouteCandidate, cooldowns model.ChannelCooldowns, commit bool) (RouteChoice, routeAttempt, bool) {
	entries := make([]routeEntry, len(candidates))
	var priorities []int64
	for i, candidate := range candidates {
		id := candidate.Channel.Id
		entry := routeEntry{candidate: candidate, zero: candidate.Weight <= 0, failed: r.triedChannels[id], score: routeChannelScore(r.request.seed, id, max(candidate.Weight, 1))}
		multiKey := candidate.Channel.ChannelInfo.IsMultiKey
		for _, key := range r.untriedKeys(candidate.Channel) {
			entry.keys = append(entry.keys, key)
			fingerprint := ""
			if multiKey && cooldowns.HasChannel(id) {
				fingerprint = model.ChannelKeyFingerprint(key.Value)
			}
			if !cooldowns.Cooling(id, fingerprint) {
				entry.ready = append(entry.ready, key)
			}
		}
		entries[i] = entry
		if !slices.Contains(priorities, candidate.Priority) {
			priorities = append(priorities, candidate.Priority)
		}
	}
	slices.SortFunc(priorities, func(a, b int64) int { return compareInt64(b, a) })
	for pass := range 3 {
		ignoreCooling := pass > 0 || r.mode == "strict"
		failed := pass == 2
		for _, priority := range priorities {
			best := -1
			for i, entry := range entries {
				if entry.candidate.Priority != priority || entry.failed != failed || len(entry.usable(ignoreCooling)) == 0 {
					continue
				}
				if best < 0 || entry.before(entries[best]) {
					best = i
				}
			}
			if best < 0 {
				continue
			}
			chosen := entries[best]
			key := r.pickKey(chosen.candidate.Channel, chosen.usable(ignoreCooling), commit)
			attempt := routeAttempt{
				Group: group, Priority: priority, ChannelID: chosen.candidate.Channel.Id, KeyIndex: key.Index,
				TierDown: priority != priorities[0],
			}
			r.rankAttempt(&attempt, entries, chosen, ignoreCooling)
			if failed {
				attempt.Reason = "other_key"
			}
			return RouteChoice{Channel: chosen.candidate.Channel, Key: key, Group: group}, attempt, true
		}
	}
	return RouteChoice{}, routeAttempt{}, false
}

func (entry routeEntry) usable(ignoreCooling bool) []model.ChannelKey {
	if ignoreCooling {
		return entry.keys
	}
	return entry.ready
}

// rankAttempt records where the chosen candidate ranks in its tier for the
// seed, and why the higher ranked ones were passed over.
func (r *ChannelRouter) rankAttempt(attempt *routeAttempt, entries []routeEntry, chosen routeEntry, ignoreCooling bool) {
	var tier []routeEntry
	for _, entry := range entries {
		if entry.candidate.Priority == chosen.candidate.Priority {
			tier = append(tier, entry)
		}
	}
	slices.SortFunc(tier, func(a, b routeEntry) int {
		switch {
		case a.before(b):
			return -1
		case b.before(a):
			return 1
		}
		return 0
	})
	attempt.TierSize = len(tier)
	for i, entry := range tier {
		if entry.candidate.Channel.Id == chosen.candidate.Channel.Id {
			attempt.Rank = i + 1
			break
		}
		if attempt.Skipped == nil {
			attempt.Skipped = &routeSkipped{}
		}
		if entry.failed || len(entry.keys) == 0 {
			attempt.Skipped.Tried = append(attempt.Skipped.Tried, entry.candidate.Channel.Id)
		} else if !ignoreCooling {
			attempt.Skipped.Cooling = append(attempt.Skipped.Cooling, entry.candidate.Channel.Id)
		}
	}
	switch {
	case r.request.seed.random():
		attempt.Reason = "random"
	case attempt.Rank == 1:
		attempt.Reason = "seed_top"
	default:
		attempt.Reason = "seed_fallback"
	}
}

// untriedKeys lists the enabled keys of channel the request has not tried.
func (r *ChannelRouter) untriedKeys(channel *model.Channel) []model.ChannelKey {
	if channel == nil {
		return nil
	}
	return slices.DeleteFunc(channel.EnabledKeys(), func(key model.ChannelKey) bool { return r.triedKeys[channel.Id][key.Value] })
}

// pickKey chooses among keys of channel by the seed. A committed choice for
// a request without a seed on a polling channel takes the keys in turn, as
// before.
func (r *ChannelRouter) pickKey(channel *model.Channel, keys []model.ChannelKey, commit bool) model.ChannelKey {
	if len(keys) == 1 {
		return keys[0]
	}
	if commit && r.request.seed.random() && channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
		if key, err := channel.NextPolledKey(keys); err == nil {
			return key
		}
	}
	best, bestScore := keys[0], routeKeyScore(r.request.seed, channel.Id, keys[0].Value)
	for _, key := range keys[1:] {
		if score := routeKeyScore(r.request.seed, channel.Id, key.Value); score < bestScore {
			best, bestScore = key, score
		}
	}
	return best
}

func (r *ChannelRouter) record(attempt routeAttempt) {
	attempt.N = len(r.attempts) + 1
	r.attempts = append(r.attempts, attempt)
}

// selectedGroup is the group of the latest routed attempt.
func (r *ChannelRouter) selectedGroup() string {
	for i := len(r.attempts) - 1; i >= 0; i-- {
		if r.attempts[i].Group != "" {
			return r.attempts[i].Group
		}
	}
	return r.tokenGroup
}

func (r *ChannelRouter) noChannel(c *gin.Context, modelName string) *ChannelSelectError {
	return &ChannelSelectError{
		StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel,
		Params: map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName}, NoAvailableChannel: true,
	}
}

func (r *ChannelRouter) selectionFailed(group, modelName string, err error) *ChannelSelectError {
	showGroup := group
	if r.tokenGroup == "auto" {
		showGroup = fmt.Sprintf("auto(%s)", group)
	}
	return &ChannelSelectError{
		StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGetChannelFailed,
		Params: map[string]any{"Group": showGroup, "Model": modelName, "Error": err.Error()},
	}
}

// routeChannelScore is the rendezvous score of a channel for seed: lower
// ranks first, and over many seeds each channel ranks first in proportion to
// its weight.
func routeChannelScore(seed routeSeed, channelID int, weight int) float64 {
	var buf [41]byte
	copy(buf[:32], seed.digest[:])
	buf[32] = 'c'
	binary.BigEndian.PutUint64(buf[33:], uint64(channelID))
	return -math.Log(routeUniform(sha256.Sum256(buf[:]))) / float64(weight)
}

// routeKeyScore ranks the keys of one channel for seed, all weighted alike.
func routeKeyScore(seed routeSeed, channelID int, key string) float64 {
	buf := make([]byte, 0, 41+len(key))
	buf = append(buf, seed.digest[:]...)
	buf = append(buf, 'k')
	buf = binary.BigEndian.AppendUint64(buf, uint64(channelID))
	buf = append(buf, key...)
	return -math.Log(routeUniform(sha256.Sum256(buf)))
}

// routeUniform maps a digest to a uniform value in (0, 1).
func routeUniform(sum [32]byte) float64 {
	n := binary.BigEndian.Uint64(sum[:8]) >> 12
	return (float64(n) + 0.5) / float64(uint64(1)<<52)
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// routeLog is the routing record in the admin part of a request's log.
type routeLog struct {
	Seed       *routeLogSeed  `json:"seed,omitempty"`
	Rule       *routeLogRule  `json:"rule,omitempty"`
	Mode       string         `json:"mode"`
	ModeReason string         `json:"mode_reason,omitempty"`
	History    string         `json:"history,omitempty"`
	Attempts   []routeAttempt `json:"attempts"`
}

type routeLogSeed struct {
	Source      string `json:"source"`
	Fingerprint string `json:"fp,omitempty"`
}

type routeLogRule struct {
	Name              string `json:"name"`
	Mode              string `json:"mode"`
	TemplateApplied   bool   `json:"template_applied,omitempty"`
	ParamOverrideKeys int    `json:"param_override_keys,omitempty"`
}

// AppendRoutingAdminInfo adds how the request on c was routed to the admin
// part of its log.
func AppendRoutingAdminInfo(c *gin.Context, other *model.LogOther) {
	router := currentRouter(c)
	if router == nil || other == nil || len(router.attempts) == 0 {
		return
	}
	record := routeLog{Mode: router.mode, ModeReason: router.modeReason, Attempts: router.attempts}
	if router.inspected {
		record.Seed = &routeLogSeed{Source: router.request.seed.source, Fingerprint: router.request.seed.fingerprint()}
		record.History = string(router.request.history)
	}
	if rule := router.request.rule; rule != nil {
		record.Rule = &routeLogRule{Name: rule.name, Mode: rule.mode, TemplateApplied: router.templateApplied}
		if router.templateApplied {
			record.Rule.ParamOverrideKeys = len(rule.template)
		}
	}
	other.SetAdmin("routing", record)
}
