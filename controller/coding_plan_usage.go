package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codingplan"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetCodingPlanChannelUsage reads how much of each window the key of a Kimi
// For Coding, GLM Coding Plan or MiniMax Coding Plan channel has used.
//
//	@Summary		获取编程套餐渠道用量
//	@Description	需要 channel.read 权限。适用于 Kimi For Coding、GLM Coding Plan 和 MiniMax Coding Plan 渠道，向上游查询各额度窗口的用量。不支持多密钥渠道。
//	@Tags			渠道
//	@Param			id	path		int	true	"渠道 ID"
//	@Success		200	{object}	common.ApiResponse{data=codingplan.Usage}
//	@Security		AdminAuth
//	@Router			/api/channel/{id}/coding-plan/usage [get]
func GetCodingPlanChannelUsage(c *gin.Context) {
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
	plan, ok := codingplan.Detect(ch.Type, ch.GetBaseURL())
	if !ok {
		common.ApiErrorT(c, "Channel is not a Kimi For Coding, GLM Coding Plan or MiniMax Coding Plan channel")
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		common.ApiErrorT(c, "Multi-key channels are not supported")
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

	usage, err := plan.FetchUsage(ctx, client, ch.Key, time.Now())
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			common.ApiError(c, err)
			return
		}
		common.SysError(common.LogText("failed to fetch %s usage: %s", plan.Provider.Name(), err.Error()))
		common.ApiErrorT(c, "Failed to fetch usage. Please try again later")
		return
	}
	common.ApiSuccess(c, usage)
}
