package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// @Summary	获取模型定价配置
// @Tags		系统设置
// @Param		model	query		[]string	false	"只返回这些模型，可重复；不传时返回全部有定价的模型"	collectionFormat(multi)
// @Success	200		{object}	common.ApiResponse{data=model.ModelPricingSnapshot}
// @Security	RootAuth
// @Router		/api/option/model_pricing [get]
func GetModelPricingConfig(c *gin.Context) {
	snapshot, err := model.GetModelPricingSnapshot(c.QueryArray("model"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, snapshot)
}

type modelPricingDraft struct {
	ModelName string              `json:"model_name"`
	Pricing   model.PricingValues `json:"pricing"`
}

// @Summary		预览定价转换
// @Description	把一个模型的旧版倍率或按次价格草稿转换为计费表达式，只预览不保存。
// @Tags			系统设置
// @Param			body	body		modelPricingDraft	true	"模型名和定价草稿"
// @Success		200		{object}	common.ApiResponse{data=model.ModelPricingConversion}
// @Failure		400		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/option/model_pricing/convert [post]
func PreviewModelPricingConversion(c *gin.Context) {
	var request modelPricingDraft
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	preview, err := model.PreviewModelPricingConversion(request.ModelName, request.Pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

type legacyModelPricingConversions struct {
	Entries []model.LegacyModelPricingConversion `json:"entries"`
}

// @Summary		预览旧版定价批量转换
// @Description	列出仍使用旧版倍率或按次价格的模型及其转换结果，只预览不保存；保存时以 version 作为 expected_version。
// @Tags			系统设置
// @Success		200	{object}	common.ApiResponse{data=legacyModelPricingConversions}
// @Security		RootAuth
// @Router			/api/option/model_pricing/convert_legacy [post]
func PreviewLegacyModelPricingConversions(c *gin.Context) {
	entries, err := model.PreviewLegacyModelPricingConversions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, legacyModelPricingConversions{Entries: entries})
}

// @Summary		预览模型定价
// @Description	按定价草稿计算一个模型的生效定价，只预览不保存。
// @Tags			系统设置
// @Param			body	body		modelPricingDraft	true	"模型名和定价草稿"
// @Success		200		{object}	common.ApiResponse{data=model.ModelPricingDescription}
// @Failure		400		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/option/model_pricing/preview [post]
func PreviewModelPricing(c *gin.Context) {
	var request modelPricingDraft
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	preview, err := model.PreviewModelPricing(request.ModelName, request.Pricing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, model.ModelPricingDescription{
		Effective:      preview,
		CacheWriteMode: model.ResolveCacheWriteMode(request.ModelName, request.Pricing),
		BillingDetails: model.ResolveLegacyBillingDetails(request.ModelName, preview, request.Pricing),
	})
}

type updateModelPricingRequest struct {
	Changes []model.ModelPricingChange `json:"changes"`
}

type modelPricingUpdateResult struct {
	UpdatedModels []string `json:"updated_models"`
}

// @Summary		保存模型定价
// @Description	每项变更带 expected_version；有模型的定价已被他人修改时返回 409，需重新加载后再保存。
// @Tags			系统设置
// @Param			body	body		updateModelPricingRequest	true	"定价变更"
// @Success		200		{object}	common.ApiResponse{data=modelPricingUpdateResult}
// @Failure		400		{object}	common.ApiResponse
// @Failure		409		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/option/model_pricing [patch]
func UpdateModelPricingConfig(c *gin.Context) {
	var request updateModelPricingRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpdateModelPricing(request.Changes); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrModelPricingConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	names := make([]string, 0, len(request.Changes))
	for _, change := range request.Changes {
		names = append(names, change.ModelName)
	}
	recordManageAudit(c, "model.pricing.update", map[string]any{"models": names})
	common.ApiSuccess(c, modelPricingUpdateResult{UpdatedModels: names})
}
