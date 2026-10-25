package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LoginRequest struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	PasswordEncrypted string `json:"password_encrypted"`
	EncryptionKeyID   string `json:"encryption_key_id"`
}

type passwordEncryptionKey struct {
	Enabled bool `json:"enabled"`
	// Kid and PublicKey are present while password encryption is enabled.
	Kid       string `json:"kid,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

// @Summary		获取登录密码加密公钥
// @Description	开启登录密码加密时返回公钥和 kid；登录和密码验证用它加密密码，填入 password_encrypted 和 encryption_key_id。
// @Tags			认证
// @Success		200	{object}	common.ApiResponse{data=passwordEncryptionKey}
// @Router			/api/user/login/encryption-key [get]
func GetPasswordEncryptionKey(c *gin.Context) {
	if !common.PasswordLoginEncryptionEnabled {
		common.ApiSuccess(c, passwordEncryptionKey{Enabled: false})
		return
	}
	keyID, publicKey := common.PasswordEncryptionPublicKey()
	if keyID == "" || publicKey == "" {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	common.ApiSuccess(c, passwordEncryptionKey{
		Enabled:   true,
		Kid:       keyID,
		PublicKey: publicKey,
	})
}

// @Summary		密码登录
// @Description	username 可以是用户名或邮箱；开启登录密码加密时提交 password_encrypted 和 encryption_key_id 代替 password。成功时 data 为访问令牌、登录会话和用户，并写入 new_api_refresh Cookie。
// @Description	账户需要二次登录验证时 data 改为 {require_verification: true, flow_token, expires_at, methods}，再用 POST /api/user/login/verify 或 /api/user/login/passkey/* 完成登录。
// @Tags			认证
// @Param			turnstile	query		string			false	"Turnstile 令牌，开启 Turnstile 时必填"
// @Param			body		body		LoginRequest	true	"用户名或邮箱与密码"
// @Success		200			{object}	common.ApiResponse{data=loginResult}
// @Router			/api/user/login [post]
func Login(c *gin.Context) {
	if !common.PasswordLoginEnabled {
		common.ApiErrorT(c, "Password login has been disabled by administrator")
		return
	}
	var loginRequest LoginRequest
	err := common.DecodeJson(c.Request.Body, &loginRequest)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	username := loginRequest.Username
	password := loginRequest.Password
	if common.PasswordLoginEncryptionEnabled {
		if loginRequest.PasswordEncrypted == "" || loginRequest.EncryptionKeyID == "" {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		password, err = common.DecryptPassword(loginRequest.PasswordEncrypted, loginRequest.EncryptionKeyID)
		if err != nil {
			common.ApiErrorT(c, "Username or password is incorrect, or user has been banned")
			return
		}
	}
	if username == "" || password == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	user := model.User{
		Username: username,
		Password: password,
	}
	err = user.ValidateAndFill()
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDatabase):
			common.SysLog(common.LogText("Login database error for user %s: %v", username, err))
			common.ApiErrorT(c, "Database error, please contact the administrator")
		case errors.Is(err, model.ErrUserEmptyCredentials):
			common.ApiErrorT(c, "Invalid parameters")
		default:
			common.ApiErrorT(c, "Username or password is incorrect, or user has been banned")
		}
		return
	}

	setupLogin(&user, nil, c)
}

// loginMethodFromContext 根据请求路径推导登录方式，用于登录审计日志。
func loginMethodFromContext(c *gin.Context) string {
	if method := c.GetString("login_method"); method != "" {
		return method
	}
	switch c.FullPath() {
	case "/api/user/login":
		return "password"
	case "/api/user/login/2fa":
		return "2fa"
	case "/api/user/passkey/login/finish":
		return "passkey"
	case "/api/oauth/wechat":
		return "wechat"
	case "/api/oauth/telegram/login":
		return "telegram"
	case "/api/oauth/:provider":
		if provider := c.Param("provider"); provider != "" {
			return "oauth:" + provider
		}
		return "oauth"
	default:
		return "unknown"
	}
}

// recordLoginAudit 记录登录成功审计日志（对所有用户启用，仅记录成功，不记录失败）。
func recordLoginAudit(user *model.User, c *gin.Context) {
	method := loginMethodFromContext(c)
	ip := c.ClientIP()
	extra := model.AuditOther{
		LoginMethod: method,
		UserAgent:   c.Request.UserAgent(),
	}
	content := fmt.Sprintf("Logged in successfully via %s", method)
	params := map[string]any{
		"method": method,
	}
	if verifiedMethod := c.GetString("login_verification_method"); verifiedMethod != "" {
		params["verification_method"] = verifiedMethod
	}
	model.RecordLoginLog(user.Id, user.Role, user.Username, content, ip, "login", params, extra, c)
}

// setupLogin evaluates the shared login policy after primary authentication.
// Only a completed Passkey ceremony may go directly to session issuance. A
// pending legacy GitHub binding rewrite travels inside the challenge and is
// written only when the verification completes.
func setupLogin(user *model.User, migration *service.LegacyGitHubMigration, c *gin.Context) {
	challenge, err := service.StartLoginVerification(user, loginMethodFromContext(c), migration)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if challenge != nil {
		setAuthNoStore(c)
		common.ApiSuccess(c, challenge)
		return
	}
	setupLoginAtAuthVersion(user, user.AuthVersion, c)
}

func setupLoginAtAuthVersion(user *model.User, expectedAuthVersion int64, c *gin.Context) {
	if user == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled {
		common.ApiErrorT(c, "User has been banned")
		return
	}
	currentUser, err := model.GetSelfUserById(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var bundle *service.AuthBundle
	if expectedAuthVersion > 0 {
		bundle, err = service.CreateLoginSessionAtAuthVersion(
			user.Id,
			expectedAuthVersion,
			loginMethodFromContext(c),
			c.ClientIP(),
			c.Request.UserAgent(),
		)
	} else {
		bundle, err = service.CreateLoginSession(
			user.Id,
			loginMethodFromContext(c),
			c.ClientIP(),
			c.Request.UserAgent(),
		)
	}
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	writeLoginResponse(c, currentUser, bundle)
}

// loginResult is the data of a completed login or refresh: the access token,
// the login session and the signed-in user. The refresh token goes into the
// new_api_refresh cookie.
type loginResult struct {
	*service.AuthBundle
	User selfUserData `json:"user"`
}

func writeLoginResponse(c *gin.Context, user *model.User, bundle *service.AuthBundle) {
	c.Set("login_method", bundle.Session.LoginMethod)
	model.UpdateUserLastLoginAt(user.Id)
	service.WriteRefreshCookie(c, bundle.RefreshToken)
	setAuthNoStore(c)
	recordLoginAudit(user, c)
	common.ApiSuccess(c, loginResult{AuthBundle: bundle, User: buildSelfUserData(user)})
}

// @Summary		注册
// @Description	开启邮箱验证时须提交 email 和 verification_code（由 GET /api/verification 发送）；aff_code 填邀请人的邀请码。
// @Tags			认证
// @Param			turnstile	query		string		false	"Turnstile 令牌，开启 Turnstile 时必填"
// @Param			body		body		model.User	true	"username、password，可选 email、verification_code、aff_code"
// @Success		200			{object}	common.ApiResponse
// @Router			/api/user/register [post]
func Register(c *gin.Context) {
	if !common.RegisterEnabled {
		common.ApiErrorT(c, "New user registration has been disabled by administrator")
		return
	}
	if !common.PasswordRegisterEnabled {
		common.ApiErrorT(c, "Password registration has been disabled by administrator, please use third-party account verification")
		return
	}
	var user model.User
	err := common.DecodeJson(c.Request.Body, &user)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	user.Username = strings.TrimSpace(user.Username)
	user.Email = model.NormalizeEmail(user.Email)
	if user.Username == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	if common.EmailVerificationEnabled {
		if user.Email == "" || user.VerificationCode == "" {
			common.ApiErrorT(c, "Email verification is enabled, please enter email address and verification code")
			return
		}
		if !common.VerifyCodeWithKey(user.Email, user.VerificationCode, common.EmailVerificationPurpose) {
			common.ApiErrorT(c, "Verification code is incorrect or has expired")
			return
		}
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				common.ApiErrorT(c, "Email address is already in use")
				return
			}
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
	}
	emailForExistCheck := ""
	if common.EmailVerificationEnabled {
		emailForExistCheck = user.Email
	}
	exist, err := model.CheckUserExistOrDeleted(user.Username, emailForExistCheck)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		common.SysLog(common.LogText("CheckUserExistOrDeleted error: %v", err))
		return
	}
	if exist {
		common.ApiErrorT(c, "Username already exists or has been deleted")
		return
	}
	affCode := user.AffCode // this code is the inviter's code, not the user's own code
	inviterId, _ := model.GetUserIdByAffCode(affCode)
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.Username,
		InviterId:   inviterId,
		Role:        common.RoleCommonUser, // 明确设置角色为普通用户
	}
	if common.EmailVerificationEnabled {
		cleanUser.Email = user.Email
	}
	if err := cleanUser.Insert(inviterId); err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorT(c, "Email address is already in use")
			return
		}
		common.ApiError(c, err)
		return
	}

	// 获取插入后的用户ID
	var insertedUser model.User
	if err := model.DB.Where("username = ?", cleanUser.Username).First(&insertedUser).Error; err != nil {
		common.ApiErrorT(c, "User registration failed or user ID retrieval failed")
		return
	}
	// 生成默认令牌
	if constant.GenerateDefaultToken {
		key, err := common.GenerateKey()
		if err != nil {
			common.ApiErrorT(c, "Failed to generate default token")
			common.SysLog(common.LogText("failed to generate token key: %s", err.Error()))
			return
		}
		// 生成默认令牌
		token := model.Token{
			UserId:             insertedUser.Id, // 使用插入后的用户ID
			Name:               i18n.T(c, i18n.MsgUserDefaultTokenName, map[string]any{"Username": cleanUser.Username}),
			Key:                key,
			CreatedTime:        common.GetTimestamp(),
			AccessedTime:       common.GetTimestamp(),
			ExpiredTime:        -1,     // 永不过期
			RemainQuota:        500000, // 示例额度
			UnlimitedQuota:     true,
			ModelLimitsEnabled: false,
		}
		token.AllowUserQuotaQuery = true
		if setting.DefaultUseAutoGroup {
			token.Group = "auto"
		}
		if err := token.Insert(); err != nil {
			common.ApiErrorT(c, "Failed to create default token")
			return
		}
	}

	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

// @Summary	获取用户列表
// @Tags		用户管理
// @Param		p			query		int		false	"页码，从 1 开始"
// @Param		page_size	query		int		false	"每页条数"
// @Param		sort_by		query		string	false	"排序字段：id、username、quota、group、created_at、last_login_at，默认 id"
// @Param		sort_order	query		string	false	"asc 或 desc，默认 desc"
// @Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.User}}
// @Security	AdminAuth
// @Router		/api/user/ [get]
func GetAllUsers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.GetAllUsers(pageInfo, sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)

	common.ApiSuccess(c, pageInfo)
	return
}

// @Summary	搜索用户
// @Tags		用户管理
// @Param		keyword		query		string	false	"匹配用户名、邮箱、显示名，为数字时也匹配用户 ID"
// @Param		group		query		string	false	"分组"
// @Param		role		query		int		false	"角色"
// @Param		status		query		int		false	"状态，-1 表示已删除的用户"
// @Param		p			query		int		false	"页码，从 1 开始"
// @Param		page_size	query		int		false	"每页条数"
// @Param		sort_by		query		string	false	"排序字段：id、username、quota、group、created_at、last_login_at，默认 id"
// @Param		sort_order	query		string	false	"asc 或 desc，默认 desc"
// @Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.User}}
// @Security	AdminAuth
// @Router		/api/user/search [get]
func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	var role *int
	if roleStr := c.Query("role"); roleStr != "" {
		if parsed, err := strconv.Atoi(roleStr); err == nil {
			role = &parsed
		}
	}
	var status *int
	if statusStr := c.Query("status"); statusStr != "" {
		if parsed, err := strconv.Atoi(statusStr); err == nil {
			status = &parsed
		}
	}
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.SearchUsers(keyword, group, role, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)
	common.ApiSuccess(c, pageInfo)
	return
}

func canManageTargetRole(myRole int, targetRole int) bool {
	return myRole == common.RoleRootUser || myRole > targetRole
}

// @Summary		获取用户
// @Description	只能查看角色低于自己的用户，超级管理员不受此限；admin_permissions 为该用户的后台权限。
// @Tags			用户管理
// @Param			id	path		int	true	"用户 ID"
// @Success		200	{object}	common.ApiResponse{data=model.User}
// @Security		AdminAuth
// @Router			/api/user/{id} [get]
func GetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return
	}
	user.AdminPermissions = authz.Capabilities(user.Id, user.Role)
	common.ApiSuccess(c, user)
	return
}

type TransferAffQuotaRequest struct {
	Quota int `json:"quota" binding:"required"`
}

// @Summary		邀请额度转入余额
// @Description	需要管理员已确认支付合规。
// @Tags			个人
// @Param			body	body		TransferAffQuotaRequest	true	"转入的额度"
// @Success		200		{object}	common.ApiResponse
// @Security		UserAuth
// @Router			/api/user/aff_transfer [post]
func TransferAffQuota(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tran := TransferAffQuotaRequest{}
	if err := c.ShouldBindJSON(&tran); err != nil {
		common.ApiError(c, err)
		return
	}
	err = user.TransferAffQuotaToQuota(tran.Quota)
	var message *common.Message
	if errors.As(err, &message) {
		common.ApiError(c, err)
		return
	}
	if err != nil {
		common.ApiErrorT(c, "Transfer failed {{error}}", map[string]any{"error": err.Error()})
		return
	}
	common.ApiSuccessT(c, "Transfer successful", nil)
}

// @Summary		获取邀请码
// @Description	还没有邀请码时先生成一个。
// @Tags			个人
// @Success		200	{object}	common.ApiResponse{data=string}
// @Security		UserAuth
// @Router			/api/user/aff [get]
func GetAffCode(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.AffCode == "" {
		user.AffCode = common.GetRandomString(4)
		if err := user.Update(false); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, user.AffCode)
	return
}

// @Summary	获取当前用户
// @Tags		个人
// @Success	200	{object}	common.ApiResponse{data=selfUserData}
// @Security	UserAuth
// @Router		/api/user/self [get]
func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	userRole := c.GetInt("role")
	user, err := model.GetSelfUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responseData := buildSelfUserData(user)
	// The authenticated role is loaded from GetUserCache. It should equal the
	// row role, but use it for capabilities so GetSelf and login/refresh remain
	// consistent with the authorization decision made for this request.
	responseData.Permissions = calculateUserPermissions(id, userRole)

	common.ApiSuccess(c, responseData)
	return
}

// selfUserData is the single safe dashboard-user DTO used by GetSelf, login
// and refresh. It intentionally excludes password, management PAT and
// administrator-only remarks.
type selfUserData struct {
	Id              int             `json:"id"`
	Username        string          `json:"username"`
	DisplayName     string          `json:"display_name"`
	HasPassword     bool            `json:"has_password"`
	Role            int             `json:"role"`
	Status          int             `json:"status"`
	Email           string          `json:"email"`
	GitHubId        string          `json:"github_id"`
	DiscordId       string          `json:"discord_id"`
	OidcId          string          `json:"oidc_id"`
	WeChatId        string          `json:"wechat_id"`
	TelegramId      string          `json:"telegram_id"`
	Group           string          `json:"group"`
	Quota           int             `json:"quota"`
	UsedQuota       int             `json:"used_quota"`
	RequestCount    int             `json:"request_count"`
	AffCode         string          `json:"aff_code"`
	AffCount        int             `json:"aff_count"`
	AffQuota        int             `json:"aff_quota"`
	AffHistoryQuota int             `json:"aff_history_quota"`
	InviterId       int             `json:"inviter_id"`
	LinuxDOId       string          `json:"linux_do_id"`
	Setting         string          `json:"setting"`
	SidebarModules  string          `json:"sidebar_modules"`
	Permissions     userPermissions `json:"permissions"`
}

func buildSelfUserData(user *model.User) selfUserData {
	userSetting := user.GetSetting()
	return selfUserData{
		Id:              user.Id,
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		HasPassword:     user.HasPassword,
		Role:            user.Role,
		Status:          user.Status,
		Email:           user.Email,
		GitHubId:        user.GitHubId,
		DiscordId:       user.DiscordId,
		OidcId:          user.OidcId,
		WeChatId:        user.WeChatId,
		TelegramId:      user.TelegramId,
		Group:           user.Group,
		Quota:           user.Quota,
		UsedQuota:       user.UsedQuota,
		RequestCount:    user.RequestCount,
		AffCode:         user.AffCode,
		AffCount:        user.AffCount,
		AffQuota:        user.AffQuota,
		AffHistoryQuota: user.AffHistoryQuota,
		InviterId:       user.InviterId,
		LinuxDOId:       user.LinuxDOId,
		Setting:         user.Setting,
		SidebarModules:  userSetting.SidebarModules, // 正确提取sidebar_modules字段
		Permissions:     calculateUserPermissions(user.Id, user.Role),
	}
}

type userPermissions struct {
	SidebarSettings bool `json:"sidebar_settings"`
	// SidebarModules marks the sidebar areas the role cannot use.
	SidebarModules   map[string]any       `json:"sidebar_modules"`
	AdminPermissions authz.PermissionsMap `json:"admin_permissions"`
}

// 计算用户权限的辅助函数
func calculateUserPermissions(userID int, userRole int) userPermissions {
	permissions := userPermissions{AdminPermissions: authz.Capabilities(userID, userRole)}

	// 根据用户角色计算权限
	if userRole == common.RoleRootUser {
		// 超级管理员不需要边栏设置功能
		permissions.SidebarSettings = false
		permissions.SidebarModules = map[string]any{}
	} else if userRole == common.RoleAdminUser {
		// 管理员可以设置边栏，但不包含系统设置功能
		permissions.SidebarSettings = true
		permissions.SidebarModules = map[string]any{
			"admin": map[string]any{
				"setting": false, // 管理员不能访问系统设置
			},
		}
	} else {
		// 普通用户只能设置个人功能，不包含管理员区域
		permissions.SidebarSettings = true
		permissions.SidebarModules = map[string]any{
			"admin": false, // 普通用户不能访问管理员区域
		}
	}

	return permissions
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfig(userRole int) string {
	defaultConfig := map[string]any{}

	// 聊天区域 - 所有用户都可以访问
	defaultConfig["chat"] = map[string]any{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	// 控制台区域 - 所有用户都可以访问
	defaultConfig["console"] = map[string]any{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	// 个人中心区域 - 所有用户都可以访问
	defaultConfig["personal"] = map[string]any{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	// 管理员区域 - 根据角色决定
	if userRole == common.RoleAdminUser {
		// 管理员可以访问管理员区域，但不能访问系统设置
		defaultConfig["admin"] = map[string]any{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false, // 管理员不能访问系统设置
		}
	} else if userRole == common.RoleRootUser {
		// 超级管理员可以访问所有功能
		defaultConfig["admin"] = map[string]any{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}
	// 普通用户不包含admin区域

	// 转换为JSON字符串
	configBytes, err := common.Marshal(defaultConfig)
	if err != nil {
		common.SysLog(common.LogText("failed to generate the default sidebar config: %s", err.Error()))
		return ""
	}

	return string(configBytes)
}

// @Summary	获取可用模型
// @Tags		个人
// @Param		group	query		string	false	"分组；不填时返回所有可用分组的模型，auto 为自动分组"
// @Success	200		{object}	common.ApiResponse{data=[]string}
// @Security	UserAuth
// @Router		/api/user/models [get]
func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	user, err := model.GetUserCache(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := service.GetUserUsableGroups(user.Group)
	group := c.Query("group")
	var groupsToQuery []string
	switch {
	case group == "":
		for g := range groups {
			groupsToQuery = append(groupsToQuery, g)
		}
	case group == "auto":
		if _, ok := groups[group]; ok {
			groupsToQuery = service.GetUserAutoGroup(user.Group)
		}
	default:
		if _, ok := groups[group]; ok {
			groupsToQuery = []string{group}
		}
	}
	common.ApiSuccess(c, model.UsableModelNames(groupsToQuery, model.AcceptsUnpricedModels(user.Id)))
}

// @Summary		更新用户
// @Description	更新 username、display_name、group、remark，password 非空时重置密码，角色不能在这里修改；admin_permissions 只有超级管理员可以提交。重置密码或提交 admin_permissions 时需要 scope 为 admin.user.update 的 X-Security-Proof。只能修改角色低于自己的用户，超级管理员不受此限。
// @Tags			用户管理
// @Param			body	body		model.User	true	"用户，id 必填"
// @Success		200		{object}	common.ApiResponse
// @Security		AdminAuth
// @Security		AdminAuth && SecurityProof
// @Router			/api/user/ [put]
func UpdateUser(c *gin.Context) {
	var updatedUser model.User
	err := common.DecodeJson(c.Request.Body, &updatedUser)
	if err != nil || updatedUser.Id == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	updatedUser.Username = strings.TrimSpace(updatedUser.Username)
	if updatedUser.Username == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.StructExcept(&updatedUser, "Password"); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	originUser, err := model.GetUserById(updatedUser.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if updatedUser.Role != common.RoleGuestUser && updatedUser.Role != originUser.Role {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	updatedUser.Role = originUser.Role
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, originUser.Role) {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	updatePassword := updatedUser.Password != ""
	auditParams := map[string]any{
		"username": originUser.Username,
		"id":       updatedUser.Id,
	}
	// Resetting a password or rewriting the admin permission matrix changes what
	// the managed account can do or who can sign in to it.
	if updatePassword || updatedUser.AdminPermissions != nil {
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserUpdate, service.AdminUserContext{UserID: updatedUser.Id})
		if authorization == nil {
			return
		}
		auditParams["verification_method"] = authorization.Method
		auditParams["password_reset"] = updatePassword
		auditParams["admin_permissions_updated"] = updatedUser.AdminPermissions != nil
	}
	authzTouched := false
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := updatedUser.EditWithTx(tx, updatePassword); err != nil {
			return err
		}
		touched, err := updateAdminPermissionsForUserInTx(c, tx, updatedUser.Id, originUser.Role, updatedUser.AdminPermissions)
		authzTouched = touched
		return err
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if updatedUser.AuthVersion > originUser.AuthVersion {
		if _, err := model.RevokeAllUserSessions(updatedUser.Id, "admin_user_update"); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if err := model.PublishUserAuthCache(updatedUser.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, updatedUser.Id, "user.update", auditParams)
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

// adminBindingClearResponse is the body of a cleared binding, whose message
// is "success".
type adminBindingClearResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// @Summary		清除用户绑定
// @Description	需要 scope 为 admin.user.binding.clear 的 X-Security-Proof，其 context 为 {user_id, binding_type}；只能操作角色低于自己的用户，超级管理员不受此限。
// @Tags			用户管理
// @Param			id				path		int		true	"用户 ID"
// @Param			binding_type	path		string	true	"绑定类型：email、github、discord、oidc、wechat、telegram、linuxdo"
// @Success		200				{object}	adminBindingClearResponse
// @Security		AdminAuth && SecurityProof
// @Router			/api/user/{id}/bindings/{binding_type} [delete]
func AdminClearUserBinding(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	bindingType := strings.ToLower(strings.TrimSpace(c.Param("binding_type")))
	if bindingType == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserBindingClear, service.AdminUserBindingContext{UserID: user.Id, BindingType: bindingType})
	if authorization == nil {
		return
	}

	if err := user.ClearBinding(bindingType); err != nil {
		common.ApiError(c, err)
		return
	}

	recordManageAuditFor(c, user.Id, "user.binding_clear", map[string]any{
		"bindingType":         bindingType,
		"username":            user.Username,
		"verification_method": authorization.Method,
	})

	c.JSON(http.StatusOK, adminBindingClearResponse{
		Success: true,
		Message: "success",
	})
}

// passwordChangeResult rotates the current login session after a password
// change; the session fields are absent when the change came through an
// access token, which has no browser session.
type passwordChangeResult struct {
	*service.AuthBundle
	HasPassword         bool `json:"has_password"`
	NotificationWarning bool `json:"notification_warning"`
}

// @Summary		更新个人资料
// @Description	请求体有 sidebar_modules 或 language 且不改密码时只保存该项设置；否则更新 username、display_name，password 非空时修改密码，需要 original_password 和 X-Security-Proof（scope 为 account.password.change，首次设置密码为 account.password.set）。
// @Description	只有修改密码时返回 data：替换当前会话的访问令牌和会话（经个人访问令牌修改时没有）、has_password 和 notification_warning（安全通知邮件是否发送失败）。
// @Tags			个人
// @Param			body	body		object	true	"username、display_name、password、original_password，或 sidebar_modules、language"
// @Success		200		{object}	common.ApiResponse{data=passwordChangeResult}
// @Security		UserAuth
// @Security		UserAuth && SecurityProof
// @Router			/api/user/self [put]
func UpdateSelf(c *gin.Context) {
	var requestData map[string]any
	if err := common.DecodeJson(c.Request.Body, &requestData); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	passwordRequested := false
	if value, exists := requestData["password"]; exists && value != nil {
		password, isString := value.(string)
		passwordRequested = !isString || password != ""
	}
	succeeded, notificationFailed := false, false
	if passwordRequested {
		defer func() {
			recordUserSecurityAudit(c, c.GetInt("id"), "user.password_change", map[string]any{"success": succeeded, "notification_failed": notificationFailed})
		}()
	}
	// 检查是否是用户设置更新请求 (sidebar_modules 或 language)
	if sidebarModules, sidebarExists := requestData["sidebar_modules"]; sidebarExists && !passwordRequested {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新sidebar_modules字段
		if sidebarModulesStr, ok := sidebarModules.(string); ok {
			currentSetting.SidebarModules = sidebarModulesStr
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorT(c, "Update failed")
			return
		}

		common.ApiSuccessT(c, "Update successful", nil)
		return
	}

	// 检查是否是语言偏好更新请求
	if language, langExists := requestData["language"]; langExists && !passwordRequested {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新language字段
		if langStr, ok := language.(string); ok {
			currentSetting.Language = langStr
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorT(c, "Update failed")
			return
		}

		common.ApiSuccessT(c, "Update successful", nil)
		return
	}

	// 原有的用户信息更新逻辑
	var user model.User
	requestDataBytes, err := common.Marshal(requestData)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err = common.Unmarshal(requestDataBytes, &user); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	if err := common.Validate.StructExcept(&user, "Password"); err != nil {
		common.ApiErrorT(c, "Invalid input")
		return
	}

	cleanUser := model.User{
		Id:          c.GetInt("id"),
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
	}
	if user.Password != "" {
		identity, ok := middleware.GetStepUpIdentity(c)
		if !ok {
			writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
			return
		}
		current, err := model.GetUserById(identity.UserID, true)
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		firstPassword := current.Password == ""
		scope := service.VerificationScopePasswordChange
		if firstPassword {
			scope = service.VerificationScopePasswordSet
		}
		if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: scope}) == nil {
			return
		}
		cleanUser.OriginalPassword = user.OriginalPassword
		if err := model.ChangeUserPassword(identity, &cleanUser, firstPassword); err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		succeeded = true
		notificationFailed = service.NotifyAccountSecurityChange(service.AccountMailLanguage(c, current.Id), current.Email, i18n.MsgSecurityEventPasswordUpdated) != nil
		if err := model.PublishUserAuthCache(cleanUser.Id); err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		bundle, err := service.AdvanceCurrentSessionToUserVersion(identity, "password_changed")
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		common.ApiSuccess(c, passwordChangeResult{
			AuthBundle:          bundle,
			HasPassword:         true,
			NotificationWarning: notificationFailed,
		})
		return
	}
	if err := cleanUser.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

// @Summary		删除用户
// @Description	永久删除用户并撤销其个人访问令牌；需要 scope 为 admin.user.delete 的 X-Security-Proof，只能删除角色低于自己的用户。
// @Tags			用户管理
// @Param			id	path		int	true	"用户 ID"
// @Success		200	{object}	common.ApiResponse
// @Security		AdminAuth && SecurityProof
// @Router			/api/user/{id} [delete]
func DeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	originUser, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserDelete, service.AdminUserContext{UserID: originUser.Id})
	if authorization == nil {
		return
	}
	revokedAccessTokens, err := model.HardDeleteUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, originUser.Id, "user.delete", map[string]any{
		"username":              originUser.Username,
		"id":                    originUser.Id,
		"verification_method":   authorization.Method,
		"revoked_access_tokens": revokedAccessTokens,
	})
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

// @Summary		注销账户
// @Description	删除当前账户并清除 Refresh Cookie，data 为空对象；需要 scope 为 account.delete 的 X-Security-Proof，超级管理员不能注销。
// @Tags			个人
// @Success		200	{object}	common.ApiResponse{data=object}
// @Security		UserAuth && SecurityProof
// @Router			/api/user/self [delete]
func DeleteSelf(c *gin.Context) {
	setAuthNoStore(c)
	succeeded := false
	var revokedAccessTokens int64
	defer func() {
		params := map[string]any{"success": succeeded}
		if succeeded {
			params["revoked_access_tokens"] = revokedAccessTokens
		}
		recordUserSecurityAudit(c, c.GetInt("id"), "user.account_delete", params)
	}()
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountDelete}) == nil {
		return
	}
	identity, _ := middleware.GetStepUpIdentity(c)
	var err error
	if revokedAccessTokens, err = model.DeleteUserForSession(identity); err != nil {
		if errors.Is(err, model.ErrCannotDeleteRootUser) {
			common.ApiErrorT(c, "Cannot delete super administrator account")
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	service.ClearRefreshCookie(c)
	common.ApiSuccess(c, struct{}{})
}

// @Summary		创建用户
// @Description	只能创建角色低于自己的用户；创建管理员时需要 scope 为 admin.user.create 的 X-Security-Proof；admin_permissions 只有超级管理员可以提交。
// @Tags			用户管理
// @Param			body	body		model.User	true	"username、password、display_name、role，可选 admin_permissions"
// @Success		200		{object}	common.ApiResponse
// @Security		AdminAuth
// @Security		AdminAuth && SecurityProof
// @Router			/api/user/ [post]
func CreateUser(c *gin.Context) {
	var user model.User
	err := common.DecodeJson(c.Request.Body, &user)
	user.Username = strings.TrimSpace(user.Username)
	if err != nil || user.Username == "" || user.Password == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorT(c, "Invalid input {{error}}", map[string]any{"error": err.Error()})
		return
	}
	if !common.IsValidateRole(user.Role) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	myRole := c.GetInt("role")
	if user.Role >= myRole {
		common.ApiErrorT(c, "Cannot create users with permission level equal to or higher than yourself")
		return
	}
	auditParams := map[string]any{"role": user.Role}
	// Creating an administrator grants privilege just like promoting one.
	if user.Role >= common.RoleAdminUser {
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserCreate, service.AdminUserCreateContext{Role: user.Role})
		if authorization == nil {
			return
		}
		auditParams["verification_method"] = authorization.Method
	}
	// Even for admin users, we cannot fully trust them!
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
		Role:        user.Role, // 保持管理员设置的角色
	}
	authzTouched := false
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := cleanUser.InsertWithTx(tx, 0); err != nil {
			return err
		}
		touched, err := updateAdminPermissionsForUserInTx(c, tx, cleanUser.Id, cleanUser.Role, user.AdminPermissions)
		authzTouched = touched
		return err
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	cleanUser.FinishInsert(0)

	auditParams["username"] = cleanUser.Username
	recordManageAuditFor(c, cleanUser.Id, "user.create", auditParams)
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

func updateAdminPermissionsForUserInTx(c *gin.Context, tx *gorm.DB, userID int, userRole int, permissions map[string]map[string]bool) (bool, error) {
	if permissions == nil {
		if userRole < common.RoleAdminUser && c.GetInt("role") == common.RoleRootUser {
			return true, authz.ClearUserAuthorizationInTx(tx, userID)
		}
		return false, nil
	}
	if c.GetInt("role") != common.RoleRootUser {
		return false, fmt.Errorf("only root can update admin permissions")
	}
	if userRole < common.RoleAdminUser {
		return true, authz.ClearUserAuthorizationInTx(tx, userID)
	}
	return true, authz.SetUserPermissionsInTx(tx, userID, permissions)
}

type ManageRequest struct {
	Id     int    `json:"id"`
	Action string `json:"action"`
	Value  int    `json:"value"`
	Mode   string `json:"mode"`
}

// ManageUser Only admin user can do this
//
//	@Summary		管理用户
//	@Description	action 为 disable、enable、promote、demote、delete 或 add_quota；只能操作角色低于自己的用户（超级管理员不受此限），promote 仅限超级管理员。除 add_quota 外都需要 X-Security-Proof：delete 的 scope 为 admin.user.delete，其余为 admin.user.manage。
//	@Description	add_quota 按 mode（add、subtract、override）和 value 调整额度。delete 和 add_quota 不返回 data，其余操作的 data 只有用户新的 role 和 status。
//	@Tags			用户管理
//	@Param			body	body		ManageRequest	true	"用户 ID 和操作"
//	@Success		200		{object}	common.ApiResponse{data=model.User}
//	@Security		AdminAuth
//	@Security		AdminAuth && SecurityProof
//	@Router			/api/user/manage [post]
func ManageUser(c *gin.Context) {
	var req ManageRequest
	err := common.DecodeJson(c.Request.Body, &req)

	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if req.Action == "add_quota" {
		manageUserQuota(c, req)
		return
	}
	user := model.User{
		Id: req.Id,
	}
	// Fill attributes
	model.DB.Unscoped().Where(&user).First(&user)
	if user.Id == 0 {
		common.ApiErrorT(c, "User does not exist")
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		return
	}
	switch req.Action {
	case "disable":
		user.Status = common.UserStatusDisabled
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot disable super administrator user")
			return
		}
	case "enable":
		user.Status = common.UserStatusEnabled
	case "delete":
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot delete super administrator account")
			return
		}
		authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserDelete, service.AdminUserContext{UserID: user.Id})
		if authorization == nil {
			return
		}
		revokedAccessTokens, err := user.Delete()
		if err != nil {
			common.ApiError(c, err)
			return
		}
		// 删除用户后，强制清理 Redis 中所有该用户令牌的缓存，
		// 避免已缓存的令牌在 TTL 过期前仍能通过 TokenAuth 校验。
		if err := model.InvalidateUserTokensCache(user.Id); err != nil {
			common.SysLog(common.LogText("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
		}
		recordManageAuditFor(c, user.Id, "user.manage", map[string]any{
			"action":                req.Action,
			"username":              user.Username,
			"id":                    user.Id,
			"verification_method":   authorization.Method,
			"revoked_access_tokens": revokedAccessTokens,
		})
		c.JSON(http.StatusOK, common.ApiResponse{Success: true})
		return
	case "promote":
		if myRole != common.RoleRootUser {
			common.ApiErrorT(c, "Regular administrators cannot promote other users to administrator")
			return
		}
		if user.Role >= common.RoleAdminUser {
			common.ApiErrorT(c, "This user is already an administrator")
			return
		}
		user.Role = common.RoleAdminUser
	case "demote":
		if user.Role == common.RoleRootUser {
			common.ApiErrorT(c, "Cannot demote super administrator user")
			return
		}
		if user.Role == common.RoleCommonUser {
			common.ApiErrorT(c, "This user is already a common user")
			return
		}
		user.Role = common.RoleCommonUser
	default:
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserManage, service.AdminUserManageContext{UserID: user.Id, Action: req.Action})
	if authorization == nil {
		return
	}
	if req.Action == "demote" {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := user.UpdateWithTx(tx, false); err != nil {
				return err
			}
			return authz.ClearUserAuthorizationInTx(tx, user.Id)
		}); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := authz.ReloadPolicy(); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.PublishUserAuthCache(user.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		if _, err := model.RevokeAllUserSessions(user.Id, "admin_demote"); err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		if err := user.Update(false); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	// Update/UpdateWithTx has already published the new user hash and revoked
	// browser sessions exactly once. Only PAT/relay token caches still need an
	// explicit invalidation; deleting the user hash here would discard the
	// freshly published auth-version floor.
	if err := model.InvalidateUserTokensCache(user.Id); err != nil {
		common.SysLog(common.LogText("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
	}
	recordManageAuditFor(c, user.Id, "user.manage", map[string]any{
		"action":              req.Action,
		"username":            user.Username,
		"id":                  user.Id,
		"verification_method": authorization.Method,
	})
	clearUser := model.User{
		Role:   user.Role,
		Status: user.Status,
	}
	common.ApiSuccess(c, clearUser)
	return
}

type topUpRequest struct {
	Key string `json:"key"`
}

var topUpLocks sync.Map
var topUpCreateLock sync.Mutex

type topUpTryLock struct {
	ch chan struct{}
}

func newTopUpTryLock() *topUpTryLock {
	return &topUpTryLock{ch: make(chan struct{}, 1)}
}

func (l *topUpTryLock) TryLock() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *topUpTryLock) Unlock() {
	select {
	case <-l.ch:
	default:
	}
}

func getTopUpLock(userID int) *topUpTryLock {
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	topUpCreateLock.Lock()
	defer topUpCreateLock.Unlock()
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	l := newTopUpTryLock()
	topUpLocks.Store(userID, l)
	return l
}

type selfTopUpInfo struct {
	EnableRedemption bool   `json:"enable_redemption"`
	TopUpLink        string `json:"topup_link"`
}

// GetTopUpInfo tells the wallet whether redemption codes can be used and
// where to buy them.
//
//	@Summary	获取兑换码充值信息
//	@Tags		个人
//	@Success	200	{object}	common.ApiResponse{data=selfTopUpInfo}
//	@Security	UserAuth
//	@Router		/api/user/topup/info [get]
func GetTopUpInfo(c *gin.Context) {
	common.ApiSuccess(c, selfTopUpInfo{
		EnableRedemption: operation_setting.IsPaymentComplianceConfirmed(),
		TopUpLink:        common.TopUpLink,
	})
}

// @Summary		兑换码充值
// @Description	需要管理员已确认支付合规；data 为到账额度。
// @Tags			个人
// @Param			body	body		topUpRequest	true	"兑换码"
// @Success		200		{object}	common.ApiResponse{data=int}
// @Security		UserAuth
// @Router			/api/user/topup [post]
func TopUp(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}

	id := c.GetInt("id")
	lock := getTopUpLock(id)
	if !lock.TryLock() {
		common.ApiErrorT(c, "Top-up is processing, please try again later")
		return
	}
	defer lock.Unlock()
	req := topUpRequest{}
	err := c.ShouldBindJSON(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	quota, err := model.Redeem(req.Key, id)
	if err != nil {
		// 不向用户暴露兑换失败的细分原因，避免攻击者根据错误类型判断兑换码状态。
		common.ApiErrorT(c, "Redemption failed, please try again later")
		logger.LogError(c, common.LogText("failed to redeem key %s for user %d: %s", req.Key, id, err.Error()))
		return
	}
	common.ApiSuccess(c, quota)
}

type UpdateUserSettingRequest struct {
	QuotaWarningType                 string  `json:"notify_type"`
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold"`
	QuotaWarningEnabled              *bool   `json:"quota_warning_enabled,omitempty"`
	WebhookUrl                       string  `json:"webhook_url,omitempty"`
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`
	NotificationEmail                string  `json:"notification_email,omitempty"`
	BarkUrl                          string  `json:"bark_url,omitempty"`
	GotifyUrl                        string  `json:"gotify_url,omitempty"`
	GotifyToken                      string  `json:"gotify_token,omitempty"`
	GotifyPriority                   int     `json:"gotify_priority,omitempty"`
	UpstreamModelUpdateNotifyEnabled *bool   `json:"upstream_model_update_notify_enabled,omitempty"`
	ChannelNotifyEnabled             *bool   `json:"channel_notify_enabled,omitempty"`
	AcceptUnsetModelRatioModel       bool    `json:"accept_unset_model_ratio_model"`
	RecordIpLog                      bool    `json:"record_ip_log"`
}

// @Summary		更新通知设置
// @Description	notify_type 为 email、webhook、bark 或 gotify，只保存所选通知方式的配置；upstream_model_update_notify_enabled 和 channel_notify_enabled 只对管理员生效。
// @Tags			个人
// @Param			body	body		UpdateUserSettingRequest	true	"通知方式和额度预警设置"
// @Success		200		{object}	common.ApiResponse
// @Security		UserAuth
// @Router			/api/user/setting [put]
func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	// 验证预警类型
	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		common.ApiErrorT(c, "Invalid warning type")
		return
	}

	// 验证预警阈值，0 表示使用系统默认阈值
	if req.QuotaWarningThreshold < 0 {
		common.ApiErrorT(c, "Reminder threshold cannot be negative")
		return
	}

	// 如果是webhook类型,验证webhook地址
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			common.ApiErrorT(c, "Webhook URL cannot be empty")
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.WebhookUrl); err != nil {
			common.ApiErrorT(c, "Invalid Webhook URL")
			return
		}
	}

	// 如果是邮件类型，验证邮箱地址
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		// 验证邮箱格式
		if !strings.Contains(req.NotificationEmail, "@") {
			common.ApiErrorT(c, "Invalid email address")
			return
		}
	}

	// 如果是Bark类型，验证Bark URL
	if req.QuotaWarningType == dto.NotifyTypeBark {
		if req.BarkUrl == "" {
			common.ApiErrorT(c, "Bark push URL cannot be empty")
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.BarkUrl); err != nil {
			common.ApiErrorT(c, "Invalid Bark push URL")
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.BarkUrl, "https://") && !strings.HasPrefix(req.BarkUrl, "http://") {
			common.ApiErrorT(c, "URL must start with http:// or https://")
			return
		}
	}

	// 如果是Gotify类型，验证Gotify URL和Token
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			common.ApiErrorT(c, "Gotify server URL cannot be empty")
			return
		}
		if req.GotifyToken == "" {
			common.ApiErrorT(c, "Gotify token cannot be empty")
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.GotifyUrl); err != nil {
			common.ApiErrorT(c, "Invalid Gotify server URL")
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.GotifyUrl, "https://") && !strings.HasPrefix(req.GotifyUrl, "http://") {
			common.ApiErrorT(c, "URL must start with http:// or https://")
			return
		}
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 从已保存的设置出发，语言、侧边栏、扣费策略等不由此表单管理的字段保持不变；
	// 请求未携带的开关也保持原值
	settings := user.GetSetting()
	settings.NotifyType = req.QuotaWarningType
	settings.QuotaWarningThreshold = req.QuotaWarningThreshold
	settings.AcceptUnsetRatioModel = req.AcceptUnsetModelRatioModel
	settings.RecordIpLog = req.RecordIpLog
	if req.QuotaWarningEnabled != nil {
		settings.QuotaWarningEnabled = req.QuotaWarningEnabled
	}
	if user.Role >= common.RoleAdminUser {
		if req.UpstreamModelUpdateNotifyEnabled != nil {
			settings.UpstreamModelUpdateNotifyEnabled = *req.UpstreamModelUpdateNotifyEnabled
		}
		if req.ChannelNotifyEnabled != nil {
			settings.ChannelNotifyEnabled = req.ChannelNotifyEnabled
		}
	}

	// 只保留所选通知方式的配置，其他方式的配置清空
	settings.WebhookUrl, settings.WebhookSecret = "", ""
	settings.NotificationEmail = ""
	settings.BarkUrl = ""
	settings.GotifyUrl, settings.GotifyToken, settings.GotifyPriority = "", "", 0
	switch req.QuotaWarningType {
	case dto.NotifyTypeWebhook:
		settings.WebhookUrl = req.WebhookUrl
		settings.WebhookSecret = req.WebhookSecret
	case dto.NotifyTypeEmail:
		settings.NotificationEmail = req.NotificationEmail
	case dto.NotifyTypeBark:
		settings.BarkUrl = req.BarkUrl
	case dto.NotifyTypeGotify:
		settings.GotifyUrl = req.GotifyUrl
		settings.GotifyToken = req.GotifyToken
		// Gotify优先级范围0-10，超出范围则使用默认值5
		if req.GotifyPriority < 0 || req.GotifyPriority > 10 {
			settings.GotifyPriority = 5
		} else {
			settings.GotifyPriority = req.GotifyPriority
		}
	}

	// 更新用户设置
	if err := model.UpdateUserSetting(user.Id, settings); err != nil {
		common.ApiErrorT(c, "Update failed")
		return
	}

	common.ApiSuccessT(c, "Settings updated", nil)
}
