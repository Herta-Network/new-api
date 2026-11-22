package controller

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// https://platform.openai.com/docs/api-reference/models/list

var openAIModels []dto.OpenAIModels
var channelId2Models map[int][]string

func init() {
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := range constant.APITypeDummy {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, dto.OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: channelName,
			})
		}
	}
	for _, modelName := range ai360.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: ai360.ChannelName,
		})
	}
	for _, modelName := range moonshot.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: moonshot.ChannelName,
		})
	}
	for _, modelName := range lingyiwanwu.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: lingyiwanwu.ChannelName,
		})
	}
	for _, modelName := range minimax.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: minimax.ChannelName,
		})
	}
	for modelName, _ := range constant.MidjourneyModel2Action {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "midjourney",
		})
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i <= constant.ChannelTypeDummy; i++ {
		apiType, success := common.ChannelType2APIType(i)
		if !success || apiType == constant.APITypeAIProxyLibrary {
			if models := channelTypePluginModels(i); len(models) > 0 {
				channelId2Models[i] = models
			}
			continue
		}
		meta := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: i,
		}}
		adaptor := relay.GetAdaptor(apiType)
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
		if len(channelId2Models[i]) == 0 {
			if models := channelTypePluginModels(i); len(models) > 0 {
				channelId2Models[i] = models
			}
		}
	}
	openAIModels = lo.UniqBy(openAIModels, func(m dto.OpenAIModels) string {
		return m.Id
	})
}

// channelTypePluginModels lists the models of every plugin declaring a
// built-in channel type, each once, in plugin key order.
func channelTypePluginModels(channelType int) []string {
	var models []string
	for _, plugin := range jsplugin.DefaultRegistry.PluginsByChannelType(channelType) {
		for _, name := range plugin.Meta.Models {
			if !slices.Contains(models, name) {
				models = append(models, name)
			}
		}
	}
	return models
}

// modelOwnerSystem owns a model whose metadata names no vendor, as OpenAI
// reports the models its platform owns.
const modelOwnerSystem = "system"

// buildOpenAIModel names the owner from ownerByModel, the vendors of model
// metadata. A channel never names it: a model list must not tell callers
// which channel serves a model.
func buildOpenAIModel(modelName string, ownerByModel map[string]string) dto.OpenAIModels {
	oaiModel := dto.OpenAIModels{
		Id:      modelName,
		Object:  "model",
		Created: 1626777600,
		OwnedBy: modelOwnerSystem,
	}
	if owner, ok := ownerByModel[modelName]; ok && owner != "" {
		oaiModel.OwnedBy = owner
	}
	oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(modelName)
	return oaiModel
}

type modelListGroups struct {
	userGroup   string
	tokenGroup  string
	ownerGroups []string
}

func getModelListGroups(c *gin.Context) (modelListGroups, error) {
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if userGroup == "" && (tokenGroup == "" || tokenGroup == "auto") {
		var err error
		userGroup, err = model.GetUserGroup(c.GetInt("id"), false)
		if err != nil {
			return modelListGroups{}, err
		}
	}

	if tokenGroup == "auto" {
		return modelListGroups{
			userGroup:   userGroup,
			tokenGroup:  tokenGroup,
			ownerGroups: service.GetRequestAutoGroups(c, userGroup),
		}, nil
	}

	group := userGroup
	if tokenGroup != "" {
		group = tokenGroup
	}
	return modelListGroups{
		userGroup:   userGroup,
		tokenGroup:  tokenGroup,
		ownerGroups: []string{group},
	}, nil
}

// openAIModelListResponse is the model list in the OpenAI format.
type openAIModelListResponse struct {
	Success bool               `json:"success"`
	Data    []dto.OpenAIModels `json:"data"`
	Object  string             `json:"object"`
}

// anthropicModelListResponse is the model list in the Anthropic format.
type anthropicModelListResponse struct {
	Data    []dto.AnthropicModel `json:"data"`
	FirstID string               `json:"first_id"`
	HasMore bool                 `json:"has_more"`
	LastID  string               `json:"last_id"`
}

// geminiModelListResponse is the model list in the Gemini format; the list
// is never paged, so nextPageToken is always null.
type geminiModelListResponse struct {
	Models        []dto.GeminiModel `json:"models"`
	NextPageToken *string           `json:"nextPageToken"`
}

// usableOpenAIModels lists the models the caller's token may use, in the
// order ListModels returns them, each owned by the vendor the model square
// shows for it.
func usableOpenAIModels(c *gin.Context) ([]dto.OpenAIModels, error) {
	groups, err := getModelListGroups(c)
	if err != nil {
		return nil, err
	}
	modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	var tokenModelLimit map[string]bool
	if modelLimitEnable {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		if ok {
			tokenModelLimit, _ = s.(map[string]bool)
		}
		if tokenModelLimit == nil {
			tokenModelLimit = map[string]bool{}
		}
	}
	vendorNames := make(map[int]string)
	for _, vendor := range model.GetVendors() {
		vendorNames[vendor.ID] = vendor.Name
	}
	ownerByModel := make(map[string]string)
	for _, pricing := range model.GetPricing() {
		ownerByModel[pricing.ModelName] = vendorNames[pricing.VendorID]
	}
	userOpenAiModels := make([]dto.OpenAIModels, 0)
	for _, modelName := range model.UsableModelNames(groups.ownerGroups, model.AcceptsUnpricedModels(c.GetInt("id"))) {
		if modelLimitEnable {
			matchingName := ratio_setting.RoutingMatchModelName(modelName)
			if !tokenModelLimit[modelName] && !tokenModelLimit[matchingName] {
				continue
			}
		}
		userOpenAiModels = append(userOpenAiModels, buildOpenAIModel(modelName, ownerByModel))
	}
	return userOpenAiModels, nil
}

func ListModels(c *gin.Context, modelType int) {
	userOpenAiModels, err := usableOpenAIModels(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "get user group failed",
		})
		return
	}

	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		firstID := ""
		lastID := ""
		if len(useranthropicModels) > 0 {
			firstID = useranthropicModels[0].ID
			lastID = useranthropicModels[len(useranthropicModels)-1].ID
		}
		c.JSON(200, anthropicModelListResponse{
			Data:    useranthropicModels,
			FirstID: firstID,
			HasMore: false,
			LastID:  lastID,
		})
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(200, geminiModelListResponse{
			Models:        userGeminiModels,
			NextPageToken: nil,
		})
	default:
		c.JSON(200, openAIModelListResponse{
			Success: true,
			Data:    userOpenAiModels,
			Object:  "list",
		})
	}
}

// channelModelListResponse is ChannelListModels' body, which has no message.
type channelModelListResponse struct {
	Success bool               `json:"success"`
	Data    []dto.OpenAIModels `json:"data"`
}

// @Summary		列出内置模型
// @Description	需要 channel.read 权限。各渠道适配器内置的模型，按模型名去重。成功时响应体没有 message。
// @Tags			渠道
// @Success		200	{object}	channelModelListResponse
// @Security		AdminAuth
// @Router			/api/channel/models [get]
func ChannelListModels(c *gin.Context) {
	c.JSON(200, channelModelListResponse{
		Success: true,
		Data:    openAIModels,
	})
}

// dashboardModelListResponse is DashboardListModels' body, which has no
// message.
type dashboardModelListResponse struct {
	Success bool             `json:"success"`
	Data    map[int][]string `json:"data"`
}

// @Summary		列出各渠道类型的模型
// @Description	data 的键是渠道类型，值是该类型内置或任务插件声明的模型。成功时响应体没有 message。
// @Tags			模型
// @Success		200	{object}	dashboardModelListResponse
// @Security		UserAuth
// @Router			/api/models [get]
func DashboardListModels(c *gin.Context) {
	modelsByChannel := make(map[int][]string, len(channelId2Models))
	for channelType, models := range channelId2Models {
		modelsByChannel[channelType] = append([]string(nil), models...)
	}
	for channelType := 1; channelType <= constant.ChannelTypeDummy; channelType++ {
		if models := channelTypePluginModels(channelType); len(models) > 0 {
			modelsByChannel[channelType] = models
		}
	}
	c.JSON(200, dashboardModelListResponse{
		Success: true,
		Data:    modelsByChannel,
	})
}

// enabledModelListResponse is EnabledListModels' body, which has no message.
type enabledModelListResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

// @Summary		列出已启用的模型
// @Description	需要 channel.read 权限。已启用渠道提供的全部模型名。成功时响应体没有 message。
// @Tags			渠道
// @Success		200	{object}	enabledModelListResponse
// @Security		AdminAuth
// @Router			/api/channel/models_enabled [get]
func EnabledListModels(c *gin.Context) {
	c.JSON(200, enabledModelListResponse{
		Success: true,
		Data:    model.GetEnabledModels(),
	})
}

func RetrieveModel(c *gin.Context, modelType int) {
	modelId := c.Param("model")
	userOpenAiModels, err := usableOpenAIModels(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "get user group failed",
		})
		return
	}
	if index := slices.IndexFunc(userOpenAiModels, func(m dto.OpenAIModels) bool { return m.Id == modelId }); index >= 0 {
		aiModel := userOpenAiModels[index]
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(200, dto.AnthropicModel{
				ID:          aiModel.Id,
				CreatedAt:   time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: aiModel.Id,
				Type:        "model",
			})
		default:
			c.JSON(200, aiModel)
		}
	} else {
		openAIError := types.OpenAIError{
			Message: fmt.Sprintf("The model '%s' does not exist", modelId),
			Type:    "invalid_request_error",
			Param:   "model",
			Code:    "model_not_found",
		}
		c.JSON(200, OpenAIErrorResponse{Error: openAIError})
	}
}
