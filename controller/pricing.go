package controller

import (
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
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
// @Description	访问取决于顶部导航的定价模块设置：公开（登录可选）、需要登录，或关闭（返回 403，带 Authorization 的请求改为要求超级管理员）。已登录时按用户分组返回可用分组和分组倍率。
// @Tags			系统
// @Success		200	{object}	pricingResponse
// @Router			/api/pricing [get]
func GetPricing(c *gin.Context) {
	userId := c.GetInt("id")
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if userId > 0 {
		user, err := model.GetUserCache(userId)
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	usableGroup := service.GetUserUsableGroups(group)
	pricing := model.UsableCatalog(slices.Collect(maps.Keys(usableGroup)), model.AcceptsUnpricedModels(userId))
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, pricingResponse{
		Success:           true,
		Data:              pricing,
		Vendors:           model.GetVendors(),
		GroupRatio:        groupRatio,
		UsableGroup:       usableGroup,
		SupportedEndpoint: model.GetSupportedEndpointMap(),
		AutoGroups:        service.GetUserAutoGroup(group),
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
