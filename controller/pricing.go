package controller

import (
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

type pricingResponse struct {
	Success           bool                           `json:"success"`
	Data              []model.Pricing                `json:"data"`
	Vendors           []model.PricingVendor          `json:"vendors"`
	GroupRatio        map[string]float64             `json:"group_ratio"`
	UsableGroup       map[string]string              `json:"usable_group"`
	SupportedEndpoint map[string]common.EndpointInfo `json:"supported_endpoint"`
	AutoGroups        []string                       `json:"auto_groups"`
	PricingVersion    string                         `json:"pricing_version"`
}

// @Summary		获取模型定价
// @Description	访问取决于顶部导航的定价模块设置：公开（登录可选）、需要登录，或关闭（返回 403，带 Authorization 的请求改为要求超级管理员）。只返回访问者可用分组里的模型，每个模型的 enable_groups 和 group_ratio 也只含这些分组；已登录时分组倍率套用用户分组的组间倍率。
// @Tags			系统
// @Success		200	{object}	pricingResponse
// @Router			/api/pricing [get]
func GetPricing(c *gin.Context) {
	userId := c.GetInt("id")
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	usableGroup := service.GetUserUsableGroups(userGroup)
	pricing := model.UsableCatalog(slices.Collect(maps.Keys(usableGroup)), model.AcceptsUnpricedModels(userId))
	// The catalog entries are copies, but their EnableGroup slices belong to
	// the pricing cache every viewer shares, so build new ones.
	for i := range pricing {
		pricing[i].EnableGroup = slices.DeleteFunc(slices.Clone(pricing[i].EnableGroup), func(group string) bool {
			_, ok := usableGroup[group]
			return !ok
		})
	}
	groupRatio := ratio_setting.GetGroupRatioCopy()
	for group := range groupRatio {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
			continue
		}
		if userGroup != "" {
			if ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group); ok {
				groupRatio[group] = ratio
			}
		}
	}

	c.JSON(200, pricingResponse{
		Success:           true,
		Data:              pricing,
		Vendors:           model.GetVendors(),
		GroupRatio:        groupRatio,
		UsableGroup:       usableGroup,
		SupportedEndpoint: model.GetSupportedEndpointMap(),
		AutoGroups:        service.GetUserAutoGroup(userGroup),
		PricingVersion:    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

// @Summary		重置模型倍率
// @Description	把 ModelRatio 恢复为内置默认值。
// @Tags			系统设置
// @Success		200	{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/option/rest_model_ratio [post]
func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
