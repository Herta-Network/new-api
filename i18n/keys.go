package i18n

// Message keys of i18n/locales/*.yaml, for text the backend translates itself:
// errors returned to AI clients, emails and notifications. Web console messages
// are not listed here; the web console translates them (see common.Message).

// Errors returned to AI clients
const (
	MsgDatabaseError          = "common.database_error"
	MsgAuthUserBanned         = "auth.user_banned"
	MsgTokenGetInfoFailed     = "token.get_info_failed"
	MsgTokenInvalid           = "token.invalid"
	MsgTokenNotProvided       = "token.not_provided"
	MsgTokenStatusUnavailable = "token.status_unavailable"

	MsgAuthClientIPUnresolved     = "auth.client_ip_unresolved"
	MsgAuthTokenIPNotAllowed      = "auth.token_ip_not_allowed"
	MsgAuthTokenGroupForbidden    = "auth.token_group_forbidden"
	MsgAuthTokenGroupDeprecated   = "auth.token_group_deprecated"
	MsgAuthChannelSelectForbidden = "auth.channel_select_forbidden"

	MsgRateLimitReached      = "rate_limit.reached"
	MsgRateLimitTotalReached = "rate_limit.total_reached"

	MsgDistributorInvalidRequest               = "distributor.invalid_request"
	MsgDistributorInvalidChannelId             = "distributor.invalid_channel_id"
	MsgDistributorChannelDisabled              = "distributor.channel_disabled"
	MsgDistributorTokenNoModelAccess           = "distributor.token_no_model_access"
	MsgDistributorTokenModelForbidden          = "distributor.token_model_forbidden"
	MsgDistributorModelNameRequired            = "distributor.model_name_required"
	MsgDistributorInvalidPlayground            = "distributor.invalid_playground_request"
	MsgDistributorGroupAccessDenied            = "distributor.group_access_denied"
	MsgDistributorGetChannelFailed             = "distributor.get_channel_failed"
	MsgDistributorNoAvailableChannel           = "distributor.no_available_channel"
	MsgDistributorNoAvailableChannelTaskPlugin = "distributor.no_available_channel_task_plugin"
	MsgDistributorInvalidParseModel            = "distributor.invalid_request_parse_model"

	MsgQuotaUserInsufficient             = "quota.user_insufficient"
	MsgQuotaPreConsumeInsufficient       = "quota.pre_consume_insufficient"
	MsgQuotaSubscriptionInsufficient     = "quota.subscription_insufficient"
	MsgRelayModelPriceNotConfigured      = "relay.model_price_not_configured"
	MsgRelayModelPriceNotConfiguredAdmin = "relay.model_price_not_configured_admin"
	MsgRelayTaskUsagePriceUnsupported    = "relay.task_usage_price_unsupported"
	MsgRelaySharedTaskPriceNeedsOverride = "relay.shared_task_price_needs_override"
	MsgRelayTaskChannelDisabled          = "relay.task_channel_disabled"
	MsgRelayGroupSaturated               = "relay.group_saturated"
	MsgRelayGroupUpstreamSaturated       = "relay.group_upstream_saturated"
	MsgRelayRetryGetChannelFailed        = "relay.retry_get_channel_failed"
	MsgRelayRetryNoAvailableChannel      = "relay.retry_no_available_channel"
	MsgRelayAccessTokenUnsupported       = "relay.access_token_unsupported"
)

// Emails and the name of the token created at registration, in the
// recipient's language
const (
	MsgUserDefaultTokenName = "user.default_token_name"

	// Parts every built-in email shares: the layout's footer and the
	// labels of the code box and of the link under a button.
	MsgEmailLayoutFooter  = "email.layout.footer"
	MsgEmailLayoutSupport = "email.layout.support"
	MsgEmailLayoutTerms   = "email.layout.terms"
	MsgEmailLayoutPrivacy = "email.layout.privacy"
	MsgEmailCodeLabel     = "email.code_label"
	MsgEmailLinkFallback  = "email.link_fallback"

	// Each email has a tag shown in the layout's header, a subject, the
	// preview text inboxes show after it (preheader), and its own texts.
	MsgEmailVerificationTag       = "email.verification.tag"
	MsgEmailVerificationSubject   = "email.verification.subject"
	MsgEmailVerificationPreheader = "email.verification.preheader"
	MsgEmailVerificationTitle     = "email.verification.title"
	MsgEmailVerificationIntro     = "email.verification.intro"
	MsgEmailVerificationOutro     = "email.verification.outro"

	MsgEmailPasswordResetTag       = "email.password_reset.tag"
	MsgEmailPasswordResetSubject   = "email.password_reset.subject"
	MsgEmailPasswordResetPreheader = "email.password_reset.preheader"
	MsgEmailPasswordResetTitle     = "email.password_reset.title"
	MsgEmailPasswordResetIntro     = "email.password_reset.intro"
	MsgEmailPasswordResetButton    = "email.password_reset.button"
	MsgEmailPasswordResetOutro     = "email.password_reset.outro"

	MsgEmailBindingNewTag       = "email.binding_new.tag"
	MsgEmailBindingNewSubject   = "email.binding_new.subject"
	MsgEmailBindingNewPreheader = "email.binding_new.preheader"
	MsgEmailBindingNewTitle     = "email.binding_new.title"
	MsgEmailBindingNewIntro     = "email.binding_new.intro"
	MsgEmailBindingNewOutro     = "email.binding_new.outro"

	MsgEmailBindingCurrentTag       = "email.binding_current.tag"
	MsgEmailBindingCurrentSubject   = "email.binding_current.subject"
	MsgEmailBindingCurrentPreheader = "email.binding_current.preheader"
	MsgEmailBindingCurrentTitle     = "email.binding_current.title"
	MsgEmailBindingCurrentIntro     = "email.binding_current.intro"
	MsgEmailBindingCurrentOutro     = "email.binding_current.outro"

	MsgEmailSecurityNoticeTag       = "email.security_notice.tag"
	MsgEmailSecurityNoticeSubject   = "email.security_notice.subject"
	MsgEmailSecurityNoticePreheader = "email.security_notice.preheader"
	MsgEmailSecurityNoticeTitle     = "email.security_notice.title"
	MsgEmailSecurityNoticeIntro     = "email.security_notice.intro"
	MsgEmailSecurityNoticeEvent     = "email.security_notice.event"
	MsgEmailSecurityNoticeTime      = "email.security_notice.time"
	MsgEmailSecurityNoticeButton    = "email.security_notice.button"
	MsgEmailSecurityNoticeOutro     = "email.security_notice.outro"

	MsgSecurityEventPasswordUpdated      = "email.security_event.password_updated"
	MsgSecurityEventEmailChangeRequested = "email.security_event.email_change_requested"
	MsgSecurityEventEmailChanged         = "email.security_event.email_changed"
	MsgSecurityEventEmailConfirmed       = "email.security_event.email_confirmed"
	MsgSecurityEventLoginLinked          = "email.security_event.login_linked"
	MsgSecurityEventLoginUnlinked        = "email.security_event.login_unlinked"
	MsgSecurityEventWeChatLinked         = "email.security_event.wechat_linked"

	MsgEmailQuotaLowTag       = "email.quota_low.tag"
	MsgEmailQuotaLowPreheader = "email.quota_low.preheader"
	MsgEmailQuotaLowIntro     = "email.quota_low.intro"
	MsgEmailQuotaLowLabel     = "email.quota_low.label"
	MsgEmailQuotaLowButton    = "email.quota_low.button"
	MsgEmailQuotaLowOutro     = "email.quota_low.outro"

	MsgEmailTopupReceiptTag       = "email.topup_receipt.tag"
	MsgEmailTopupReceiptSubject   = "email.topup_receipt.subject"
	MsgEmailTopupReceiptPreheader = "email.topup_receipt.preheader"
	MsgEmailTopupReceiptTitle     = "email.topup_receipt.title"
	MsgEmailTopupReceiptIntro     = "email.topup_receipt.intro"
	MsgEmailTopupReceiptOrderNo   = "email.topup_receipt.order_no"
	MsgEmailTopupReceiptTime      = "email.topup_receipt.time"
	MsgEmailTopupReceiptMethod    = "email.topup_receipt.method"
	MsgEmailTopupReceiptAmount    = "email.topup_receipt.amount"
	MsgEmailTopupReceiptCredit    = "email.topup_receipt.credit"
	MsgEmailTopupReceiptBalance   = "email.topup_receipt.balance"
	MsgEmailTopupReceiptButton    = "email.topup_receipt.button"
	MsgEmailTopupReceiptOutro     = "email.topup_receipt.outro"

	MsgEmailNoticeTag = "email.notice.tag"

	MsgEmailTestTag       = "email.test.tag"
	MsgEmailTestSubject   = "email.test.subject"
	MsgEmailTestPreheader = "email.test.preheader"
	MsgEmailTestTitle     = "email.test.title"
	MsgEmailTestIntro     = "email.test.intro"
)

// Low-quota notices, in the recipient's saved language
const (
	MsgNotifyQuotaLowTitle             = "notify.quota_low_title"
	MsgNotifySubscriptionQuotaLowTitle = "notify.subscription_quota_low_title"
	MsgNotifyQuotaLowBark              = "notify.quota_low_bark"
	MsgNotifyQuotaLowGotify            = "notify.quota_low_gotify"
	MsgNotifyQuotaLowEmail             = "notify.quota_low_email"
)

// Channel notices sent to administrators who receive them and to model update
// watchers
const (
	MsgChannelNotifyDisabledSubject = "channel.notify_disabled_subject"
	MsgChannelNotifyDisabledContent = "channel.notify_disabled_content"
	MsgChannelNotifyEnabledSubject  = "channel.notify_enabled_subject"
	MsgChannelNotifyEnabledContent  = "channel.notify_enabled_content"
	MsgChannelTestCompletedSubject  = "channel.test_completed_subject"
	MsgChannelTestCompletedContent  = "channel.test_completed_content"
	MsgChannelResponseTimeExceeded  = "channel.response_time_exceeded"

	MsgChannelUpstreamUpdateNotifySubject   = "channel.upstream_update_notify_subject"
	MsgChannelUpstreamUpdateSummary         = "channel.upstream_update_summary"
	MsgChannelUpstreamUpdateChangedChannels = "channel.upstream_update_changed_channels"
	MsgChannelUpstreamUpdateMoreChannels    = "channel.upstream_update_more_channels"
	MsgChannelUpstreamUpdateAddedModels     = "channel.upstream_update_added_models"
	MsgChannelUpstreamUpdateRemovedModels   = "channel.upstream_update_removed_models"
	MsgChannelUpstreamUpdateFailedChannels  = "channel.upstream_update_failed_channels"
	MsgChannelUpstreamUpdateMoreOmitted     = "channel.upstream_update_more_omitted"
)
