package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCacheUsesImmutablePolicyForRsbuildAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cache())
	router.GET("/*path", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		path string
		want string
	}{
		{path: "/static/js/index.abc123.js", want: "public, max-age=31536000, immutable"},
		{path: "/assets/index-abc123.js", want: "public, max-age=31536000, immutable"},
		{path: "/dashboard", want: "no-cache"},
	}

	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		router.ServeHTTP(recorder, request)
		assert.Equal(t, test.want, recorder.Header().Get("Cache-Control"))
	}
}

func TestGlobalWebRateLimitSkipsStaticAssetsWhenRedisUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalEnabled := common.GlobalWebRateLimitEnable
	originalRedisEnabled := common.RedisEnabled
	originalRedis := common.RDB
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable = originalEnabled
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRedis
	})

	common.GlobalWebRateLimitEnable = true
	common.RedisEnabled = true
	common.RDB = nil

	router := gin.New()
	router.Use(GlobalWebRateLimit())
	router.GET("/static/js/app.js", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}
