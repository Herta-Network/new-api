package service

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelSelectAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()
	affinity := operation_setting.GetChannelAffinitySetting()
	originalAffinity := *affinity

	pgtest.Reset(t, model.DB)
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","empty":"Empty"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"empty":1}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("3"))

	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		*affinity = originalAffinity
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))

		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})

	return model.DB
}

func createChannelSelectAutoGroupsChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	t.Helper()
	createRouterChannel(t, routerChannel{id: id, group: group, model: modelName, weight: 100})
}

// routerChannel describes a channel fixture; keys > 1 makes it multi-key.
type routerChannel struct {
	id       int
	group    string
	model    string
	priority int64
	weight   uint
	keys     int
	polling  bool
}

func createRouterChannel(t *testing.T, spec routerChannel) *model.Channel {
	t.Helper()
	priority, weight := spec.priority, spec.weight
	channel := &model.Channel{
		Id: spec.id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", spec.id), Status: common.ChannelStatusEnabled,
		Name: fmt.Sprintf("channel-%d", spec.id), Weight: &weight, Models: spec.model, Group: spec.group, Priority: &priority,
	}
	if spec.keys > 1 {
		keys := make([]string, spec.keys)
		for i := range keys {
			keys[i] = fmt.Sprintf("key-%d-%d", spec.id, i)
		}
		channel.Key = strings.Join(keys, "\n")
		channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: spec.keys, MultiKeyMode: constant.MultiKeyModeRandom}
		if spec.polling {
			channel.ChannelInfo.MultiKeyMode = constant.MultiKeyModePolling
		}
	}
	require.NoError(t, channel.Insert())
	return channel
}

// routerTestContext is a request of user 7 in group "default".
func routerTestContext(t *testing.T, path, body string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 7)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c
}

// testSeed is a fixed seed for distribution tests.
func testSeed(i int) routeSeed {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(i))
	return routeSeed{digest: sha256.Sum256(buf[:]), source: "test"}
}

// seededRouter starts routing c by seed.
func seededRouter(c *gin.Context, seed routeSeed) *ChannelRouter {
	router := NewChannelRouter(c)
	router.inspected = true
	router.request.seed = seed
	return router
}

// failAttempt records a failed attempt on choice with status, as the relay
// loops do after SetupContextForSelectedChannel.
func failAttempt(c *gin.Context, router *ChannelRouter, choice RouteChoice, status int) {
	common.SetContextKey(c, constant.ContextKeyChannelKey, choice.Key.Value)
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, choice.Channel.ChannelInfo.IsMultiKey)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, choice.Key.Index)
	router.Fail(c, choice.Channel.Id, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, status))
}

func TestRouterSameSeedChoosesSameChannelAndKey(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-same-seed"
	for id := 3001; id <= 3005; id++ {
		createRouterChannel(t, routerChannel{id: id, group: "default", model: modelName, weight: 10, keys: 4})
	}
	model.InitChannelCache()

	choose := func(userID int, session string) RouteChoice {
		c := routerTestContext(t, "/v1/chat/completions", `{"model":"router-same-seed"}`)
		c.Set("id", userID)
		c.Request.Header.Set("Session_id", session)
		choice, selectErr := SelectChannelForRequest(c, modelName)
		require.Nil(t, selectErr)
		assert.Equal(t, "header:Session_id", RouterFor(c).request.seed.source)
		return choice
	}
	first := choose(7, "conversation-1")
	for range 3 {
		again := choose(7, "conversation-1")
		assert.Equal(t, first.Channel.Id, again.Channel.Id, "the same seed chooses the same channel")
		assert.Equal(t, first.Key, again.Key, "and the same key of it")
	}
	assert.NotEqual(t, scopedRouteSeed(7, modelName, "header:Session_id", []byte("conversation-1")).digest,
		scopedRouteSeed(8, modelName, "header:Session_id", []byte("conversation-1")).digest, "seeds are scoped to the user")
}

// A channel's rendezvous score is its seed-drawn exponential divided by its
// weight: with three times the weight it scores a third, so it ranks first
// three times as often over many seeds. Weight 0 is used only once no
// positive weight is left.
func TestRouterScoresCandidatesByWeight(t *testing.T) {
	for i := range 8 {
		seed := testSeed(i)
		assert.InDelta(t, routeChannelScore(seed, 3101, 1)/3, routeChannelScore(seed, 3101, 3), 1e-12)
		assert.Greater(t, routeChannelScore(seed, 3101, 1), 0.0)
		assert.Equal(t, routeChannelScore(seed, 3101, 2), routeChannelScore(seed, 3101, 2), "the score depends only on the seed, channel and weight")
	}
	assert.NotEqual(t, routeChannelScore(testSeed(1), 3101, 1), routeChannelScore(testSeed(1), 3102, 1))
	assert.NotEqual(t, routeChannelScore(testSeed(1), 3101, 1), routeChannelScore(testSeed(2), 3101, 1))

	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-weights"
	createRouterChannel(t, routerChannel{id: 3101, group: "default", model: modelName, weight: 1})
	createRouterChannel(t, routerChannel{id: 3102, group: "default", model: modelName, weight: 3})
	createRouterChannel(t, routerChannel{id: 3103, group: "default", model: modelName, weight: 0})
	model.InitChannelCache()

	// A small fixed sample: both weighted channels rank first for some seeds,
	// the heavier one more often, and weight 0 never while they are left.
	c := routerTestContext(t, "/v1/chat/completions", `{}`)
	counts := map[int]int{}
	for i := range 64 {
		choice, selectErr := seededRouter(c, testSeed(i)).Next(c, modelName)
		require.Nil(t, selectErr)
		counts[choice.Channel.Id]++
	}
	assert.Positive(t, counts[3101])
	assert.Greater(t, counts[3102], counts[3101])
	assert.Zero(t, counts[3103])

	common.RetryTimes = 5
	router := seededRouter(c, testSeed(1))
	for range 2 {
		choice, selectErr := router.Next(c, modelName)
		require.Nil(t, selectErr)
		failAttempt(c, router, choice, http.StatusInternalServerError)
	}
	choice, selectErr := router.Next(c, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, 3103, choice.Channel.Id, "the weight-0 channel takes over once the others failed")
}

// The relay loops ask Remaining after each failure: a retry is decided while
// the group has budget and an untried candidate, and stops otherwise.
func TestRouterRemainingDrivesTheRetryDecision(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-decide"
	createRouterChannel(t, routerChannel{id: 3151, group: "default", model: modelName, weight: 1})
	createRouterChannel(t, routerChannel{id: 3152, group: "default", model: modelName, weight: 1})
	model.InitChannelCache()
	upstream := types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	for _, tc := range []struct {
		name      string
		retries   int
		decisions []PolicyDecision
	}{
		{name: "budget left and a candidate left", retries: 1, decisions: []PolicyDecision{
			{Action: "retry", Reason: "retry_status_matched", Source: "global"},
			{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"},
		}},
		{name: "candidates run out before the budget", retries: 5, decisions: []PolicyDecision{
			{Action: "retry", Reason: "retry_status_matched", Source: "global"},
			{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"},
		}},
		{name: "no budget", decisions: []PolicyDecision{
			{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.RetryTimes = tc.retries
			c := routerTestContext(t, "/v1/chat/completions", `{}`)
			router := RouterFor(c)
			var decisions []PolicyDecision
			for {
				choice, selectErr := router.Next(c, modelName)
				require.Nil(t, selectErr)
				failAttempt(c, router, choice, http.StatusInternalServerError)
				decision := DecideRelayRetry(c, upstream, router.Remaining(c))
				decisions = append(decisions, decision)
				if decision.Action != "retry" {
					break
				}
			}
			assert.Equal(t, tc.decisions, decisions)
		})
	}
}

// Peek ranks like Next but leaves the request's routing as it was: no
// attempt, no seed kept, no policy event, no polling cursor moved.
func TestRouterPeekLeavesRoutingUntouched(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-peek"
	createRouterChannel(t, routerChannel{id: 3171, group: "default", model: modelName, weight: 1, keys: 3, polling: true})
	model.InitChannelCache()
	setting := operation_setting.GetChannelAffinitySetting()
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, Rules: []operation_setting.ChannelAffinityRule{{
		Name: "header", ModelRegex: []string{".*"}, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}, SessionMode: "prefer",
	}}}

	c := routerTestContext(t, "/v1/chat/completions", `{}`)
	c.Request.Header.Set("X-Session", "conversation")
	peeked, selectErr := PeekChannelForRequest(c, modelName)
	require.Nil(t, selectErr)
	router := RouterFor(c)
	assert.False(t, router.inspected)
	assert.Empty(t, router.attempts)
	assert.Empty(t, RequestPolicy(c).Events())
	chosen, selectErr := router.Next(c, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, peeked, chosen, "the same seed chooses what Peek saw")
	assert.Len(t, router.attempts, 1)

	unseeded := routerTestContext(t, "/v1/chat/completions", `{}`)
	*setting = operation_setting.ChannelAffinitySetting{}
	for range 2 {
		_, selectErr = PeekChannelForRequest(unseeded, modelName)
		require.Nil(t, selectErr)
	}
	choice, selectErr := RouterFor(unseeded).Next(unseeded, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, 0, choice.Key.Index, "peeking moves no polling cursor")
}

// A Responses WebSocket create is not the connection's HTTP request: its
// payload seeds the route and tells whether it continues a conversation.
func TestRouterSeedsFromTheCreateBody(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-ws-seed"
	createRouterChannel(t, routerChannel{id: 3181, group: "default", model: modelName, weight: 1})
	model.InitChannelCache()
	setting := operation_setting.GetChannelAffinitySetting()
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: true, TemplateSpread: 1, Rules: []operation_setting.ChannelAffinityRule{{
		Name: "strict", ModelRegex: []string{".*"}, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Strict"}}, SessionMode: "strict",
	}}}
	for _, tc := range []struct {
		name, create, header, source string
		strict                       bool
	}{
		{name: "the create's cache key", create: `{"model":"router-ws-seed","prompt_cache_key":"pck","input":"hi"}`, source: "prompt_cache_key"},
		{name: "the create's first user message", create: `{"model":"router-ws-seed","input":"hi"}`, source: "first_user"},
		{name: "a first create fails over", create: `{"model":"router-ws-seed","input":"hi"}`, header: "s", source: "rule:strict"},
		{name: "a create continuing a stored response is strict", create: `{"model":"router-ws-seed","previous_response_id":"resp_1","input":"next"}`, header: "s", source: "rule:strict", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"type":"response.create","response":{"model":"router-ws-seed"}}`))
			c.Set("id", 7)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			if tc.header != "" {
				c.Request.Header.Set("X-Strict", tc.header)
			}
			router := NewChannelRouter(c)
			router.SetSeedBody([]byte(tc.create))
			_, selectErr := router.Next(c, modelName)
			require.Nil(t, selectErr)
			assert.Equal(t, tc.source, router.request.seed.source)
			assert.Equal(t, tc.strict, IsStrictRoute(c))
		})
	}
}

// Only an earlier turn followed by more is history: a trailing assistant
// message is a prefill. A body that is not JSON cannot show it has none.
func TestRouteHistoryIgnoresPrefills(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, Rules: []operation_setting.ChannelAffinityRule{{
		Name: "strict", ModelRegex: []string{".*"}, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Strict"}}, SessionMode: "strict",
	}}}
	for _, tc := range []struct {
		name, path, body string
		history          routeHistory
	}{
		{name: "chat first turn", path: "/v1/chat/completions", body: `{"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`, history: routeHistoryNone},
		{name: "chat prefill", path: "/v1/chat/completions", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"Sure,"}]}`, history: routeHistoryNone},
		{name: "chat later turn", path: "/v1/chat/completions", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"},{"role":"user","content":"more"}]}`, history: routeHistoryPresent},
		{name: "messages prefill", path: "/v1/messages", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"{"}]}`, history: routeHistoryNone},
		{name: "messages tool result", path: "/v1/messages", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"t"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t"}]}]}`, history: routeHistoryPresent},
		{name: "responses trailing reasoning", path: "/v1/responses", body: `{"input":[{"role":"user","content":"hi"},{"type":"reasoning","summary":[]}]}`, history: routeHistoryNone},
		{name: "responses tool round trip", path: "/v1/responses", body: `{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"c"},{"type":"function_call_output","call_id":"c","output":"1"}]}`, history: routeHistoryPresent},
		{name: "responses stored response", path: "/v1/responses", body: `{"previous_response_id":"resp_1","input":"next"}`, history: routeHistoryPresent},
		{name: "gemini prefill", path: "/v1beta/models/g:generateContent", body: `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"text":"ok"}]}]}`, history: routeHistoryNone},
		{name: "a body that is not JSON", path: "/v1/chat/completions", body: `{"messages":[{"role":"user"`, history: routeHistoryUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := routerTestContext(t, tc.path, tc.body)
			c.Request.Header.Set("X-Strict", "s")
			request := inspectRouteRequest(c, "m", nil)
			assert.Equal(t, tc.history, request.history)
			mode, _ := routeMode(request)
			assert.Equal(t, tc.history != routeHistoryNone, mode == "strict")
		})
	}
}

// Retries take the untried candidates of the best tier first and move down a
// tier only when it has none left; RetryTimes caps the attempts. Both cache
// paths walk the same way.
func TestRouterRetriesWalkCandidatesBeforeTiers(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-walk"
	createRouterChannel(t, routerChannel{id: 3201, group: "default", model: modelName, priority: 10, weight: 1})
	createRouterChannel(t, routerChannel{id: 3202, group: "default", model: modelName, priority: 10, weight: 1})
	createRouterChannel(t, routerChannel{id: 3203, group: "default", model: modelName, priority: 5, weight: 1})
	for _, memoryCache := range []bool{false, true} {
		for _, tc := range []struct {
			retries   int
			attempts  int
			remaining []int
		}{
			{retries: 3, attempts: 3, remaining: []int{3, 2, 0}},
			{retries: 1, attempts: 2, remaining: []int{1, 0}},
			{retries: 0, attempts: 1, remaining: []int{0}},
		} {
			t.Run(fmt.Sprintf("cache=%v/retries=%d", memoryCache, tc.retries), func(t *testing.T) {
				common.MemoryCacheEnabled = memoryCache
				model.InitChannelCache()
				common.RetryTimes = tc.retries
				c := routerTestContext(t, "/v1/chat/completions", `{}`)
				router := seededRouter(c, testSeed(42))
				var visited []int
				var remaining []int
				for {
					choice, selectErr := router.Next(c, modelName)
					if selectErr != nil {
						assert.True(t, selectErr.NoAvailableChannel)
						break
					}
					visited = append(visited, choice.Channel.Id)
					failAttempt(c, router, choice, http.StatusInternalServerError)
					remaining = append(remaining, router.Remaining(c))
					if remaining[len(remaining)-1] == 0 {
						break
					}
				}
				require.Len(t, visited, tc.attempts)
				assert.Equal(t, tc.remaining, remaining)
				if len(visited) >= 2 {
					assert.ElementsMatch(t, []int{3201, 3202}, visited[:2], "the top tier is used up first")
				}
				if len(visited) == 3 {
					assert.Equal(t, 3203, visited[2])
					assert.True(t, router.attempts[2].TierDown)
					assert.Nil(t, router.attempts[2].Skipped, "the lower tier has no candidate ranked above")
				}
				if len(visited) >= 2 {
					assert.Equal(t, []int{visited[0]}, router.attempts[1].Skipped.Tried)
					assert.Equal(t, "seed_fallback", router.attempts[1].Reason)
				}
			})
		}
	}
}

func TestRouterNeverRetriesTheFailedChannelOfASingleTier(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-single"
	createRouterChannel(t, routerChannel{id: 3301, group: "default", model: modelName, weight: 1})
	model.InitChannelCache()
	common.RetryTimes = 3

	c := routerTestContext(t, "/v1/chat/completions", `{}`)
	router := RouterFor(c)
	choice, selectErr := router.Next(c, modelName)
	require.Nil(t, selectErr)
	failAttempt(c, router, choice, http.StatusBadGateway)
	assert.Zero(t, router.Remaining(c))
	_, selectErr = router.Next(c, modelName)
	require.NotNil(t, selectErr)
	assert.True(t, selectErr.NoAvailableChannel)
}

// A channel cooling down after a 429 is passed over for the next candidate,
// a cooling tier for the next tier, and used when every candidate cools.
// A strict request stays on its seed's first choice.
func TestRouterPassesOverCoolingCandidates(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const a, b, low = 3401, 3402, 3403
	seed := testSeed(7)
	first, second := a, b
	if routeChannelScore(seed, b, 1) < routeChannelScore(seed, a, 1) {
		first, second = b, a
	}
	for _, tc := range []struct {
		name    string
		cooling []int
		strict  bool
		want    int
		reason  string
	}{
		{name: "the seed's first choice", want: first, reason: "seed_top"},
		{name: "a cooling first choice hands over within the tier", cooling: []int{first}, want: second, reason: "seed_fallback"},
		{name: "a cooling tier hands over to the next tier", cooling: []int{first, second}, want: low, reason: "seed_top"},
		{name: "every candidate cooling keeps the first choice", cooling: []int{first, second, low}, want: first, reason: "seed_top"},
		{name: "strict does not route around a cooldown", cooling: []int{first}, strict: true, want: first, reason: "seed_top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupChannelSelectAutoGroupsTest(t)
			modelName := "router-cooling-" + strings.ReplaceAll(tc.name, " ", "-")
			createRouterChannel(t, routerChannel{id: a, group: "default", model: modelName, priority: 10, weight: 1})
			createRouterChannel(t, routerChannel{id: b, group: "default", model: modelName, priority: 10, weight: 1})
			createRouterChannel(t, routerChannel{id: low, group: "default", model: modelName, priority: 0, weight: 1})
			model.InitChannelCache()
			for _, id := range tc.cooling {
				model.RecordChannelRateLimit(id, "", modelName, 2*time.Minute)
			}
			c := routerTestContext(t, "/v1/chat/completions", `{}`)
			router := seededRouter(c, seed)
			if tc.strict {
				router.mode = "strict"
			}
			choice, selectErr := router.Next(c, modelName)
			require.Nil(t, selectErr)
			assert.Equal(t, tc.want, choice.Channel.Id)
			assert.Equal(t, tc.reason, router.attempts[0].Reason)
			if tc.want == second {
				assert.Equal(t, []int{first}, router.attempts[0].Skipped.Cooling)
			}
		})
	}
}

// A key-level failure or cooldown moves a conversation to another key of the
// same channel; any other failure moves it to another channel first, and to
// the failed channel's other keys once no other channel is left. Disabled
// keys are never chosen.
func TestRouterChoosesKeysOfMultiKeyChannels(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-keys"
	multi := createRouterChannel(t, routerChannel{id: 3501, group: "default", model: modelName, priority: 10, weight: 1, keys: 3})
	createRouterChannel(t, routerChannel{id: 3502, group: "default", model: modelName, priority: 0, weight: 1})
	model.InitChannelCache()
	common.RetryTimes = 5
	seed := testSeed(11)

	c := routerTestContext(t, "/v1/chat/completions", `{}`)
	router := seededRouter(c, seed)
	choice, selectErr := router.Next(c, modelName)
	require.Nil(t, selectErr)
	require.Equal(t, multi.Id, choice.Channel.Id)
	firstKey := choice.Key
	failAttempt(c, router, choice, http.StatusTooManyRequests)
	assert.Equal(t, "key", router.attempts[0].Failure.Scope)
	choice, selectErr = router.Next(c, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, multi.Id, choice.Channel.Id, "a key-level failure keeps the channel")
	assert.NotEqual(t, firstKey, choice.Key)
	failAttempt(c, router, choice, http.StatusServiceUnavailable)
	assert.Equal(t, "channel", router.attempts[1].Failure.Scope)
	choice, selectErr = router.Next(c, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, 3502, choice.Channel.Id, "a channel-level failure moves on")
	failAttempt(c, router, choice, http.StatusServiceUnavailable)
	choice, selectErr = router.Next(c, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, multi.Id, choice.Channel.Id, "with no other channel left, the failed channel's untried key is used")
	assert.Equal(t, "other_key", router.attempts[3].Reason)
	failAttempt(c, router, choice, http.StatusServiceUnavailable)
	assert.Zero(t, router.Remaining(c), "every key was tried")

	cooling := routerTestContext(t, "/v1/chat/completions", `{}`)
	model.RecordChannelRateLimit(multi.Id, model.ChannelKeyFingerprint(firstKey.Value), modelName, time.Minute)
	choice, selectErr = seededRouter(cooling, seed).Next(cooling, modelName)
	require.Nil(t, selectErr)
	assert.Equal(t, multi.Id, choice.Channel.Id, "a cooling key leaves its channel usable")
	assert.NotEqual(t, firstKey, choice.Key)

	_, err := model.UpdateScheduledChannelLocked(t.Context(), multi.Id, func(stored *model.Channel) error {
		require.True(t, model.SetScheduledChannelKeyStatus(stored, firstKey.Index, common.ChannelStatusAutoDisabled, "test"))
		return nil
	})
	require.NoError(t, err)
	disabled := routerTestContext(t, "/v1/chat/completions", `{}`)
	for i := range 20 {
		choice, selectErr = seededRouter(disabled, testSeed(100+i)).Next(disabled, modelName)
		require.Nil(t, selectErr)
		if choice.Channel.Id == multi.Id {
			assert.NotEqual(t, firstKey.Index, choice.Key.Index, "a disabled key is never chosen")
		}
	}
}

func TestRouterTakesPollingKeysInTurnWithoutASeed(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-polling"
	createRouterChannel(t, routerChannel{id: 3601, group: "default", model: modelName, weight: 1, keys: 3, polling: true})
	model.InitChannelCache()
	var indexes []int
	for range 4 {
		c := routerTestContext(t, "/v1/chat/completions", `{}`)
		choice, selectErr := RouterFor(c).Next(c, modelName)
		require.Nil(t, selectErr)
		indexes = append(indexes, choice.Key.Index)
	}
	assert.Equal(t, []int{0, 1, 2, 0}, indexes)
}

func TestRouterUsesTokenAutoGroupsWhenGlobalAutoIsEmpty(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-groups-runtime-model"
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", modelName)
	model.InitChannelCache()

	for _, tc := range []struct {
		name       string
		groups     []string
		crossGroup bool
		want       []int
		wantGroups []string
	}{
		{name: "cross-group retry moves to the next group", groups: []string{"vip", "default"}, crossGroup: true, want: []int{2101, 2102}, wantGroups: []string{"vip", "default"}},
		{name: "without cross-group retry the first group is the only one", groups: []string{"vip", "default"}, want: []int{2101}, wantGroups: []string{"vip"}},
		{name: "a group without candidates is skipped", groups: []string{"empty", "default"}, want: []int{2102}, wantGroups: []string{"default"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := routerTestContext(t, "/v1/chat/completions", `{}`)
			common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "auto")
			common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, tc.groups)
			common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, tc.crossGroup)
			router := NewChannelRouter(ctx)
			var visited []int
			var groups []string
			for {
				choice, selectErr := router.Next(ctx, modelName)
				if selectErr != nil {
					break
				}
				visited = append(visited, choice.Channel.Id)
				groups = append(groups, choice.Group)
				assert.Equal(t, choice.Group, common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup), "group pricing reads the routed group")
				failAttempt(ctx, router, choice, http.StatusInternalServerError)
				if router.Remaining(ctx) == 0 {
					break
				}
			}
			assert.Equal(t, tc.want, visited)
			assert.Equal(t, tc.wantGroups, groups)
			assert.Empty(t, setting.GetAutoGroups(), "the selection must not depend on the global Auto list")
		})
	}
}

// A pinned or locked channel is the only candidate; its retries rotate
// through its keys and start over once each was tried.
func TestRouterKeepsPinnedAndLockedChannels(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-pinned"
	pinned := createRouterChannel(t, routerChannel{id: 3701, group: "default", model: modelName, weight: 1, keys: 2})
	createRouterChannel(t, routerChannel{id: 3702, group: "default", model: modelName, weight: 100})
	model.InitChannelCache()
	common.RetryTimes = 3

	for _, mode := range []string{"pin", "lock"} {
		t.Run(mode, func(t *testing.T) {
			c := routerTestContext(t, "/v1/chat/completions", `{}`)
			router := NewChannelRouter(c)
			if mode == "pin" {
				GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: pinned.Id, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
			} else {
				router.Lock(pinned)
			}
			var keys []int
			for range common.RetryTimes + 1 {
				choice, selectErr := router.Next(c, modelName)
				require.Nil(t, selectErr)
				assert.Equal(t, pinned.Id, choice.Channel.Id)
				keys = append(keys, choice.Key.Index)
				failAttempt(c, router, choice, http.StatusInternalServerError)
			}
			assert.ElementsMatch(t, []int{0, 1}, keys[:2], "each key is tried before any repeats")
			assert.Equal(t, 0, router.Remaining(c), "the budget still caps the attempts")
			_, selectErr := router.Next(c, modelName)
			require.NotNil(t, selectErr, "no attempt past the budget, whatever the retry decision")
		})
	}
}

func TestRouterRecordsRoutingInAdminInfo(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	const modelName = "router-log"
	createRouterChannel(t, routerChannel{id: 3801, group: "default", model: modelName, weight: 1})
	createRouterChannel(t, routerChannel{id: 3802, group: "default", model: modelName, weight: 1})
	model.InitChannelCache()
	common.RetryTimes = 1

	c := routerTestContext(t, "/v1/chat/completions", `{"model":"router-log","prompt_cache_key":"conversation-9"}`)
	router := RouterFor(c)
	choice, selectErr := router.Next(c, modelName)
	require.Nil(t, selectErr)
	failAttempt(c, router, choice, http.StatusBadGateway)
	_, selectErr = router.Next(c, modelName)
	require.Nil(t, selectErr)

	other := model.NewLogOther()
	AppendRoutingAdminInfo(c, other)
	record, ok := other.Snapshot()["admin_info"].(map[string]any)["routing"].(routeLog)
	require.True(t, ok)
	require.NotNil(t, record.Seed)
	assert.Equal(t, "prompt_cache_key", record.Seed.Source)
	assert.Len(t, record.Seed.Fingerprint, 8)
	assert.Equal(t, "prefer", record.Mode)
	assert.Equal(t, "none", record.History)
	require.Len(t, record.Attempts, 2)
	assert.Equal(t, "seed_top", record.Attempts[0].Reason)
	assert.Equal(t, &routeFailure{Status: http.StatusBadGateway, Code: string(types.ErrorCodeBadResponseStatusCode), Scope: "channel"}, record.Attempts[0].Failure)
	assert.Equal(t, "seed_fallback", record.Attempts[1].Reason)
	assert.Equal(t, 2, record.Attempts[1].N)

	stats, ok := GetChannelAffinityStatsContext(c)
	require.True(t, ok)
	assert.Equal(t, ChannelAffinityStatsContext{RuleName: "prompt_cache_key", UsingGroup: "default", KeyFingerprint: record.Seed.Fingerprint, TTLSeconds: 3600}, stats)
}

// The seed comes from the first of a rule's key, a session header, the
// body's cache key, the system prompt and tools, and the first user message.
func TestRouteSeedSources(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	longSystem := strings.Repeat("You are a careful assistant. ", 40)
	shortSystem := strings.Repeat("s", 1000)
	for _, tc := range []struct {
		name    string
		path    string
		body    string
		header  string
		spread  int
		derive  bool
		rule    bool
		source  string
		history routeHistory
	}{
		{name: "a rule's key wins", path: "/v1/chat/completions", body: `{"prompt_cache_key":"pck"}`, header: "conversation", rule: true, derive: true, source: "rule:router-rule"},
		{name: "a session header", path: "/v1/chat/completions", body: `{"prompt_cache_key":"pck"}`, header: "conversation", derive: true, source: "header:Session_id"},
		{name: "chat prompt_cache_key", path: "/v1/chat/completions", body: `{"prompt_cache_key":"pck","messages":[{"role":"user","content":"hi"}]}`, derive: true, source: "prompt_cache_key"},
		{name: "messages metadata.user_id", path: "/v1/messages", body: `{"metadata":{"user_id":"user_1_session_2"},"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"},{"role":"user","content":"more"}]}`, derive: true, source: "metadata.user_id", history: routeHistoryPresent},
		{name: "chat template", path: "/v1/chat/completions", body: `{"messages":[{"role":"system","content":"` + longSystem + `"},{"role":"user","content":"hi"}]}`, derive: true, spread: 1, source: "template"},
		{name: "a short template falls to the first user message", path: "/v1/chat/completions", body: `{"messages":[{"role":"system","content":"` + shortSystem + `"},{"role":"user","content":"hi"}]}`, derive: true, spread: 1, source: "first_user"},
		{name: "spread 0 skips the template", path: "/v1/chat/completions", body: `{"messages":[{"role":"system","content":"` + longSystem + `"},{"role":"user","content":"hi"}]}`, derive: true, source: "first_user"},
		{name: "messages system blocks", path: "/v1/messages", body: `{"system":[{"type":"text","text":"` + longSystem + `"}],"messages":[{"role":"user","content":"hi"}]}`, derive: true, spread: 1, source: "template"},
		{name: "responses string input", path: "/v1/responses", body: `{"input":"hello there"}`, derive: true, source: "first_user"},
		{name: "responses previous_response_id is history", path: "/v1/responses", body: `{"previous_response_id":"resp_1","input":"next"}`, derive: true, source: "first_user", history: routeHistoryPresent},
		{name: "responses tool output is history", path: "/v1/responses", body: `{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"c"},{"type":"function_call_output","call_id":"c","output":"1"}]}`, derive: true, source: "first_user", history: routeHistoryPresent},
		{name: "gemini first user parts", path: "/v1beta/models/g:generateContent", body: `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"text":"yo"}]},{"role":"user","parts":[{"text":"more"}]}]}`, derive: true, source: "first_user", history: routeHistoryPresent},
		{name: "derivation off leaves a random seed", path: "/v1/chat/completions", body: `{"messages":[{"role":"user","content":"hi"}]}`, source: "random"},
		{name: "an embedding has no derived seed", path: "/v1/embeddings", body: `{"input":"hi"}`, derive: true, source: "random", history: routeHistoryUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: tc.derive, TemplateSpread: tc.spread}
			if tc.rule {
				setting.Rules = []operation_setting.ChannelAffinityRule{{Name: "router-rule", ModelRegex: []string{".*"},
					KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "gjson", Path: "prompt_cache_key"}}}}
			}
			c := routerTestContext(t, tc.path, tc.body)
			if tc.header != "" {
				c.Request.Header.Set("Session_id", tc.header)
			}
			request := inspectRouteRequest(c, "m", nil)
			assert.Equal(t, tc.source, request.seed.source)
			if tc.history != "" && tc.source != "header:Session_id" && !tc.rule {
				assert.Equal(t, tc.history, request.history)
			}
		})
	}
}

// Digests ignore what clients change between turns of one conversation, and
// a spread template keeps each conversation in its own bucket.
func TestRouteSeedDigestsAreStable(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: true, TemplateSpread: 1}
	system := strings.Repeat("Rules of the house. ", 80)
	seedOf := func(body string) routeSeed {
		return inspectRouteRequest(routerTestContext(t, "/v1/messages", body), "m", nil).seed
	}
	turn1 := seedOf(`{"system":[{"type":"text","text":"` + system + `","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"read","input_schema":{"type":"object"}},{"name":"write","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	turn2 := seedOf(`{"system":[{"type":"text","text":"` + system + `"}],"tools":[{"name":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}},{"name":"write","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":"yo"},{"role":"user","content":"more"}]}`)
	assert.Equal(t, turn1, turn2, "moved cache_control markers and later turns do not change the seed")
	other := seedOf(`{"system":[{"type":"text","text":"` + system + `"}],"tools":[{"name":"read","input_schema":{"type":"string"}},{"name":"write","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	assert.NotEqual(t, turn1, other, "a changed tool definition changes the seed")

	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: false}
	firstUser := func(body string) routeSeed {
		*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: true}
		return seedOf(body)
	}
	assert.Equal(t,
		firstUser(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]}`),
		firstUser(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"base64","data":"BBBB"}}]}]}`),
		"images are not part of the first user message's digest")

	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: true, TemplateSpread: 4}
	buckets := map[[32]byte]bool{}
	for i := range 32 {
		seed := seedOf(fmt.Sprintf(`{"system":"%s","messages":[{"role":"user","content":"conversation %d"}]}`, system, i))
		require.Equal(t, routeSourceTemplate, seed.source)
		buckets[seed.digest] = true
		again := seedOf(fmt.Sprintf(`{"system":"%s","messages":[{"role":"user","content":"conversation %d"}]}`, system, i))
		require.Equal(t, seed, again, "a conversation keeps its bucket")
	}
	assert.LessOrEqual(t, len(buckets), 4)
	assert.Greater(t, len(buckets), 1)
}

// A body kept on disk gives the same seed as one kept in memory.
func TestRouteSeedReadsBodiesOnDisk(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, DeriveFromPrompt: true, TemplateSpread: 1}
	body := `{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", 1<<20) + `"}}]},{"role":"assistant","content":"ok"},{"role":"user","content":"and?"}]}`
	memory := inspectRouteRequest(routerTestContext(t, "/v1/chat/completions", body), "m", nil)

	previousDisk := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
	t.Cleanup(func() { common.SetDiskCacheConfig(previousDisk) })
	c := routerTestContext(t, "/v1/chat/completions", body)
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	require.True(t, storage.IsDisk())
	disk := inspectRouteRequest(c, "m", nil)
	assert.Equal(t, memory, disk)
	assert.Equal(t, routeSourceFirstUser, disk.seed.source)
	assert.Equal(t, routeHistoryPresent, disk.history)
}
