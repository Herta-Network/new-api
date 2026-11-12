package controller

import (
	"errors"
	"io"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type pluginLoginStartRequest struct {
	KeyIndex *int `json:"key_index"`
	Add      bool `json:"add"`
}

type pluginLoginContinueRequest struct {
	Flow  string                   `json:"flow"`
	Input service.PluginLoginInput `json:"input"`
}

type pluginLoginCallbackRequest struct {
	Plugin string            `json:"plugin"`
	Params map[string]string `json:"params"`
}

// pluginLoginReply answers a login call: the step the flow reached, the id
// of a flow just opened, and the channel of a vendor callback.
type pluginLoginReply struct {
	Flow      string                      `json:"flow,omitempty"`
	ChannelID int                         `json:"channel_id,omitempty"`
	Step      service.PluginLoginStepView `json:"step"`
}

type pluginCredentialList struct {
	Items []service.PluginCredentialView `json:"items"`
}

// pluginLoginIdentity is the administrator session a login flow belongs to:
// a browser session, or a scoped access token with its own session.
func pluginLoginIdentity(c *gin.Context) (service.PluginLoginIdentity, bool) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return service.PluginLoginIdentity{}, false
	}
	return service.PluginLoginIdentity{UserID: identity.UserID, SessionID: identity.SessionID}, true
}

func pluginLoginChannelID(c *gin.Context) (int, bool) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		common.ApiErrorT(c, "Channel does not exist")
		return 0, false
	}
	return channelID, true
}

// finishPluginLogin answers a login call; a done step stored a credential, so
// the channel cache is reloaded and the change is audited.
func finishPluginLogin(c *gin.Context, result service.PluginLoginResult, reply pluginLoginReply) {
	if credential := result.Step.Credential; credential != nil {
		model.InitChannelCache()
		recordManageAudit(c, "channel.update", map[string]any{
			"id":             result.ChannelID,
			"name":           result.ChannelName,
			"changed_fields": []string{"key"},
			"source":         "plugin_login",
			"label":          credential.Label,
			"account_id":     credential.AccountID,
		})
	}
	reply.Step = result.Step
	common.ApiSuccess(c, reply)
}

// StartPluginLogin opens a plugin login flow on a channel.
//
// @Summary		开始插件登录
// @Description	需要 channel.sensitive_write 权限，且须是浏览器会话或带会话的访问令牌。在任务插件渠道上开始插件的登录流程（设备码、粘贴、表单或跳转），返回流程 ID 和第一步。key_index 指定重新登录的密钥行，add 为 true 时登录一个新账号加到渠道。登录完成时凭证存入渠道密钥并记审计日志。
// @Tags			渠道
// @Param			id		path		int						true	"渠道 ID"
// @Param			body	body		pluginLoginStartRequest	false	"要登录的密钥行"
// @Success		200		{object}	common.ApiResponse{data=pluginLoginReply}
// @Security		AdminAuth
// @Router			/api/channel/{id}/plugin_login [post]
func StartPluginLogin(c *gin.Context) {
	identity, ok := pluginLoginIdentity(c)
	if !ok {
		return
	}
	channelID, ok := pluginLoginChannelID(c)
	if !ok {
		return
	}
	var request pluginLoginStartRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil && !errors.Is(err, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.StartPluginLogin(c.Request.Context(), identity, channelID, request.KeyIndex, request.Add)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	finishPluginLogin(c, result, pluginLoginReply{Flow: result.Flow})
}

// ContinuePluginLogin submits the browser's input to a login flow.
//
// @Summary		继续插件登录
// @Description	需要 channel.sensitive_write 权限。把浏览器的输入（粘贴的内容、表单字段，或设备码流程的轮询）交给开始插件登录返回的流程，返回下一步。流程只属于开始它的会话。
// @Tags			渠道
// @Param			id		path		int							true	"渠道 ID"
// @Param			body	body		pluginLoginContinueRequest	true	"流程 ID 和输入"
// @Success		200		{object}	common.ApiResponse{data=pluginLoginReply}
// @Security		AdminAuth
// @Router			/api/channel/{id}/plugin_login/continue [post]
func ContinuePluginLogin(c *gin.Context) {
	identity, ok := pluginLoginIdentity(c)
	if !ok {
		return
	}
	channelID, ok := pluginLoginChannelID(c)
	if !ok {
		return
	}
	var request pluginLoginContinueRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.ContinuePluginLogin(c.Request.Context(), identity, channelID, request.Flow, request.Input)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	finishPluginLogin(c, result, pluginLoginReply{})
}

// CompletePluginLoginCallback receives the query of the vendor's redirect to
// a plugin's callback page.
//
// @Summary		完成插件登录跳转
// @Description	需要 channel.sensitive_write 权限。控制台的插件回调页把供应商跳转带回的查询参数交给这里，完成开始插件登录时打开的跳转流程，返回所属渠道和下一步。
// @Tags			渠道
// @Param			body	body		pluginLoginCallbackRequest	true	"插件和回调查询参数"
// @Success		200		{object}	common.ApiResponse{data=pluginLoginReply}
// @Security		AdminAuth
// @Router			/api/channel/plugin_login/callback [post]
func CompletePluginLoginCallback(c *gin.Context) {
	identity, ok := pluginLoginIdentity(c)
	if !ok {
		return
	}
	var request pluginLoginCallbackRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := service.CompletePluginLoginCallback(c.Request.Context(), identity, request.Plugin, request.Params)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	finishPluginLogin(c, result, pluginLoginReply{ChannelID: result.ChannelID})
}

// GetPluginCredentials lists a channel's credentials without their secrets.
//
// @Summary		列出插件凭证
// @Description	需要 channel.read 权限。按密钥行列出任务插件渠道的凭证：标签、账号、过期时间等，不含密钥本身；静态密钥行没有标签和账号。
// @Tags			渠道
// @Param			id	path		int	true	"渠道 ID"
// @Success		200	{object}	common.ApiResponse{data=pluginCredentialList}
// @Security		AdminAuth
// @Router			/api/channel/{id}/plugin_credentials [get]
func GetPluginCredentials(c *gin.Context) {
	channelID, ok := pluginLoginChannelID(c)
	if !ok {
		return
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		common.ApiErrorT(c, "Channel does not exist")
		return
	}
	common.ApiSuccess(c, pluginCredentialList{Items: service.PluginCredentialViews(channel)})
}
