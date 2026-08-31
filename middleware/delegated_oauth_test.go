package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDelegatedOAuthAuthRejectsVersionlessTokenWhenCompatibilityIsDisabled(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.OAuthApp{}, &model.OAuthGrant{}))
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	user := &model.User{
		Username: "legacy-delegated-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)
	app := &model.OAuthApp{
		Name:         "Legacy Delegated App",
		ClientId:     "gai_legacy_delegated",
		ClientType:   model.OAuthClientTypeLegacy,
		RedirectUris: `["https://tool.example.com/callback"]`,
		UserId:       user.Id,
		Status:       common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(app).Error)
	grant := &model.OAuthGrant{
		UserId:               user.Id,
		ClientId:             app.ClientId,
		Scopes:               common.OAuthScopeTokenManage,
		AuthorizationVersion: 0,
	}
	require.NoError(t, db.Create(grant).Error)
	now := time.Now()
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       user.Id,
		"client_id": app.ClientId,
		"grant_id":  grant.Id,
		"aud":       app.ClientId,
		"scope":     common.OAuthScopeTokenManage,
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
		"iss":       common.OAuthTokenIssuerGemaiAPI,
		"jti":       "legacy-delegated-token",
		"typ":       common.OAuthAccessTokenType,
		"token_use": common.OAuthTokenUseDelegatedAPI,
	}).SignedString([]byte(common.CryptoSecret))
	require.NoError(t, err)

	request := func() int {
		recorder := httptest.NewRecorder()
		router := gin.New()
		router.GET("/", DelegatedOAuthAuth(common.OAuthScopeTokenManage), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}

	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "true")
	require.Equal(t, http.StatusNoContent, request())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "false")
	require.Equal(t, http.StatusUnauthorized, request())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "true")
	require.NoError(t, db.Migrator().DropTable(&model.OAuthGrant{}))
	require.Equal(t, http.StatusServiceUnavailable, request())
}
