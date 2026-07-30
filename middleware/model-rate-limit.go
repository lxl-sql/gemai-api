package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
)

var reserveRedisSuccessRequestScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local window_ms = tonumber(ARGV[1])
local max_count = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms - window_ms)
if redis.call('ZCARD', KEYS[1]) >= max_count then
  return 0
end
if redis.call('ZADD', KEYS[1], 'NX', now_ms, ARGV[3]) ~= 1 then
  return 0
end
redis.call('PEXPIRE', KEYS[1], window_ms * 2)
return 1
`)

var releaseRedisSuccessRequestScript = redis.NewScript(`
local removed = redis.call('ZREM', KEYS[1], ARGV[1])
if redis.call('ZCARD', KEYS[1]) == 0 then
  redis.call('DEL', KEYS[1])
end
return removed
`)

func reserveRedisSuccessRequest(
	ctx context.Context,
	rdb *redis.Client,
	key string,
	requestId string,
	maxCount int,
	duration int64,
) (bool, error) {
	if maxCount == 0 {
		return true, nil
	}
	allowed, err := reserveRedisSuccessRequestScript.Run(
		ctx,
		rdb,
		[]string{key},
		(time.Duration(duration) * time.Second).Milliseconds(),
		maxCount,
		requestId,
	).Int()
	return allowed == 1, err
}

func releaseRedisSuccessRequest(rdb *redis.Client, key string, requestId string) {
	if rdb == nil || key == "" || requestId == "" {
		return
	}
	if _, err := releaseRedisSuccessRequestScript.Run(
		context.Background(),
		rdb,
		[]string{key},
		requestId,
	).Result(); err != nil {
		common.SysLog("failed to release model success rate reservation: " + err.Error())
	}
}

// Redis限流处理器
func redisRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		ctx := c.Request.Context()
		rdb := common.RDB

		// 先原子预留成功请求槽位，失败响应再释放，保证多实例共享同一上限。
		successKey := fmt.Sprintf("rateLimit:%s:v2:%s", ModelRequestRateLimitSuccessCountMark, userId)
		requestId := c.GetString(common.RequestIdKey)
		if requestId == "" {
			requestId = common.NewRequestId()
		}
		allowed, err := reserveRedisSuccessRequest(
			ctx,
			rdb,
			successKey,
			requestId,
			successMaxCount,
			duration,
		)
		if err != nil {
			fmt.Println("检查成功请求数限制失败:", err.Error())
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if !allowed {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", duration/60, successMaxCount))
			return
		}
		successReserved := successMaxCount > 0
		defer func() {
			if successReserved && c.Writer.Status() >= http.StatusBadRequest {
				releaseRedisSuccessRequest(rdb, successKey, requestId)
			}
		}()

		//2.检查总请求数限制并记录总请求（当totalMaxCount为0时会自动跳过，使用令牌桶限流器
		if totalMaxCount > 0 {
			totalKey := fmt.Sprintf("rateLimit:%s", userId)
			// 初始化
			tb := limiter.New(ctx, rdb)
			allowed, err = tb.Allow(
				ctx,
				totalKey,
				limiter.WithCapacity(int64(totalMaxCount)*duration),
				limiter.WithRate(int64(totalMaxCount)),
				limiter.WithRequested(duration),
			)

			if err != nil {
				fmt.Println("检查总请求数限制失败:", err.Error())
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
				return
			}

			if !allowed {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", duration/60, totalMaxCount))
				return
			}
		}

		c.Next()
	}
}

// 内存限流处理器
func memoryRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	inMemoryRateLimiter.Init(time.Duration(duration) * time.Second)

	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		totalKey := ModelRequestRateLimitCountMark + userId
		successKey := ModelRequestRateLimitSuccessCountMark + userId

		// 1. 检查总请求数限制（当totalMaxCount为0时跳过）
		if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 2. 检查成功请求数限制
		// 使用一个临时key来检查限制，这样可以避免实际记录
		checkKey := successKey + "_check"
		if !inMemoryRateLimiter.Request(checkKey, successMaxCount, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 3. 处理请求
		c.Next()

		// 4. 如果请求成功，记录到实际的成功请求计数中
		if c.Writer.Status() < 400 {
			inMemoryRateLimiter.Request(successKey, successMaxCount, duration)
		}
	}
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if group == "" {
			group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}
		config := setting.GetModelRequestRateLimitConfig(group)
		if !config.Enabled {
			c.Next()
			return
		}
		duration := int64(config.DurationMinutes * 60)

		// 根据存储类型选择并执行限流处理器
		if common.RedisEnabled {
			redisRateLimitHandler(duration, config.TotalCount, config.SuccessCount)(c)
		} else {
			memoryRateLimitHandler(duration, config.TotalCount, config.SuccessCount)(c)
		}
	}
}
