package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitGateFallsBackToMemoryWhenRedisIsUnavailable(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedis := common.RDB
	previousLogTime := rateLimitDegradeLoggedAtUnix.Load()

	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:0",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		PoolTimeout:  10 * time.Millisecond,
		MaxRetries:   -1,
	})
	common.RedisEnabled = true
	common.RDB = redisClient
	inMemoryRateLimiter.Init(0)
	rateLimitDegradeLoggedAtUnix.Store(0)
	memoryKey := "test:" + common.GetUUID()
	t.Cleanup(func() {
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedis
		rateLimitDegradeLoggedAtUnix.Store(previousLogTime)
	})

	gin.SetMode(gin.TestMode)
	newContext := func() *gin.Context {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		return ctx
	}

	first := newContext()
	rateLimitGate(first, "rateLimit:"+memoryKey, memoryKey, 1, 60)
	assert.False(t, first.IsAborted())
	assert.Equal(t, http.StatusOK, first.Writer.Status())

	second := newContext()
	rateLimitGate(second, "rateLimit:"+memoryKey, memoryKey, 1, 60)
	require.True(t, second.IsAborted())
	assert.Equal(t, http.StatusTooManyRequests, second.Writer.Status())
}

func TestRateLimitGateUsesAtomicRedisWindow(t *testing.T) {
	redisURL := os.Getenv("TOKEN_SECURITY_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("TOKEN_SECURITY_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	redisClient := redis.NewClient(options)
	ctx := context.Background()
	require.NoError(t, redisClient.Ping(ctx).Err())

	previousRedisEnabled := common.RedisEnabled
	previousRedis := common.RDB
	common.RedisEnabled = true
	common.RDB = redisClient
	redisKey := "rateLimit:test:" + common.GetUUID()
	t.Cleanup(func() {
		_ = redisClient.Del(ctx, redisKey).Err()
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedis
	})

	newContext := func() *gin.Context {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		return ctx
	}

	first := newContext()
	rateLimitGate(first, redisKey, "unused-memory-key", 2, 60)
	assert.False(t, first.IsAborted())

	second := newContext()
	rateLimitGate(second, redisKey, "unused-memory-key", 2, 60)
	assert.False(t, second.IsAborted())

	third := newContext()
	rateLimitGate(third, redisKey, "unused-memory-key", 2, 60)
	require.True(t, third.IsAborted())
	assert.Equal(t, http.StatusTooManyRequests, third.Writer.Status())
}
