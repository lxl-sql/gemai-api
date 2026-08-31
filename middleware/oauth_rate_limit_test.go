package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestGlobalAPIRateLimitExemptsOnlyExactOAuthRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	api.POST("/oauth-server/token", func(c *gin.Context) { assert.True(t, isGlobalAPIRateLimitExempt(c)); c.Status(http.StatusOK) })
	api.GET("/oauth-server/userinfo", func(c *gin.Context) { assert.True(t, isGlobalAPIRateLimitExempt(c)); c.Status(http.StatusOK) })
	api.GET("/oauth-server/token-exchanges/:id", func(c *gin.Context) { assert.False(t, isGlobalAPIRateLimitExempt(c)); c.Status(http.StatusOK) })
	api.GET("/oauth-server/authorize", func(c *gin.Context) { assert.False(t, isGlobalAPIRateLimitExempt(c)); c.Status(http.StatusOK) })

	request := func(method string, path string) int {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "198.51.100.77:40000"
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, request(http.MethodPost, "/api/oauth-server/token"))
	assert.Equal(t, http.StatusOK, request(http.MethodGet, "/api/oauth-server/userinfo"))
	assert.Equal(t, http.StatusOK, request(http.MethodGet, "/api/oauth-server/token-exchanges/exchange-1"))
	assert.Equal(t, http.StatusOK, request(http.MethodGet, "/api/oauth-server/authorize"))
}

func TestGlobalAPIRateLimitStillProtectsOAuthWhenQueueIsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousEnabled := common.GlobalApiRateLimitEnable
	previousLimit := common.GlobalApiRateLimitNum
	previousDuration := common.GlobalApiRateLimitDuration
	previousRedisEnabled := common.RedisEnabled
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 1
	common.GlobalApiRateLimitDuration = 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousEnabled
		common.GlobalApiRateLimitNum = previousLimit
		common.GlobalApiRateLimitDuration = previousDuration
		common.RedisEnabled = previousRedisEnabled
	})

	router := gin.New()
	api := router.Group("/api")
	api.Use(GlobalAPIRateLimit())
	api.POST("/oauth-server/token", func(c *gin.Context) { c.Status(http.StatusOK) })
	request := func() int {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/oauth-server/token", nil)
		req.RemoteAddr = "198.51.100.77:40000"
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, request())
	assert.Equal(t, http.StatusTooManyRequests, request())
}
