package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	passkeysvc "github.com/QuantumNous/new-api/service/passkey"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

type passkeyFinishRequest struct {
	FlowToken  string          `json:"flow_token"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyVerifyBeginRequest struct {
	RPID    string          `json:"rp_id"`
	Scope   string          `json:"scope"`
	Context json.RawMessage `json:"context,omitempty"`
}

type passkeyRegisterOptions struct {
	// Options are the WebAuthn creation options for navigator.credentials.create.
	Options   *protocol.CredentialCreation `json:"options" swaggertype:"object"`
	FlowToken string                       `json:"flow_token"`
	ExpiresAt int64                        `json:"expires_at"`
}

// passkeyAssertionOptions start a Passkey login or verification ceremony.
type passkeyAssertionOptions struct {
	// Options are the WebAuthn request options for navigator.credentials.get.
	Options *protocol.CredentialAssertion `json:"options" swaggertype:"object"`
	// RPIDs are the Passkey domains this website can use.
	RPIDs     []string `json:"rp_ids"`
	FlowToken string   `json:"flow_token"`
	ExpiresAt int64    `json:"expires_at"`
}

func parsePasskeyFinishRequest(c *gin.Context) (*passkeyFinishRequest, error) {
	var request passkeyFinishRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		return nil, err
	}
	if request.FlowToken == "" || len(request.Credential) == 0 {
		return nil, errors.New("incomplete Passkey flow parameters")
	}
	return &request, nil
}

// @Summary		开始注册 Passkey
// @Description	需要 scope 为 passkey.register 的 X-Security-Proof。把 data.options 交给 navigator.credentials.create，再把结果和 flow_token 提交到 POST /api/user/passkey/register/finish。
// @Tags			Passkey
// @Success		200	{object}	common.ApiResponse{data=passkeyRegisterOptions}
// @Security		UserAuth && SecurityProof
// @Router			/api/user/passkey/register/begin [post]
func PasskeyRegisterBegin(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	authorization := middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopePasskeyRegister})
	if authorization == nil {
		return
	}

	credential, err := model.GetPasskeyByUserID(user.Id)
	if err != nil && !errors.Is(err, model.ErrPasskeyNotFound) {
		writeSecurityOperationError(c, err)
		return
	}
	if errors.Is(err, model.ErrPasskeyNotFound) {
		credential = nil
	}

	wa, err := passkeysvc.BuildWebAuthn(c.Request)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	waUser := passkeysvc.NewWebAuthnUser(user, credential)
	selection := wa.Config.AuthenticatorSelection
	selection.UserVerification = protocol.VerificationRequired
	options := []webauthnlib.RegistrationOption{webauthnlib.WithAuthenticatorSelection(selection)}
	if credential != nil {
		descriptor := credential.ToWebAuthnCredential().Descriptor()
		options = append(options, webauthnlib.WithExclusions([]protocol.CredentialDescriptor{descriptor}))
	}

	creation, sessionData, err := wa.BeginRegistration(waUser, options...)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		common.ApiErrorT(c, "The current sign-in method does not support security verification")
		return
	}
	flowToken, expiresAt, err := passkeysvc.CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyRegister,
		passkeysvc.FlowSecurity{AuthSessionIdentity: identity, Scope: authorization.Scope, ContextHash: authorization.ContextHash, Authorization: authorization},
		sessionData,
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	common.ApiSuccess(c, passkeyRegisterOptions{
		Options:   creation,
		FlowToken: flowToken,
		ExpiresAt: expiresAt,
	})
}

// @Summary		完成注册 Passkey
// @Description	开始注册时的 X-Security-Proof 随 flow_token 一起校验，这里不再需要。成功后 data 为替换当前会话的访问令牌和会话，经个人访问令牌注册时为空对象。
// @Tags			Passkey
// @Param			body	body		passkeyFinishRequest	true	"开始注册返回的 flow_token 和 navigator.credentials.create 的结果"
// @Success		200		{object}	common.ApiResponse{data=sessionRotation}
// @Security		UserAuth
// @Router			/api/user/passkey/register/finish [post]
func PasskeyRegisterFinish(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	request, err := parsePasskeyFinishRequest(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid Passkey verification request")
		return
	}
	parsedCredential, err := protocol.ParseCredentialCreationResponseBytes(request.Credential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	credentialRecord, err := model.GetPasskeyByUserID(user.Id)
	if err != nil && !errors.Is(err, model.ErrPasskeyNotFound) {
		writeSecurityOperationError(c, err)
		return
	}
	if errors.Is(err, model.ErrPasskeyNotFound) {
		credentialRecord = nil
	}

	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		common.ApiErrorT(c, "The current sign-in method does not support security verification")
		return
	}
	sessionData, security, err := passkeysvc.PopSessionDataFlow(
		request.FlowToken,
		model.AuthFlowPurposePasskeyRegister,
		identity,
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	wa, err := passkeysvc.BuildWebAuthnForRPID(c.Request, sessionData.RelyingPartyID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	c.Set("passkey_rp_id", sessionData.RelyingPartyID)
	if sessionData.UserVerification != protocol.VerificationRequired {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	if err := service.ValidateFlowAuthorization(identity, service.VerificationOperation{Scope: service.VerificationScopePasskeyRegister}, security.Authorization); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	waUser := passkeysvc.NewWebAuthnUser(user, credentialRecord)
	credential, err := wa.CreateCredential(waUser, *sessionData, parsedCredential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	passkeyCredential := model.NewPasskeyCredentialFromWebAuthn(user.Id, credential)
	if passkeyCredential == nil {
		common.ApiErrorT(c, "Unable to create the Passkey credential")
		return
	}

	passkeyCredential.RPID = &sessionData.RelyingPartyID
	if err := model.RegisterPasskeyForSession(identity, passkeyCredential); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "passkey_registered")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	recordUserSecurityAudit(c, user.Id, "user.passkey_register", nil)
	common.ApiSuccessT(c, "Passkey registered successfully", sessionRotation{bundle})
}

// @Summary		删除 Passkey
// @Description	需要 scope 为 passkey.delete 的 X-Security-Proof；data 为替换当前会话的访问令牌和会话，经个人访问令牌删除时为空对象。
// @Tags			Passkey
// @Success		200	{object}	common.ApiResponse{data=sessionRotation}
// @Security		UserAuth && SecurityProof
// @Router			/api/user/passkey [delete]
func PasskeyDelete(c *gin.Context) {
	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopePasskeyDelete}) == nil {
		return
	}

	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		common.ApiErrorT(c, "The current sign-in method does not support security verification")
		return
	}
	if err := model.DeletePasskeyForSession(identity); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "passkey_deleted")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	recordUserSecurityAudit(c, user.Id, "user.passkey_delete", nil)
	common.ApiSuccessT(c, "Passkey removed successfully", sessionRotation{bundle})
}

// passkeyStatus is the registered Passkey of the current user. Without one,
// the data is a passkeyAbsentStatus.
type passkeyStatus struct {
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

type passkeyAbsentStatus struct {
	Enabled bool `json:"enabled"`
}

// @Summary		获取 Passkey 状态
// @Description	没有注册 Passkey 时 data 只有 enabled: false。
// @Tags			Passkey
// @Success		200	{object}	common.ApiResponse{data=passkeyStatus}
// @Security		UserAuth
// @Router			/api/user/passkey [get]
func PasskeyStatus(c *gin.Context) {
	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	credential, err := model.GetPasskeyByUserID(user.Id)
	if errors.Is(err, model.ErrPasskeyNotFound) {
		common.ApiSuccess(c, passkeyAbsentStatus{Enabled: false})
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	common.ApiSuccess(c, passkeyStatus{
		Enabled:    true,
		LastUsedAt: credential.LastUsedAt,
	})
}

type passkeyLoginBeginRequest struct {
	RPID string `json:"rp_id"`
}

// @Summary		开始 Passkey 登录
// @Description	无需用户名的 Passkey 登录：把 data.options 交给 navigator.credentials.get，再把结果和 flow_token 提交到 POST /api/user/passkey/login/finish。请求体可以省略。
// @Tags			认证
// @Param			body	body		passkeyLoginBeginRequest	false	"要使用的 Passkey 域名，可选"
// @Success		200		{object}	common.ApiResponse{data=passkeyAssertionOptions}
// @Router			/api/user/passkey/login/begin [post]
func PasskeyLoginBegin(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	var request passkeyLoginBeginRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if common.DecodeJson(c.Request.Body, &request) != nil {
			common.ApiErrorT(c, "Invalid Passkey verification request")
			return
		}
	}
	wa, rpIDs, err := passkeysvc.BuildLoginWebAuthn(c.Request, request.RPID, "")
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	assertion, sessionData, err := wa.BeginDiscoverableLogin(webauthnlib.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	flowToken, expiresAt, err := passkeysvc.CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyLogin,
		passkeysvc.FlowSecurity{},
		sessionData,
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	common.ApiSuccess(c, passkeyAssertionOptions{
		Options:   assertion,
		RPIDs:     rpIDs,
		FlowToken: flowToken,
		ExpiresAt: expiresAt,
	})
}

// @Summary		完成 Passkey 登录
// @Description	成功后直接签发会话，不再要求二次登录验证：data 为访问令牌、refresh_token、登录会话和用户。
// @Tags			认证
// @Param			body	body		passkeyFinishRequest	true	"开始登录返回的 flow_token 和 navigator.credentials.get 的结果"
// @Success		200		{object}	common.ApiResponse{data=loginResult}
// @Router			/api/user/passkey/login/finish [post]
func PasskeyLoginFinish(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	request, err := parsePasskeyFinishRequest(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid Passkey verification request")
		return
	}
	parsedCredential, err := protocol.ParseCredentialRequestResponseBytes(request.Credential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	sessionData, _, err := passkeysvc.PopSessionDataFlow(
		request.FlowToken,
		model.AuthFlowPurposePasskeyLogin,
		service.AuthIdentity{},
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	wa, err := passkeysvc.BuildWebAuthnForRPID(c.Request, sessionData.RelyingPartyID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	c.Set("passkey_rp_id", sessionData.RelyingPartyID)
	if sessionData.UserVerification != protocol.VerificationRequired {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}

	handler := func(rawID, userHandle []byte) (webauthnlib.User, error) {
		// 首先通过凭证ID查找用户
		credential, err := model.GetPasskeyByCredentialID(rawID)
		if err != nil {
			return nil, fmt.Errorf("passkey credential not found: %w", err)
		}

		if credential.RPID != nil && *credential.RPID != "" && *credential.RPID != sessionData.RelyingPartyID {
			return nil, service.ErrVerificationFailed
		}

		// 通过凭证获取用户
		user := &model.User{Id: credential.UserID}
		if err := user.FillUserById(); err != nil {
			return nil, fmt.Errorf("failed to load the passkey user: %w", err)
		}

		if user.Status != common.UserStatusEnabled {
			return nil, model.ErrUserSessionInactive
		}

		if len(userHandle) > 0 {
			userID, parseErr := strconv.Atoi(string(userHandle))
			if parseErr != nil {
				// 记录异常但继续验证，因为某些客户端可能使用非数字格式
				common.SysLog(common.LogText("PasskeyLogin: userHandle parse error for credential, length: %d", len(userHandle)))
			} else if userID != user.Id {
				return nil, errors.New("user handle does not match the credential")
			}
		}

		return passkeysvc.NewWebAuthnUser(user, credential), nil
	}

	waUser, credential, err := wa.ValidatePasskeyLogin(handler, *sessionData, parsedCredential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	userWrapper, ok := waUser.(*passkeysvc.WebAuthnUser)
	if !ok {
		common.ApiErrorT(c, "Invalid Passkey login state")
		return
	}

	modelUser := userWrapper.ModelUser()
	if modelUser == nil {
		common.ApiErrorT(c, "Invalid Passkey login state")
		return
	}

	if modelUser.Status != common.UserStatusEnabled {
		common.ApiErrorT(c, "This user account is disabled.")
		return
	}

	if err := model.UpdatePasskeyAssertionState(modelUser.Id, credential, time.Now(), sessionData.RelyingPartyID); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	c.Set("login_verification_method", service.VerificationMethodPasskey)
	setupLoginAtAuthVersion(modelUser, modelUser.AuthVersion, c)
}

// @Summary		重置用户 Passkey
// @Description	删除该用户的 Passkey 并撤销其全部登录会话；需要 scope 为 admin.user.passkey.reset 的 X-Security-Proof，只能操作角色低于自己的用户，超级管理员不受此限。
// @Tags			用户管理
// @Param			id	path		int	true	"用户 ID"
// @Success		200	{object}	common.ApiResponse
// @Security		AdminAuth && SecurityProof
// @Router			/api/user/{id}/reset_passkey [delete]
func AdminResetPasskey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}

	user := &model.User{Id: id}
	if err := user.FillUserById(); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorMsg(c, "no permission")
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserPasskeyReset, service.AdminUserContext{UserID: user.Id})
	if authorization == nil {
		return
	}

	if _, err := model.GetPasskeyByUserID(user.Id); err != nil {
		if errors.Is(err, model.ErrPasskeyNotFound) {
			common.ApiErrorT(c, "No Passkey is registered.")
			return
		}
		writeSecurityOperationError(c, err)
		return
	}

	if err := model.DeletePasskeyByUserIDWithAuthVersion(user.Id); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if _, err := model.RevokeAllUserSessions(user.Id, "admin_passkey_reset"); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	recordManageAuditFor(c, user.Id, "user.reset_passkey", map[string]any{
		"username":            user.Username,
		"id":                  user.Id,
		"verification_method": authorization.Method,
	})
	common.ApiSuccessT(c, "Passkey reset successfully", nil)
}

// @Summary		开始 Passkey 安全验证
// @Description	为某个 scope 的敏感操作做 Passkey 验证：把 data.options 交给 navigator.credentials.get，再把结果和 flow_token 提交到 POST /api/user/passkey/verify/finish 换取 X-Security-Proof。
// @Tags			安全验证
// @Param			body	body		passkeyVerifyBeginRequest	true	"操作的 scope 和 context，可选的 Passkey 域名"
// @Success		200		{object}	common.ApiResponse{data=passkeyAssertionOptions}
// @Security		UserAuth
// @Router			/api/user/passkey/verify/begin [post]
func PasskeyVerifyBegin(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	var request passkeyVerifyBeginRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid Passkey verification request")
		return
	}
	binding, err := service.BindVerificationOperation(service.VerificationOperation{Scope: request.Scope, Context: request.Context})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	if _, err := service.RequireVerificationMethod(identity, request.Scope, service.VerificationMethodPasskey); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	credential, err := model.GetPasskeyByUserID(user.Id)
	if err != nil {
		common.ApiErrorT(c, "No Passkey is registered.")
		return
	}

	credentialRPID := ""
	if credential.RPID != nil {
		credentialRPID = *credential.RPID
	}
	wa, rpIDs, err := passkeysvc.BuildLoginWebAuthn(c.Request, request.RPID, credentialRPID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	waUser := passkeysvc.NewWebAuthnUser(user, credential)
	assertion, sessionData, err := wa.BeginLogin(waUser, webauthnlib.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	flowToken, expiresAt, err := passkeysvc.CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyStepUp,
		passkeysvc.FlowSecurity{AuthSessionIdentity: identity, Scope: binding.Scope, ContextHash: binding.ContextHash},
		sessionData,
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	common.ApiSuccess(c, passkeyAssertionOptions{
		Options:   assertion,
		RPIDs:     rpIDs,
		FlowToken: flowToken,
		ExpiresAt: expiresAt,
	})
}

// @Summary		完成 Passkey 安全验证
// @Description	data 中的 proof_token 用作该操作的 X-Security-Proof 请求头。
// @Tags			安全验证
// @Param			body	body		passkeyFinishRequest	true	"开始验证返回的 flow_token 和 navigator.credentials.get 的结果"
// @Success		200		{object}	common.ApiResponse{data=service.SecurityProof}
// @Security		UserAuth
// @Router			/api/user/passkey/verify/finish [post]
func PasskeyVerifyFinish(c *gin.Context) {
	if !system_setting.PasskeySettingsSnapshot().Enabled {
		common.ApiErrorT(c, "Passkey authentication is disabled.")
		return
	}

	user, err := getAuthenticatedUser(c)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	request, err := parsePasskeyFinishRequest(c)
	if err != nil {
		common.ApiErrorT(c, "Invalid Passkey verification request")
		return
	}
	parsedCredential, err := protocol.ParseCredentialRequestResponseBytes(request.Credential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	credential, err := model.GetPasskeyByUserID(user.Id)
	if err != nil {
		common.ApiErrorT(c, "No Passkey is registered.")
		return
	}

	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		common.ApiErrorT(c, "The current sign-in method does not support security verification")
		return
	}
	sessionData, security, err := passkeysvc.PopSessionDataFlow(
		request.FlowToken,
		model.AuthFlowPurposePasskeyStepUp,
		identity,
	)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	wa, err := passkeysvc.BuildWebAuthnForRPID(c.Request, sessionData.RelyingPartyID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	c.Set("passkey_rp_id", sessionData.RelyingPartyID)
	if sessionData.UserVerification != protocol.VerificationRequired {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}

	if credential.RPID != nil && *credential.RPID != "" && *credential.RPID != sessionData.RelyingPartyID {
		writeSecurityOperationError(c, service.ErrVerificationFailed)
		return
	}
	waUser := passkeysvc.NewWebAuthnUser(user, credential)
	validatedCredential, err := wa.ValidateLogin(waUser, *sessionData, parsedCredential)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	if err := model.UpdatePasskeyAssertionState(user.Id, validatedCredential, time.Now(), sessionData.RelyingPartyID); err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	proof, err := service.CompleteSecurityVerification(identity, service.VerificationBinding{Scope: security.Scope, ContextHash: security.ContextHash}, service.VerificationMethodPasskey)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	recordUserSecurityAudit(c, user.Id, "user.security_verify", map[string]any{"method": proof.Method, "scope": proof.Scope})
	common.ApiSuccess(c, proof)
}

func getAuthenticatedUser(c *gin.Context) (*model.User, error) {
	id := c.GetInt("id")
	if id == 0 {
		return nil, service.ErrAuthTokenInvalid
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, model.ErrUserSessionInactive
	}
	return user, nil
}
