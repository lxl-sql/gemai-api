package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DelegatedOAuthAuth(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "missing or invalid Authorization header")
			return
		}

		claims, err := service.ParseDelegatedOAuthAccessToken(strings.TrimPrefix(authHeader, "Bearer "))
		if err != nil {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "access token is invalid or expired")
			return
		}
		userId := claims.UserID
		clientId := claims.ClientID
		if _, err := model.GetOAuthAppByClientIdContext(c.Request.Context(), clientId); err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				abortDelegatedOAuth(c, http.StatusServiceUnavailable, "server_error", "failed to load OAuth client")
				return
			}
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "client is disabled or deleted")
			return
		}

		user, err := model.GetUserByIdContext(c.Request.Context(), userId, false)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				abortDelegatedOAuth(c, http.StatusServiceUnavailable, "server_error", "failed to load OAuth user")
				return
			}
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "user is disabled or not found")
			return
		}
		if user.Status != common.UserStatusEnabled {
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "user is disabled or not found")
			return
		}

		var grant *model.OAuthGrant
		if !claims.GrantVersionPresent {
			if !service.AcceptLegacyOAuthGrantTokens() {
				abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "legacy access tokens are no longer accepted")
				return
			}
			grant, err = model.GetActiveOAuthGrantLegacy(claims.GrantID, userId, clientId)
		} else {
			grant, err = model.GetActiveOAuthGrant(claims.GrantID, userId, clientId, claims.GrantVersion)
		}
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				abortDelegatedOAuth(c, http.StatusServiceUnavailable, "server_error", "failed to load authorization grant")
				return
			}
			abortDelegatedOAuth(c, http.StatusUnauthorized, "invalid_token", "authorization grant has been revoked")
			return
		}

		if !scopeContains(claims.Scope, requiredScope) || !scopeContains(grant.Scopes, requiredScope) {
			abortDelegatedOAuth(c, http.StatusForbidden, "insufficient_scope", "required scope: "+requiredScope)
			return
		}

		c.Set("id", userId)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("group", user.Group)
		c.Set("auth_type", "delegated_oauth")
		c.Set("oauth_client_id", clientId)
		c.Set("oauth_grant_id", claims.GrantID)
		c.Set("oauth_scopes", claims.Scope)
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
