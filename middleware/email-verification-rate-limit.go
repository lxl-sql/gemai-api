package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

const (
	EmailVerificationRateLimitMark        = "EV"
	EmailVerificationMaxRequests          = 2   // 30秒内同 IP 最多2次
	EmailVerificationDuration             = 30  // 30秒时间窗口
	EmailVerificationEmailMaxRequests     = 3   // 10分钟内同邮箱最多3次
	EmailVerificationEmailDurationSeconds = 600 // 10分钟时间窗口
)

func redisEmailVerificationRateLimiter(c *gin.Context) {
	if !checkRedisEmailVerificationLimit(c, "ip:"+c.ClientIP(), EmailVerificationMaxRequests, EmailVerificationDuration) {
		return
	}

	email := common.NormalizeEmail(c.Query("email"))
	if email != "" && !checkRedisEmailVerificationLimit(c, "email:"+email, EmailVerificationEmailMaxRequests, EmailVerificationEmailDurationSeconds) {
		return
	}

	c.Next()
}

func checkRedisEmailVerificationLimit(c *gin.Context, limiterKey string, maxRequests int, durationSeconds int) bool {
	ctx := context.Background()
	rdb := common.RDB
	key := "emailVerification:" + EmailVerificationRateLimitMark + ":" + limiterKey

	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		// fallback
		memoryEmailVerificationRateLimiter(c)
		return false
	}

	// 第一次设置键时设置过期时间
	if count == 1 {
		_ = rdb.Expire(ctx, key, time.Duration(durationSeconds)*time.Second).Err()
	}

	// 检查是否超出限制
	if count <= int64(maxRequests) {
		return true
	}

	// 获取剩余等待时间
	ttl, err := rdb.TTL(ctx, key).Result()
	waitSeconds := int64(durationSeconds)
	if err == nil && ttl > 0 {
		waitSeconds = int64(ttl.Seconds())
	}

	c.JSON(http.StatusTooManyRequests, gin.H{
		"success": false,
		"message": fmt.Sprintf("发送过于频繁，请等待 %d 秒后再试", waitSeconds),
	})
	c.Abort()
	return false
}

func memoryEmailVerificationRateLimiter(c *gin.Context) {
	ipKey := EmailVerificationRateLimitMark + ":ip:" + c.ClientIP()
	if !inMemoryRateLimiter.Request(ipKey, EmailVerificationMaxRequests, EmailVerificationDuration) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"message": "发送过于频繁，请稍后再试",
		})
		c.Abort()
		return
	}

	email := common.NormalizeEmail(c.Query("email"))
	if email != "" {
		emailKey := EmailVerificationRateLimitMark + ":email:" + email
		if !inMemoryRateLimiter.Request(emailKey, EmailVerificationEmailMaxRequests, EmailVerificationEmailDurationSeconds) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"message": "该邮箱验证码发送过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
	}

	c.Next()
}

func EmailVerificationRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.RedisEnabled {
			redisEmailVerificationRateLimiter(c)
		} else {
			inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
			memoryEmailVerificationRateLimiter(c)
		}
	}
}
