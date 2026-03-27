package controller

import (
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OAuthServerAuthorize validates client params and returns app info for the consent page.
// The frontend renders the consent UI based on this response.
func OAuthServerAuthorize(c *gin.Context) {
	clientId := c.Query("client_id")
	redirectUri := c.Query("redirect_uri")
	scope := c.Query("scope")

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

	if scope == "" {
		scope = "profile"
	}

	session := sessions.Default(c)
	username := session.Get("username")

	csrfToken := common.GetRandomString(32)
	session.Set("oauth_csrf_token", csrfToken)
	_ = session.Save()

	common.ApiSuccess(c, gin.H{
		"app_name":        app.Name,
		"app_description": app.Description,
		"app_logo":        app.Logo,
		"scope":           scope,
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
	if savedCsrf == nil || savedCsrf.(string) != req.CsrfToken {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "CSRF token invalid or expired, please refresh and try again",
		})
		return
	}

	app, err := model.GetOAuthAppByClientId(req.ClientId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid client_id"})
		return
	}

	if !app.IsRedirectUriAllowed(req.RedirectUri) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "redirect_uri not allowed"})
		return
	}

	if req.Scope == "" {
		req.Scope = "profile"
	}

	code := common.GetRandomString(48)
	authCode := &model.OAuthAuthorizationCode{
		Code:        code,
		ClientId:    req.ClientId,
		UserId:      userId.(int),
		RedirectUri: req.RedirectUri,
		Scope:       req.Scope,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := model.CreateOAuthAuthorizationCode(authCode); err != nil {
		common.ApiError(c, err)
		return
	}

	redirectUrl := req.RedirectUri
	if strings.Contains(redirectUrl, "?") {
		redirectUrl += "&"
	} else {
		redirectUrl += "?"
	}
	redirectUrl += "code=" + code
	if req.State != "" {
		redirectUrl += "&state=" + req.State
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

	if grantType == "" {
		var req struct {
			GrantType    string `json:"grant_type"`
			Code         string `json:"code"`
			ClientId     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			RedirectUri  string `json:"redirect_uri"`
		}
		if c.ShouldBindJSON(&req) == nil {
			grantType = req.GrantType
			code = req.Code
			clientId = req.ClientId
			clientSecret = req.ClientSecret
			redirectUri = req.RedirectUri
		}
	}

	if grantType != "authorization_code" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "unsupported_grant_type",
			"error_description": "only authorization_code is supported",
		})
		return
	}

	if code == "" || clientId == "" || clientSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "missing required parameters: code, client_id, client_secret",
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

	if !app.ValidateClientSecret(clientSecret) {
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

	_ = model.MarkOAuthAuthorizationCodeUsed(code)

	go model.CleanExpiredOAuthAuthorizationCodes()

	now := time.Now()
	expiresIn := 3600
	claims := jwt.MapClaims{
		"sub":       authCode.UserId,
		"client_id": clientId,
		"scope":     authCode.Scope,
		"iat":       now.Unix(),
		"exp":       now.Add(time.Duration(expiresIn) * time.Second).Unix(),
		"iss":       "gemai-api",
		"typ":       "oauth_access_token",
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

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
		"scope":        authCode.Scope,
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
	if typ != "oauth_access_token" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "token type mismatch",
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

	user, err := model.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":             "invalid_token",
			"error_description": "user not found",
		})
		return
	}

	scope, _ := claims["scope"].(string)
	scopeSet := make(map[string]bool)
	for _, s := range strings.Split(scope, " ") {
		scopeSet[strings.TrimSpace(s)] = true
	}

	response := gin.H{
		"sub": user.Id,
	}

	if scopeSet["profile"] {
		response["username"] = user.Username
		response["display_name"] = user.DisplayName
		response["role"] = user.Role
		response["status"] = user.Status
		response["group"] = user.Group
	}

	if scopeSet["email"] {
		response["email"] = user.Email
	}

	if scopeSet["api"] {
		if user.AccessToken == nil || *user.AccessToken == "" {
			randI := common.GetRandomInt(4)
			key, err := common.GenerateRandomKey(29 + randI)
			if err == nil {
				user.SetAccessToken(key)
				_ = user.Update(false)
			}
		}
		if user.AccessToken != nil {
			response["access_token"] = *user.AccessToken
		}
	}

	c.JSON(http.StatusOK, response)
}
