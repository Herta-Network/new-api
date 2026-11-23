package controller

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/kiro"

	"github.com/gin-gonic/gin"
)

// GetKiroChannelUsage reads the plan of a Kiro channel's account and how much
// of each metered resource its current cycle has used.
//
//	@Summary		获取 Kiro 渠道用量
//	@Description	需要 channel.read 权限。向 Kiro 管理面（GetUsageLimits）查询账号的套餐、本周期各资源（如 credit）的已用量和上限、赠送额度、下次重置时间，以及超额开关和费率；不返回账号的用户信息。凭证过期时与转发请求一样续期并保存，Kiro 提前拒绝 access token 时续期后重试一次。不支持多密钥渠道。
//	@Tags			渠道
//	@Param			id	path		int	true	"Kiro 渠道 ID"
//	@Success		200	{object}	common.ApiResponse{data=kiro.Usage}
//	@Security		AdminAuth
//	@Router			/api/channel/{id}/kiro/usage [get]
func GetKiroChannelUsage(c *gin.Context) {
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
	if ch.Type != constant.ChannelTypeKiro {
		common.ApiErrorT(c, "Channel type is not Kiro")
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		common.ApiErrorT(c, "Multi-key channels are not supported")
		return
	}

	usage, err := kiro.ChannelUsage(c.Request.Context(), ch)
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			common.ApiError(c, err)
			return
		}
		common.SysError(common.LogText("failed to fetch Kiro usage: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch usage. Please try again later")
		return
	}
	common.ApiSuccess(c, usage)
}
