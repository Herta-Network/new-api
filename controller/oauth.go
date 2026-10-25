package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const oauthAuthFlowTTL = 10 * time.Minute

type oauthStateRequest struct {
	Provider string          `json:"provider"`
	Intent   string          `json:"intent"`
	Aff      string          `json:"aff,omitempty"`
	Scope    string          `json:"scope,omitempty"`
	Context  json.RawMessage `json:"context,omitempty"`
	// Nonce is a random value the console keeps for this sign-in and sends
	// again with the callback, so only the browser that started a sign-in
	// can complete it. Required for intent login, refused otherwise.
	Nonce string `json:"nonce,omitempty"`
}

type oauthStateResult struct {
	FlowToken string `json:"flow_token"`
	ExpiresAt int64  `json:"expires_at"`
	// AuthorizationURL is present for Telegram, whose authorization request
	// carries a PKCE challenge the server keeps.
	AuthorizationURL string `json:"authorization_url,omitempty"`
}

type oauthFlowPayload struct {
	AffiliateCode   string                         `json:"affiliate_code,omitempty"`
	Verification    *service.OAuthVerificationFlow `json:"verification,omitempty"`
	Telegram        *oauth.TelegramOAuthFlow       `json:"telegram,omitempty"`
	SessionIdentity *service.AuthIdentity          `json:"session_identity,omitempty"`
	Authorization   *model.AuthFlowAuthorization   `json:"authorization,omitempty"`
	NonceHash       string                         `json:"nonce_hash,omitempty"`
}

// hashOAuthNonce hashes the nonce a sign-in is bound to; the flow keeps only
// the hash.
func hashOAuthNonce(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

// providerParams returns the params of messages that name the OAuth provider.
func providerParams(name string) map[string]any {
	return map[string]any{"provider": name}
}

// GenerateOAuthCode generates a state code for OAuth CSRF protection
//
//	@Summary		创建 OAuth state
//	@Description	flow_token 用作 OAuth 授权请求的 state，10 分钟内有效。intent 为 login、bind 或 verify：login 必须提交 nonce（32 到 128 个字符的随机值，由发起登录的浏览器保存，回调时放在 X-OAuth-Nonce 请求头里交回）；bind 和 verify 需要已登录的浏览器会话（Authorization: Bearer），bind 还需要 scope 为 account.binding.bind 的 X-Security-Proof，其 context 为 {provider}；verify 要提交 scope 和 context。aff 只用于 login。
//	@Tags			第三方登录
//	@Param			body	body		oauthStateRequest	true	"provider、intent，login 时的 nonce 和 aff，或 verify 时的 scope 和 context"
//	@Success		200		{object}	common.ApiResponse{data=oauthStateResult}
//	@Failure		401		{object}	common.ApiResponse
//	@Router			/api/oauth/state [post]
func GenerateOAuthCode(c *gin.Context) {
	var request oauthStateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Intent = strings.TrimSpace(request.Intent)
	request.Aff = strings.TrimSpace(request.Aff)
	request.Nonce = strings.TrimSpace(request.Nonce)
	if oauth.GetProvider(request.Provider) == nil ||
		(request.Intent != model.AuthFlowIntentLogin && request.Intent != model.AuthFlowIntentBind && request.Intent != model.AuthFlowIntentVerify) ||
		len(request.Aff) > 32 ||
		(request.Intent != model.AuthFlowIntentLogin && request.Aff != "") ||
		(request.Intent != model.AuthFlowIntentVerify && (request.Scope != "" || len(request.Context) != 0)) ||
		(request.Intent == model.AuthFlowIntentLogin) != (len(request.Nonce) >= 32 && len(request.Nonce) <= 128) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userID := 0
	sessionID := ""
	flowPayload := oauthFlowPayload{AffiliateCode: request.Aff}
	if request.Nonce != "" {
		flowPayload.NonceHash = hashOAuthNonce(request.Nonce)
	}
	bindingStarted := false
	if request.Provider == "telegram" {
		telegramFlow, err := oauth.NewTelegramOAuthFlow()
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		flowPayload.Telegram = telegramFlow
	}
	if request.Intent == model.AuthFlowIntentBind || request.Intent == model.AuthFlowIntentVerify {
		identity, ok := middleware.GetSessionAuthIdentity(c)
		if !ok {
			writeAccountStatusError(c, http.StatusUnauthorized, "Sign in to bind an account")
			return
		}
		userID = identity.UserID
		sessionID = identity.SessionID
		if request.Intent == model.AuthFlowIntentBind {
			defer func() {
				recordUserSecurityAudit(c, userID, "user.binding_start", map[string]any{"provider": request.Provider, "success": bindingStarted})
			}()
			context, err := common.Marshal(service.AccountBindingContext{Provider: request.Provider})
			if err != nil {
				writeSecurityOperationError(c, err)
				return
			}
			flowPayload.Authorization = middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context})
			if flowPayload.Authorization == nil {
				return
			}
			flowPayload.SessionIdentity = &identity
		}
		if flowPayload.Telegram != nil {
			if _, _, err := service.ValidateLoginSession(identity); err != nil {
				writeSecurityOperationError(c, err)
				return
			}
			flowPayload.SessionIdentity = &identity
		}
		if request.Intent == model.AuthFlowIntentVerify {
			verification, err := service.StartOAuthVerification(identity, service.VerificationOperation{Scope: request.Scope, Context: request.Context}, request.Provider)
			if err != nil {
				writeSecurityOperationError(c, err)
				return
			}
			flowPayload.Verification = verification
		}
	}
	payload, err := common.Marshal(flowPayload)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	expiresAt := time.Now().Add(oauthAuthFlowTTL)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeOAuth,
		Provider:  request.Provider,
		Intent:    request.Intent,
		UserId:    userID,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	bindingStarted = request.Intent == model.AuthFlowIntentBind
	data := oauthStateResult{FlowToken: state, ExpiresAt: expiresAt.Unix()}
	if flowPayload.Telegram != nil {
		data.AuthorizationURL = flowPayload.Telegram.AuthorizationURL(state)
	}
	common.ApiSuccess(c, data)
}

// HandleOAuth handles OAuth callback for all standard OAuth providers
//
//	@Summary		OAuth 回调
//	@Description	按 state 对应流程的 intent 处理：login 时 data 为访问令牌、refresh_token、登录会话和用户，账户需要二次登录验证时 data 改为 {require_verification: true, flow_token, expires_at, methods}；bind 时 data 为 {action: "bind", notification_warning}；verify 时 data 为 service.SecurityProof。
//	@Description	login 须在 X-OAuth-Nonce 请求头里交回创建 state 时提交的 nonce，不一致时返回 403。bind 和 verify 必须由发起流程的浏览器会话调用（Authorization: Bearer）。提供方返回 error 时 success 为 false，message 为其 error_description。
//	@Tags			第三方登录
//	@Param			provider			path		string	true	"提供方：github、discord、oidc、linuxdo、telegram 或自定义 OAuth 的 slug"
//	@Param			state				query		string	true	"POST /api/oauth/state 返回的 flow_token"
//	@Param			X-OAuth-Nonce		header		string	false	"login 时必填：创建 state 时提交的 nonce"
//	@Param			code				query		string	false	"授权码"
//	@Param			error				query		string	false	"提供方返回的错误码"
//	@Param			error_description	query		string	false	"提供方返回的错误说明"
//	@Success		200					{object}	common.ApiResponse{data=loginResult}
//	@Failure		400					{object}	common.ApiResponse
//	@Failure		403					{object}	common.ApiResponse
//	@Router			/api/oauth/{provider} [get]
func HandleOAuth(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		writeAccountStatusError(c, http.StatusBadRequest, "Unknown OAuth provider")
		return
	}

	// 1. Validate state (CSRF protection)
	state := c.Query("state")
	pendingFlow, err := model.GetAuthFlow(state, model.AuthFlowMatch{
		Purpose:  model.AuthFlowPurposeOAuth,
		Provider: providerName,
	})
	if err != nil {
		writeAccountStatusError(c, http.StatusForbidden, "State parameter is empty or mismatched")
		return
	}

	consumeMatch := model.AuthFlowMatch{
		Purpose:  model.AuthFlowPurposeOAuth,
		Provider: providerName,
		Intent:   pendingFlow.Intent,
	}
	bindSucceeded, notificationFailed := false, false
	if pendingFlow.Intent == model.AuthFlowIntentBind {
		defer func() {
			recordUserSecurityAudit(c, pendingFlow.UserId, "user.binding_bind", map[string]any{"provider": providerName, "success": bindSucceeded, "notification_failed": notificationFailed})
		}()
	}
	// Bind and verification callbacks must use the dashboard session that started them.
	if pendingFlow.Intent == model.AuthFlowIntentBind || pendingFlow.Intent == model.AuthFlowIntentVerify {
		identity, ok := middleware.GetSessionAuthIdentity(c)
		if !ok || identity.UserID != pendingFlow.UserId || identity.SessionID != pendingFlow.SessionId {
			writeAccountStatusError(c, http.StatusForbidden, "State parameter is empty or mismatched")
			return
		}
		consumeMatch.UserId = identity.UserID
		consumeMatch.SessionId = identity.SessionID
		if pendingFlow.Intent == model.AuthFlowIntentBind {
			var payload oauthFlowPayload
			if err := common.UnmarshalJsonStr(pendingFlow.Payload, &payload); err != nil {
				writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
				return
			}
			context, err := common.Marshal(service.AccountBindingContext{Provider: providerName})
			if err != nil {
				writeSecurityOperationError(c, err)
				return
			}
			if err := service.ValidateFlowAuthorization(identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context}, payload.Authorization); err != nil {
				writeSecurityOperationError(c, err)
				return
			}
		}
	} else if pendingFlow.Intent != model.AuthFlowIntentLogin {
		common.ApiErrorT(c, "Invalid parameters")
		return
	} else {
		// A sign-in completes only in the browser that started it, which
		// sends back the nonce it kept when it asked for the state.
		var payload oauthFlowPayload
		nonce := strings.TrimSpace(c.GetHeader("X-OAuth-Nonce"))
		if err := common.UnmarshalJsonStr(pendingFlow.Payload, &payload); err != nil || payload.NonceHash == "" || nonce == "" ||
			!hmac.Equal([]byte(hashOAuthNonce(nonce)), []byte(payload.NonceHash)) {
			writeAccountStatusError(c, http.StatusForbidden, "State parameter is empty or mismatched")
			return
		}
	}

	// 3. Check if provider is enabled
	var telegramPayload oauthFlowPayload
	if providerName == "telegram" {
		if err := oauth.TelegramConfigurationError(); err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		if err := common.UnmarshalJsonStr(pendingFlow.Payload, &telegramPayload); err != nil || telegramPayload.Telegram == nil {
			writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
			return
		}
		if pendingFlow.Intent != model.AuthFlowIntentLogin {
			identity, _ := middleware.GetSessionAuthIdentity(c)
			if telegramPayload.SessionIdentity == nil || *telegramPayload.SessionIdentity != identity {
				writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
				return
			}
			if _, _, err := service.ValidateLoginSession(identity); err != nil {
				writeSecurityOperationError(c, err)
				return
			}
		}
		c.Set(oauth.TelegramOAuthFlowContextKey, telegramPayload.Telegram)
	}
	if !provider.IsEnabled() {
		common.ApiErrorT(c, "{{provider}} login and registration has not been enabled by administrator", providerParams(provider.GetName()))
		return
	}

	// 4. Handle error from provider
	errorCode := c.Query("error")
	if errorCode != "" {
		if _, err := model.ConsumeAuthFlow(state, consumeMatch); err != nil {
			writeAccountStatusError(c, http.StatusForbidden, "State parameter is empty or mismatched")
			return
		}
		errorDescription := c.Query("error_description")
		if errorDescription == "" {
			errorDescription = errorCode
		}
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": errorDescription,
		})
		return
	}
	// 5. Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		if providerName == "telegram" {
			writeSecurityOperationError(c, err)
			return
		}
		handleOAuthError(c, err)
		return
	}

	// 6. Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		if providerName == "telegram" {
			writeSecurityOperationError(c, err)
			return
		}
		handleOAuthError(c, err)
		return
	}
	if pendingFlow.Intent == model.AuthFlowIntentBind {
		bindSucceeded, notificationFailed = handleOAuthBind(c, providerName, provider, oauthUser, pendingFlow, state, consumeMatch)
		return
	}
	flow, err := model.ConsumeAuthFlow(state, consumeMatch)
	if err != nil {
		writeAccountStatusError(c, http.StatusForbidden, "State parameter is empty or mismatched")
		return
	}

	switch flow.Intent {
	case model.AuthFlowIntentLogin:
		handleOAuthLogin(c, provider, oauthUser, token, flow)
	case model.AuthFlowIntentVerify:
		handleOAuthVerification(c, providerName, oauthUser, flow)
	}
}

func handleOAuthVerification(c *gin.Context, provider string, oauthUser *oauth.OAuthUser, flow *model.AuthFlow) {
	var payload oauthFlowPayload
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	identity, _ := middleware.GetSessionAuthIdentity(c)
	proof, err := service.FinishOAuthVerification(identity, provider, oauthUser.ProviderUserID, payload.Verification)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "user.security_verify", map[string]any{"method": proof.Method, "scope": proof.Scope, "provider": provider})
	common.ApiSuccess(c, proof)
}

func handleOAuthLogin(c *gin.Context, provider oauth.Provider, oauthUser *oauth.OAuthUser, token *oauth.OAuthToken, flow *model.AuthFlow) {
	// 7. Find or create user
	var payload oauthFlowPayload
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	user, migration, err := findOrCreateOAuthUser(c, provider, oauthUser, token, payload.AffiliateCode)
	if err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorT(c, "Email address is already in use")
			return
		}
		switch err.(type) {
		case *OAuthUserDeletedError:
			common.ApiErrorT(c, "User has been deleted")
		case *OAuthRegistrationDisabledError:
			common.ApiErrorT(c, "New user registration has been disabled by administrator")
		case *OAuthEmailAlreadyTakenError:
			common.ApiErrorT(c, "Email address is already in use")
		case *OAuthLegacyBindingNotConfirmedError:
			common.ApiErrorT(c, "This {{provider}} account cannot be linked to an existing account automatically, please sign in or register another way and then link {{provider}} in account settings", providerParams(provider.GetName()))
		default:
			writeSecurityOperationError(c, err)
		}
		return
	}

	// 8. Check user status
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorT(c, "User has been banned")
		return
	}

	// 9. Setup login
	setupLogin(user, migration, c)
}

type oauthBindResult struct {
	Action              string `json:"action"`
	NotificationWarning bool   `json:"notification_warning"`
}

// handleOAuthBind handles binding OAuth account to existing user
func handleOAuthBind(c *gin.Context, providerName string, provider oauth.Provider, oauthUser *oauth.OAuthUser, flow *model.AuthFlow, state string, match model.AuthFlowMatch) (bool, bool) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return false, false
	}
	var payload oauthFlowPayload
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return false, false
	}
	context, err := common.Marshal(service.AccountBindingContext{Provider: providerName})
	if err != nil {
		writeSecurityOperationError(c, err)
		return false, false
	}
	// Recheck after the external provider round trip, then validate the session
	// under the transaction's locks before consuming the flow and writing.
	if err := service.ValidateFlowAuthorization(identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context}, payload.Authorization); err != nil {
		writeSecurityOperationError(c, err)
		return false, false
	}
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		common.ApiErrorT(c, "This {{provider}} account has already been bound", providerParams(provider.GetName()))
		return false, false
	}
	_, err = model.ConsumeAuthFlowWithAction(state, match, func(tx *gorm.DB, _ *model.AuthFlow) error {
		if providerName == "telegram" {
			return model.BindTelegramForSessionWithTx(tx, identity, oauthUser.ProviderUserID)
		}
		if custom, ok := provider.(*oauth.GenericOAuthProvider); ok {
			return model.UpdateUserOAuthBindingForSessionWithTx(tx, identity, custom.GetProviderId(), oauthUser.ProviderUserID)
		}
		return model.UpdateUserBindColumnForSessionWithTx(tx, identity, provider.ProviderUserIDColumn(), oauthUser.ProviderUserID)
	})
	if err != nil {
		writeSecurityOperationError(c, err)
		return false, false
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		writeSecurityOperationError(c, err)
		return true, true
	}
	notificationFailed := service.NotifyAccountSecurityChange(service.AccountMailLanguage(c, user.Id), user.Email, i18n.MsgSecurityEventLoginLinked, map[string]any{"Provider": provider.GetName()}) != nil
	common.ApiSuccessT(c, "Binding successful", oauthBindResult{Action: "bind", NotificationWarning: notificationFailed})
	return true, notificationFailed
}

// findOrCreateOAuthUser finds the existing user or creates a new one. For a
// legacy GitHub binding that still waits for the login verification, it also
// returns the rewrite to carry into the challenge.
func findOrCreateOAuthUser(c *gin.Context, provider oauth.Provider, oauthUser *oauth.OAuthUser, token *oauth.OAuthToken, affiliateCode string) (*model.User, *service.LegacyGitHubMigration, error) {
	user := &model.User{}
	if provider.ProviderUserIDColumn() == "telegram_id" {
		err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, oauth.ErrTelegramAccountNotBound
		}
		return user, nil, err
	}

	// Check if user already exists with new ID
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID)
		if err != nil {
			return nil, nil, err
		}
		// Check if user has been deleted
		if user.Id == 0 {
			return nil, nil, &OAuthUserDeletedError{}
		}
		return user, nil, nil
	}

	// Legacy GitHub bindings stored the login name, which only points at a
	// candidate account. Values that are all digits are already numeric account
	// IDs and never take part in the comparison.
	legacyID, _ := oauthUser.Extra["legacy_id"].(string)
	if strings.ContainsFunc(legacyID, func(r rune) bool { return r < '0' || r > '9' }) && provider.IsUserIDTaken(legacyID) {
		if err := provider.FillUserByProviderID(user, legacyID); err != nil {
			return nil, nil, err
		}
		if user.Id != 0 {
			state, err := model.GetUserVerificationState(user.Id)
			if err != nil {
				return nil, nil, err
			}
			if state.HasTwoFA || state.HasPasskey {
				// The rewrite is written in the transaction that issues the session
				// once the login verification completes.
				return user, &service.LegacyGitHubMigration{GitHubID: oauthUser.ProviderUserID, LegacyID: legacyID}, nil
			}
			// Without a second factor, one of the addresses the provider has
			// confirmed must match the account email. The list is fetched only here
			// and never recorded.
			reason, matched := "no_matching_evidence", false
			if emailProvider, ok := provider.(oauth.VerifiedEmailProvider); ok && user.Email != "" {
				emails, err := emailProvider.GetVerifiedEmails(c.Request.Context(), token)
				if err != nil {
					common.SysError(common.LogText("[OAuth] Failed to load verified emails for user %d: %s", user.Id, err.Error()))
					reason = "verified_emails_unavailable"
				}
				accountEmail := model.NormalizeEmail(user.Email)
				matched = slices.ContainsFunc(emails, func(email string) bool { return model.NormalizeEmail(email) == accountEmail })
			}
			if !matched {
				recordLegacyGitHubBindingAudit(c, user, false, map[string]any{"legacy_id": legacyID, "provider_user_id": oauthUser.ProviderUserID, "reason": reason})
				return nil, nil, &OAuthLegacyBindingNotConfirmedError{}
			}
			written := false
			err = model.DB.Transaction(func(tx *gorm.DB) error {
				var err error
				written, err = model.MigrateLegacyGitHubBindingWithTx(tx, user.Id, legacyID, oauthUser.ProviderUserID)
				return err
			})
			if err != nil {
				return nil, nil, err
			}
			if written {
				user.GitHubId = oauthUser.ProviderUserID
				notificationFailed := service.NotifyAccountSecurityChange(service.AccountMailLanguage(c, user.Id), user.Email, i18n.MsgSecurityEventLoginLinked, map[string]any{"Provider": provider.GetName()}) != nil
				recordLegacyGitHubBindingAudit(c, user, true, map[string]any{
					"legacy_id": legacyID, "provider_user_id": oauthUser.ProviderUserID,
					"verified_email_matched": true, "notification_failed": notificationFailed,
				})
			}
			return user, nil, nil
		}
	}

	// User doesn't exist, create new user if registration is enabled
	if !common.RegisterEnabled {
		return nil, nil, &OAuthRegistrationDisabledError{}
	}

	// Set up new user
	user.Username = provider.GetProviderPrefix() + strconv.Itoa(model.GetMaxUserId()+1)

	if oauthUser.Username != "" {
		if exists, err := model.CheckUserExistOrDeleted(oauthUser.Username, ""); err == nil && !exists {
			// 防止索引退化
			if len(oauthUser.Username) <= model.UserNameMaxLength {
				user.Username = oauthUser.Username
			}
		}
	}

	if oauthUser.DisplayName != "" {
		user.DisplayName = oauthUser.DisplayName
	} else if oauthUser.Username != "" {
		user.DisplayName = oauthUser.Username
	} else {
		user.DisplayName = provider.GetName() + " User"
	}
	if oauthUser.Email != "" {
		user.Email = model.NormalizeEmail(oauthUser.Email)
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				return nil, nil, &OAuthEmailAlreadyTakenError{}
			}
			return nil, nil, err
		}
	}
	user.Role = common.RoleCommonUser
	user.Status = common.UserStatusEnabled

	// Handle affiliate code
	inviterId := 0
	if affiliateCode != "" {
		inviterId, _ = model.GetUserIdByAffCode(affiliateCode)
	}

	// Use transaction to ensure user creation and OAuth binding are atomic
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: create user and binding in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Create OAuth binding
			binding := &model.UserOAuthBinding{
				UserId:         user.Id,
				ProviderId:     genericProvider.GetProviderId(),
				ProviderUserId: oauthUser.ProviderUserID,
			}
			if err := model.CreateUserOAuthBindingWithTx(tx, binding); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		// Perform post-transaction tasks (logs, sidebar config, inviter rewards)
		user.FinalizeOAuthUserCreation(inviterId)
	} else {
		// Built-in provider: create user and update provider ID in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Set the provider user ID on the user model and update
			provider.SetProviderUserID(user, oauthUser.ProviderUserID)
			if err := tx.Model(user).Updates(map[string]any{
				"github_id":   user.GitHubId,
				"discord_id":  user.DiscordId,
				"oidc_id":     user.OidcId,
				"linux_do_id": user.LinuxDOId,
				"wechat_id":   user.WeChatId,
				"telegram_id": user.TelegramId,
			}).Error; err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		// Perform post-transaction tasks
		user.FinalizeOAuthUserCreation(inviterId)
	}

	return user, nil, nil
}

// recordLegacyGitHubBindingAudit records the outcome of a legacy GitHub binding
// rewrite as an account binding event. No session exists yet on the login path,
// so the row carries the user's own role instead of the request context.
func recordLegacyGitHubBindingAudit(c *gin.Context, user *model.User, success bool, params map[string]any) {
	params["provider"], params["legacy_migration"], params["success"] = "github", true, success
	model.RecordOperationAuditLog(user.Id, user.Role, auditContentEN("user.binding_bind", params), c.ClientIP(), "user.binding_bind", params, nil, &model.AuditRequestInfo{
		Method: c.Request.Method, Route: c.FullPath(), Path: c.FullPath(), Status: c.Writer.Status(), Success: success,
	}, c)
}

// Error types for OAuth
type OAuthUserDeletedError struct{}

func (e *OAuthUserDeletedError) Error() string {
	return "user has been deleted"
}

type OAuthRegistrationDisabledError struct{}

func (e *OAuthRegistrationDisabledError) Error() string {
	return "registration is disabled"
}

type OAuthEmailAlreadyTakenError struct{}

func (e *OAuthEmailAlreadyTakenError) Error() string {
	return "email is already in use"
}

// OAuthLegacyBindingNotConfirmedError reports a legacy GitHub binding match that
// neither a second factor nor a confirmed provider email backed.
type OAuthLegacyBindingNotConfirmedError struct{}

func (e *OAuthLegacyBindingNotConfirmedError) Error() string {
	return "legacy binding was not confirmed"
}

// handleOAuthError handles OAuth errors and returns a web console message
func handleOAuthError(c *gin.Context, err error) {
	switch e := err.(type) {
	case *oauth.OAuthError:
		common.ApiError(c, e.Message)
	case *oauth.AccessDeniedError:
		common.ApiErrorMsg(c, e.Message)
	case *oauth.TrustLevelError:
		common.ApiErrorT(c, "Linux DO trust level does not meet the minimum required by administrator")
	default:
		writeSecurityOperationError(c, err)
	}
}
