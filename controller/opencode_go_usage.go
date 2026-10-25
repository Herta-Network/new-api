package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/opencodego"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetOpenCodeGoChannelUsage reads how much of each allowance window the key of
// an OpenCode Go channel has used.
//
//	@Summary		获取 OpenCode Go 渠道用量
//	@Description	需要 channel.read 权限。向上游查询各额度窗口的已用比例。不支持多密钥渠道。
//	@Tags			渠道
//	@Param			id	path		int	true	"OpenCode Go 渠道 ID"
//	@Success		200	{object}	common.ApiResponse{data=opencodego.Usage}
//	@Security		AdminAuth
//	@Router			/api/channel/{id}/opencode-go/usage [get]
func GetOpenCodeGoChannelUsage(c *gin.Context) {
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
	if ch.Type != constant.ChannelTypeOpenCodeGo {
		common.ApiErrorT(c, "Channel type is not OpenCode Go")
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		common.ApiErrorT(c, "Multi-key channels are not supported")
		return
	}

	baseURL := ch.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(ch.Type)
	}
	setting := ch.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	usage, err := opencodego.FetchUsage(ctx, client, baseURL, ch.Key, time.Now())
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			common.ApiError(c, err)
			return
		}
		common.SysError(common.LogText("failed to fetch OpenCode Go usage: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch usage. Please try again later")
		return
	}
	common.ApiSuccess(c, usage)
}
