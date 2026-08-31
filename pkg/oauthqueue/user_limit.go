package oauthqueue

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-redis/redis/v8"
)

type UserDecision struct {
	Allowed    bool
	Limit      int
	Burst      int
	Remaining  int
	RetryAfter int64
}

var userLimitScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local capacity = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local duration_ms = tonumber(ARGV[3])
local rate = limit / duration_ms
local values = redis.call('HMGET', KEYS[1], 'tokens', 'updated_ms')
local tokens = tonumber(values[1])
local updated_ms = tonumber(values[2])
if tokens == nil or updated_ms == nil then
  tokens = capacity
  updated_ms = now_ms
end
if now_ms > updated_ms then
  tokens = math.min(capacity, tokens + (now_ms - updated_ms) * rate)
end
local allowed = 0
local retry_ms = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry_ms = math.ceil((1 - tokens) / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], math.ceil(duration_ms * 2))
return {allowed, math.floor(tokens), retry_ms}
`)

var userLimitRefundScript = redis.NewScript(`
local values = redis.call('HMGET', KEYS[1], 'tokens', 'updated_ms')
local tokens = tonumber(values[1])
if tokens == nil then return 0 end
tokens = math.min(tonumber(ARGV[1]), tokens + 1)
redis.call('HSET', KEYS[1], 'tokens', tokens)
return 1
`)

func (queue *Queue) AllowUserOperation(ctx context.Context, operation string, appID int, userID int) (UserDecision, error) {
	partition, key := queue.userLimitKey(operation, appID, userID)
	result, err := userLimitScript.Run(
		ctx,
		queue.shard(partition),
		[]string{key},
		queue.config.UserBurst,
		queue.config.UserLimit,
		queue.config.UserDuration.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return UserDecision{}, err
	}
	if len(result) != 3 {
		return UserDecision{}, fmt.Errorf("unexpected OAuth user limit response")
	}
	decision := UserDecision{
		Allowed:   result[0] == 1,
		Limit:     queue.config.UserLimit,
		Burst:     queue.config.UserBurst,
		Remaining: int(result[1]),
	}
	if !decision.Allowed {
		decision.RetryAfter = int64(math.Ceil(float64(result[2]) / float64(time.Second/time.Millisecond)))
		if decision.RetryAfter < 1 {
			decision.RetryAfter = 1
		}
	}
	return decision, nil
}

func (queue *Queue) RefundUserOperation(ctx context.Context, operation string, appID int, userID int) error {
	partition, key := queue.userLimitKey(operation, appID, userID)
	return userLimitRefundScript.Run(
		ctx,
		queue.shard(partition),
		[]string{key},
		queue.config.UserBurst,
	).Err()
}

func (queue *Queue) userLimitKey(operation string, appID int, userID int) (int, string) {
	principal := fmt.Sprintf("%s:%d:%d", operation, appID, userID)
	partition := queue.Partition(principal)
	tag := fmt.Sprintf("{%s-q-%03d}", queue.config.Namespace, partition)
	return partition, fmt.Sprintf("%s:%s:user:%s:%d:%d", queue.config.Namespace, tag, operation, appID, userID)
}
