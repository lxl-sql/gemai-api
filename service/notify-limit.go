package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

// notifyLimitStore is used for in-memory rate limiting when Redis is unavailable.
var (
	notifyLimitStore          sync.Map
	notifyLimitMu             sync.Mutex
	cleanupOnce               sync.Once
	reserveNotificationScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
if limit <= 0 then return 0 end
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if current >= limit then return 0 end
current = redis.call('INCR', KEYS[1])
if current == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
return now_ms + redis.call('PTTL', KEYS[1])
`)
	releaseNotificationScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if current <= 0 then return 0 end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then return 0 end
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
if math.abs((now_ms + ttl) - tonumber(ARGV[1])) > 10 then return 0 end
if current == 1 then
  redis.call('DEL', KEYS[1])
  return 1
end
redis.call('DECR', KEYS[1])
return 1
`)
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

type notificationPermit struct {
	userId         int
	notifyType     string
	memoryReserved bool
	redisReserved  bool
	memoryWindow   time.Time
	redisWindowEnd int64
	releaseOnce    sync.Once
}

func getDuration() time.Duration {
	minute := constant.NotificationLimitDurationMinute
	return time.Duration(minute) * time.Minute
}

func notificationLimitKey(userId int, notifyType string) string {
	return fmt.Sprintf("%d:%s", userId, notifyType)
}

func redisNotificationLimitKey(userId int, notifyType string) string {
	return "notify_limit:" + notificationLimitKey(userId, notifyType)
}

// startCleanupTask starts a background task to clean up expired entries
func startCleanupTask() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Hour)
			now := time.Now()
			notifyLimitMu.Lock()
			notifyLimitStore.Range(func(key, value interface{}) bool {
				if limit, ok := value.(limitCount); ok {
					if now.Sub(limit.Timestamp) >= getDuration() {
						notifyLimitStore.Delete(key)
					}
				}
				return true
			})
			notifyLimitMu.Unlock()
		}
	})
}

func acquireNotificationPermit(userId int, notifyType string) (*notificationPermit, bool, error) {
	memoryWindow, allowed := reserveMemoryNotification(userId, notifyType)
	if !allowed {
		return nil, false, nil
	}
	permit := &notificationPermit{
		userId:         userId,
		notifyType:     notifyType,
		memoryReserved: true,
		memoryWindow:   memoryWindow,
	}
	if common.RedisEnabled && common.RDB != nil {
		redisWindowEnd, redisAllowed, err := checkRedisLimit(userId, notifyType)
		if err == nil {
			if !redisAllowed {
				permit.releaseMemory()
				return nil, false, nil
			}
			permit.redisReserved = true
			permit.redisWindowEnd = redisWindowEnd
			return permit, true, nil
		}
		common.SysLog(fmt.Sprintf(
			"notification limit Redis unavailable; using in-memory fallback user_id=%d type=%s error=%v",
			userId,
			notifyType,
			err,
		))
	}
	return permit, true, nil
}

func checkRedisLimit(userId int, notifyType string) (int64, bool, error) {
	windowEnd, err := reserveNotificationScript.Run(
		context.Background(),
		common.RDB,
		[]string{redisNotificationLimitKey(userId, notifyType)},
		constant.NotifyLimitCount,
		getDuration().Milliseconds(),
	).Int64()
	if err != nil {
		return 0, false, fmt.Errorf("failed to update notification limit: %w", err)
	}
	return windowEnd, windowEnd > 0, nil
}

func reserveMemoryNotification(userId int, notifyType string) (time.Time, bool) {
	// Ensure cleanup task is started
	cleanupOnce.Do(startCleanupTask)

	notifyLimitMu.Lock()
	defer notifyLimitMu.Unlock()

	key := notificationLimitKey(userId, notifyType)
	now := time.Now()

	var currentLimit limitCount
	if value, ok := notifyLimitStore.Load(key); ok {
		currentLimit = value.(limitCount)
		if now.Sub(currentLimit.Timestamp) >= getDuration() {
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
	} else {
		currentLimit = limitCount{Count: 0, Timestamp: now}
	}

	if currentLimit.Count >= constant.NotifyLimitCount {
		return time.Time{}, false
	}
	currentLimit.Count++
	notifyLimitStore.Store(key, currentLimit)
	return currentLimit.Timestamp, true
}

func (permit *notificationPermit) releaseMemory() {
	if permit == nil || !permit.memoryReserved {
		return
	}
	notifyLimitMu.Lock()
	defer notifyLimitMu.Unlock()
	key := notificationLimitKey(permit.userId, permit.notifyType)
	if value, ok := notifyLimitStore.Load(key); ok {
		currentLimit := value.(limitCount)
		if !currentLimit.Timestamp.Equal(permit.memoryWindow) {
			permit.memoryReserved = false
			return
		}
		if currentLimit.Count <= 1 {
			notifyLimitStore.Delete(key)
		} else {
			currentLimit.Count--
			notifyLimitStore.Store(key, currentLimit)
		}
	}
	permit.memoryReserved = false
}

func (permit *notificationPermit) Release() {
	if permit == nil {
		return
	}
	permit.releaseOnce.Do(func() {
		permit.releaseMemory()
		if !permit.redisReserved || !common.RedisEnabled || common.RDB == nil {
			return
		}
		released, err := releaseNotificationScript.Run(
			context.Background(),
			common.RDB,
			[]string{redisNotificationLimitKey(permit.userId, permit.notifyType)},
			permit.redisWindowEnd,
		).Int()
		if err != nil {
			common.SysLog(fmt.Sprintf(
				"failed to release unused notification permit user_id=%d type=%s error=%v",
				permit.userId,
				permit.notifyType,
				err,
			))
		} else if released == 0 && common.DebugEnabled {
			common.SysLog(fmt.Sprintf(
				"unused notification permit was not released because its window changed user_id=%d type=%s",
				permit.userId,
				permit.notifyType,
			))
		}
		permit.redisReserved = false
	})
}
