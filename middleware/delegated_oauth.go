package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func DelegatedOAuthAuth(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "missing or invalid Authorization header")
			return
		}

		tokenString := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(common.CryptoSecret), nil
		})
		if err != nil || !token.Valid {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "access token is invalid or expired")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "invalid token claims")
			return
		}
		typ, _ := claims["typ"].(string)
		tokenUse, _ := claims["token_use"].(string)
		if typ != common.OAuthAccessTokenType || tokenUse != common.OAuthTokenUseDelegatedAPI {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "token type mismatch")
			return
		}

		userIdFloat, ok := claims["sub"].(float64)
		if !ok {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "invalid user id in token")
			return
		}
		userId := int(userIdFloat)

		grantIdFloat, ok := claims["grant_id"].(float64)
		if !ok {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "missing grant_id in token")
			return
		}
		grantId := int(grantIdFloat)

		clientId, _ := claims["client_id"].(string)
		if clientId == "" {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "missing client_id in token")
			return
		}
		if _, err := model.GetOAuthAppByClientId(clientId); err != nil {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "client is disabled or deleted")
			return
		}

		user, err := model.GetUserById(userId, false)
		if err != nil || user.Status != common.UserStatusEnabled {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "user is disabled or not found")
			return
		}

		grant, err := model.GetActiveOAuthGrant(grantId, userId, clientId)
		if err != nil {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "authorization grant has been revoked")
			return
		}

		tokenScope, _ := claims["scope"].(string)
		if !scopeContains(tokenScope, requiredScope) || !scopeContains(grant.Scopes, requiredScope) {
			abortDelegatedOAuth(c, http.StatusForbidden, "insufficient_scope", "required scope: "+requiredScope)
			return
		}

		c.Set("id", userId)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("group", user.Group)
		c.Set("auth_type", "delegated_oauth")
		c.Set("oauth_client_id", clientId)
		c.Set("oauth_grant_id", grantId)
		c.Set("oauth_scopes", tokenScope)
		c.Next()
	}
}

func scopeContains(scopes string, requiredScope string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == requiredScope {
			return true
		}
	}
	return false
}

func abortDelegatedOAuth(c *gin.Context, status int, code string, description string) {
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": description,
	})
	c.Abort()
}
