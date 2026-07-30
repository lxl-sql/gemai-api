package service

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestNotificationPermitFallsBackWhenRedisClientIsUnavailable(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = true
	common.RDB = nil
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91001
	const notifyType = "redis-unavailable-fallback"
	t.Cleanup(func() {
		notifyLimitStore.Delete(notificationLimitKey(userId, notifyType))
	})

	_, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestNotificationPermitFallsBackWhenRedisOperationsFail(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91002
	const notifyType = "redis-operation-fallback"
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	_, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)

	_, allowed, err = acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestNotificationPermitMemoryFallbackIsAtomic(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	common.RDB = nil
	constant.NotifyLimitCount = 10
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91003
	const notifyType = "memory-fallback-atomic"
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	const attempts = 100
	var allowedCount atomic.Int32
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, allowed, err := acquireNotificationPermit(userId, notifyType)
			if err != nil {
				errs <- err
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(constant.NotifyLimitCount), allowedCount.Load())
}

func TestNotificationPermitReleaseRestoresUnusedCapacity(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91004
	const notifyType = "released-permit"
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	permit, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotNil(t, permit)
	permit.Release()

	_, allowed, err = acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestNotificationPermitDoesNotReleaseNewMemoryWindow(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91007
	const notifyType = "stale-memory-permit"
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	permit, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotNil(t, permit)

	newWindow := limitCount{Count: 1, Timestamp: permit.memoryWindow.Add(time.Second)}
	notifyLimitStore.Store(key, newWindow)
	permit.Release()

	stored, ok := notifyLimitStore.Load(key)
	require.True(t, ok)
	require.Equal(t, newWindow, stored.(limitCount))
}

func TestNotifyUserWithoutConfiguredChannelDoesNotConsumeLimit(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91005
	const notifyType = dto.NotifyTypeTokenSecurity
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	require.NoError(t, NotifyUser(
		userId,
		"",
		dto.UserSetting{NotifyType: dto.NotifyTypeEmail},
		dto.NewNotify(notifyType, "security", "security", nil),
	))

	_, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestNotifyUserReleasesLimitWhenDeliveryFails(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91008
	const notifyType = dto.NotifyTypeTokenSecurity
	key := notificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(key)
	})

	err := NotifyUser(
		userId,
		"",
		dto.UserSetting{
			NotifyType: dto.NotifyTypeBark,
			BarkUrl:    "://invalid",
		},
		dto.NewNotify(notifyType, "security", "security", nil),
	)
	require.Error(t, err)

	_, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestNotificationPermitRedisIntegration(t *testing.T) {
	redisURL := os.Getenv("TOKEN_SECURITY_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("TOKEN_SECURITY_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = true
	common.RDB = client
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 1
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})

	const userId = 91006
	notifyType := "redis-permit-" + common.NewRequestId()
	memoryKey := notificationLimitKey(userId, notifyType)
	redisKey := redisNotificationLimitKey(userId, notifyType)
	t.Cleanup(func() {
		notifyLimitStore.Delete(memoryKey)
		require.NoError(t, client.Del(context.Background(), redisKey).Err())
	})

	permit, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotNil(t, permit)
	permit.Release()

	_, allowed, err = acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
	_, allowed, err = acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.False(t, allowed)

	notifyLimitStore.Delete(memoryKey)
	require.NoError(t, client.Del(context.Background(), redisKey).Err())
	stalePermit, allowed, err := acquireNotificationPermit(userId, notifyType)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotNil(t, stalePermit)

	newWindow := limitCount{Count: 1, Timestamp: stalePermit.memoryWindow.Add(time.Second)}
	notifyLimitStore.Store(memoryKey, newWindow)
	require.NoError(t, client.Del(context.Background(), redisKey).Err())
	require.NoError(t, client.Set(context.Background(), redisKey, "1", 2*time.Minute).Err())
	stalePermit.Release()

	stored, ok := notifyLimitStore.Load(memoryKey)
	require.True(t, ok)
	require.Equal(t, newWindow, stored.(limitCount))
	redisCount, err := client.Get(context.Background(), redisKey).Int()
	require.NoError(t, err)
	require.Equal(t, 1, redisCount)
}
