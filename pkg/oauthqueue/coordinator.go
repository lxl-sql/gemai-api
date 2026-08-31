package oauthqueue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

type Permit struct {
	ID    string
	Fence int64
}

type Outcome struct {
	Duration time.Duration
	Failed   bool
	PoolWait bool
}

type ConcurrencyAdjustment struct {
	Previous int
	Next     int
	Total    int64
	Failed   int64
	PoolWait int64
	P95      time.Duration
}

var acquirePermitScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
local stored_target = redis.call('GET', KEYS[2])
local target = tonumber(stored_target)
if target == nil then target = tonumber(ARGV[1]) end
if target < tonumber(ARGV[2]) then target = tonumber(ARGV[2]) end
if target > tonumber(ARGV[3]) then target = tonumber(ARGV[3]) end
if stored_target == false or tonumber(stored_target) ~= target then redis.call('SET', KEYS[2], target) end
if redis.call('ZCARD', KEYS[1]) >= target then return {0, target, 0, 0} end
local fence = redis.call('INCR', KEYS[3])
local permit_id = ARGV[4] .. ':' .. tostring(fence)
local expires_ms = now_ms + tonumber(ARGV[5])
redis.call('ZADD', KEYS[1], expires_ms, permit_id)
return {1, target, fence, expires_ms, permit_id}
`)

var renewPermitScript = redis.NewScript(`
if redis.call('ZSCORE', KEYS[1], ARGV[1]) == false then return 0 end
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZADD', KEYS[1], now_ms + tonumber(ARGV[2]), ARGV[1])
return 1
`)

var renewLeaderScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

func (queue *Queue) AcquirePermit(ctx context.Context, owner string) (*Permit, error) {
	keys := queue.coordinatorKeys()
	values, err := acquirePermitScript.Run(
		ctx,
		queue.clients.Coordinator,
		[]string{keys[0], keys[1], keys[2]},
		queue.config.InitialConcurrency,
		queue.config.MinConcurrency,
		queue.config.MaxConcurrency,
		owner,
		queue.config.LeaseTTL.Milliseconds(),
	).Slice()
	if err != nil {
		return nil, err
	}
	if len(values) < 4 {
		return nil, fmt.Errorf("unexpected OAuth permit response")
	}
	allowed, _ := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
	if allowed != 1 {
		return nil, nil
	}
	fence, err := strconv.ParseInt(fmt.Sprint(values[2]), 10, 64)
	if err != nil {
		return nil, err
	}
	permitID := ""
	if len(values) > 4 {
		permitID = fmt.Sprint(values[4])
	}
	return &Permit{ID: permitID, Fence: fence}, nil
}

func (queue *Queue) RenewPermit(ctx context.Context, permit *Permit) error {
	if permit == nil || permit.ID == "" {
		return ErrLeaseLost
	}
	keys := queue.coordinatorKeys()
	result, err := renewPermitScript.Run(
		ctx,
		queue.clients.Coordinator,
		[]string{keys[0]},
		permit.ID,
		queue.config.LeaseTTL.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (queue *Queue) ReleasePermit(ctx context.Context, permit *Permit) error {
	if permit == nil || permit.ID == "" {
		return nil
	}
	return queue.clients.Coordinator.ZRem(ctx, queue.coordinatorKeys()[0], permit.ID).Err()
}

func (queue *Queue) RecordOutcome(ctx context.Context, outcome Outcome) error {
	now, err := queue.clients.Coordinator.Time(ctx).Result()
	if err != nil {
		return err
	}
	bucket := now.UnixMilli() / queue.config.AdjustInterval.Milliseconds()
	key := queue.metricKey(bucket)
	pipe := queue.clients.Coordinator.Pipeline()
	pipe.HIncrBy(ctx, key, "total", 1)
	pipe.HIncrBy(ctx, key, "duration_us", outcome.Duration.Microseconds())
	if outcome.Failed {
		pipe.HIncrBy(ctx, key, "failed", 1)
	}
	if outcome.PoolWait {
		pipe.HIncrBy(ctx, key, "pool_wait", 1)
	}
	millis := outcome.Duration.Milliseconds()
	switch {
	case millis <= 50:
		pipe.HIncrBy(ctx, key, "le_50", 1)
	case millis <= 100:
		pipe.HIncrBy(ctx, key, "le_100", 1)
	case millis <= 200:
		pipe.HIncrBy(ctx, key, "le_200", 1)
	case millis <= 500:
		pipe.HIncrBy(ctx, key, "le_500", 1)
	case millis <= 1000:
		pipe.HIncrBy(ctx, key, "le_1000", 1)
	case millis <= 2000:
		pipe.HIncrBy(ctx, key, "le_2000", 1)
	case millis <= 5000:
		pipe.HIncrBy(ctx, key, "le_5000", 1)
	case millis <= 10000:
		pipe.HIncrBy(ctx, key, "le_10000", 1)
	default:
		pipe.HIncrBy(ctx, key, "gt_10000", 1)
	}
	pipe.Expire(ctx, key, 3*queue.config.AdjustInterval)
	_, err = pipe.Exec(ctx)
	return err
}

func (queue *Queue) AdjustConcurrency(ctx context.Context, owner string) (ConcurrencyAdjustment, error) {
	keys := queue.coordinatorKeys()
	leaseTTL := 3 * queue.config.AdjustInterval
	acquired, err := queue.clients.Coordinator.SetNX(ctx, keys[3], owner, leaseTTL).Result()
	if err != nil {
		return ConcurrencyAdjustment{}, err
	}
	if !acquired {
		result, renewErr := renewLeaderScript.Run(
			ctx,
			queue.clients.Coordinator,
			[]string{keys[3]},
			owner,
			leaseTTL.Milliseconds(),
		).Int()
		if renewErr != nil {
			return ConcurrencyAdjustment{}, renewErr
		}
		if result != 1 {
			return ConcurrencyAdjustment{}, nil
		}
	}
	now, err := queue.clients.Coordinator.Time(ctx).Result()
	if err != nil {
		return ConcurrencyAdjustment{}, err
	}
	currentBucket := now.UnixMilli() / queue.config.AdjustInterval.Milliseconds()
	metrics, err := queue.clients.Coordinator.HGetAll(
		ctx,
		queue.metricKey(currentBucket-1),
	).Result()
	if err != nil {
		return ConcurrencyAdjustment{}, err
	}
	target, err := queue.clients.Coordinator.Get(ctx, keys[1]).Int()
	if err == redis.Nil {
		target = queue.config.InitialConcurrency
	} else if err != nil {
		return ConcurrencyAdjustment{}, err
	}
	if target < queue.config.MinConcurrency {
		target = queue.config.MinConcurrency
	}
	if target > queue.config.MaxConcurrency {
		target = queue.config.MaxConcurrency
	}
	total := parseMetric(metrics, "total")
	failed := parseMetric(metrics, "failed")
	poolWait := parseMetric(metrics, "pool_wait")
	p95 := histogramP95(metrics, total)
	adjustment := ConcurrencyAdjustment{
		Previous: target,
		Next:     target,
		Total:    total,
		Failed:   failed,
		PoolWait: poolWait,
		P95:      p95,
	}
	next := target
	if failed > 0 || poolWait > 0 || p95 > queue.config.TargetP95 {
		next = target / 2
		if next < queue.config.MinConcurrency {
			next = queue.config.MinConcurrency
		}
	} else if total > 0 && p95 > 0 && p95 <= queue.config.TargetP95/2 {
		next = target + queue.config.IncreaseStep
		if next > queue.config.MaxConcurrency {
			next = queue.config.MaxConcurrency
		}
	}
	if next != target {
		if err := queue.clients.Coordinator.Set(ctx, keys[1], next, 0).Err(); err != nil {
			return adjustment, err
		}
	}
	adjustment.Next = next
	return adjustment, nil
}

func parseMetric(metrics map[string]string, name string) int64 {
	value, _ := strconv.ParseInt(metrics[name], 10, 64)
	return value
}

func histogramP95(metrics map[string]string, total int64) time.Duration {
	if total <= 0 {
		return 0
	}
	threshold := (total*95 + 99) / 100
	var cumulative int64
	for _, bucket := range []struct {
		name     string
		duration time.Duration
	}{
		{"le_50", 50 * time.Millisecond},
		{"le_100", 100 * time.Millisecond},
		{"le_200", 200 * time.Millisecond},
		{"le_500", 500 * time.Millisecond},
		{"le_1000", time.Second},
		{"le_2000", 2 * time.Second},
		{"le_5000", 5 * time.Second},
		{"le_10000", 10 * time.Second},
		{"gt_10000", 11 * time.Second},
	} {
		cumulative += parseMetric(metrics, bucket.name)
		if cumulative >= threshold {
			return bucket.duration
		}
	}
	return 11 * time.Second
}

func (queue *Queue) coordinatorKeys() []string {
	tag := fmt.Sprintf("{%s-global}", queue.config.Namespace)
	prefix := queue.config.Namespace + ":" + tag + ":"
	return []string{
		prefix + "permits",
		prefix + "target",
		prefix + "fence",
		prefix + "leader",
	}
}

func (queue *Queue) metricKey(bucket int64) string {
	tag := fmt.Sprintf("{%s-global}", queue.config.Namespace)
	return fmt.Sprintf("%s:%s:metrics:%d", queue.config.Namespace, tag, bucket)
}
