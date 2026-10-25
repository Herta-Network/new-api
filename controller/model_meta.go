package controller

import (
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetAllModelsMeta 获取模型列表（分页）
//
//	@Summary	列出模型元数据
//	@Tags		模型
//	@Param		status					query		string	false	"上架状态：enabled（或 1）、disabled（或 0），默认全部"
//	@Param		sync_official			query		string	false	"是否同步官方元数据：yes（或 1）、no（或 0），默认全部"
//	@Param		square_state			query		string	false	"模型广场状态：visible、unavailable、unpriced、hidden、partial"
//	@Param		pricing_mode			query		string	false	"计价方式：expression、task、legacy、unpriced；只含精确名称的模型"
//	@Param		include_channel_models	query		bool	false	"为 true 时还列出渠道里有、但没有元数据的模型"
//	@Param		p						query		int		false	"页码，从 1 开始"
//	@Param		page_size				query		int		false	"每页条数，最多 100"
//	@Success	200						{object}	common.ApiResponse{data=modelMetaPage}
//	@Failure	400						{object}	common.ApiResponse	"square_state、pricing_mode 或分页参数无效"
//	@Security	AdminAuth
//	@Router		/api/models/ [get]
func GetAllModelsMeta(c *gin.Context) {
	listModelsMeta(c, "", "")
}

// SearchModelsMeta 搜索模型列表
//
//	@Summary	搜索模型元数据
//	@Tags		模型
//	@Param		keyword					query		string	false	"在模型名、描述和标签中搜索"
//	@Param		vendor					query		string	false	"供应商 ID，或供应商名称的一部分"
//	@Param		status					query		string	false	"上架状态：enabled（或 1）、disabled（或 0），默认全部"
//	@Param		sync_official			query		string	false	"是否同步官方元数据：yes（或 1）、no（或 0），默认全部"
//	@Param		square_state			query		string	false	"模型广场状态：visible、unavailable、unpriced、hidden、partial"
//	@Param		pricing_mode			query		string	false	"计价方式：expression、task、legacy、unpriced；只含精确名称的模型"
//	@Param		include_channel_models	query		bool	false	"为 true 时还列出渠道里有、但没有元数据的模型"
//	@Param		p						query		int		false	"页码，从 1 开始"
//	@Param		page_size				query		int		false	"每页条数，最多 100"
//	@Success	200						{object}	common.ApiResponse{data=modelMetaPage}
//	@Failure	400						{object}	common.ApiResponse	"square_state、pricing_mode 或分页参数无效"
//	@Security	AdminAuth
//	@Router		/api/models/search [get]
func SearchModelsMeta(c *gin.Context) {
	listModelsMeta(c, c.Query("keyword"), c.Query("vendor"))
}

type modelMetaPage struct {
	Items    []*model.Model `json:"items"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	// VendorCounts maps vendor IDs to their model counts over all models.
	VendorCounts map[int64]int64 `json:"vendor_counts"`
}

func listModelsMeta(c *gin.Context, keyword, vendor string) {
	squareState := model.ModelSquareState(c.Query("square_state"))
	switch squareState {
	case "", model.ModelSquareVisible, model.ModelSquareUnavailable, model.ModelSquareUnpriced, model.ModelSquareHidden, model.ModelSquarePartial:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid model square state"})
		return
	}

	pricingMode := c.Query("pricing_mode")
	switch pricingMode {
	case "", model.ModelPricingModeExpression, model.ModelPricingModeTask, model.ModelPricingModeLegacy, model.ModelPricingModeUnpriced:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid pricing mode"})
		return
	}

	pageInfo := common.GetPageQuery(c)
	filtered := squareState != "" || pricingMode != ""
	if filtered && (pageInfo.GetPage() < 1 || pageInfo.GetPageSize() < 1) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid pagination"})
		return
	}
	offset, limit := pageInfo.GetStartIdx(), pageInfo.GetPageSize()
	if filtered {
		// Visibility depends on live channels and metadata rules, and pricing
		// on the option maps. Filter the candidate set before counting and
		// paginating the results.
		offset, limit = 0, -1
	}
	search := model.SearchModels
	if c.Query("include_channel_models") == "true" {
		search = model.SearchModelsWithChannels
	}
	modelsMeta, total, err := search(keyword, vendor, c.Query("status"), c.Query("sync_official"), offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if pricingMode != "" {
		// Prices belong to concrete names; name-rule metadata has none.
		names := make([]string, 0, len(modelsMeta))
		for _, metadata := range modelsMeta {
			names = append(names, metadata.ModelName)
		}
		modes, err := model.ModelPricingModes(names)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		modelsMeta = slices.DeleteFunc(modelsMeta, func(metadata *model.Model) bool {
			return metadata.NameRule != model.NameRuleExact || modes[metadata.ModelName] != pricingMode
		})
	}
	if err := enrichModels(modelsMeta); err != nil {
		common.ApiError(c, err)
		return
	}
	if filtered {
		if squareState != "" {
			modelsMeta = slices.DeleteFunc(modelsMeta, func(metadata *model.Model) bool {
				return metadata.SquareState != squareState
			})
		}
		total = int64(len(modelsMeta))
		start := len(modelsMeta)
		if pageInfo.GetPage()-1 <= len(modelsMeta)/pageInfo.GetPageSize() {
			start = (pageInfo.GetPage() - 1) * pageInfo.GetPageSize()
		}
		end := min(start+pageInfo.GetPageSize(), len(modelsMeta))
		modelsMeta = modelsMeta[start:end]
	}

	vendorCounts, _ := model.GetVendorModelCounts()
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(modelsMeta)
	common.ApiSuccess(c, modelMetaPage{
		Items:        modelsMeta,
		Total:        total,
		Page:         pageInfo.GetPage(),
		PageSize:     pageInfo.GetPageSize(),
		VendorCounts: vendorCounts,
	})
}

// GetModelMeta 根据 ID 获取单条模型信息
//
//	@Summary	获取模型元数据
//	@Tags		模型
//	@Param		id	path		int	true	"模型元数据 ID"
//	@Success	200	{object}	common.ApiResponse{data=model.Model}
//	@Security	AdminAuth
//	@Router		/api/models/{id} [get]
func GetModelMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var m model.Model
	if err := model.DB.First(&m, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := enrichModels([]*model.Model{&m}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &m)
}

// CreateModelMeta 新建模型
//
//	@Summary	创建模型元数据
//	@Tags		模型
//	@Param		body	body		model.Model	true	"模型元数据；model_name 必填且不能重复"
//	@Success	200		{object}	common.ApiResponse{data=model.Model}
//	@Security	AdminAuth
//	@Router		/api/models/ [post]
func CreateModelMeta(c *gin.Context) {
	var m model.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		common.ApiError(c, err)
		return
	}
	if m.ModelName == "" {
		common.ApiErrorT(c, "Model name cannot be empty")
		return
	}
	if err := model.ValidateMetadataValues(model.MetadataValues{Endpoints: m.Endpoints, Status: m.Status, NameRule: m.NameRule}); err != nil {
		common.ApiError(c, err)
		return
	}
	// 名称冲突检查
	if dup, err := model.IsModelNameDuplicated(0, m.ModelName); err != nil {
		common.ApiError(c, err)
		return
	} else if dup {
		common.ApiErrorT(c, "Model name already exists")
		return
	}

	if err := m.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RefreshPricing()
	m.HasMetadata = m.Id > 0
	common.ApiSuccess(c, &m)
}

// UpdateModelMeta 更新模型
//
//	@Summary	更新模型元数据
//	@Tags		模型
//	@Param		status_only	query		bool		false	"为 true 时只更新 status（0 或 1）"
//	@Param		body		body		model.Model	true	"模型元数据；按 id 更新"
//	@Success	200			{object}	common.ApiResponse{data=model.Model}
//	@Security	AdminAuth
//	@Router		/api/models/ [put]
func UpdateModelMeta(c *gin.Context) {
	statusOnly := c.Query("status_only") == "true"

	var m model.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		common.ApiError(c, err)
		return
	}
	if m.Id == 0 {
		common.ApiErrorT(c, "Missing model ID")
		return
	}

	if statusOnly {
		if m.Status != 0 && m.Status != 1 {
			common.ApiErrorMsg(c, "invalid catalog visibility")
			return
		}
		// 只更新状态，防止误清空其他字段
		if err := model.DB.Model(&model.Model{}).Where("id = ?", m.Id).Update("status", m.Status).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		if strings.TrimSpace(m.ModelName) == "" {
			common.ApiErrorT(c, "Model name cannot be empty")
			return
		}
		if err := model.ValidateMetadataValues(model.MetadataValues{Endpoints: m.Endpoints, Status: m.Status, NameRule: m.NameRule}); err != nil {
			common.ApiError(c, err)
			return
		}
		// 名称冲突检查
		if dup, err := model.IsModelNameDuplicated(m.Id, m.ModelName); err != nil {
			common.ApiError(c, err)
			return
		} else if dup {
			common.ApiErrorT(c, "Model name already exists")
			return
		}

		if err := m.Update(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	model.RefreshPricing()
	m.HasMetadata = m.Id > 0
	common.ApiSuccess(c, &m)
}

// DeleteModelMeta 删除模型
//
//	@Summary	删除模型元数据
//	@Tags		模型
//	@Param		id						path		int		true	"模型元数据 ID"
//	@Param		remove_from_channels	query		bool	false	"同时从所有渠道移除此模型名（仅精确名称）"
//	@Param		remove_pricing			query		bool	false	"同时清除此模型的价格，只有超级管理员能用"
//	@Success	200						{object}	common.ApiResponse{data=model.ModelDeleteResult}
//	@Failure	403						{object}	common.ApiResponse	"非超级管理员要求 remove_pricing"
//	@Security	AdminAuth
//	@Router		/api/models/{id} [delete]
func DeleteModelMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	removeFromChannels, err := strconv.ParseBool(c.DefaultQuery("remove_from_channels", "false"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	removePricing, err := strconv.ParseBool(c.DefaultQuery("remove_pricing", "false"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if removePricing && c.GetInt("role") != common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Model pricing is managed by a super administrator."})
		return
	}
	result, err := model.DeleteModelMetadata([]int{id}, removeFromChannels, removePricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "model.delete", map[string]any{"model_ids": []int{id}, "remove_from_channels": removeFromChannels, "remove_pricing": removePricing, "updated_channels": result.UpdatedChannels})
	common.ApiSuccess(c, result)
}

type batchDeleteModelMetaRequest struct {
	ModelIDs           []int `json:"model_ids"`
	RemoveFromChannels bool  `json:"remove_from_channels"`
	RemovePricing      bool  `json:"remove_pricing"`
}

// @Summary	批量删除模型元数据
// @Tags		模型
// @Param		body	body		batchDeleteModelMetaRequest	true	"模型元数据 ID（1 到 1000 个）；remove_pricing 只有超级管理员能用"
// @Success	200		{object}	common.ApiResponse{data=model.ModelDeleteResult}
// @Failure	403		{object}	common.ApiResponse	"非超级管理员要求 remove_pricing"
// @Security	AdminAuth
// @Router		/api/models/delete [post]
func BatchDeleteModelMeta(c *gin.Context) {
	var request batchDeleteModelMetaRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.RemovePricing && c.GetInt("role") != common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Model pricing is managed by a super administrator."})
		return
	}
	result, err := model.DeleteModelMetadata(request.ModelIDs, request.RemoveFromChannels, request.RemovePricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "model.delete_batch", map[string]any{"model_ids": request.ModelIDs, "remove_from_channels": request.RemoveFromChannels, "remove_pricing": request.RemovePricing, "updated_channels": result.UpdatedChannels})
	common.ApiSuccess(c, result)
}

// maxCatalogStatusModels bounds one channel editor lookup.
const maxCatalogStatusModels = 5000

type modelCatalogStatusRequest struct {
	Models []string `json:"models"`
}

// GetModelCatalogStatus tells the channel editor which supplied models users
// will not see because they are unpriced or unlisted.
//
//	@Summary	查询模型的目录状态
//	@Tags		模型
//	@Param		body	body		modelCatalogStatusRequest										true	"模型名，最多 5000 个"
//	@Success	200		{object}	common.ApiResponse{data=map[string]model.ModelCatalogStatus}	"键为模型名"
//	@Security	AdminAuth
//	@Router		/api/models/catalog_status [post]
func GetModelCatalogStatus(c *gin.Context) {
	var request modelCatalogStatusRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if len(request.Models) > maxCatalogStatusModels {
		common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": maxCatalogStatusModels})
		return
	}
	statuses, err := model.GetModelCatalogStatus(request.Models)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, statuses)
}

// enrichModels keeps configured endpoints intact and derives connections from
// enabled routes, including hidden or unpriced models absent from the catalog.
func enrichModels(models []*model.Model) error {
	if len(models) == 0 {
		return nil
	}
	configured, err := model.GetConfiguredModelChannels()
	if err != nil {
		return err
	}
	for _, metadata := range models {
		if metadata == nil {
			continue
		}
		metadata.HasMetadata = metadata.Id > 0
		channelIDs := make(map[int]struct{})
		for name, ids := range configured {
			if metadata.MatchesName(name) {
				for _, id := range ids {
					channelIDs[id] = struct{}{}
				}
			}
		}
		metadata.ConfiguredChannelCount = len(channelIDs)
	}
	connections, err := model.GetModelConnections()
	if err != nil {
		return err
	}
	if err := model.FillModelSquareStates(models, configured, connections); err != nil {
		return err
	}
	for _, metadata := range models {
		if metadata == nil {
			continue
		}
		channels := make(map[int]model.BoundChannel)
		groups := make(map[string]bool)
		names := make(map[string]bool)
		endpoints := make(map[string]bool)
		quotas := make(map[int]bool)
		for _, connection := range connections {
			name := connection.Model
			if !metadata.MatchesName(name) {
				continue
			}
			names[name] = true
			groups[connection.Group] = true
			channels[connection.ChannelId] = model.BoundChannel{Name: connection.ChannelName, Type: connection.ChannelType}
			for _, endpoint := range model.GetModelSupportEndpointTypes(name) {
				endpoints[string(endpoint)] = true
			}
			for _, quota := range model.GetModelQuotaTypes(name) {
				quotas[quota] = true
			}
		}
		metadata.BoundChannels = nil
		metadata.EnableGroups = nil
		metadata.SupportedEndpoints = nil
		metadata.QuotaTypes = nil
		metadata.MatchedModels = nil
		for _, channel := range channels {
			metadata.BoundChannels = append(metadata.BoundChannels, channel)
		}
		sort.Slice(metadata.BoundChannels, func(i, j int) bool {
			a, b := metadata.BoundChannels[i], metadata.BoundChannels[j]
			if a.Name == b.Name {
				return a.Type < b.Type
			}
			return a.Name < b.Name
		})
		for group := range groups {
			metadata.EnableGroups = append(metadata.EnableGroups, group)
		}
		for endpoint := range endpoints {
			metadata.SupportedEndpoints = append(metadata.SupportedEndpoints, endpoint)
		}
		for quota := range quotas {
			metadata.QuotaTypes = append(metadata.QuotaTypes, quota)
		}
		sort.Strings(metadata.EnableGroups)
		sort.Strings(metadata.SupportedEndpoints)
		sort.Ints(metadata.QuotaTypes)
		if metadata.NameRule != model.NameRuleExact {
			for name := range names {
				metadata.MatchedModels = append(metadata.MatchedModels, name)
			}
			sort.Strings(metadata.MatchedModels)
			metadata.MatchedCount = len(names)
		}
	}
	return nil
}
