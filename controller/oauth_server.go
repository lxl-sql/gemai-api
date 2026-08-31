package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	oauthCodeChallengeMethodS256         = "S256"
	oauthAuthorizeSessionClientIdKey     = "oauth_authorize_client_id"
	oauthAuthorizeSessionRedirectUriKey  = "oauth_authorize_redirect_uri"
	oauthAuthorizeSessionScopeKey        = "oauth_authorize_scope"
	oauthAuthorizeSessionCodeChallenge   = "oauth_authorize_code_challenge"
	oauthAuthorizeSessionChallengeMethod = "oauth_authorize_code_challenge_method"
)

var allowedOAuthScopes = map[string]struct{}{
	common.OAuthScopeProfile:     {},
	common.OAuthScopeEmail:       {},
	common.OAuthScopeTokenManage: {},
}

func oauthAppOperationDetail(app *model.OAuthApp, scope string, extra map[string]interface{}) map[string]interface{} {
	detail := map[string]interface{}{
		"client_id": app.ClientId,
		"app_id":    app.Id,
		"app_name":  app.Name,
		"scope":     scope,
	}
	for k, v := range extra {
		detail[k] = v
	}
	return detail
}

// OAuthServerAuthorize validates client params and returns app info for the consent page.
// The frontend renders the consent UI based on this response.
func OAuthServerAuthorize(c *gin.Context) {
	responseType := c.Query("response_type")
	clientId := c.Query("client_id")
	redirectUri := c.Query("redirect_uri")
	scope := c.Query("scope")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")
	if responseType != "" && responseType != "code" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "response_type must be code",
		})
		return
	}

	if clientId == "" || redirectUri == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "missing required parameters: client_id, redirect_uri",
		})
		return
	}

	app, err := model.GetCachedOAuthAppByClientIdContext(c.Request.Context(), clientId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid or disabled client_id",
		})
		return
	}

	if !app.IsRedirectUriAllowed(redirectUri) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "redirect_uri is not registered for this application",
		})
		return
	}

	normalizedScope, err := normalizeOAuthScopes(scope)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err = validateOAuthCodeChallenge(codeChallenge, codeChallengeMethod); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if app.EffectiveClientType() == model.OAuthClientTypePublic && codeChallenge == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "public clients must use PKCE"})
		return
	}

	session := sessions.Default(c)
	username := session.Get("username")
	userId := session.Get("id")

	csrfToken, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	session.Set("oauth_csrf_token", csrfToken)
	session.Set(oauthAuthorizeSessionClientIdKey, clientId)
	session.Set(oauthAuthorizeSessionRedirectUriKey, redirectUri)
	session.Set(oauthAuthorizeSessionScopeKey, normalizedScope)
	session.Set(oauthAuthorizeSessionCodeChallenge, codeChallenge)
	session.Set(oauthAuthorizeSessionChallengeMethod, codeChallengeMethod)
	_ = session.Save()

	common.ApiSuccess(c, gin.H{
		"app_name":        app.Name,
		"app_description": app.Description,
		"app_logo":        app.Logo,
		"scope":           normalizedScope,
		"redirect_uri":    redirectUri,
		"logged_in":       username != nil,
		"user": gin.H{
			"id":       userId,
			"username": username,
		},
		"csrf_token": csrfToken,
	})
}

// OAuthServerApprove is called when the user clicks "Allow" on the consent page.
// It generates an authorization code and returns the redirect URL.
func OAuthServerApprove(c *gin.Context) {
	session := sessions.Default(c)
	userId := session.Get("id")
	if userId == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "user not logged in",
		})
		return
	}
	userIdValue, ok := userId.(int)
	if !ok || userIdValue <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "user not logged in",
		})
		return
	}

	var req struct {
		ClientId    string `json:"client_id" binding:"required"`
		RedirectUri string `json:"redirect_uri" binding:"required"`
		Scope       string `json:"scope"`
		State       string `json:"state"`
		CsrfToken   string `json:"csrf_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request: " + err.Error()})
		return
	}

	savedCsrf := session.Get("oauth_csrf_token")
	session.Delete("oauth_csrf_token")
	_ = session.Save()
	savedCsrfValue, ok := savedCsrf.(string)
	if !ok || savedCsrfValue != req.CsrfToken {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "CSRF token invalid or expired, please refresh and try again",
		})
		return
	}

	savedClientId, _ := session.Get(oauthAuthorizeSessionClientIdKey).(string)
	savedRedirectUri, _ := session.Get(oauthAuthorizeSessionRedirectUriKey).(string)
	savedScope, _ := session.Get(oauthAuthorizeSessionScopeKey).(string)
	codeChallenge, _ := session.Get(oauthAuthorizeSessionCodeChallenge).(string)
	codeChallengeMethod, _ := session.Get(oauthAuthorizeSessionChallengeMethod).(string)
	clearOAuthAuthorizeSession(session)
	_ = session.Save()
	if savedClientId == "" || savedRedirectUri == "" || savedScope == "" {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "authorization request expired, please restart"})
		return
	}
	if req.ClientId != savedClientId || req.RedirectUri != savedRedirectUri {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "authorization request changed, please restart"})
		return
	}
	if req.Scope != "" && req.Scope != savedScope {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "scope changed, please restart authorization"})
		return
	}

	app, err := model.GetOAuthAppByClientIdContext(c.Request.Context(), savedClientId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid client_id"})
		return
	}

	if !app.IsRedirectUriAllowed(savedRedirectUri) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "redirect_uri not allowed"})
		return
	}
	if app.EffectiveClientType() == model.OAuthClientTypePublic && codeChallenge == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "public clients must use PKCE"})
		return
	}

	code, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	authCode := &model.OAuthAuthorizationCode{
		Code:                code,
		ClientId:            savedClientId,
		UserId:              userIdValue,
		RedirectUri:         savedRedirectUri,
		Scope:               savedScope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           time.Now().Add(10 * time.Minute),
	}
	if err := model.CreateOAuthAuthorizationCode(authCode); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionOAuthAuthorize, "oauth_app", strconv.Itoa(app.Id), true, oauthAppOperationDetail(app, savedScope, map[string]interface{}{
		"redirect_uri": savedRedirectUri,
		"pkce":         codeChallenge != "",
	}))

	redirectUrl, err := appendOAuthRedirectParams(savedRedirectUri, map[string]string{"code": code})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.State != "" {
		redirectUrl, err = appendOAuthRedirectParams(redirectUrl, map[string]string{"state": req.State})
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}

	common.ApiSuccess(c, gin.H{
		"redirect_url": redirectUrl,
	})
}

// OAuthServerToken exchanges an authorization code for a JWT access token.
// This endpoint follows the OAuth 2.0 token endpoint spec (RFC 6749 Section 4.1.3).
func OAuthServerToken(c *gin.Context) {
	grantType := c.PostForm("grant_type")
	code := c.PostForm("code")
	clientId := c.PostForm("client_id")
	clientSecret := c.PostForm("client_secret")
	redirectUri := c.PostForm("redirect_uri")
	codeVerifier := c.PostForm("code_verifier")
	refreshToken := c.PostForm("refresh_token")

	if grantType == "" {
		var req struct {
			GrantType    string `json:"grant_type"`
			Code         string `json:"code"`
			ClientId     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			RedirectUri  string `json:"redirect_uri"`
			CodeVerifier string `json:"code_verifier"`
			RefreshToken string `json:"refresh_token"`
		}
		if c.ShouldBindJSON(&req) == nil {
			grantType = req.GrantType
			code = req.Code
			clientId = req.ClientId
			clientSecret = req.ClientSecret
			redirectUri = req.RedirectUri
			codeVerifier = req.CodeVerifier
			refreshToken = req.RefreshToken
		}
	}
	if basicClientId, basicClientSecret, ok := c.Request.BasicAuth(); ok {
		if (clientId != "" && clientId != basicClientId) ||
			(clientSecret != "" && clientSecret != basicClientSecret) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_request",
				"error_description": "multiple client authentication methods are not allowed",
			})
			return
		}
		clientId = basicClientId
		clientSecret = basicClientSecret
	}
	exchangeContext, cancelExchange := service.OAuthExchangeRequestContext(c.Request.Context())
	defer cancelExchange()
	c.Request = c.Request.WithContext(exchangeContext)

	if grantType == "refresh_token" {
		handleOAuthRefreshTokenGrant(c, clientId, clientSecret, refreshToken)
		return
	}

	if grantType != "authorization_code" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "unsupported_grant_type",
			"error_description": "grant_type must be authorization_code or refresh_token",
		})
		return
	}

	if code == "" || clientId == "" || redirectUri == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "missing required parameters: code, client_id, redirect_uri",
		})
		return
	}
	validationAdmission, err := service.AcquireOAuthValidationAdmission(c.Request.Context())
	if err != nil {
		writeOAuthValidationAdmissionError(c, err)
		return
	}
	defer validationAdmission.Finish()

	app, err := model.GetOAuthAppByClientIdContext(c.Request.Context(), clientId)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load OAuth client",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client authentication failed",
		})
		return
	}

	authCode, err := model.GetOAuthAuthorizationCodeContext(c.Request.Context(), code)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load authorization code",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code is invalid",
		})
		return
	}

	if authCode.Used {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code has already been used",
		})
		return
	}

	if time.Now().After(authCode.ExpiresAt) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code has expired",
		})
		return
	}

	if authCode.ClientId != clientId {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code was issued to a different client",
		})
		return
	}

	if redirectUri != "" && authCode.RedirectUri != redirectUri {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "redirect_uri mismatch",
		})
		return
	}

	switch app.EffectiveClientType() {
	case model.OAuthClientTypeLegacy:
		if authCode.CodeChallenge != "" {
			if !verifyOAuthCodeVerifierLegacy(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":             "invalid_grant",
					"error_description": "code_verifier is invalid",
				})
				return
			}
		} else if clientSecret == "" || !app.ValidateClientSecretContext(c.Request.Context(), clientSecret) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_client",
				"error_description": "client authentication failed",
			})
			return
		}
	case model.OAuthClientTypeConfidential:
		if clientSecret == "" || !app.ValidateClientSecretContext(c.Request.Context(), clientSecret) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_client",
				"error_description": "client authentication failed",
			})
			return
		}
		if authCode.CodeChallenge != "" &&
			!verifyOAuthCodeVerifier(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_grant",
				"error_description": "code_verifier is invalid",
			})
			return
		}
	case model.OAuthClientTypePublic:
		if authCode.CodeChallenge == "" ||
			!verifyOAuthCodeVerifier(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_grant",
				"error_description": "public clients must use valid PKCE",
			})
			return
		}
	}
	if !allowOAuthAppUser(c, service.OAuthUserOperationToken, app.Id, authCode.UserId) {
		return
	}
	validationAdmission.Finish()
	if handleQueuedOAuthAuthorizationCode(c, app, authCode) {
		return
	}

	admission, err := service.AcquireOAuthExchangeAdmission(c.Request.Context())
	if err != nil {
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationToken, app.Id, authCode.UserId)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":             "temporarily_unavailable",
			"error_description": "OAuth exchange capacity is temporarily unavailable",
			"retryable":         true,
			"state_changed":     false,
		})
		return
	}
	now := time.Now()
	exchange, err := service.ExchangeOAuthAuthorizationCode(
		c.Request.Context(),
		code,
		clientId,
		redirectUri,
		authCode.CodeChallenge,
		authCode.CodeChallengeMethod,
		now,
	)
	admission.Finish(err != nil && !errors.Is(err, model.ErrOAuthAuthorizationCodeInvalid) && !errors.Is(err, service.ErrOAuthTokenClientUnavailable) && !errors.Is(err, service.ErrOAuthTokenUserUnavailable))
	if errors.Is(err, service.ErrOAuthTokenClientUnavailable) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client is disabled or deleted",
		})
		return
	}
	if errors.Is(err, model.ErrOAuthAuthorizationCodeInvalid) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code is invalid",
		})
		return
	}
	if errors.Is(err, service.ErrOAuthTokenUserUnavailable) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_grant",
			"error_description": "user is disabled or not found",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to persist authorization grant",
		})
		return
	}
	model.RecordOperationLogWithOperator(c, exchange.User.Id, exchange.User.Username, exchange.User.Role, model.OpActionOAuthTokenIssue, "oauth_app", strconv.Itoa(app.Id), true, oauthAppOperationDetail(app, exchange.Scope, map[string]interface{}{
		"grant_id":     exchange.Grant.Id,
		"expires_in":   exchange.AccessTokenExpiresIn,
		"token_type":   "Bearer",
		"redirect_uri": exchange.RedirectURI,
	}))

	resp := gin.H{
		"access_token":             exchange.AccessToken,
		"token_type":               "Bearer",
		"expires_in":               exchange.AccessTokenExpiresIn,
		"scope":                    exchange.Scope,
		"refresh_token":            exchange.RefreshToken,
		"refresh_token_expires_in": exchange.RefreshTokenExpiresIn,
	}
	c.JSON(http.StatusOK, resp)
}

func allowOAuthAppUser(c *gin.Context, operation string, appID int, userID int) bool {
	decision, err := service.AllowOAuthExchangeUser(c.Request.Context(), operation, appID, userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":             "temporarily_unavailable",
			"error_description": "OAuth user protection is temporarily unavailable",
			"retryable":         false,
			"state_changed":     false,
		})
		return false
	}
	if decision.Allowed {
		return true
	}
	c.Header("Retry-After", strconv.FormatInt(decision.RetryAfter, 10))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error":         "rate_limit_exceeded",
		"retryable":     true,
		"state_changed": false,
		"bucket":        "oauth_" + operation + "_user",
		"limit":         decision.Limit,
		"burst":         decision.Burst,
		"remaining":     decision.Remaining,
		"retry_after":   decision.RetryAfter,
		"request_id":    c.GetString(common.RequestIdKey),
	})
	return false
}

func handleOAuthRefreshTokenGrant(c *gin.Context, clientId string, clientSecret string, refreshToken string) {
	if clientId == "" || refreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "missing required parameters: client_id, refresh_token",
		})
		return
	}
	validationAdmission, err := service.AcquireOAuthValidationAdmission(c.Request.Context())
	if err != nil {
		writeOAuthValidationAdmissionError(c, err)
		return
	}
	defer validationAdmission.Finish()

	app, err := model.GetOAuthAppByClientIdContext(c.Request.Context(), clientId)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load OAuth client",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client authentication failed",
		})
		return
	}
	if app.EffectiveClientType() != model.OAuthClientTypePublic &&
		(clientSecret == "" || !app.ValidateClientSecretContext(c.Request.Context(), clientSecret)) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client authentication failed",
		})
		return
	}
	grant, err := model.GetActiveOAuthGrantByRefreshToken(clientId, refreshToken)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load authorization grant",
			})
			return
		}
		// 检测已轮换 refresh token 的重放（RFC 9700）：命中说明 token 已泄露，
		// 撤销整个授权，强制用户重新授权。
		validationAdmission.Finish()
		replayAdmission, admissionErr := service.AcquireOAuthExchangeAdmission(c.Request.Context())
		if admissionErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":             "temporarily_unavailable",
				"error_description": "OAuth exchange capacity is temporarily unavailable",
				"retryable":         true,
				"state_changed":     false,
			})
			return
		}
		replayed, revokeErr := model.RevokeOAuthGrantByReplayedRefreshToken(clientId, refreshToken)
		replayAdmission.Finish(revokeErr != nil)
		if revokeErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to verify refresh token replay",
			})
			return
		}
		if replayed {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("OAuth refresh token replay detected, grant revoked client_id=%s client_ip=%s", clientId, c.ClientIP()))
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_grant",
				"error_description": "refresh token reuse detected, authorization revoked; please re-authorize",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "refresh token is invalid, expired, or revoked",
		})
		return
	}

	user, err := model.GetUserByIdContext(c.Request.Context(), grant.UserId, false)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load OAuth user",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_grant",
			"error_description": "user is disabled or not found",
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_grant",
			"error_description": "user is disabled or not found",
		})
		return
	}
	if !allowOAuthAppUser(c, service.OAuthUserOperationRefresh, app.Id, grant.UserId) {
		return
	}
	validationAdmission.Finish()

	admission, err := service.AcquireOAuthExchangeAdmission(c.Request.Context())
	if err != nil {
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationRefresh, app.Id, grant.UserId)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":             "temporarily_unavailable",
			"error_description": "OAuth exchange capacity is temporarily unavailable",
			"retryable":         true,
			"state_changed":     false,
		})
		return
	}
	admissionFailed := true
	defer func() { admission.Finish(admissionFailed) }()
	now := time.Now()
	nextRefreshToken, refreshTokenExpiresIn, nextRefreshExpiresAt, err := service.GenerateOAuthRefreshToken(now)
	if err != nil {
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationRefresh, app.Id, grant.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to generate refresh token",
		})
		return
	}

	accessToken, expiresIn, err := service.SignOAuthDelegatedAccessToken(
		grant.UserId,
		clientId,
		grant.Id,
		grant.AuthorizationVersion,
		grant.Scopes,
		now,
	)
	if err != nil {
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationRefresh, app.Id, grant.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to generate access token",
		})
		return
	}

	if err = model.RotateOAuthGrantRefreshTokenCAS(grant, refreshToken, nextRefreshToken, nextRefreshExpiresAt); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to rotate refresh token",
			})
			return
		}
		admissionFailed = false
		// CAS 失败：token 在本请求处理期间被并发轮换，最可能是同一客户端的
		// 并发刷新（多实例/多标签页），属于合法竞争，不触发授权撤销；
		// 真正的泄露重放由宽限期后的重放检测（上方 GetActive 失败路径）兜底。
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "refresh token is invalid, expired, or revoked",
		})
		return
	}
	admissionFailed = false

	model.RecordOperationLogWithOperator(c, user.Id, user.Username, user.Role, model.OpActionOAuthTokenIssue, "oauth_app", strconv.Itoa(app.Id), true, oauthAppOperationDetail(app, grant.Scopes, map[string]interface{}{
		"grant_id":   grant.Id,
		"expires_in": expiresIn,
		"token_type": "Bearer",
		"grant_type": "refresh_token",
	}))

	c.JSON(http.StatusOK, gin.H{
		"access_token":             accessToken,
		"token_type":               "Bearer",
		"expires_in":               expiresIn,
		"scope":                    grant.Scopes,
		"refresh_token":            nextRefreshToken,
		"refresh_token_expires_in": refreshTokenExpiresIn,
	})
}

// OAuthServerUserInfo returns user information for a valid access token.
// Fields returned depend on the granted scope (profile, email).
func OAuthServerUserInfo(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "missing or invalid Authorization header",
		})
		return
	}

	delegatedClaims, err := service.ParseDelegatedOAuthAccessToken(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "access token is invalid or expired",
		})
		return
	}
	clientId := delegatedClaims.ClientID
	validationAdmission, err := service.AcquireOAuthValidationAdmission(c.Request.Context())
	if err != nil {
		writeOAuthValidationAdmissionError(c, err)
		return
	}
	defer validationAdmission.Finish()
	app, err := model.GetOAuthAppByClientIdContext(c.Request.Context(), clientId)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load OAuth client",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "client is disabled or deleted",
		})
		return
	}
	if !allowOAuthAppUser(c, service.OAuthUserOperationUserInfo, app.Id, delegatedClaims.UserID) {
		return
	}

	user, err := model.GetUserByIdContext(c.Request.Context(), delegatedClaims.UserID, false)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationUserInfo, app.Id, delegatedClaims.UserID)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load OAuth user",
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error":             "invalid_token",
			"error_description": "user not found",
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "user is disabled",
		})
		return
	}

	var grant *model.OAuthGrant
	if !delegatedClaims.GrantVersionPresent {
		if !service.AcceptLegacyOAuthGrantTokens() {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_token",
				"error_description": "legacy access tokens are no longer accepted",
			})
			return
		}
		grant, err = model.GetActiveOAuthGrantLegacy(
			delegatedClaims.GrantID,
			delegatedClaims.UserID,
			clientId,
		)
	} else {
		grant, err = model.GetActiveOAuthGrant(
			delegatedClaims.GrantID,
			delegatedClaims.UserID,
			clientId,
			delegatedClaims.GrantVersion,
		)
	}
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationUserInfo, app.Id, delegatedClaims.UserID)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "failed to load authorization grant",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "authorization grant has been revoked",
		})
		return
	}
	scopeSet := make(map[string]bool)
	for _, s := range strings.Split(delegatedClaims.Scope, " ") {
		scopeSet[strings.TrimSpace(s)] = true
	}
	grantScopeSet := make(map[string]bool)
	for _, s := range strings.Split(grant.Scopes, " ") {
		grantScopeSet[strings.TrimSpace(s)] = true
	}

	response := gin.H{
		"sub": user.Id,
	}

	if scopeSet[common.OAuthScopeProfile] && grantScopeSet[common.OAuthScopeProfile] {
		response["username"] = user.Username
		response["display_name"] = user.DisplayName
	}

	if scopeSet[common.OAuthScopeEmail] && grantScopeSet[common.OAuthScopeEmail] {
		response["email"] = user.Email
	}

	c.JSON(http.StatusOK, response)
}

func GetMyOAuthGrants(c *gin.Context) {
	userId := c.GetInt("id")
	grants, err := model.GetOAuthGrantsByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, grants)
}

func RevokeMyOAuthGrant(c *gin.Context) {
	userId := c.GetInt("id")
	grantId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	grant, err := model.GetOAuthGrantForUser(grantId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	app, err := model.GetOAuthAppByClientIdAnyStatus(grant.ClientId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.RevokeOAuthGrantForUser(grantId, userId); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionOAuthGrantRevoke, "oauth_app", strconv.Itoa(app.Id), true, oauthAppOperationDetail(app, grant.Scopes, map[string]interface{}{
		"grant_id": grant.Id,
	}))
	common.ApiSuccess(c, nil)
}

func normalizeOAuthScopes(scope string) (string, error) {
	fields := strings.Fields(scope)
	if len(fields) == 0 {
		fields = []string{common.OAuthScopeProfile}
	}

	seen := make(map[string]struct{}, len(fields))
	normalized := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, ok := allowedOAuthScopes[field]; !ok {
			return "", fmt.Errorf("unsupported scope: %s", field)
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		normalized = append(normalized, field)
	}
	return strings.Join(normalized, " "), nil
}

func validateOAuthCodeChallenge(challenge string, method string) error {
	if challenge == "" {
		if method != "" {
			return fmt.Errorf("code_challenge_method requires code_challenge")
		}
		return nil
	}
	if method != oauthCodeChallengeMethodS256 {
		return fmt.Errorf("only S256 PKCE is supported")
	}
	if len(challenge) < 43 || len(challenge) > 128 {
		return fmt.Errorf("code_challenge length must be between 43 and 128")
	}
	return nil
}

func verifyOAuthCodeVerifier(verifier string, challenge string, method string) bool {
	if !validOAuthCodeVerifier(verifier) || method != oauthCodeChallengeMethodS256 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return expected == challenge
}

func verifyOAuthCodeVerifierLegacy(verifier string, challenge string, method string) bool {
	if verifier == "" || method != oauthCodeChallengeMethodS256 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return expected == challenge
}

func validOAuthCodeVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, char := range verifier {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '.' || char == '_' || char == '~' {
			continue
		}
		return false
	}
	return true
}

func appendOAuthRedirectParams(rawUrl string, params map[string]string) (string, error) {
	parsed, err := url.Parse(rawUrl)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func clearOAuthAuthorizeSession(session sessions.Session) {
	session.Delete(oauthAuthorizeSessionClientIdKey)
	session.Delete(oauthAuthorizeSessionRedirectUriKey)
	session.Delete(oauthAuthorizeSessionScopeKey)
	session.Delete(oauthAuthorizeSessionCodeChallenge)
	session.Delete(oauthAuthorizeSessionChallengeMethod)
}
