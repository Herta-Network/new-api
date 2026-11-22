package controller

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type listModelsResponse struct {
	Success bool               `json:"success"`
	Data    []dto.OpenAIModels `json:"data"`
	Object  string             `json:"object"`
}

type userModelsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

func setupModelListControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false
	pgtest.Reset(t, model.DB)
	// Every model list reads the catalog, which caches the abilities.
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	return model.DB
}

func withTieredBillingConfig(t *testing.T, modes map[string]string, exprs map[string]string) {
	t.Helper()

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		model.InvalidatePricingCache()
	})

	modeBytes, err := common.Marshal(modes)
	require.NoError(t, err)
	exprBytes, err := common.Marshal(exprs)
	require.NoError(t, err)

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(modeBytes),
		"billing_setting.billing_expr": string(exprBytes),
	}))
	model.InvalidatePricingCache()
}

func withSelfUseModeDisabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func withSelfUseModeEnabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func decodeListModelsPayload(t *testing.T, recorder *httptest.ResponseRecorder) listModelsResponse {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload listModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "list", payload.Object)
	return payload
}

func decodeListModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]struct{} {
	t.Helper()

	payload := decodeListModelsPayload(t, recorder)
	ids := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		ids[item.Id] = struct{}{}
	}
	return ids
}

func pricingByModelName(pricings []model.Pricing) map[string]model.Pricing {
	byName := make(map[string]model.Pricing, len(pricings))
	for _, pricing := range pricings {
		byName[pricing.ModelName] = pricing
	}
	return byName
}

func modelSquareData[T any](t *testing.T, handler gin.HandlerFunc, path, userGroup string) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if userGroup != "" {
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, userGroup)
	}
	handler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    T    `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response.Data
}

// The model square, its performance details, and its summary show a viewer
// only the groups the viewer can use; the admin dashboard summary covers
// every group.
func TestModelSquareGroupVisibility(t *testing.T) {
	// Self-use mode lists these unpriced models in the catalog.
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	originalGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Public"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"internal":2}`))
	require.NoError(t, db.Create(&[]model.Ability{
		{Model: "shared", Group: "default", ChannelId: 1, Enabled: true},
		{Model: "shared", Group: "internal", ChannelId: 1, Enabled: true},
		{Model: "private-only", Group: "internal", ChannelId: 1, Enabled: true},
		// "all" is not a group the catalog matches, so it is trimmed like any
		// other group the viewer cannot use.
		{Model: "wildcard", Group: "all", ChannelId: 1, Enabled: true},
		{Model: "wildcard", Group: "internal", ChannelId: 1, Enabled: true},
	}).Error)

	guest := pricingByModelName(modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", ""))
	assert.Equal(t, []string{"default"}, guest["shared"].EnableGroup)
	assert.NotContains(t, guest, "wildcard")
	assert.NotContains(t, guest, "private-only")
	member := pricingByModelName(modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", "internal"))
	assert.ElementsMatch(t, []string{"default", "internal"}, member["shared"].EnableGroup)
	assert.Equal(t, []string{"internal"}, member["private-only"].EnableGroup)
	assert.Equal(t, []string{"internal"}, member["wildcard"].EnableGroup)
	shared := pricingByModelName(model.GetPricing())["shared"]
	assert.ElementsMatch(t, []string{"default", "internal"}, shared.EnableGroup, "trimming must not change the shared pricing cache")

	ts := time.Now().Add(-time.Hour).Unix()
	require.NoError(t, db.Create(&[]model.PerfMetric{
		{ModelName: "shared", Group: "default", BucketTs: ts, RequestCount: 1, SuccessCount: 1},
		{ModelName: "shared", Group: "internal", BucketTs: ts, RequestCount: 1},
		{ModelName: "private-only", Group: "internal", BucketTs: ts, RequestCount: 1},
	}).Error)
	const detailPath = "/api/perf-metrics?model=shared"
	const summaryPath = "/api/perf-metrics/summary?recent_model=private-only"
	detail := modelSquareData[perfmetrics.QueryResult](t, GetPerfMetrics, detailPath, "")
	require.Len(t, detail.Groups, 1)
	assert.Equal(t, "default", detail.Groups[0].Group)
	denied := modelSquareData[perfmetrics.QueryResult](t, GetPerfMetrics, detailPath+"&group=internal", "")
	assert.Empty(t, denied.Groups)
	summary := modelSquareData[perfmetrics.SummaryAllResult](t, GetPerfMetricsSummary, summaryPath, "")
	require.Len(t, summary.Models, 1)
	assert.Equal(t, "shared", summary.Models[0].ModelName)
	assert.Equal(t, 100.0, summary.Models[0].SuccessRate)
	assert.NotContains(t, summary.RecentSuccessSeries, "private-only")
	allowed := modelSquareData[perfmetrics.QueryResult](t, GetPerfMetrics, detailPath+"&group=internal", "internal")
	require.Len(t, allowed.Groups, 1)
	assert.Equal(t, "internal", allowed.Groups[0].Group)

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{}`))
	assert.Empty(t, modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", ""))
	assert.Empty(t, modelSquareData[perfmetrics.QueryResult](t, GetPerfMetrics, detailPath, "").Groups)
	assert.Empty(t, modelSquareData[perfmetrics.SummaryAllResult](t, GetPerfMetricsSummary, summaryPath, "").Models)
	dashboard := modelSquareData[perfmetrics.SummaryAllResult](t, GetDashboardPerfMetricsSummary, "/api/data/perf-metrics/summary", "")
	assert.Len(t, dashboard.Models, 2)
	require.NotNil(t, dashboard.Summary)
	assert.Equal(t, 33.33, dashboard.Summary.SuccessRate)
}

func decodeUserModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload userModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload.Data
}

func TestGetUserModelsFiltersByRequestedGroup(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1002,
		Username: "playground-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-default-only-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-disabled-model", ChannelId: 1, Enabled: false},
	}).Error)

	defaultRecorder := httptest.NewRecorder()
	defaultContext, _ := gin.CreateTestContext(defaultRecorder)
	defaultContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=default", nil)
	defaultContext.Set("id", 1002)

	GetUserModels(defaultContext)

	defaultModels := decodeUserModelsResponse(t, defaultRecorder)
	require.ElementsMatch(t, []string{"zz-default-only-model"}, defaultModels)

	vipRecorder := httptest.NewRecorder()
	vipContext, _ := gin.CreateTestContext(vipRecorder)
	vipContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=vip", nil)
	vipContext.Set("id", 1002)

	GetUserModels(vipContext)

	require.Empty(t, decodeUserModelsResponse(t, vipRecorder))
}

func TestGetUserModelsExpandsAutoGroupsInConfiguredOrder(t *testing.T) {
	withSelfUseModeEnabled(t)
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalSpecialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		specialGroups.Clear()
		specialGroups.AddAll(originalSpecialGroups)
	})

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","unavailable"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"auto":"自动分组","default":"默认分组","unavailable":"不可用分组"}`))
	specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	specialGroups.Clear()
	specialGroups.Set("default", map[string]string{
		"+:vip":         "VIP 分组",
		"-:unavailable": "",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "playground-auto-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-model", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-shared-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-shared-model", ChannelId: 2, Enabled: true},
		{Group: "unavailable", Model: "zz-unavailable-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=auto", nil)
	context.Set("id", 1003)

	GetUserModels(context)

	models := decodeUserModelsResponse(t, recorder)
	require.Len(t, models, 3)
	assert.ElementsMatch(t, []string{"zz-vip-model", "zz-shared-model"}, models[:2])
	assert.Equal(t, "zz-default-model", models[2])
}

func TestModelListsShareCatalogPolicy(t *testing.T) {
	withSelfUseModeDisabled(t)
	savedRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"zz-catalog-priced":2,"zz-catalog-base":3,"zz-catalog-hidden":1}`))

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1004,
		Username: "catalog-policy-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, (&model.Model{ModelName: "zz-catalog-hidden", Status: 0}).Insert())
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-catalog-priced", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-catalog-base@thinking:on", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-catalog-unpriced", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-catalog-hidden", ChannelId: 1, Enabled: true},
	}).Error)

	lists := func() (openAI, user []string, pricing map[string]model.Pricing) {
		model.InvalidatePricingCache()
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		ctx.Set("id", 1004)
		ListModels(ctx, constant.ChannelTypeOpenAI)
		for _, item := range decodeListModelsPayload(t, recorder).Data {
			openAI = append(openAI, item.Id)
		}

		recorder = httptest.NewRecorder()
		ctx, _ = gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/models", nil)
		ctx.Set("id", 1004)
		GetUserModels(ctx)
		user = decodeUserModelsResponse(t, recorder)

		recorder = httptest.NewRecorder()
		ctx, _ = gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
		ctx.Set("id", 1004)
		GetPricing(ctx)
		var payload struct {
			Success bool            `json:"success"`
			Data    []model.Pricing `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.True(t, payload.Success)
		return openAI, user, pricingByModelName(payload.Data)
	}

	// Unpriced and unlisted models stay out of every list; a modifier name
	// shows the base price that bills it.
	expected := []string{"zz-catalog-base@thinking:on", "zz-catalog-priced"}
	openAI, user, pricing := lists()
	assert.Equal(t, expected, openAI)
	assert.Equal(t, expected, user)
	assert.ElementsMatch(t, expected, slices.Collect(maps.Keys(pricing)))
	assert.Equal(t, 3.0, pricing["zz-catalog-base@thinking:on"].ModelRatio)
	assert.Equal(t, 2.0, pricing["zz-catalog-priced"].ModelRatio)

	// Opting in to unpriced models adds them everywhere at the default ratio
	// they bill at; listing policy still applies.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1004).Update("setting", `{"accept_unset_model_ratio_model":true}`).Error)
	expected = []string{"zz-catalog-base@thinking:on", "zz-catalog-priced", "zz-catalog-unpriced"}
	openAI, user, pricing = lists()
	assert.Equal(t, expected, openAI)
	assert.Equal(t, expected, user)
	assert.ElementsMatch(t, expected, slices.Collect(maps.Keys(pricing)))
	assert.Equal(t, 37.5, pricing["zz-catalog-unpriced"].ModelRatio)
}

// A channel's model_mapping sends the names callers use to upstream names.
// The channel neither serves nor lists a name it maps to; a channel offering
// that name without mapping to it still does. No model list names a channel:
// owned_by is the vendor of the model's metadata, else "system".
func TestModelMappingHidesUpstreamModels(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1005,
		Username: "mapped-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	vendor := &model.Vendor{Name: "Acme Labs", Status: 1}
	require.NoError(t, db.Create(vendor).Error)
	require.NoError(t, (&model.Model{ModelName: "zz-public-alias", VendorID: vendor.Id, Status: 1}).Insert())
	mapping := `{"zz-public-alias":"zz-upstream-real","zz-alias-b":"zz-offered-elsewhere"}`
	for _, channel := range []*model.Channel{
		{
			Id: 801, Type: constant.ChannelTypeOpenRouter, Key: "mapped-key", Status: common.ChannelStatusEnabled,
			Name: "zz-secret-channel", Group: "default", ModelMapping: &mapping,
			Models: "zz-public-alias,zz-upstream-real,zz-alias-b,zz-offered-elsewhere,zz-plain",
		},
		{
			Id: 802, Type: constant.ChannelTypeOpenAI, Key: "plain-key", Status: common.ChannelStatusEnabled,
			Name: "zz-other-channel", Group: "default", Models: "zz-offered-elsewhere",
		},
	} {
		require.NoError(t, channel.Insert())
	}
	// A channel saved before mappings hid upstream names keeps abilities for
	// them until startup deletes those, once.
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "zz-upstream-real", ChannelId: 801, Enabled: true}).Error)
	require.NoError(t, model.HideMappedUpstreamAbilities())
	require.NoError(t, model.HideMappedUpstreamAbilities())

	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache })
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		model.InitChannelCache()
		served := make(map[string]int)
		for _, name := range []string{"zz-public-alias", "zz-upstream-real", "zz-offered-elsewhere"} {
			if channel, _ := model.GetRandomSatisfiedChannel("default", name, 0, nil); channel != nil {
				served[name] = channel.Id
			}
		}
		assert.Equal(t, map[string]int{"zz-public-alias": 801, "zz-offered-elsewhere": 802}, served, "memory cache %t", memoryCache)
	}
	common.MemoryCacheEnabled = originalMemoryCache

	expected := []string{"zz-alias-b", "zz-offered-elsewhere", "zz-plain", "zz-public-alias"}
	serve := func(target, modelParam string, handle gin.HandlerFunc) []byte {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
		ctx.Params = gin.Params{{Key: "model", Value: modelParam}}
		ctx.Set("id", 1005)
		handle(ctx)
		require.Equal(t, http.StatusOK, recorder.Code)
		return recorder.Body.Bytes()
	}
	serveListed := func(target, modelParam string, handle gin.HandlerFunc) []byte {
		body := serve(target, modelParam, handle)
		for _, leak := range []string{"zz-secret-channel", "zz-other-channel", "zz-upstream-real", "openrouter"} {
			assert.NotContains(t, string(body), leak, target)
		}
		return body
	}

	var openAI openAIModelListResponse
	require.NoError(t, common.Unmarshal(serveListed("/v1/models", "", func(c *gin.Context) {
		ListModels(c, constant.ChannelTypeOpenAI)
	}), &openAI))
	owners := make(map[string]string)
	for _, item := range openAI.Data {
		owners[item.Id] = item.OwnedBy
	}
	assert.Equal(t, map[string]string{
		"zz-alias-b":           "system",
		"zz-offered-elsewhere": "system",
		"zz-plain":             "system",
		"zz-public-alias":      "Acme Labs",
	}, owners)

	var anthropic anthropicModelListResponse
	require.NoError(t, common.Unmarshal(serveListed("/v1/models", "", func(c *gin.Context) {
		ListModels(c, constant.ChannelTypeAnthropic)
	}), &anthropic))
	var anthropicIDs []string
	for _, item := range anthropic.Data {
		anthropicIDs = append(anthropicIDs, item.ID)
	}
	assert.Equal(t, expected, anthropicIDs)

	var gemini geminiModelListResponse
	require.NoError(t, common.Unmarshal(serveListed("/v1beta/models", "", func(c *gin.Context) {
		ListModels(c, constant.ChannelTypeGemini)
	}), &gemini))
	var geminiNames []string
	for _, item := range gemini.Models {
		geminiNames = append(geminiNames, fmt.Sprint(item.Name))
	}
	assert.Equal(t, expected, geminiNames)

	var retrieved dto.OpenAIModels
	require.NoError(t, common.Unmarshal(serveListed("/v1/models/zz-public-alias", "zz-public-alias", func(c *gin.Context) {
		RetrieveModel(c, constant.ChannelTypeOpenAI)
	}), &retrieved))
	assert.Equal(t, "zz-public-alias", retrieved.Id)
	assert.Equal(t, "Acme Labs", retrieved.OwnedBy)
	var missing OpenAIErrorResponse
	require.NoError(t, common.Unmarshal(serve("/v1/models/zz-upstream-real", "zz-upstream-real", func(c *gin.Context) {
		RetrieveModel(c, constant.ChannelTypeOpenAI)
	}), &missing))
	assert.Equal(t, "model_not_found", missing.Error.Code)

	var user userModelsResponse
	require.NoError(t, common.Unmarshal(serveListed("/api/user/self/models", "", GetUserModels), &user))
	assert.Equal(t, expected, user.Data)

	var pricing pricingResponse
	require.NoError(t, common.Unmarshal(serveListed("/api/pricing", "", GetPricing), &pricing))
	assert.ElementsMatch(t, expected, slices.Collect(maps.Keys(pricingByModelName(pricing.Data))))
}

func TestListModelsIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-tiered-visible-model":      "tiered_expr",
		"zz-tiered-empty-expr-model":   "tiered_expr",
		"zz-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-tiered-empty-expr-model": "   ",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1001,
		Username: "model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1001)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-tiered-visible-model")
	require.NotContains(t, ids, "zz-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-unpriced-model")

	pricingByName := pricingByModelName(model.GetPricing())
	visiblePricing, ok := pricingByName["zz-tiered-visible-model"]
	require.True(t, ok)
	require.Equal(t, "tiered_expr", visiblePricing.BillingMode)
	require.NotEmpty(t, visiblePricing.BillingExpr)

	emptyExprPricing, ok := pricingByName["zz-tiered-empty-expr-model"]
	require.True(t, ok)
	require.Empty(t, emptyExprPricing.BillingMode)
	require.Empty(t, emptyExprPricing.BillingExpr)

	missingExprPricing, ok := pricingByName["zz-tiered-missing-expr-model"]
	require.True(t, ok)
	require.Empty(t, missingExprPricing.BillingMode)
	require.Empty(t, missingExprPricing.BillingExpr)
}

func TestListModelsUsesAdvancedCustomEndpointTypesFromPricingCache(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.InvalidatePricingCache()
	})

	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "advanced-custom-model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)

	channel := &model.Channel{
		Id:     701,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "advanced-custom-key",
		Status: common.ChannelStatusEnabled,
		Name:   "advanced-custom-channel",
		Group:  "default",
		Models: "gemini-3.5-flash",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: []dto.AdvancedCustomRoute{
				{
					IncomingPath: "/v1/chat/completions",
					UpstreamPath: "/v1/chat/completions",
				},
				{
					IncomingPath: "/v1/responses",
					UpstreamPath: "/v1beta/models/{model}:generateContent",
					Converter:    "openai_responses_to_gemini_generate_content",
					Models:       []string{"re:^gemini-"},
				},
			},
		},
	})
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gemini-3.5-flash",
		ChannelId: 701,
		Enabled:   true,
	}).Error)

	model.InitChannelCache()
	model.GetPricing()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1003)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	payload := decodeListModelsPayload(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "gemini-3.5-flash", payload.Data[0].Id)
	require.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, payload.Data[0].SupportedEndpointTypes)
}

func TestListModelsTokenLimitIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-token-tiered-visible-model":      "tiered_expr",
		"zz-token-tiered-empty-expr-model":   "tiered_expr",
		"zz-token-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-token-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-token-tiered-empty-expr-model": "",
	})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-token-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-token-tiered-visible-model":      true,
		"zz-token-tiered-empty-expr-model":   true,
		"zz-token-tiered-missing-expr-model": true,
		"zz-token-unpriced-model":            true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-token-tiered-visible-model")
	require.NotContains(t, ids, "zz-token-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-token-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-token-unpriced-model")
}

func TestListModelsTokenLimitUsesResolvedCustomAutoGroups(t *testing.T) {
	withSelfUseModeEnabled(t)
	originalMax := setting.GetMaxTokenAutoGroups()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("5"))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-allowed", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-vip-denied", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-outside-snapshot", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vip-allowed":              true,
		"zz-default-outside-snapshot": true,
		"zz-not-enabled":              true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)
	ids := decodeListModelsResponse(t, recorder)
	require.Equal(t, map[string]struct{}{"zz-vip-allowed": {}}, ids)

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	emptyRecorder := httptest.NewRecorder()
	emptyCtx, _ := gin.CreateTestContext(emptyRecorder)
	emptyCtx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(emptyCtx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimit, map[string]bool{"zz-vip-allowed": true})

	require.NotPanics(t, func() {
		ListModels(emptyCtx, constant.ChannelTypeAnthropic)
	})
	var anthropicResponse struct {
		Data    []dto.AnthropicModel `json:"data"`
		FirstID string               `json:"first_id"`
		LastID  string               `json:"last_id"`
	}
	require.NoError(t, common.Unmarshal(emptyRecorder.Body.Bytes(), &anthropicResponse))
	require.Empty(t, anthropicResponse.Data)
	require.Empty(t, anthropicResponse.FirstID)
	require.Empty(t, anthropicResponse.LastID)
}

func TestSetupLoginDoesNotTouchPasswordWhenPasswordFieldOmitted(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	hashedPassword, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := &model.User{
		Username: "twofa-user",
		Password: hashedPassword,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)

	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		setupLogin(&model.User{
			Id:          user.Id,
			AuthVersion: user.AuthVersion,
			Username:    user.Username,
			Role:        user.Role,
			Status:      user.Status,
			Group:       user.Group,
		}, nil, c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, hashedPassword, stored.Password)
}
