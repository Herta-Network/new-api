package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// channelPriorityOrderResponse is the body of the channel priority handlers,
// which has no message.
type channelPriorityOrderResponse struct {
	Success bool                       `json:"success"`
	Data    model.ChannelPriorityOrder `json:"data"`
}

// @Summary		获取渠道优先级顺序
// @Description	需要 channel.read 权限。成功时响应体没有 message；revision 用于调整顺序时检查并发修改。
// @Tags			渠道
// @Success		200	{object}	channelPriorityOrderResponse
// @Security		AdminAuth
// @Router			/api/channel/priority/order [get]
func GetChannelPriorityOrder(c *gin.Context) {
	order, err := model.GetChannelPriorityOrder()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelPriorityOrderResponse{Success: true, Data: order})
}

// @Summary		调整渠道优先级
// @Description	需要 channel.write 权限。把 id 渠道移到 target_id 渠道之前（before）、之后（after）或同一优先级（same），按需改写相关渠道的优先级；或用 restore 写回一组优先级以撤销上次调整。preview 为 true 时只返回将要发生的变化；revision 与当前顺序不一致时失败。成功时响应体没有 message。
// @Tags			渠道
// @Param			body	body		model.ChannelPriorityMove	true	"移动的渠道、目标渠道和位置"
// @Success		200		{object}	channelPriorityOrderResponse
// @Security		AdminAuth
// @Router			/api/channel/priority/reorder [post]
func ReorderChannelPriority(c *gin.Context) {
	var move model.ChannelPriorityMove
	if err := common.DecodeJson(c.Request.Body, &move); err != nil {
		common.ApiErrorT(c, "Invalid channel priority move")
		return
	}
	order, err := model.ReorderChannelPriority(move)
	if err != nil {
		if errors.Is(err, model.ErrChannelPriorityConflict) || errors.Is(err, model.ErrChannelPriorityInvalid) || errors.Is(err, model.ErrChannelPriorityRange) {
			common.ApiErrorT(c, err.Error())
		} else {
			common.ApiError(c, err)
		}
		return
	}
	if !move.Preview && len(order.Changes) > 0 {
		model.InitChannelCache()
	}
	c.JSON(http.StatusOK, channelPriorityOrderResponse{Success: true, Data: order})
}
