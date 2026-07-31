package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

// rateLimitRedisTimeout 限流只是请求路径上的旁路检查，不允许因为 Redis 变慢而拖住整个请求。
// 超时后降级到内存限流器，而不是把等待时间累加到响应里。
const rateLimitRedisTimeout = 500 * time.Millisecond

// rateLimitDegradeLoggedAtUnix 用于把降级日志压到每 5 秒一条。Redis 故障时每个请求
// 都会走降级分支，逐条打日志本身就会成为新的故障放大器。
var rateLimitDegradeLoggedAtUnix atomic.Int64

// allowRateLimitScript 在一次往返内完成"取长度 / 比较最旧时间戳 / 入队 / 续期"。
// 旧实现把这些拆成 3~4 条串行命令，每条都要单独从连接池借一次连接；池打满时单个请求
// 会在池上排队 3~4 个 PoolTimeout，这正是线上 /api/status 响应 12s 的来源。
// 返回 1 表示放行，0 表示超出限额。
var allowRateLimitScript = redis.NewScript(`
local key = KEYS[1]
local max = tonumber(ARGV[1])
local duration = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local expiration = tonumber(ARGV[4])

if redis.call('LLEN', key) < max then
	redis.call('LPUSH', key, now)
	redis.call('EXPIRE', key, expiration)
	return 1
end

-- 列表按 [新 --> 旧] 排列，下标 -1 是窗口内最旧的一次请求。
-- 旧版本写入的是 RFC3339 字符串，tonumber 会得到 nil，此时按已过期处理并覆盖，
-- 让存量 key 自然自愈成新格式。
local oldest = tonumber(redis.call('LINDEX', key, -1))
if oldest ~= nil and now - oldest < duration then
	redis.call('EXPIRE', key, expiration)
	return 0
end

redis.call('LPUSH', key, now)
redis.call('LTRIM', key, 0, max - 1)
redis.call('EXPIRE', key, expiration)
return 1
`)

// rateLimitGate 判定并在超限时中断请求。Redis 不可用时降级到内存限流器：
// 限流失败绝不能升级成 5xx，否则一次 Redis 抖动就会打挂全部 /api 路由。
func rateLimitGate(c *gin.Context, redisKey string, memoryKey string, maxRequestNum int, duration int64) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), rateLimitRedisTimeout)
		result, err := allowRateLimitScript.Run(ctx, common.RDB, []string{redisKey},
			maxRequestNum, duration, time.Now().Unix(),
			int64(common.RateLimitKeyExpirationDuration.Seconds()),
		).Int()
		cancel()
		if err == nil {
			if result != 1 {
				c.Status(http.StatusTooManyRequests)
				c.Abort()
			}
			return
		}
		if !errors.Is(err, context.Canceled) {
			now := time.Now().Unix()
			if last := rateLimitDegradeLoggedAtUnix.Load(); now-last >= 5 &&
				rateLimitDegradeLoggedAtUnix.CompareAndSwap(last, now) {
				common.SysError("rate limit degraded to in-memory limiter: " + err.Error())
			}
		}
	}
	if !inMemoryRateLimiter.Request(memoryKey, maxRequestNum, duration) {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
	}
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	// 内存限流器同时是 Redis 故障时的降级路径，两种模式下都要初始化。
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		rateLimitGate(c, "rateLimit:"+mark+ip, mark+ip, maxRequestNum, duration)
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	if common.GlobalWebRateLimitEnable {
		limiter := rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
		return func(c *gin.Context) {
			if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) &&
				common.IsFrontendAssetPath(c.Request.URL.Path) {
				c.Next()
				return
			}
			limiter(c)
		}
	}
	return defNext
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		if userId == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		rateLimitGate(c,
			fmt.Sprintf("rateLimit:%s:user:%d", mark, userId),
			fmt.Sprintf("%s:user:%d", mark, userId),
			maxRequestNum, duration)
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}
