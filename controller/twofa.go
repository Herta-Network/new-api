package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Verify2FARequest struct {
	Code      string `json:"code" binding:"required"`
	FlowToken string `json:"flow_token,omitempty"`
}

// @Summary		开始设置两步验证
// @Description	生成 TOTP 密钥、二维码数据和备用码，用 POST /api/user/2fa/enable 提交验证码完成启用；需要 scope 为 2fa.setup 的 X-Security-Proof。
// @Tags			两步验证
// @Success		200	{object}	common.ApiResponse{data=service.TwoFASetup}
// @Security		UserAuth && SecurityProof
// @Router			/api/user/2fa/setup [post]
func Setup2FA(c *gin.Context) {
	authorization := middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeTwoFASetup})
	if authorization == nil {
		return
	}
	identity, _ := middleware.GetStepUpIdentity(c)
	setup, err := service.StartTwoFASetup(identity, authorization)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "user.2fa_setup", nil)
	common.ApiSuccess(c, setup)
}

// @Summary		启用两步验证
// @Description	提交设置时得到的 flow_token 和 TOTP 验证码；成功后 data 为替换当前会话的访问令牌和会话，经个人访问令牌启用时为空对象。
// @Tags			两步验证
// @Param			body	body		Verify2FARequest	true	"验证码和设置流程的 flow_token"
// @Success		200		{object}	common.ApiResponse{data=sessionRotation}
// @Security		UserAuth
// @Router			/api/user/2fa/enable [post]
func Enable2FA(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	var req Verify2FARequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.FinishTwoFASetup(identity, req.FlowToken, req.Code); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "twofa_enabled")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "user.2fa_enable", nil)
	common.ApiSuccess(c, sessionRotation{bundle})
}

// Disable2FA 禁用2FA
//
//	@Summary		禁用两步验证
//	@Description	需要 scope 为 2fa.disable 的 X-Security-Proof；data 为替换当前会话的访问令牌和会话，经个人访问令牌禁用时为空对象。
//	@Tags			两步验证
//	@Success		200	{object}	common.ApiResponse{data=sessionRotation}
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/2fa/disable [post]
func Disable2FA(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeTwoFADisable}) == nil {
		return
	}
	identity, _ := middleware.GetStepUpIdentity(c)
	userId := identity.UserID
	if err := model.DisableTwoFAForSession(identity); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "twofa_disabled")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	// 记录操作日志
	recordUserSecurityAudit(c, userId, "user.2fa_disable_self", nil)

	common.ApiSuccessT(c, "Two-factor authentication disabled", sessionRotation{bundle})
}

type twoFAStatus struct {
	Enabled bool `json:"enabled"`
	Locked  bool `json:"locked"`
	// BackupCodesRemaining is present while two-factor authentication is
	// enabled and the unused backup codes could be counted.
	BackupCodesRemaining *int `json:"backup_codes_remaining,omitempty"`
}

// Get2FAStatus 获取用户2FA状态
//
//	@Summary	获取两步验证状态
//	@Tags		两步验证
//	@Success	200	{object}	common.ApiResponse{data=twoFAStatus}
//	@Security	UserAuth
//	@Router		/api/user/2fa/status [get]
func Get2FAStatus(c *gin.Context) {
	userId := c.GetInt("id")

	twoFA, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	status := twoFAStatus{
		Enabled: false,
		Locked:  false,
	}

	if twoFA != nil {
		status.Enabled = twoFA.IsEnabled
		status.Locked = twoFA.IsLocked()
		if twoFA.IsEnabled {
			// 获取剩余备用码数量
			backupCount, err := model.GetUnusedBackupCodeCount(userId)
			if err != nil {
				common.SysLog(common.LogText("failed to count backup codes: %s", err.Error()))
			} else {
				status.BackupCodesRemaining = &backupCount
			}
		}
	}

	common.ApiSuccess(c, status)
}

// backupCodesResult holds the new backup codes and, like sessionRotation, the
// access token and login session that replace the current ones.
type backupCodesResult struct {
	*service.AuthBundle
	BackupCodes []string `json:"backup_codes"`
}

// RegenerateBackupCodes 重新生成备用码
//
//	@Summary		重新生成备用码
//	@Description	作废旧备用码；需要 scope 为 2fa.backup_codes.regenerate 的 X-Security-Proof。data 含新备用码和替换当前会话的访问令牌与会话（经个人访问令牌操作时只有备用码）。
//	@Tags			两步验证
//	@Success		200	{object}	common.ApiResponse{data=backupCodesResult}
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/2fa/backup_codes [post]
func RegenerateBackupCodes(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeTwoFABackupCodes}) == nil {
		return
	}
	identity, _ := middleware.GetStepUpIdentity(c)
	userId := identity.UserID
	// 生成新的备用码
	backupCodes, err := common.GenerateBackupCodes()
	if err != nil {
		common.ApiErrorT(c, "Failed to regenerate backup codes")
		common.SysLog(common.LogText("failed to generate backup codes: %s", err.Error()))
		return
	}

	// 保存新的备用码并原子推进用户鉴权版本
	if err := model.ReplaceBackupCodesForSession(identity, backupCodes); err != nil {
		common.ApiErrorT(c, "Failed to save backup codes")
		common.SysLog(common.LogText("failed to save backup codes: %s", err.Error()))
		return
	}
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "twofa_backup_codes_regenerated")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	// 记录操作日志
	recordUserSecurityAudit(c, userId, "user.2fa_backup_codes", nil)

	common.ApiSuccessT(c, "Backup codes regenerated successfully", backupCodesResult{AuthBundle: bundle, BackupCodes: backupCodes})
}

// Verify2FALogin 登录时验证2FA
//
//	@Summary		两步验证登录
//	@Description	与 POST /api/user/login/verify 相同：提交登录返回的 flow_token 和 TOTP 验证码或备用码，成功后返回访问令牌、refresh_token、登录会话和用户。
//	@Tags			认证
//	@Param			body	body		loginVerifyRequest	true	"登录流程的 flow_token 和验证码"
//	@Success		200		{object}	common.ApiResponse{data=loginResult}
//	@Router			/api/user/login/2fa [post]
func Verify2FALogin(c *gin.Context) {
	VerifyLogin(c)
}

// Admin2FAStats 管理员获取2FA统计信息
//
//	@Summary		获取两步验证统计
//	@Description	data 为 total_users（用户总数）、enabled_users（已启用两步验证的用户数）和 enabled_rate（启用率，如 "12.5%"）。
//	@Tags			用户管理
//	@Success		200	{object}	common.ApiResponse{data=map[string]any}
//	@Security		AdminAuth
//	@Router			/api/user/2fa/stats [get]
func Admin2FAStats(c *gin.Context) {
	stats, err := model.GetTwoFAStats()
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	common.ApiSuccess(c, stats)
}

// AdminDisable2FA 管理员强制禁用用户2FA
//
//	@Summary		重置用户两步验证
//	@Description	禁用该用户的两步验证并撤销其全部登录会话；需要 scope 为 admin.user.2fa.disable 的 X-Security-Proof，只能操作角色低于自己的用户，超级管理员不受此限。
//	@Tags			用户管理
//	@Param			id	path		int	true	"用户 ID"
//	@Success		200	{object}	common.ApiResponse
//	@Security		AdminAuth && SecurityProof
//	@Router			/api/user/{id}/2fa [delete]
func AdminDisable2FA(c *gin.Context) {
	userIdStr := c.Param("id")
	userId, err := strconv.Atoi(userIdStr)
	if err != nil || userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}

	// 检查目标用户权限
	targetUser, err := model.GetUserById(userId, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorT(c, "User does not exist")
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, targetUser.Role) {
		common.ApiErrorT(c, "No permission to change the 2FA settings of users at the same or higher level")
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserTwoFADisable, service.AdminUserContext{UserID: userId})
	if authorization == nil {
		return
	}

	// 禁用2FA
	if err := model.DisableTwoFAWithAuthVersion(userId); err != nil {
		if errors.Is(err, model.ErrTwoFANotEnabled) {
			common.ApiErrorT(c, "Two-factor authentication is not enabled.")
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	if _, err := model.RevokeAllUserSessions(userId, "admin_twofa_disabled"); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	recordManageAuditFor(c, userId, "user.2fa_disable", map[string]any{
		"username":            targetUser.Username,
		"verification_method": authorization.Method,
	})

	common.ApiSuccessT(c, "Two-factor authentication reset", nil)
}
