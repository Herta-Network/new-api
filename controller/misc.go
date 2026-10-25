package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

type testStatusResponse struct {
	Success   bool                 `json:"success"`
	Message   string               `json:"message"`
	HttpStats middleware.StatsInfo `json:"http_stats"`
}

// @Summary		检查服务状态
// @Description	检查数据库连接并返回 HTTP 统计；数据库不可用时返回 503。
// @Tags			系统信息
// @Success		200	{object}	testStatusResponse
// @Failure		503	{object}	common.ApiResponse
// @Security		AdminAuth
// @Router			/api/status/test [get]
func TestStatus(c *gin.Context) {
	err := model.PingDB()
	if err != nil {
		writeAccountStatusError(c, http.StatusServiceUnavailable, "Database connection failed")
		return
	}
	// 获取HTTP统计信息
	httpStats := middleware.GetStats()
	c.JSON(http.StatusOK, testStatusResponse{
		Success:   true,
		Message:   "Server is running",
		HttpStats: httpStats,
	})
	return
}

type siteStatus struct {
	Version                  string  `json:"version"`
	StartTime                int64   `json:"start_time"`
	EmailVerification        bool    `json:"email_verification"`
	GitHubOAuth              bool    `json:"github_oauth"`
	GitHubClientId           string  `json:"github_client_id"`
	DiscordOAuth             bool    `json:"discord_oauth"`
	DiscordClientId          string  `json:"discord_client_id"`
	LinuxDOOAuth             bool    `json:"linuxdo_oauth"`
	LinuxDOClientId          string  `json:"linuxdo_client_id"`
	LinuxDOMinimumTrustLevel int     `json:"linuxdo_minimum_trust_level"`
	TelegramOAuth            bool    `json:"telegram_oauth"`
	TelegramOAuthConfigured  bool    `json:"telegram_oauth_configured"`
	TelegramBotName          string  `json:"telegram_bot_name"`
	Theme                    string  `json:"theme"`
	SystemName               string  `json:"system_name"`
	Logo                     string  `json:"logo"`
	FooterHTML               string  `json:"footer_html"`
	WeChatQRCode             string  `json:"wechat_qrcode"`
	WeChatLogin              bool    `json:"wechat_login"`
	ServerAddress            string  `json:"server_address"`
	TurnstileCheck           bool    `json:"turnstile_check"`
	TurnstileSiteKey         string  `json:"turnstile_site_key"`
	DocsLink                 string  `json:"docs_link"`
	QuotaPerUnit             float64 `json:"quota_per_unit"`
	QuotaDisplayMode         string  `json:"quota_display_mode"`
	// Amounts are shown in the site currency at usd_rate, one USD in it;
	// usd_rate is empty while the site currency has no usable rate.
	SiteCurrency                   siteCurrencyStatus  `json:"site_currency"`
	EnableBatchUpdate              bool                `json:"enable_batch_update"`
	EnableDrawing                  bool                `json:"enable_drawing"`
	EnableTask                     bool                `json:"enable_task"`
	EnableDataExport               bool                `json:"enable_data_export"`
	DataExportDefaultTime          string              `json:"data_export_default_time"`
	DefaultCollapseSidebar         bool                `json:"default_collapse_sidebar"`
	MjNotifyEnabled                bool                `json:"mj_notify_enabled"`
	Chats                          []map[string]string `json:"chats"`
	DemoSiteEnabled                bool                `json:"demo_site_enabled"`
	SelfUseModeEnabled             bool                `json:"self_use_mode_enabled"`
	RegisterEnabled                bool                `json:"register_enabled"`
	PasswordLoginEnabled           bool                `json:"password_login_enabled"`
	PasswordRegisterEnabled        bool                `json:"password_register_enabled"`
	DefaultUseAutoGroup            bool                `json:"default_use_auto_group"`
	PasswordLoginEncryptionEnabled bool                `json:"password_login_encryption_enabled"`
	ApiInfoEnabled                 bool                `json:"api_info_enabled"`
	UptimeKumaEnabled              bool                `json:"uptime_kuma_enabled"`
	AnnouncementsEnabled           bool                `json:"announcements_enabled"`
	FAQEnabled                     bool                `json:"faq_enabled"`
	HeaderNavModules               string              `json:"HeaderNavModules"`
	SidebarModulesAdmin            string              `json:"SidebarModulesAdmin"`
	OIDCEnabled                    bool                `json:"oidc_enabled"`
	OIDCClientId                   string              `json:"oidc_client_id"`
	OIDCAuthorizationEndpoint      string              `json:"oidc_authorization_endpoint"`
	OIDCDisplayName                string              `json:"oidc_display_name"`
	PasskeyLogin                   bool                `json:"passkey_login"`
	PasskeyDisplayName             string              `json:"passkey_display_name"`
	PasskeyRPID                    string              `json:"passkey_rp_id"`
	PasskeyRPIDs                   []string            `json:"passkey_rp_ids"`
	PasskeyAllowInsecure           bool                `json:"passkey_allow_insecure"`
	PasskeyUserVerification        string              `json:"passkey_user_verification"`
	PasskeyAttachment              string              `json:"passkey_attachment"`
	Setup                          bool                `json:"setup"`
	UserAgreementEnabled           bool                `json:"user_agreement_enabled"`
	PrivacyPolicyEnabled           bool                `json:"privacy_policy_enabled"`
	CheckinEnabled                 bool                `json:"checkin_enabled"`
	LogExportEnabled               bool                `json:"log_export_enabled"`
	LogMaxRangeDays                int                 `json:"log_max_range_days"`
	QuotaRemindEnabled             bool                `json:"quota_remind_enabled"`
	QuotaRemindThreshold           int                 `json:"quota_remind_threshold"`
	// ApiInfo, Announcements and FAQ are present only while their panel is
	// enabled.
	ApiInfo       *[]map[string]any `json:"api_info,omitempty"`
	Announcements *[]map[string]any `json:"announcements,omitempty"`
	FAQ           *[]map[string]any `json:"faq,omitempty"`
	// CustomOAuthProviders are the enabled custom OAuth providers; absent
	// when there are none.
	CustomOAuthProviders []statusCustomOAuthProvider `json:"custom_oauth_providers,omitempty"`
}

type siteCurrencyStatus struct {
	Code          string `json:"code"`
	Symbol        string `json:"symbol"`
	USDRate       string `json:"usd_rate"`
	RateSource    string `json:"rate_source"`
	RateFetchedAt int64  `json:"rate_fetched_at"`
}

type statusCustomOAuthProvider struct {
	Id                    int    `json:"id"`
	Name                  string `json:"name"`
	Slug                  string `json:"slug"`
	Icon                  string `json:"icon"`
	ClientId              string `json:"client_id"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	Scopes                string `json:"scopes"`
}

// @Summary	获取站点状态
// @Tags		系统
// @Success	200	{object}	common.ApiResponse{data=siteStatus}
// @Router		/api/status [get]
func GetStatus(c *gin.Context) {

	cs := console_setting.GetConsoleSetting()
	passkeySetting := system_setting.PasskeySettingsSnapshot()
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()

	legalSetting := system_setting.GetLegalSettings()
	siteCurrency := operation_setting.GetSiteCurrencyRate()

	data := siteStatus{
		Version:                  common.Version,
		StartTime:                common.StartTime,
		EmailVerification:        common.EmailVerificationEnabled,
		GitHubOAuth:              common.GitHubOAuthEnabled,
		GitHubClientId:           common.GitHubClientId,
		DiscordOAuth:             system_setting.GetDiscordSettings().Enabled,
		DiscordClientId:          system_setting.GetDiscordSettings().ClientId,
		LinuxDOOAuth:             common.LinuxDOOAuthEnabled,
		LinuxDOClientId:          common.LinuxDOClientId,
		LinuxDOMinimumTrustLevel: common.LinuxDOMinimumTrustLevel,
		TelegramOAuth:            common.TelegramOAuthEnabled,
		TelegramOAuthConfigured:  oauth.TelegramConfigurationError() == nil,
		TelegramBotName:          common.TelegramBotName,
		Theme:                    "default",
		SystemName:               common.SystemName,
		Logo:                     common.Logo,
		FooterHTML:               common.Footer,
		WeChatQRCode:             common.WeChatAccountQRCodeImageURL,
		WeChatLogin:              common.WeChatAuthEnabled,
		ServerAddress:            system_setting.ServerAddress,
		TurnstileCheck:           common.TurnstileCheckEnabled,
		TurnstileSiteKey:         common.TurnstileSiteKey,
		DocsLink:                 operation_setting.GetGeneralSetting().DocsLink,
		QuotaPerUnit:             common.QuotaPerUnit,
		QuotaDisplayMode:         operation_setting.GetGeneralSetting().QuotaDisplayMode,
		SiteCurrency: siteCurrencyStatus{
			Code:          siteCurrency.Currency,
			Symbol:        operation_setting.CurrencySymbol(siteCurrency.Currency),
			USDRate:       siteCurrency.USDRate,
			RateSource:    siteCurrency.Source,
			RateFetchedAt: siteCurrency.FetchedAt,
		},
		EnableBatchUpdate:       common.BatchUpdateEnabled,
		EnableDrawing:           common.DrawingEnabled,
		EnableTask:              common.TaskEnabled,
		EnableDataExport:        common.DataExportEnabled,
		DataExportDefaultTime:   common.DataExportDefaultTime,
		DefaultCollapseSidebar:  common.DefaultCollapseSidebar,
		MjNotifyEnabled:         setting.MjNotifyEnabled,
		Chats:                   setting.Chats,
		DemoSiteEnabled:         operation_setting.DemoSiteEnabled,
		SelfUseModeEnabled:      operation_setting.SelfUseModeEnabled,
		RegisterEnabled:         common.RegisterEnabled,
		PasswordLoginEnabled:    common.PasswordLoginEnabled,
		PasswordRegisterEnabled: common.PasswordRegisterEnabled,
		DefaultUseAutoGroup:     setting.DefaultUseAutoGroup,

		PasswordLoginEncryptionEnabled: common.PasswordLoginEncryptionEnabled,

		// 面板启用开关
		ApiInfoEnabled:       cs.ApiInfoEnabled,
		UptimeKumaEnabled:    cs.UptimeKumaEnabled,
		AnnouncementsEnabled: cs.AnnouncementsEnabled,
		FAQEnabled:           cs.FAQEnabled,

		// 模块管理配置
		HeaderNavModules:    common.OptionMap["HeaderNavModules"],
		SidebarModulesAdmin: common.OptionMap["SidebarModulesAdmin"],

		OIDCEnabled:               system_setting.GetOIDCSettings().Enabled,
		OIDCClientId:              system_setting.GetOIDCSettings().ClientId,
		OIDCAuthorizationEndpoint: system_setting.GetOIDCSettings().AuthorizationEndpoint,
		OIDCDisplayName:           system_setting.GetOIDCSettings().GetEffectiveDisplayName(),
		PasskeyLogin:              passkeySetting.Enabled,
		PasskeyDisplayName:        passkeySetting.RPDisplayName,
		PasskeyRPID:               passkeySetting.EffectiveRPID(),
		PasskeyRPIDs:              passkeySetting.RelyingPartyIDs(),
		PasskeyAllowInsecure:      passkeySetting.AllowInsecureOrigin,
		PasskeyUserVerification:   passkeySetting.UserVerification,
		PasskeyAttachment:         passkeySetting.AttachmentPreference,
		Setup:                     constant.Setup,
		UserAgreementEnabled:      legalSetting.UserAgreement != "",
		PrivacyPolicyEnabled:      legalSetting.PrivacyPolicy != "",
		CheckinEnabled:            operation_setting.GetCheckinSetting().Enabled,
		LogExportEnabled:          operation_setting.GetLogSetting().UserExportEnabled,
		LogMaxRangeDays:           operation_setting.GetLogSetting().MaxRangeDays,
		QuotaRemindEnabled:        common.QuotaRemindEnabled,
		QuotaRemindThreshold:      common.QuotaRemindThreshold,
	}

	// 根据启用状态注入可选内容
	if cs.ApiInfoEnabled {
		apiInfo := console_setting.GetApiInfo()
		data.ApiInfo = &apiInfo
	}
	if cs.AnnouncementsEnabled {
		announcements := console_setting.GetAnnouncements()
		data.Announcements = &announcements
	}
	if cs.FAQEnabled {
		faq := console_setting.GetFAQ()
		data.FAQ = &faq
	}

	// Add enabled custom OAuth providers
	customProviders := oauth.GetEnabledCustomProviders()
	if len(customProviders) > 0 {
		providersInfo := make([]statusCustomOAuthProvider, 0, len(customProviders))
		for _, p := range customProviders {
			config := p.GetConfig()
			providersInfo = append(providersInfo, statusCustomOAuthProvider{
				Id:                    config.Id,
				Name:                  config.Name,
				Slug:                  config.Slug,
				Icon:                  config.Icon,
				ClientId:              config.ClientId,
				AuthorizationEndpoint: config.AuthorizationEndpoint,
				Scopes:                config.Scopes,
			})
		}
		data.CustomOAuthProviders = providersInfo
	}

	common.ApiSuccess(c, data)
	return
}

// @Summary		获取公告
// @Description	响应带 ETag；请求头 If-None-Match 与之相同时返回 304，无响应体。
// @Tags			系统
// @Param			If-None-Match	header		string	false	"上次响应的 ETag"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Router			/api/notice [get]
func GetNotice(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	notice := common.OptionMap["Notice"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, notice)
}

// @Summary		获取关于页内容
// @Description	响应带 ETag；请求头 If-None-Match 与之相同时返回 304，无响应体。
// @Tags			系统
// @Param			If-None-Match	header		string	false	"上次响应的 ETag"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Router			/api/about [get]
func GetAbout(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	about := common.OptionMap["About"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, about)
}

// @Summary		获取用户协议
// @Description	响应带 ETag；请求头 If-None-Match 与之相同时返回 304，无响应体。
// @Tags			系统
// @Param			If-None-Match	header		string	false	"上次响应的 ETag"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Router			/api/user-agreement [get]
func GetUserAgreement(c *gin.Context) {
	serveRevalidatedJSON(c, system_setting.GetLegalSettings().UserAgreement)
}

// @Summary		获取隐私政策
// @Description	响应带 ETag；请求头 If-None-Match 与之相同时返回 304，无响应体。
// @Tags			系统
// @Param			If-None-Match	header		string	false	"上次响应的 ETag"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Router			/api/privacy-policy [get]
func GetPrivacyPolicy(c *gin.Context) {
	serveRevalidatedJSON(c, system_setting.GetLegalSettings().PrivacyPolicy)
}

func GetMidjourney(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["Midjourney"],
	})
	return
}

// @Summary		获取首页内容
// @Description	响应带 ETag；请求头 If-None-Match 与之相同时返回 304，无响应体。
// @Tags			系统
// @Param			If-None-Match	header		string	false	"上次响应的 ETag"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Router			/api/home_page_content [get]
func GetHomePageContent(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	homePageContent := common.OptionMap["HomePageContent"]
	common.OptionMapRWMutex.RUnlock()
	serveRevalidatedJSON(c, homePageContent)
}

// @Summary		发送邮箱验证码
// @Description	向一个尚未被使用的邮箱发送注册验证码。
// @Tags			认证
// @Param			email		query		string	true	"邮箱"
// @Param			turnstile	query		string	false	"Turnstile 令牌，开启 Turnstile 时必填"
// @Success		200			{object}	common.ApiResponse
// @Router			/api/verification [get]
func SendEmailVerification(c *gin.Context) {
	email, err := service.ValidateAccountEmail(c.Query("email"))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	if model.IsEmailAlreadyTaken(email) {
		common.ApiErrorT(c, "Email address is already in use")
		return
	}
	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	// The address may not belong to an account yet, so the mail follows the
	// request language.
	err = common.SendTemplateEmail(i18n.StatedLang(c), email, common.TemplateEmail{
		Kind: common.EmailKindVerification,
		Vars: map[string]string{"Code": code, "Minutes": strconv.Itoa(common.VerificationValidMinutes)},
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
	return
}

// @Summary		发送密码重置邮件
// @Description	邮箱属于某个账户时发送重置链接；无论邮箱是否注册都返回成功。
// @Tags			认证
// @Param			email		query		string	true	"邮箱"
// @Param			turnstile	query		string	false	"Turnstile 令牌，开启 Turnstile 时必填"
// @Success		200			{object}	common.ApiResponse
// @Router			/api/reset_password [get]
func SendPasswordResetEmail(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if err := common.Validate.Var(email, "required,email"); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if user, err := model.GetUniqueUserByEmail(email); err == nil {
		code := common.GenerateVerificationCode(0)
		common.RegisterVerificationCodeWithKey(email, code, common.PasswordResetPurpose)
		// OWASP Forgot Password Cheat Sheet: the link starts from the
		// configured server address, never from the request's Host header.
		link := strings.TrimRight(system_setting.ServerAddress, "/") + "/user/reset?" + url.Values{"email": {email}, "token": {code}}.Encode()
		lang := user.GetSetting().Language
		if lang == "" {
			lang = i18n.StatedLang(c)
		}
		err := common.SendTemplateEmail(lang, email, common.TemplateEmail{
			Kind: common.EmailKindPasswordReset,
			Vars: map[string]string{"Link": link, "Minutes": strconv.Itoa(common.VerificationValidMinutes)},
		})
		if err != nil {
			logger.LogError(c.Request.Context(), common.LogText("failed to send password reset email to %s: %s", email, err.Error()))
		}
	} else if err != nil && !errors.Is(err, model.ErrEmailNotFound) {
		logger.LogWarn(c.Request.Context(), common.LogText("skip password reset email for %s: %s", email, err.Error()))
	}
	c.JSON(http.StatusOK, common.ApiResponse{Success: true})
}

type PasswordResetRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
}

// @Summary		重置密码
// @Description	校验重置邮件链接中的 email 和 token，把密码重置为随机生成的新密码，data 即新密码。
// @Tags			认证
// @Param			body	body		PasswordResetRequest	true	"重置链接中的邮箱和 token"
// @Success		200		{object}	common.ApiResponse{data=string}
// @Router			/api/user/reset [post]
func ResetPassword(c *gin.Context) {
	var req PasswordResetRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req.Email = model.NormalizeEmail(req.Email)
	if req.Email == "" || req.Token == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !common.VerifyCodeWithKey(req.Email, req.Token, common.PasswordResetPurpose) {
		common.ApiErrorT(c, "Password reset link is invalid or has expired")
		return
	}
	password := common.GenerateVerificationCode(12)
	err := model.ResetUserPasswordByEmail(req.Email, password)
	if err != nil {
		if errors.Is(err, model.ErrEmailNotFound) || errors.Is(err, model.ErrEmailAmbiguous) {
			common.ApiErrorT(c, "Password reset link is invalid or has expired")
			return
		}
		common.ApiError(c, err)
		return
	}
	common.DeleteKey(req.Email, common.PasswordResetPurpose)
	common.ApiSuccess(c, password)
	return
}
