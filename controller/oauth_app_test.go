package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResetPublicOAuthAppSecretReportsConfidentialConversion(t *testing.T) {
	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.OAuthApp{}))
	model.DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
	})
	app := &model.OAuthApp{
		Name:         "Public App",
		ClientId:     "gai_public_reset",
		ClientType:   model.OAuthClientTypePublic,
		RedirectUris: `["https://tool.example.com/callback"]`,
		UserId:       7,
		Status:       common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(app).Error)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/oauth-app/:id/reset-secret", func(c *gin.Context) {
		c.Set("id", app.UserId)
		ResetOAuthAppSecret(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/oauth-app/"+strconv.Itoa(app.Id)+"/reset-secret", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ClientSecret string `json:"client_secret"`
			ClientType   string `json:"client_type"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.NotEmpty(t, response.Data.ClientSecret)
	assert.Equal(t, model.OAuthClientTypeConfidential, response.Data.ClientType)

	stored, err := model.GetOAuthAppById(app.Id)
	require.NoError(t, err)
	assert.Equal(t, model.OAuthClientTypeConfidential, stored.ClientType)
	assert.True(t, stored.ValidateClientSecret(response.Data.ClientSecret))
}
