package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// @Summary		获取公开倍率配置
// @Description	供其他实例同步倍率，需在设置中开启倍率公开，否则返回 403。data 含 model_ratio、completion_ratio、cache_ratio、create_cache_ratio、model_price，有配置时还含 billing_mode、billing_expr。
// @Tags			系统
// @Success		200	{object}	common.ApiResponse{data=map[string]object}
// @Failure		403	{object}	common.ApiResponse
// @Router			/api/ratio_config [get]
func GetRatioConfig(c *gin.Context) {
	if !ratio_setting.IsExposeRatioEnabled() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("The ratio config API is not enabled"))
		return
	}

	common.ApiSuccess(c, billing_setting.GetPricingSyncData(map[string]any(ratio_setting.GetExposedData())))
}
