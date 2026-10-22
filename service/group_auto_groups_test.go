package service

import (
	"fmt"
	"maps"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureRequestAutoGroupsTest(t *testing.T) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","svip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func newRequestAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestGetRequestAutoGroupsInheritedListIsNotLimited(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default", "svip"}, groups)
}

func TestGetRequestAutoGroupsFiltersBeforeApplyingCurrentLimit(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"revoked", "vip", "default", "svip"})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default"}, groups)
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"))
}

func TestGetRequestAutoGroupsDoesNotFallBackAfterPermissionChange(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Empty(t, groups)
}

// testdata/group_rules_cases.json is shared with the web group pricing
// simulator (web/src/features/system-settings/billing/group-pricing), whose
// tests assert the same expectations, so both sides resolve groups alike.
type groupRulesCases struct {
	Settings struct {
		GroupRatio              map[string]float64            `json:"GroupRatio"`
		UserUsableGroups        map[string]string             `json:"UserUsableGroups"`
		GroupGroupRatio         map[string]map[string]float64 `json:"GroupGroupRatio"`
		GroupSpecialUsableGroup map[string]map[string]string  `json:"GroupSpecialUsableGroup"`
		AutoGroups              []string                      `json:"AutoGroups"`
	} `json:"settings"`
	UsableGroups map[string][]string `json:"usable_groups"`
	AutoGroups   map[string][]string `json:"auto_groups"`
	GroupRatios  []struct {
		UserGroup  string  `json:"user_group"`
		UsingGroup string  `json:"using_group"`
		Ratio      float64 `json:"ratio"`
	} `json:"group_ratios"`
}

func loadGroupRulesCases(t *testing.T) groupRulesCases {
	t.Helper()
	data, err := os.ReadFile("testdata/group_rules_cases.json")
	require.NoError(t, err)
	var cases groupRulesCases
	require.NoError(t, common.Unmarshal(data, &cases))

	special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	original := map[string]string{
		"GroupRatio":       ratio_setting.GroupRatio2JSONString(),
		"UserUsableGroups": setting.UserUsableGroups2JSONString(),
		"GroupGroupRatio":  ratio_setting.GroupGroupRatio2JSONString(),
		"Special":          special.MarshalJSONString(),
		"AutoGroups":       setting.AutoGroups2JsonString(),
	}
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(original["GroupRatio"]))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(original["UserUsableGroups"]))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(original["GroupGroupRatio"]))
		require.NoError(t, types.LoadFromJsonString(special, original["Special"]))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(original["AutoGroups"]))
	})

	load := func(value any, update func(string) error) {
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, update(string(encoded)))
	}
	load(cases.Settings.GroupRatio, ratio_setting.UpdateGroupRatioByJSONString)
	load(cases.Settings.UserUsableGroups, setting.UpdateUserUsableGroupsByJSONString)
	load(cases.Settings.GroupGroupRatio, ratio_setting.UpdateGroupGroupRatioByJSONString)
	load(cases.Settings.GroupSpecialUsableGroup, func(value string) error {
		return types.LoadFromJsonString(special, value)
	})
	load(cases.Settings.AutoGroups, setting.UpdateAutoGroupsByJsonString)
	return cases
}

func TestGroupRulesMatchTheSharedSimulatorCases(t *testing.T) {
	cases := loadGroupRulesCases(t)

	for userGroup, expected := range cases.UsableGroups {
		usable := slices.Sorted(maps.Keys(GetUserUsableGroups(userGroup)))
		assert.Equal(t, expected, usable, "usable groups of %q", userGroup)
	}
	for userGroup, expected := range cases.AutoGroups {
		assert.Equal(t, expected, GetUserAutoGroup(userGroup), "auto groups of %q", userGroup)
	}
	for _, item := range cases.GroupRatios {
		assert.Equal(t, item.Ratio, GetUserGroupRatio(item.UserGroup, item.UsingGroup),
			"ratio of %q using %q", item.UserGroup, item.UsingGroup)
	}
}
