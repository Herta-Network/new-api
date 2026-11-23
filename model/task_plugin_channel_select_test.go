package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginChannelSelectionFiltersBothCachePaths(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://example.com"
	alphaSetting := `{"task_plugin_key":"alpha"}`
	betaSetting := `{"task_plugin_key":"beta"}`
	channels := []Channel{
		{Id: 900001, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "alpha", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &alphaSetting},
		{Id: 900002, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "beta", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &betaSetting},
		{Id: 900003, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "ordinary", Models: "ordinary", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900004, Type: constant.ChannelTypeKling, Status: common.ChannelStatusEnabled, Name: "legacy-alpha", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900005, Type: constant.ChannelTypeJimeng, Status: common.ChannelStatusEnabled, Name: "legacy-beta", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		InitChannelCache()
		assert.Equal(t, []string{"alpha"}, routeCandidateNames(t, "default", "shared", identityFilters("alpha", nil)))
		assert.Empty(t, routeCandidateNames(t, "default", "shared", identityFilters("", nil)))
		assert.Equal(t, []string{"ordinary"}, routeCandidateNames(t, "default", "ordinary", identityFilters("", nil)))
		assert.Equal(t, []string{"legacy-alpha"}, routeCandidateNames(t, "default", "legacy", identityFilters("legacy-alpha", []int{constant.ChannelTypeKling})))
		assert.ElementsMatch(t, []string{"legacy-alpha", "legacy-beta"}, routeCandidateNames(t, "default", "legacy", identityFilters("legacy-alpha", []int{constant.ChannelTypeKling, constant.ChannelTypeJimeng})))
	}
}

// routeCandidateNames lists the channels routing may choose for modelName in
// group.
func routeCandidateNames(t *testing.T, group, modelName string, filters []dto.ChannelFilter) []string {
	t.Helper()
	candidates, err := ListRouteCandidates(group, modelName, filters)
	require.NoError(t, err)
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Channel.Name)
	}
	return names
}

func identityFilters(key string, channelTypes []int) []dto.ChannelFilter {
	return []dto.ChannelFilter{{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          key,
		TaskPluginChannelTypes: channelTypes,
	}}
}

func TestSharedPluginKeysFilterBothChannelSources(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	filters := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "alpha", TaskPluginKeys: []string{"alpha", "beta"}}}
	for index, key := range []string{"alpha", "beta", "unrelated"} {
		setting := `{"task_plugin_key":"` + key + `"}`
		channel := Channel{Id: 910001 + index, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: key, Models: "shared", Group: "default", Setting: &setting}
		require.NoError(t, channel.Insert())
		matches, _ := ChannelSatisfiesFilters(&channel, "shared", filters)
		assert.Equal(t, index < 2, matches)
	}
	common.MemoryCacheEnabled = false
	assert.Equal(t, []string{"alpha", "beta"}, routeCandidateNames(t, "default", "shared", filters))
	common.MemoryCacheEnabled = true
	InitChannelCache()
	channelSyncLock.RLock()
	kept, emptied := filterCandidateIDs([]int{910001, 910002, 910003}, "shared", filters)
	channelSyncLock.RUnlock()
	assert.Equal(t, []int{910001, 910002}, kept)
	assert.Empty(t, emptied)
}

func TestNewAPIChannelServesExtendedTaskPlugins(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://gateway.example"
	extended := `{"task_extend_plugin_keys":["alpha","gamma"]}`
	single := `{"task_plugin_key":"delta"}`
	channels := []Channel{
		{Id: 920001, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway", Models: "shared,chat", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &extended},
		{Id: 920002, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway-single", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &single},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	assert.Equal(t, []string{"gateway"}, routeCandidateNames(t, "default", "shared", identityFilters("alpha", nil)))
	assert.Equal(t, []string{"gateway-single"}, routeCandidateNames(t, "default", "shared", identityFilters("delta", nil)), "a single task_plugin_key still binds a New API channel")
	assert.Empty(t, routeCandidateNames(t, "default", "shared", identityFilters("beta", nil)), "unbound plugins never reach the gateway channel")
	shared := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "beta", TaskPluginKeys: []string{"beta", "gamma"}}}
	assert.Equal(t, []string{"gateway"}, routeCandidateNames(t, "default", "shared", shared), "a bound shared-model candidate admits the channel")
	assert.Equal(t, []string{"gateway"}, routeCandidateNames(t, "default", "chat", identityFilters("", nil)), "requests without a pinned plugin keep using the gateway")
}

func TestModelModifierNamesRouteWithAndWithoutMemoryCache(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	channel := Channel{Id: 930001, Type: constant.ChannelTypeDeepSeek, Status: common.ChannelStatusEnabled, Name: "deepseek", Models: "deepseek-flash", Group: "default"}
	require.NoError(t, channel.Insert())

	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		InitChannelCache()
		assert.Equal(t, []string{"deepseek"}, routeCandidateNames(t, "default", "deepseek-flash@effort:max", nil), "memory cache %v", memoryCache)
		assert.Empty(t, routeCandidateNames(t, "default", "deepseek-pro@effort:max", nil), "memory cache %v", memoryCache)
	}
}

// Both cache paths give routing the same candidates: enabled channels only,
// highest priority first, with the channel's own priority and weight.
func TestListRouteCandidatesMatchesOnBothCachePaths(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	for _, fixture := range []struct {
		id       int
		priority int64
		weight   uint
		status   int
	}{
		{950001, 0, 7, common.ChannelStatusEnabled},
		{950002, 10, 0, common.ChannelStatusEnabled},
		{950003, 10, 3, common.ChannelStatusManuallyDisabled},
		{950004, 5, 1, common.ChannelStatusEnabled},
	} {
		priority, weight := fixture.priority, fixture.weight
		channel := Channel{Id: fixture.id, Type: constant.ChannelTypeOpenAI, Status: fixture.status, Name: fmt.Sprint(fixture.id), Models: "listed", Group: "default", Priority: &priority, Weight: &weight}
		require.NoError(t, channel.Insert())
	}
	type listed struct {
		id       int
		priority int64
		weight   int
	}
	want := []listed{{950002, 10, 0}, {950004, 5, 1}, {950001, 0, 7}}
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		InitChannelCache()
		candidates, err := ListRouteCandidates("default", "listed", nil)
		require.NoError(t, err)
		var got []listed
		for _, candidate := range candidates {
			got = append(got, listed{candidate.Channel.Id, candidate.Priority, candidate.Weight})
		}
		assert.Equal(t, want, got, "memory cache %v", memoryCache)
	}
}

// A 429 cooldown sets aside one channel, or one key of a multi-key channel,
// for one model until its deadline.
func TestChannelCooldownsCoverOneTargetForOneModel(t *testing.T) {
	originalNow := channelRateLimitNow
	t.Cleanup(func() {
		channelRateLimitNow = originalNow
		require.NoError(t, getChannelRateLimitCache().Purge())
	})
	type cooldown struct {
		channel  int
		key      string
		duration time.Duration
	}
	type probe struct {
		channel int
		key     string
		model   string
		want    bool
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		cooldowns []cooldown
		elapsed   time.Duration
		probes    []probe
	}{
		{name: "a channel cools down for its model only", cooldowns: []cooldown{{channel: 1, duration: 30 * time.Second}}, probes: []probe{
			{channel: 1, model: "cool-model", want: true},
			{channel: 1, model: "other-model"},
			{channel: 2, model: "cool-model"},
		}},
		{name: "the channel returns when the cooldown ends", cooldowns: []cooldown{{channel: 1, duration: 30 * time.Second}}, elapsed: 30 * time.Second, probes: []probe{
			{channel: 1, model: "cool-model"},
		}},
		{name: "a shorter cooldown does not cut a longer one", cooldowns: []cooldown{{channel: 1, duration: time.Minute}, {channel: 1, duration: 10 * time.Second}}, elapsed: 30 * time.Second, probes: []probe{
			{channel: 1, model: "cool-model", want: true},
		}},
		{name: "a key cools down alone", cooldowns: []cooldown{{channel: 1, key: "k1", duration: 30 * time.Second}}, probes: []probe{
			{channel: 1, key: "k1", model: "cool-model", want: true},
			{channel: 1, key: "k2", model: "cool-model"},
			{channel: 1, model: "cool-model"},
		}},
		{name: "a whole-channel cooldown covers every key", cooldowns: []cooldown{{channel: 1, duration: 30 * time.Second}}, probes: []probe{
			{channel: 1, key: "k2", model: "cool-model", want: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, getChannelRateLimitCache().Purge())
			now := start
			channelRateLimitNow = func() time.Time { return now }
			for _, limit := range tc.cooldowns {
				RecordChannelRateLimit(limit.channel, limit.key, "cool-model", limit.duration)
			}
			now = now.Add(tc.elapsed)
			for _, probe := range tc.probes {
				assert.Equal(t, probe.want, IsChannelRateLimited(probe.channel, probe.key, probe.model), "%+v", probe)
			}
		})
	}
}
