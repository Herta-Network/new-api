package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// accessTokenMinLifetimeSecs is the shortest expiry a new access token may have.
const accessTokenMinLifetimeSecs = 60 * 60

type accessTokenItem struct {
	model.UserAccessToken
	Scopes []string `json:"scopes"`
}

type accessTokenCreateRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt int64    `json:"expires_at"`
}

// accessTokenUpdateRequest renames a token; a present scopes field also
// replaces its grant.
type accessTokenUpdateRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func newAccessTokenItem(token *model.UserAccessToken) accessTokenItem {
	scopes := token.GetScopes()
	if scopes == nil {
		scopes = []string{}
	}
	return accessTokenItem{UserAccessToken: *token, Scopes: scopes}
}

// writeAccessTokenError responds with a stable code and a web console message.
func writeAccessTokenError(c *gin.Context, status int, code, key string, params ...map[string]any) {
	common.ApiErrorStatus(c, status, common.NewMessage(key, params...), gin.H{"code": code})
}

// normalizeAccessTokenName returns the trimmed name, or false when it is empty
// or longer than the 64-character column.
func normalizeAccessTokenName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	count := utf8.RuneCountInString(name)
	return name, count > 0 && count <= 64
}

type accessTokenList struct {
	Items []accessTokenItem `json:"items"`
	// Legacy is the pre-scope access token, null when there is none or after
	// the legacy tokens retired.
	Legacy *legacyAccessToken `json:"legacy"`
}

type legacyAccessToken struct {
	TokenHint  string `json:"token_hint"`
	TokenRef   string `json:"token_ref"`
	CreatedAt  *int64 `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	LastUsedIp string `json:"last_used_ip"`
	RetireAt   int64  `json:"retire_at"`
}

// @Summary		获取个人访问令牌
// @Description	仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
// @Tags			个人访问令牌
// @Success		200	{object}	common.ApiResponse{data=accessTokenList}
// @Security		UserAuth
// @Router			/api/user/access_tokens [get]
func ListAccessTokens(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	tokens, err := model.ListUserAccessTokens(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	items := make([]accessTokenItem, 0, len(tokens))
	for i := range tokens {
		items = append(items, newAccessTokenItem(&tokens[i]))
	}
	var legacy *legacyAccessToken
	if !model.LegacyAccessTokensRetired(common.GetTimestamp()) {
		status, err := model.GetUserAccessTokenStatus(identity.UserID)
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		if status.Exists {
			legacy = &legacyAccessToken{
				TokenHint:  status.TokenHint,
				TokenRef:   status.TokenRef,
				CreatedAt:  status.CreatedAt,
				LastUsedAt: status.LastUsedAt,
				LastUsedIp: status.LastUsedIp,
				RetireAt:   model.LegacyAccessTokenRetireAt(),
			}
		}
	}
	common.ApiSuccess(c, accessTokenList{Items: items, Legacy: legacy})
}

type accessTokenCatalog struct {
	// Groups hold the scopes the current user can grant.
	Groups            []service.AccessTokenCatalogGroup `json:"groups"`
	MaxTokens         int                               `json:"max_tokens"`
	DefaultExpiryDays int                               `json:"default_expiry_days"`
}

// @Summary		获取可授予的令牌权限
// @Description	列出当前用户可以授予个人访问令牌的权限，以及令牌数量上限和默认有效天数；仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
// @Tags			个人访问令牌
// @Success		200	{object}	common.ApiResponse{data=accessTokenCatalog}
// @Security		UserAuth
// @Router			/api/user/access_tokens/catalog [get]
func GetAccessTokenCatalog(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, accessTokenCatalog{
		Groups:            service.AccessTokenCatalog(identity.UserID, c.GetInt("role")),
		MaxTokens:         service.AccessTokenMaxPerUser,
		DefaultExpiryDays: service.AccessTokenDefaultExpiryDays,
	})
}

type accessTokenScopes struct {
	Resources []service.AccessTokenResource `json:"resources"`
}

// GetAccessTokenScopes returns the unfiltered scope labels used to describe
// stored grants, such as those in audit records.
//
//	@Summary		获取令牌权限说明
//	@Description	列出所有令牌权限的名称，用于显示已保存的授权（如审计记录中的）；仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
//	@Tags			个人访问令牌
//	@Success		200	{object}	common.ApiResponse{data=accessTokenScopes}
//	@Security		UserAuth
//	@Router			/api/user/access_tokens/scopes [get]
func GetAccessTokenScopes(c *gin.Context) {
	if _, ok := requireBrowserSession(c); !ok {
		return
	}
	common.ApiSuccess(c, accessTokenScopes{Resources: service.AccessTokenScopeDictionary()})
}

type createdAccessToken struct {
	// Token is the plaintext token, returned only here.
	Token string          `json:"token"`
	Item  accessTokenItem `json:"item"`
}

// CreateAccessToken validates the whole request before consuming the proof, so
// a rejected grant never burns the user's verification.
//
//	@Summary		创建个人访问令牌
//	@Description	需要 scope 为 access_token.generate 的 X-Security-Proof，其 context 为 {scopes, expires_at}。expires_at 为 0 表示永不过期，否则至少在一小时后；明文令牌只在本次返回。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
//	@Tags			个人访问令牌
//	@Param			body	body		accessTokenCreateRequest	true	"名称、权限和过期时间（Unix 秒）"
//	@Success		200		{object}	common.ApiResponse{data=createdAccessToken}
//	@Failure		400		{object}	common.ApiResponse
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/access_tokens [post]
func CreateAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var req accessTokenCreateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	name, ok := normalizeAccessTokenName(req.Name)
	if !ok {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_NAME_INVALID", "Access token name must be 1 to 64 characters.")
		return
	}
	now := common.GetTimestamp()
	if req.ExpiresAt != 0 && req.ExpiresAt < now+accessTokenMinLifetimeSecs {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_EXPIRY_INVALID", "Access token expiry must be at least one hour from now.")
		return
	}
	scopes, err := service.NormalizeAccessTokenScopes(identity.UserID, c.GetInt("role"), req.Scopes)
	switch {
	case errors.Is(err, service.ErrAccessTokenScopeForbidden):
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_FORBIDDEN", "You cannot grant one or more of these permissions.")
		return
	case err != nil:
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_INVALID", "Select at least one valid permission.")
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenGenerateContext{Scopes: scopes, ExpiresAt: req.ExpiresAt})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenGenerate, Context: proofContext}) == nil {
		return
	}
	suffix, err := common.GenerateRandomCharsKey(43)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	raw := model.AccessTokenPrefix + suffix
	token := &model.UserAccessToken{
		Name:      name,
		TokenHash: model.AccessTokenFingerprint(raw),
		TokenHint: model.AccessTokenHint(raw),
		ExpiresAt: req.ExpiresAt,
		CreatedAt: now,
	}
	if err := token.SetScopes(scopes); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if err := model.CreateUserAccessToken(identity.UserID, token, service.AccessTokenMaxPerUser); err != nil {
		if errors.Is(err, model.ErrAccessTokenLimit) {
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_LIMIT", "You can create at most {{count}} access tokens", map[string]any{"count": service.AccessTokenMaxPerUser})
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "access_token.generate", map[string]any{
		"token_id":   token.Id,
		"name":       token.Name,
		"scopes":     scopes,
		"expires_at": token.ExpiresAt,
		"token_ref":  token.TokenHash,
	})
	common.ApiSuccess(c, createdAccessToken{Token: raw, Item: newAccessTokenItem(token)})
}

// UpdateAccessToken renames a token without verification. Changing the grant
// is checked in full before the proof is consumed, like creation.
//
//	@Summary		更新个人访问令牌
//	@Description	只改名称时无需二次验证；请求带 scopes 时替换授权，需要 scope 为 access_token.update 的 X-Security-Proof，其 context 为 {token_id, scopes}。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
//	@Tags			个人访问令牌
//	@Param			id		path		int							true	"令牌 ID"
//	@Param			body	body		accessTokenUpdateRequest	true	"名称，可选的新权限"
//	@Success		200		{object}	common.ApiResponse{data=accessTokenItem}
//	@Failure		400		{object}	common.ApiResponse
//	@Failure		404		{object}	common.ApiResponse
//	@Security		UserAuth
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/access_tokens/{id} [patch]
func UpdateAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	var req accessTokenUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	name, ok := normalizeAccessTokenName(req.Name)
	if !ok {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_NAME_INVALID", "Access token name must be 1 to 64 characters.")
		return
	}
	var scopes []string
	var previous *model.UserAccessToken
	if req.Scopes != nil {
		previous, err = model.GetUserAccessToken(identity.UserID, id)
		if errors.Is(err, model.ErrAccessTokenNotFound) {
			writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
			return
		}
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		scopes, err = service.NormalizeAccessTokenScopes(identity.UserID, c.GetInt("role"), req.Scopes)
		switch {
		case errors.Is(err, service.ErrAccessTokenScopeForbidden):
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_FORBIDDEN", "You cannot grant one or more of these permissions.")
			return
		case err != nil:
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_INVALID", "Select at least one valid permission.")
			return
		}
		proofContext, err := common.Marshal(service.AccessTokenUpdateContext{TokenID: id, Scopes: scopes})
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenUpdate, Context: proofContext}) == nil {
			return
		}
	}
	token, err := model.UpdateUserAccessToken(identity.UserID, id, name, scopes)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if previous == nil {
		recordUserSecurityAudit(c, identity.UserID, "access_token.rename", map[string]any{"token_id": token.Id, "name": token.Name})
		common.ApiSuccess(c, newAccessTokenItem(token))
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "access_token.update", map[string]any{
		"token_id":        token.Id,
		"name":            token.Name,
		"scopes":          scopes,
		"previous_scopes": previous.GetScopes(),
		"token_ref":       token.TokenHash,
	})
	common.ApiSuccess(c, newAccessTokenItem(token))
}

// DeleteAccessToken checks ownership before consuming the proof, so probing
// other users' token IDs never burns a verification.
//
//	@Summary		撤销个人访问令牌
//	@Description	需要 scope 为 access_token.revoke 的 X-Security-Proof，其 context 为 {token_id}。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
//	@Tags			个人访问令牌
//	@Param			id	path		int	true	"令牌 ID"
//	@Success		200	{object}	common.ApiResponse
//	@Failure		404	{object}	common.ApiResponse
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/access_tokens/{id} [delete]
func DeleteAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if _, err := model.GetUserAccessToken(identity.UserID, id); err != nil {
		if errors.Is(err, model.ErrAccessTokenNotFound) {
			writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenRevokeContext{TokenID: id})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenRevoke, Context: proofContext}) == nil {
		return
	}
	token, err := model.DeleteUserAccessToken(identity.UserID, id)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "access_token.revoke", map[string]any{"token_id": token.Id, "name": token.Name, "token_ref": token.TokenHash})
	common.ApiSuccess(c, nil)
}

// RevokeLegacyAccessToken removes the pre-scope users.access_token credential.
// After the transition deadline the legacy token no longer exists for callers.
//
//	@Summary		撤销旧版访问令牌
//	@Description	需要 scope 为 access_token.revoke 的 X-Security-Proof，其 context 为 {legacy: true}；旧版令牌停用后返回 404。仅限浏览器登录会话，个人访问令牌调用返回 403 AUTH_SESSION_REQUIRED。
//	@Tags			个人访问令牌
//	@Success		200	{object}	common.ApiResponse
//	@Failure		404	{object}	common.ApiResponse
//	@Security		UserAuth && SecurityProof
//	@Router			/api/user/access_tokens/legacy [delete]
func RevokeLegacyAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	if model.LegacyAccessTokensRetired(common.GetTimestamp()) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	status, err := model.GetUserAccessTokenStatus(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if !status.Exists {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenRevokeContext{Legacy: true})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenRevoke, Context: proofContext}) == nil {
		return
	}
	ref, err := model.RevokeUserAccessToken(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if ref != "" {
		recordUserSecurityAudit(c, identity.UserID, "access_token.revoke", map[string]any{"legacy": true, "token_ref": ref})
	}
	common.ApiSuccess(c, nil)
}

//	@Summary		获取审计日志
//	@Description	需要 audit.read 权限；非超级管理员看不到超级管理员发起的记录。
//	@Tags			日志
//	@Param			username			query		string	false	"用户名"
//	@Param			p					query		int		false	"页码，从 1 开始"
//	@Param			page_size			query		int		false	"每页条数"
//	@Param			category			query		string	false	"类别：login、security、operation、access_token"
//	@Param			token_ref			query		string	false	"只看该令牌的记录（64 位十六进制摘要）"
//	@Param			exclude_token_ref	query		string	false	"排除该令牌的记录（64 位十六进制摘要）"
//	@Param			request_id			query		string	false	"请求 ID"
//	@Param			start_timestamp		query		int		false	"开始时间（Unix 秒）"
//	@Param			end_timestamp		query		int		false	"结束时间（Unix 秒）"
//	@Param			success				query		string	false	"结果：true 或 false"
//	@Success		200					{object}	common.ApiResponse{data=common.PageInfo{items=[]model.AuditLog}}
//	@Security		AdminAuth
//	@Router			/api/audit [get]

// @Summary		获取我的审计日志
// @Tags			日志
// @Param			p					query		int		false	"页码，从 1 开始"
// @Param			page_size			query		int		false	"每页条数"
// @Param			category			query		string	false	"类别：login、security、operation、access_token"
// @Param			token_ref			query		string	false	"只看该令牌的记录（64 位十六进制摘要）"
// @Param			exclude_token_ref	query		string	false	"排除该令牌的记录（64 位十六进制摘要）"
// @Param			request_id			query		string	false	"请求 ID"
// @Param			start_timestamp		query		int		false	"开始时间（Unix 秒）"
// @Param			end_timestamp		query		int		false	"结束时间（Unix 秒）"
// @Param			success				query		string	false	"结果：true 或 false"
// @Success		200					{object}	common.ApiResponse{data=common.PageInfo{items=[]model.AuditLog}}
// @Security		UserAuth
// @Router			/api/audit/self [get]
func GetAuditLogs(c *gin.Context) {
	page := common.GetPageQuery(c)
	if page.Page < 1 || page.PageSize < 1 || page.Page > 100000000 {
		common.ApiErrorMsg(c, "Invalid audit pagination")
		return
	}
	filter := model.AuditLogFilter{Username: c.Query("username"), Category: c.Query("category"), TokenRef: c.Query("token_ref"), ExcludeTokenRef: c.Query("exclude_token_ref"), RequestId: c.Query("request_id")}
	viewerRole := c.GetInt("role")
	if c.FullPath() == "/api/audit/self" {
		filter.UserId = c.GetInt("id")
		filter.Username = ""
		filter.SelfView = true
	}
	if !model.ValidAuditCategory(filter.Category) || !model.ValidTokenFingerprint(filter.TokenRef) || !model.ValidTokenFingerprint(filter.ExcludeTokenRef) {
		common.ApiErrorMsg(c, "Invalid audit filters")
		return
	}
	for name, target := range map[string]*int64{"start_timestamp": &filter.StartTimestamp, "end_timestamp": &filter.EndTimestamp} {
		if raw := c.Query(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				common.ApiErrorMsg(c, "Invalid audit time range")
				return
			}
			*target = parsed
		}
	}
	if filter.EndTimestamp > 0 && filter.EndTimestamp < filter.StartTimestamp {
		common.ApiErrorMsg(c, "Invalid audit time range")
		return
	}
	if raw := c.Query("success"); raw != "" {
		if raw != "true" && raw != "false" {
			common.ApiErrorMsg(c, "Invalid audit result")
			return
		}
		success := raw == "true"
		filter.Success = &success
	}
	logs, total, err := model.GetAuditLogs(filter, page.GetStartIdx(), page.GetPageSize(), viewerRole)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetItems(logs)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
