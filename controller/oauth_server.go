package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
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

// OAuthServerAuthorize validates client params and returns app info for the consent page.
// The frontend renders the consent UI based on this response.
func OAuthServerAuthorize(c *gin.Context) {
	clientId := c.Query("client_id")
	redirectUri := c.Query("redirect_uri")
	scope := c.Query("scope")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")

	if clientId == "" || redirectUri == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "missing required parameters: client_id, redirect_uri",
		})
		return
	}

	app, err := model.GetOAuthAppByClientId(clientId)
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

	session := sessions.Default(c)
	username := session.Get("username")

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
		"csrf_token":      csrfToken,
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

	app, err := model.GetOAuthAppByClientId(savedClientId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid client_id"})
		return
	}

	if !app.IsRedirectUriAllowed(savedRedirectUri) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "redirect_uri not allowed"})
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
		UserId:              userId.(int),
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

	if grantType == "" {
		var req struct {
			GrantType    string `json:"grant_type"`
			Code         string `json:"code"`
			ClientId     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			RedirectUri  string `json:"redirect_uri"`
			CodeVerifier string `json:"code_verifier"`
		}
		if c.ShouldBindJSON(&req) == nil {
			grantType = req.GrantType
			code = req.Code
			clientId = req.ClientId
			clientSecret = req.ClientSecret
			redirectUri = req.RedirectUri
			codeVerifier = req.CodeVerifier
		}
	}
	if basicClientId, basicClientSecret, ok := c.Request.BasicAuth(); ok {
		if clientId == "" {
			clientId = basicClientId
		}
		if clientSecret == "" {
			clientSecret = basicClientSecret
		}
	}

	if grantType != "authorization_code" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "unsupported_grant_type",
			"error_description": "only authorization_code is supported",
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

	app, err := model.GetOAuthAppByClientId(clientId)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client authentication failed",
		})
		return
	}

	authCode, err := model.GetOAuthAuthorizationCode(code)
	if err != nil {
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

	if authCode.CodeChallenge != "" {
		if !verifyOAuthCodeVerifier(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_grant",
				"error_description": "code_verifier is invalid",
			})
			return
		}
	} else if clientSecret == "" || !app.ValidateClientSecret(clientSecret) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_client",
			"error_description": "client authentication failed",
		})
		return
	}

	consumed, err := model.ConsumeOAuthAuthorizationCode(code)
	if err != nil || !consumed {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_grant",
			"error_description": "authorization code is invalid",
		})
		return
	}

	go model.CleanExpiredOAuthAuthorizationCodes()

	now := time.Now()
	expiresIn := common.OAuthDefaultAccessTokenTTL
	jti, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to generate access token",
		})
		return
	}
	grant, err := model.UpsertOAuthGrant(authCode.UserId, clientId, authCode.Scope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to persist authorization grant",
		})
		return
	}
	claims := jwt.MapClaims{
		"sub":       authCode.UserId,
		"client_id": clientId,
		"grant_id":  grant.Id,
		"aud":       clientId,
		"scope":     authCode.Scope,
		"iat":       now.Unix(),
		"exp":       now.Add(time.Duration(expiresIn) * time.Second).Unix(),
		"iss":       common.OAuthTokenIssuerGemaiAPI,
		"jti":       jti,
		"typ":       common.OAuthAccessTokenType,
		"token_use": common.OAuthTokenUseDelegatedAPI,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString([]byte(common.CryptoSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to generate access token",
		})
		return
	}

	resp := gin.H{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
		"scope":        authCode.Scope,
	}
	c.JSON(http.StatusOK, resp)
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

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(common.CryptoSecret), nil
	})
	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "access token is invalid or expired",
		})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "invalid token claims",
		})
		return
	}

	typ, _ := claims["typ"].(string)
	tokenUse, _ := claims["token_use"].(string)
	if typ != common.OAuthAccessTokenType || tokenUse != common.OAuthTokenUseDelegatedAPI {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "token type mismatch",
		})
		return
	}
	clientId, _ := claims["client_id"].(string)
	if clientId == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "missing client_id in token",
		})
		return
	}
	if _, err := model.GetOAuthAppByClientId(clientId); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "client is disabled or deleted",
		})
		return
	}

	userIdFloat, ok := claims["sub"].(float64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "invalid user id in token",
		})
		return
	}
	userId := int(userIdFloat)
	grantIdFloat, ok := claims["grant_id"].(float64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "missing grant_id in token",
		})
		return
	}
	grantId := int(grantIdFloat)

	user, err := model.GetUserById(userId, false)
	if err != nil {
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

	scope, _ := claims["scope"].(string)
	grant, err := model.GetActiveOAuthGrant(grantId, userId, clientId)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "authorization grant has been revoked",
		})
		return
	}
	scopeSet := make(map[string]bool)
	for _, s := range strings.Split(scope, " ") {
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
	if err := model.RevokeOAuthGrantForUser(grantId, userId); err != nil {
		common.ApiError(c, err)
		return
	}
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
	if verifier == "" || method != oauthCodeChallengeMethodS256 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return expected == challenge
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
