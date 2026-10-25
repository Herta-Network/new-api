package controller

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// @Summary	列出兑换码
// @Tags		兑换码
// @Param		p			query		int	false	"页码，从 1 开始"
// @Param		page_size	query		int	false	"每页条数"
// @Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Redemption}}
// @Security	AdminAuth
// @Router		/api/redemption/ [get]
func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

// @Summary	搜索兑换码
// @Tags		兑换码
// @Param		keyword		query		string	false	"兑换码 ID 或名称前缀"
// @Param		status		query		string	false	"状态：1 可用（未过期）、2 已禁用、3 已使用、expired 已过期"
// @Param		p			query		int		false	"页码，从 1 开始"
// @Param		page_size	query		int		false	"每页条数"
// @Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Redemption}}
// @Security	AdminAuth
// @Router		/api/redemption/search [get]
func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

// @Summary	获取兑换码
// @Tags		兑换码
// @Param		id	path		int	true	"兑换码 ID"
// @Success	200	{object}	common.ApiResponse{data=model.Redemption}
// @Security	AdminAuth
// @Router		/api/redemption/{id} [get]
func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, redemption)
	return
}

// @Summary		生成兑换码
// @Description	data 为生成的兑换码。中途失败时 success 为 false，data 为已生成的兑换码。支付合规条款未确认时返回失败。
// @Tags			兑换码
// @Param			body	body		model.Redemption	true	"只用到 name（1–20 字）、count（生成个数，1–100）、quota（每个兑换码的额度，正数）和 expired_time（过期时间，Unix 秒，0 不过期）"
// @Success		200		{object}	common.ApiResponse{data=[]string}
// @Security		AdminAuth
// @Router			/api/redemption/ [post]
func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}

	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorT(c, "Redemption code name length must be between 1-20")
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorT(c, "Redemption code count must be greater than 0")
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorT(c, "Maximum 100 redemption codes can be generated at once")
		return
	}
	if redemption.Quota <= 0 {
		common.ApiErrorT(c, "Redemption quota must be positive")
		return
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
		common.ApiError(c, err)
		return
	}
	var keys []string
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		cleanRedemption := model.Redemption{
			UserId:      c.GetInt("id"),
			Name:        redemption.Name,
			Key:         key,
			CreatedTime: common.GetTimestamp(),
			Quota:       redemption.Quota,
			ExpiredTime: redemption.ExpiredTime,
		}
		err = cleanRedemption.Insert()
		if err != nil {
			common.SysError(common.LogText("failed to insert redemption: %s", err.Error()))
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Failed to create redemption code, please try again later"), gin.H{"data": keys})
			return
		}
		keys = append(keys, key)
	}
	recordManageAudit(c, "redemption.create", map[string]any{
		"name":  redemption.Name,
		"count": redemption.Count,
		"quota": logger.FormatQuota(redemption.Quota),
	})
	common.ApiSuccess(c, keys)
	return
}

// @Summary	删除兑换码
// @Tags		兑换码
// @Param		id	path		int	true	"兑换码 ID"
// @Success	200	{object}	common.ApiResponse
// @Security	AdminAuth
// @Router		/api/redemption/{id} [delete]
func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

// @Summary	更新兑换码
// @Tags		兑换码
// @Param		status_only	query		string				false	"非空时只更新 status"
// @Param		body		body		model.Redemption	true	"按 id 更新 name、quota（正数）和 expired_time（Unix 秒，0 不过期）；status_only 时只更新 status"
// @Success	200			{object}	common.ApiResponse{data=model.Redemption}
// @Security	AdminAuth
// @Router		/api/redemption/ [put]
func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if statusOnly == "" {
		if redemption.Quota <= 0 {
			common.ApiErrorT(c, "Redemption quota must be positive")
			return
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
			common.ApiError(c, err)
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, cleanRedemption)
	return
}

// @Summary		清理失效兑换码
// @Description	删除已使用、已禁用和已过期的兑换码，data 为删除的个数。
// @Tags			兑换码
// @Success		200	{object}	common.ApiResponse{data=int}
// @Security		AdminAuth
// @Router			/api/redemption/invalid [delete]
func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
	return
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return common.NewMessage("Expiration time cannot be earlier than current time")
	}
	return nil
}

type deleteRedemptionBatchRequest struct {
	Ids []int `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
}

// @Summary		批量删除兑换码
// @Description	data 为删除的个数。
// @Tags			兑换码
// @Param			body	body		deleteRedemptionBatchRequest	true	"兑换码 ID，1–1000 个正整数"
// @Success		200		{object}	common.ApiResponse{data=int}
// @Security		AdminAuth
// @Router			/api/redemption/batch [post]
func DeleteRedemptionBatch(c *gin.Context) {
	var request deleteRedemptionBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	count, err := model.BatchDeleteRedemptions(request.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}
