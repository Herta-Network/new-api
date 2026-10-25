package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// @Summary		获取 Codex 渠道用量
// @Description	需要 channel.read 权限。向渠道上游查询用量；success 跟随上游状态码，data 是上游响应体。上游返回 401 或 403 时用 refresh_token 刷新令牌、保存后重试一次。不支持多密钥渠道。
// @Tags			渠道
// @Param			id	path		int	true	"Codex 渠道 ID"
// @Success		200	{object}	codexWhamResponse
// @Security		AdminAuth
// @Router			/api/channel/{id}/codex/usage [get]
func GetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.FetchCodexWhamUsage,
		common.LogText("failed to fetch codex usage"),
		"Failed to fetch usage. Please try again later",
	)
}

// @Summary		获取 Codex 限额重置额度
// @Description	需要 channel.read 权限。向渠道上游查询限额重置额度；success 跟随上游状态码，data 是上游响应体。上游返回 401 或 403 时用 refresh_token 刷新令牌、保存后重试一次。不支持多密钥渠道。
// @Tags			渠道
// @Param			id	path		int	true	"Codex 渠道 ID"
// @Success		200	{object}	codexWhamResponse
// @Security		AdminAuth
// @Router			/api/channel/{id}/codex/usage/reset-credits [get]
func GetCodexChannelRateLimitResetCredits(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.FetchCodexWhamRateLimitResetCredits,
		common.LogText("failed to fetch codex reset credits"),
		"Failed to fetch reset credit details. Please try again later",
	)
}

// @Summary		重置 Codex 渠道用量
// @Description	需要 channel.operate 权限。消耗一次限额重置额度来重置用量；success 跟随上游状态码，data 是上游响应体。上游返回 401 或 403 时用 refresh_token 刷新令牌、保存后重试一次。不支持多密钥渠道。
// @Tags			渠道
// @Param			id	path		int	true	"Codex 渠道 ID"
// @Success		200	{object}	codexWhamResponse
// @Security		AdminAuth
// @Router			/api/channel/{id}/codex/usage/reset [post]
func ResetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.ConsumeCodexWhamRateLimitResetCredit,
		common.LogText("failed to reset codex usage"),
		"Failed to reset usage. Please try again later",
	)
}

// codexWhamResponse is the body of the Codex usage handlers, which pass the
// upstream status and body through.
type codexWhamResponse struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	UpstreamStatus int    `json:"upstream_status"`
	// Data is the upstream body: its JSON value, or its text when it is not
	// JSON.
	Data any `json:"data"`
}

type codexWhamFetchFunc func(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error)

func fetchCodexChannelWhamData(
	c *gin.Context,
	fetch codexWhamFetchFunc,
	logPrefix string,
	userMessage string,
) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != constant.ChannelTypeCodex {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Codex"})
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "multi-key channel is not supported"})
		return
	}

	oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		common.SysError(common.LogText("failed to parse oauth key: %s", err.Error()))
		common.ApiErrorT(c, "Failed to parse credentials. Please check the channel configuration")
		return
	}
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	if accessToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "codex channel: access_token is required"})
		return
	}
	if accountID == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "codex channel: account_id is required"})
		return
	}

	setting := ch.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	statusCode, body, err := fetch(ctx, client, ch.GetBaseURL(), accessToken, accountID)
	if err != nil {
		common.SysError(logPrefix + ": " + err.Error())
		common.ApiErrorT(c, userMessage)
		return
	}

	if (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) && strings.TrimSpace(oauthKey.RefreshToken) != "" {
		refreshCtx, refreshCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer refreshCancel()

		res, refreshErr := service.RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, setting.Proxy)
		if refreshErr == nil {
			oauthKey.AccessToken = res.AccessToken
			oauthKey.RefreshToken = res.RefreshToken
			oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
			oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
			if strings.TrimSpace(oauthKey.Type) == "" {
				oauthKey.Type = "codex"
			}

			encoded, encErr := common.Marshal(oauthKey)
			if encErr == nil {
				_ = model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", string(encoded)).Error
				model.InitChannelCache()
			}

			ctx2, cancel2 := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel2()
			statusCode, body, err = fetch(ctx2, client, ch.GetBaseURL(), oauthKey.AccessToken, accountID)
			if err != nil {
				common.SysError(common.LogText("%s after refresh: %s", logPrefix, err.Error()))
				common.ApiErrorT(c, userMessage)
				return
			}
		}
	}

	var payload any
	if common.Unmarshal(body, &payload) != nil {
		payload = string(body)
	}

	ok := statusCode >= 200 && statusCode < 300
	resp := codexWhamResponse{
		Success:        ok,
		Message:        "",
		UpstreamStatus: statusCode,
		Data:           payload,
	}
	if !ok {
		resp.Message = fmt.Sprintf("upstream status: %d", statusCode)
	}
	c.JSON(http.StatusOK, resp)
}
