package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryHonorsPinRetryMode(t *testing.T) {
	openaiErr := types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	c := newPinRetryContext()
	assert.True(t, service.ShouldRetryRelayError(c, openaiErr, 1))

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.True(t, service.ShouldRetryRelayError(origin, openaiErr, 1), "origin pin retries on the same channel")

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}, service.DecideRelayRetry(token, openaiErr, 1), "token pin suppresses retry")
}

func TestShouldRetryTaskRelayHonorsPinRetryMode(t *testing.T) {
	taskErr := &dto.TaskError{StatusCode: http.StatusInternalServerError}

	c := newPinRetryContext()
	assert.Equal(t, "retry", decideTaskRetry(c, taskErr, 1).Action)

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.Equal(t, "retry", decideTaskRetry(origin, taskErr, 1).Action)

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}, decideTaskRetry(token, taskErr, 1))
}

func TestSameChannelPinsMergeToStricterRetryMode(t *testing.T) {
	c := newPinRetryContext()
	constraints := service.GetChannelConstraints(c)
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	pin, found, overridden := constraints.ResolvedPin()
	require.True(t, found)
	assert.Equal(t, 7, pin.ChannelId)
	assert.Equal(t, dto.PinRetrySingleAttempt, pin.RetryMode)
	assert.Empty(t, overridden)
	assert.False(t, service.ShouldRetryRelayError(c, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), 1))
}

func newPinRetryContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestRequestPolicyConfigReturnsSettingsWithoutMigration(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetRequestPolicy(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Contains(t, response.Data, "options")
	assert.NotContains(t, response.Data, "migration")
	assert.NotContains(t, response.Data, "differences")
}

// The relay retry loop walks the router on both cache paths: the best tier's
// untried candidates first, then lower tiers, then the next Auto group with
// cross-group retry. A strict session holds a request that carries earlier
// turns on its first choice; a first turn fails over like any other.
func TestRetriesFollowTheRouterOnBothCachePaths(t *testing.T) {
	db := modelManagementDB(t)
	previousRetryTimes := common.RetryTimes
	previousGroups := setting.UserUsableGroups2JSONString()
	previousMaxGroups := setting.GetMaxTokenAutoGroups()
	previousRatios, err := common.Marshal(ratio_setting.GetGroupRatioCopy())
	require.NoError(t, err)
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinity
	t.Cleanup(func() {
		common.RetryTimes = previousRetryTimes
		*affinity = previousAffinity
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprint(previousMaxGroups)))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(previousRatios)))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","empty":"Empty"}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"empty":1}`))
	const modelName = "router-retry-test"
	for _, spec := range []struct {
		id       int
		priority int64
		group    string
	}{{24, 4, "default"}, {27, 3, "default"}, {30, 2, "default"}, {34, 4, "vip"}, {37, 3, "vip"}} {
		channel := model.Channel{
			Id: spec.id, Name: fmt.Sprintf("channel-%d", spec.id), Type: constant.ChannelTypeOpenAI,
			Key: fmt.Sprintf("key-%d", spec.id), Status: common.ChannelStatusEnabled, Models: modelName, Group: spec.group,
			Priority: common.GetPointer(spec.priority), Weight: common.GetPointer(uint(1)),
		}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(db))
	}
	firstTurn := `{"model":"router-retry-test","messages":[{"role":"user","content":"hi"}]}`
	laterTurn := `{"model":"router-retry-test","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"again"}]}`
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		model.InitChannelCache()
		for _, tc := range []struct {
			name, mode, body string
			retries          int
			autoGroups       []string
			crossGroup       bool
			want             []int
		}{
			{name: "ordinary routing", mode: "prefer", body: laterTurn, retries: 2, want: []int{24, 27, 30}},
			{name: "no retry budget", mode: "prefer", body: laterTurn, want: []int{24}},
			{name: "one retry", mode: "prefer", body: laterTurn, retries: 1, want: []int{24, 27}},
			{name: "candidates run out before the budget", mode: "prefer", body: laterTurn, retries: 5, want: []int{24, 27, 30}},
			{name: "strict session with history", mode: "strict", body: laterTurn, retries: 2, want: []int{24}},
			{name: "strict session first turn", mode: "strict", body: firstTurn, retries: 2, want: []int{24, 27, 30}},
			{name: "auto group", mode: "prefer", body: laterTurn, retries: 1, autoGroups: []string{"default", "vip"}, want: []int{24, 27}},
			{name: "cross group", mode: "prefer", body: laterTurn, retries: 1, autoGroups: []string{"default", "vip"}, crossGroup: true, want: []int{24, 27, 34, 37}},
			{name: "skip empty group", mode: "prefer", body: laterTurn, retries: 1, autoGroups: []string{"empty", "default"}, want: []int{24, 27}},
		} {
			t.Run(fmt.Sprintf("cache=%t/%s", cached, tc.name), func(t *testing.T) {
				common.RetryTimes = tc.retries
				*affinity = operation_setting.ChannelAffinitySetting{
					Enabled: true, DeriveFromPrompt: true, TemplateSpread: 1,
					Rules: []operation_setting.ChannelAffinityRule{{
						Name: "session", ModelRegex: []string{"^" + modelName + "$"}, SessionMode: tc.mode,
						KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}},
					}},
				}
				ctx := newPinRetryContext()
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
				ctx.Request.Header.Set("Content-Type", "application/json")
				ctx.Request.Header.Set("X-Session", t.Name())
				group := "default"
				common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
				if tc.autoGroups != nil {
					group = "auto"
					common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, tc.autoGroups)
					common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, tc.crossGroup)
				}
				common.SetContextKey(ctx, constant.ContextKeyUsingGroup, group)
				upstreamErr := types.NewOpenAIError(errors.New("upstream failure"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
				router := service.RouterFor(ctx)
				var attempts []int
				for {
					choice, selectErr := service.SelectChannelForRequest(ctx, modelName)
					require.Nil(t, selectErr)
					require.Nil(t, middleware.SetupContextForSelectedChannel(ctx, choice.Channel, choice.Key, modelName))
					attempts = append(attempts, choice.Channel.Id)
					require.LessOrEqual(t, len(attempts), len(tc.want), "retry budget must not increase")
					router.Fail(ctx, choice.Channel.Id, upstreamErr)
					if service.DecideRelayRetry(ctx, upstreamErr, router.Remaining(ctx)).Action != "retry" {
						break
					}
				}
				assert.Equal(t, tc.want, attempts)
			})
		}
	}
}

// The distributor never relays on a channel without an enabled key: routing
// moves to the next candidate, and with none left the request fails instead
// of reaching the relay with an empty key.
func TestDistributeSkipsChannelsWithoutEnabledKeys(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := modelManagementDB(t)
	previousGroups := setting.UserUsableGroups2JSONString()
	previousRatios, err := common.Marshal(ratio_setting.GetGroupRatioCopy())
	require.NoError(t, err)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(previousRatios)))
	})
	// Channel 1 is still enabled while its keys were all disabled since the
	// cache was loaded.
	channels := []model.Channel{
		{Id: 1, Name: "A", Type: 1, Key: "k1\nk2", Status: common.ChannelStatusEnabled, Models: "keys-test,keys-only", Group: "default", Priority: common.GetPointer(int64(10)),
			ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled}}},
		{Id: 2, Name: "B", Type: 1, Key: "test-only", Status: common.ChannelStatusEnabled, Models: "keys-test", Group: "default", Priority: common.GetPointer(int64(5))},
	}
	for i := range channels {
		require.NoError(t, db.Create(&channels[i]).Error)
		require.NoError(t, channels[i].AddAbilities(db))
	}
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		model.InitChannelCache()
		for _, tc := range []struct {
			model     string
			channelID int
		}{{"keys-test", 2}, {"keys-only", 0}} {
			t.Run(fmt.Sprintf("cache=%t/%s", cached, tc.model), func(t *testing.T) {
				request := newPinRetryContext()
				request.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q}`, tc.model)))
				request.Request.Header.Set("Content-Type", "application/json")
				common.SetContextKey(request, constant.ContextKeyUsingGroup, "default")
				middleware.Distribute()(request)
				if tc.channelID == 0 {
					assert.True(t, request.IsAborted())
					assert.Equal(t, http.StatusServiceUnavailable, request.Writer.Status())
					return
				}
				assert.False(t, request.IsAborted())
				assert.Equal(t, tc.channelID, common.GetContextKeyInt(request, constant.ContextKeyChannelId))
				assert.Equal(t, "test-only", common.GetContextKeyString(request, constant.ContextKeyChannelKey))
			})
		}
	}
}
