package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

type SubscriptionPlanDTO struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

// ---- User APIs ----

// @Summary		列出订阅套餐
// @Description	只列出已启用的套餐。
// @Tags			订阅
// @Success		200	{object}	common.ApiResponse{data=[]SubscriptionPlanDTO}
// @Security		UserAuth
// @Router			/api/subscription/plans [get]
func GetSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, SubscriptionPlanDTO{
			Plan: p,
		})
	}
	common.ApiSuccess(c, result)
}

type subscriptionSelf struct {
	BillingPreference string                      `json:"billing_preference"`
	Subscriptions     []model.SubscriptionSummary `json:"subscriptions"`     // all active subscriptions
	AllSubscriptions  []model.SubscriptionSummary `json:"all_subscriptions"` // all subscriptions including expired
}

// @Summary	获取我的订阅
// @Tags		订阅
// @Success	200	{object}	common.ApiResponse{data=subscriptionSelf}
// @Security	UserAuth
// @Router		/api/subscription/self [get]
func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired)
	allSubscriptions, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.SubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.SubscriptionSummary{}
	}

	common.ApiSuccess(c, subscriptionSelf{
		BillingPreference: pref,
		Subscriptions:     activeSubscriptions,
		AllSubscriptions:  allSubscriptions,
	})
}

// @Summary	设置扣费偏好
// @Tags		订阅
// @Param		body	body		BillingPreferenceRequest	true	"billing_preference：subscription_first、wallet_first、subscription_only 或 wallet_only，其他值按 subscription_first"
// @Success	200		{object}	common.ApiResponse{data=BillingPreferenceRequest}
// @Security	UserAuth
// @Router		/api/subscription/self/preference [put]
func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	if err := model.UpdateUserSetting(user.Id, current); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, BillingPreferenceRequest{BillingPreference: pref})
}

// ---- Admin APIs ----

// @Summary	列出全部订阅套餐
// @Tags		订阅管理
// @Success	200	{object}	common.ApiResponse{data=[]SubscriptionPlanDTO}
// @Security	AdminAuth
// @Router		/api/subscription/admin/plans [get]
func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, SubscriptionPlanDTO{
			Plan: p,
		})
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

// @Summary	创建订阅套餐
// @Tags		订阅管理
// @Param		body	body		AdminUpsertSubscriptionPlanRequest	true	"plan.price_amount 为站点货币金额（0–9999，最多两位小数）；plan.total_amount 为总额度，0 不限；allow_balance_pay、allow_wallet_overflow 不传为 true；plan.id 被忽略"
// @Success	200		{object}	common.ApiResponse{data=model.SubscriptionPlan}
// @Security	AdminAuth
// @Router		/api/subscription/admin/plans [post]
func AdminCreateSubscriptionPlan(c *gin.Context) {
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req.Plan.Id = 0
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorT(c, "Subscription plan title cannot be empty")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorT(c, "Price cannot be negative")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorT(c, "Price cannot exceed 9999")
		return
	}
	if _, err := payment.PlanPrice(req.Plan.PriceAmount); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Plan.AllowBalancePay == nil {
		req.Plan.AllowBalancePay = common.GetPointer(true)
	}
	if req.Plan.AllowWalletOverflow == nil {
		req.Plan.AllowWalletOverflow = common.GetPointer(true)
	}
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorT(c, "Purchase limit cannot be negative")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorT(c, "Total quota cannot be negative")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorT(c, "Upgrade group does not exist")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorT(c, "Downgrade group does not exist")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorT(c, "Custom reset cycle must be greater than 0 seconds")
		return
	}
	err := model.DB.Create(&req.Plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(req.Plan.Id)
	common.ApiSuccess(c, req.Plan)
}

// @Summary	更新订阅套餐
// @Tags		订阅管理
// @Param		id		path		int									true	"套餐 ID"
// @Param		body	body		AdminUpsertSubscriptionPlanRequest	true	"plan.price_amount 为站点货币金额（0–9999，最多两位小数）；plan.total_amount 为总额度，0 不限；allow_balance_pay、allow_wallet_overflow 不传则不变"
// @Success	200		{object}	common.ApiResponse
// @Security	AdminAuth
// @Router		/api/subscription/admin/plans/{id} [put]
func AdminUpdateSubscriptionPlan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorT(c, "Subscription plan title cannot be empty")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorT(c, "Price cannot be negative")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorT(c, "Price cannot exceed 9999")
		return
	}
	if _, err := payment.PlanPrice(req.Plan.PriceAmount); err != nil {
		common.ApiError(c, err)
		return
	}
	req.Plan.Id = id
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorT(c, "Purchase limit cannot be negative")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorT(c, "Total quota cannot be negative")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorT(c, "Upgrade group does not exist")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorT(c, "Downgrade group does not exist")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorT(c, "Custom reset cycle must be greater than 0 seconds")
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]any{
			"title":                      req.Plan.Title,
			"subtitle":                   req.Plan.Subtitle,
			"price_amount":               req.Plan.PriceAmount,
			"duration_unit":              req.Plan.DurationUnit,
			"duration_value":             req.Plan.DurationValue,
			"custom_seconds":             req.Plan.CustomSeconds,
			"enabled":                    req.Plan.Enabled,
			"sort_order":                 req.Plan.SortOrder,
			"max_purchase_per_user":      req.Plan.MaxPurchasePerUser,
			"total_amount":               req.Plan.TotalAmount,
			"upgrade_group":              req.Plan.UpgradeGroup,
			"downgrade_group":            req.Plan.DowngradeGroup,
			"quota_reset_period":         req.Plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": req.Plan.QuotaResetCustomSeconds,
			"updated_at":                 common.GetTimestamp(),
		}
		if req.Plan.AllowBalancePay != nil {
			updateMap["allow_balance_pay"] = *req.Plan.AllowBalancePay
		}
		if req.Plan.AllowWalletOverflow != nil {
			updateMap["allow_wallet_overflow"] = *req.Plan.AllowWalletOverflow
		}
		result := tx.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updateMap)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return common.NewMessage("Subscription plan does not exist")
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

// @Summary	启用或停用订阅套餐
// @Tags		订阅管理
// @Param		id		path		int											true	"套餐 ID"
// @Param		body	body		AdminUpdateSubscriptionPlanStatusRequest	true	"enabled 必填"
// @Success	200		{object}	common.ApiResponse
// @Security	AdminAuth
// @Router		/api/subscription/admin/plans/{id} [patch]
func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Update("enabled", *req.Enabled)
	if result.Error != nil {
		common.ApiError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		common.ApiErrorT(c, "Subscription plan does not exist")
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminBindSubscriptionRequest struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

// @Summary		为用户开通订阅
// @Description	不经支付为用户开通套餐。用户分组随之变化时 data 为提示（message、message_key、message_params），否则为 null。
// @Tags			订阅管理
// @Param			body	body		AdminBindSubscriptionRequest	true	"用户 ID 和套餐 ID"
// @Success		200		{object}	common.ApiResponse{data=subscriptionChangeNotice}
// @Security		AdminAuth
// @Router			/api/subscription/admin/bind [post]
func AdminBindSubscription(c *gin.Context) {
	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	msg, err := model.AdminBindSubscription(req.UserId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

// subscriptionChangeNotice tells the admin how the user's group changes;
// Message is its English text.
type subscriptionChangeNotice struct {
	Message       string         `json:"message"`
	MessageKey    string         `json:"message_key"`
	MessageParams map[string]any `json:"message_params,omitempty"`
}

// subscriptionChangeSuccess replies to an admin subscription change. notice
// tells the admin how the user's group changes, when it does.
func subscriptionChangeSuccess(c *gin.Context, notice *common.Message) {
	if notice == nil {
		common.ApiSuccess(c, nil)
		return
	}
	common.ApiSuccess(c, subscriptionChangeNotice{Message: notice.Error(), MessageKey: notice.Key, MessageParams: notice.Params})
}

// ---- Admin: user subscription management ----

// @Summary	列出用户的订阅
// @Tags		订阅管理
// @Param		id	path		int	true	"用户 ID"
// @Success	200	{object}	common.ApiResponse{data=[]model.SubscriptionSummary}
// @Security	AdminAuth
// @Router		/api/subscription/admin/users/{id}/subscriptions [get]
func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId int `json:"plan_id"`
}

type AdminResetSubscriptionRequest struct {
	PlanId           int   `json:"plan_id"`
	AdvanceResetTime *bool `json:"advance_reset_time"`
}

func resolveAdvanceResetTime(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func recordSubscriptionResetUserLogs(c *gin.Context, result *model.SubscriptionResetResult, adminInfo *model.AuditAdminInfo) {
	if result == nil || result.ResetCount == 0 {
		return
	}
	const action = "subscription.user_quota_reset"
	params := map[string]any{"plan_id": result.PlanId, "plan_title": result.PlanTitle}
	operation := model.AuditOperation{Action: action, Params: params}
	content := auditContentEN(action, params)
	for _, userId := range result.AffectedUserIds {
		model.RecordLogWithAdminInfo(userId, model.LogTypeManage, content, adminInfo, &operation, c)
	}
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
//
//	@Summary		为用户添加订阅
//	@Description	不经支付为用户开通套餐。用户分组随之变化时 data 为提示（message、message_key、message_params），否则为 null。
//	@Tags			订阅管理
//	@Param			id		path		int									true	"用户 ID"
//	@Param			body	body		AdminCreateUserSubscriptionRequest	true	"套餐 ID"
//	@Success		200		{object}	common.ApiResponse{data=subscriptionChangeNotice}
//	@Security		AdminAuth
//	@Router			/api/subscription/admin/users/{id}/subscriptions [post]
func AdminCreateUserSubscription(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	msg, err := model.AdminBindSubscription(userId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

// @Summary		重置用户的套餐额度
// @Description	清零有效订阅的已用额度；advance_reset_time 默认 true，即从现在起重新计算额度重置周期。
// @Tags			订阅管理
// @Param			id		path		int								true	"用户 ID"
// @Param			body	body		AdminResetSubscriptionRequest	true	"plan_id 必填"
// @Success		200		{object}	common.ApiResponse{data=model.SubscriptionResetResult}
// @Security		AdminAuth
// @Router			/api/subscription/admin/users/{id}/subscriptions/reset [post]
func AdminResetUserSubscriptionsByPlan(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetUserSubscriptionsByPlan(userId, req.PlanId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(c, result, auditOperatorInfo(c))
	recordManageAuditFor(c, userId, "subscription.user_plan_reset", map[string]any{
		"target_user_id":     userId,
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

// @Summary		重置套餐的全部订阅额度
// @Description	清零有效订阅的已用额度；advance_reset_time 默认 true，即从现在起重新计算额度重置周期。
// @Tags			订阅管理
// @Param			id		path		int								true	"套餐 ID"
// @Param			body	body		AdminResetSubscriptionRequest	true	"plan_id 被忽略"
// @Success		200		{object}	common.ApiResponse{data=model.SubscriptionResetResult}
// @Security		AdminAuth
// @Router			/api/subscription/admin/plans/{id}/subscriptions/reset [post]
func AdminResetPlanSubscriptions(c *gin.Context) {
	planId, _ := strconv.Atoi(c.Param("id"))
	if planId <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetPlanSubscriptions(planId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(c, result, auditOperatorInfo(c))
	common.SysLog(common.LogText("admin reset subscription plan %d quota: reset_count=%d user_count=%d advance_reset_time=%t",
		result.PlanId, result.ResetCount, result.UserCount, result.AdvanceResetTime))
	recordManageAudit(c, "subscription.plan_reset", map[string]any{
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
//
//	@Summary		作废用户订阅
//	@Description	用户分组随之变化时 data 为提示（message、message_key、message_params），否则为 null。
//	@Tags			订阅管理
//	@Param			id	path		int	true	"用户订阅 ID"
//	@Success		200	{object}	common.ApiResponse{data=subscriptionChangeNotice}
//	@Security		AdminAuth
//	@Router			/api/subscription/admin/user_subscriptions/{id}/invalidate [post]
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorT(c, "Invalid subscription ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
//
//	@Summary		删除用户订阅
//	@Description	用户分组随之变化时 data 为提示（message、message_key、message_params），否则为 null。
//	@Tags			订阅管理
//	@Param			id	path		int	true	"用户订阅 ID"
//	@Success		200	{object}	common.ApiResponse{data=subscriptionChangeNotice}
//	@Security		AdminAuth
//	@Router			/api/subscription/admin/user_subscriptions/{id} [delete]
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorT(c, "Invalid subscription ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}
