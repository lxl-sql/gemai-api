package oauthqueue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOAuthQueueIntegrationFixture(t *testing.T) (*Queue, redis.UniversalClient) {
	t.Helper()
	redisURL := os.Getenv("RATE_LIMIT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("RATE_LIMIT_REDIS_TEST_URL is not configured")
	}
	config := DefaultConfig()
	config.Enabled = true
	config.Namespace = "oauth_test_" + common.GetRandomString(10)
	config.Partitions = 4
	config.Capacity = 100
	config.MinConcurrency = 1
	config.InitialConcurrency = 2
	config.MaxConcurrency = 4
	config.LeaseTTL = 5 * time.Second
	config.JobTTL = time.Minute
	config.ResultTTL = 2 * time.Minute
	config.QueueRedisURLs = []string{redisURL}
	clients, err := NewClientSet(context.Background(), config, nil)
	require.NoError(t, err)
	t.Cleanup(clients.Close)
	queue, err := New(config, clients)
	require.NoError(t, err)
	t.Cleanup(func() {
		client := clients.Shards[0]
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(context.Background(), cursor, config.Namespace+":*", 100).Result()
			if scanErr != nil {
				break
			}
			if len(keys) > 0 {
				_ = client.Del(context.Background(), keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	})
	return queue, clients.Shards[0]
}

func TestOAuthQueueEnqueueClaimFinishAndPollIsolation(t *testing.T) {
	queue, _ := newOAuthQueueIntegrationFixture(t)
	deadline := time.Now().Add(time.Minute)
	created, err := queue.Enqueue(context.Background(), EnqueueInput{
		ID:        "exchange-1",
		Payload:   "encrypted-payload",
		PollHash:  "poll-hash",
		Deadline:  deadline,
		CleanupAt: deadline.Add(time.Minute),
	})
	require.NoError(t, err)
	assert.True(t, created)
	created, err = queue.Enqueue(context.Background(), EnqueueInput{
		ID:        "exchange-1",
		Payload:   "other-payload",
		PollHash:  "poll-hash",
		Deadline:  deadline,
		CleanupAt: deadline.Add(time.Minute),
	})
	require.NoError(t, err)
	assert.False(t, created)
	pendingSnapshot, err := queue.Snapshot(context.Background(), "exchange-1", "poll-hash")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, pendingSnapshot.Status)
	assert.Empty(t, pendingSnapshot.Error)
	assert.Zero(t, pendingSnapshot.Attempt)

	permit, err := queue.AcquirePermit(context.Background(), "worker-a")
	require.NoError(t, err)
	require.NotNil(t, permit)
	partition := queue.Partition("exchange-1")
	job, err := queue.Claim(context.Background(), partition, "worker-a", permit.Fence)
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, "encrypted-payload", job.Payload)
	assert.Equal(t, 1, job.Attempt)
	require.NoError(t, queue.Finish(context.Background(), job, StatusSucceeded, "encrypted-result", ""))
	require.NoError(t, queue.ReleasePermit(context.Background(), permit))

	snapshot, err := queue.Snapshot(context.Background(), "exchange-1", "poll-hash")
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, snapshot.Status)
	assert.Equal(t, "encrypted-result", snapshot.Result)
	assert.Empty(t, snapshot.Error)
	assert.Equal(t, 1, snapshot.Attempt)
	_, err = queue.Snapshot(context.Background(), "exchange-1", "wrong-poll-hash")
	require.ErrorIs(t, err, ErrPollTokenInvalid)
}

func TestOAuthQueueFailedSnapshotPreservesErrorAndAttempt(t *testing.T) {
	queue, _ := newOAuthQueueIntegrationFixture(t)
	deadline := time.Now().Add(time.Minute)
	created, err := queue.Enqueue(context.Background(), EnqueueInput{
		ID:        "exchange-failed",
		Payload:   "encrypted-payload",
		PollHash:  "poll-hash",
		Deadline:  deadline,
		CleanupAt: deadline.Add(time.Minute),
	})
	require.NoError(t, err)
	assert.True(t, created)

	permit, err := queue.AcquirePermit(context.Background(), "worker-a")
	require.NoError(t, err)
	require.NotNil(t, permit)
	partition := queue.Partition("exchange-failed")
	job, err := queue.Claim(context.Background(), partition, "worker-a", permit.Fence)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.NoError(t, queue.Finish(context.Background(), job, StatusFailed, "", "client is unavailable"))
	require.NoError(t, queue.ReleasePermit(context.Background(), permit))

	snapshot, err := queue.Snapshot(context.Background(), "exchange-failed", "poll-hash")
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, snapshot.Status)
	assert.Empty(t, snapshot.Result)
	assert.Equal(t, "client is unavailable", snapshot.Error)
	assert.Equal(t, 1, snapshot.Attempt)
}

func TestOAuthQueueGlobalPermitAndExpiredLeaseReclaim(t *testing.T) {
	queue, client := newOAuthQueueIntegrationFixture(t)
	keys := queue.coordinatorKeys()
	require.NoError(t, client.Set(context.Background(), keys[1], queue.config.MaxConcurrency+10, 0).Err())
	first, err := queue.AcquirePermit(context.Background(), "worker-a")
	require.NoError(t, err)
	require.NotNil(t, first)
	target, err := client.Get(context.Background(), keys[1]).Int()
	require.NoError(t, err)
	assert.Equal(t, queue.config.MaxConcurrency, target)
	require.NoError(t, client.Set(context.Background(), keys[1], queue.config.InitialConcurrency, 0).Err())
	second, err := queue.AcquirePermit(context.Background(), "worker-b")
	require.NoError(t, err)
	require.NotNil(t, second)
	third, err := queue.AcquirePermit(context.Background(), "worker-c")
	require.NoError(t, err)
	assert.Nil(t, third)
	require.NoError(t, queue.ReleasePermit(context.Background(), first))
	third, err = queue.AcquirePermit(context.Background(), "worker-c")
	require.NoError(t, err)
	require.NotNil(t, third)

	deadline := time.Now().Add(time.Minute)
	_, err = queue.Enqueue(context.Background(), EnqueueInput{
		ID:        "exchange-reclaim",
		Payload:   "payload",
		PollHash:  "poll",
		Deadline:  deadline,
		CleanupAt: deadline.Add(time.Minute),
	})
	require.NoError(t, err)
	partition := queue.Partition("exchange-reclaim")
	job, err := queue.Claim(context.Background(), partition, "worker-c", third.Fence)
	require.NoError(t, err)
	require.NotNil(t, job)
	partitionKeys := queue.partitionKeys(partition)
	require.NoError(t, client.ZAdd(context.Background(), partitionKeys[1], &redis.Z{
		Score:  float64(time.Now().Add(-time.Second).UnixMilli()),
		Member: job.ID,
	}).Err())
	reclaimed, err := queue.Reclaim(context.Background(), partition, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reclaimed)
	require.NoError(t, queue.ReleasePermit(context.Background(), second))
	replacement, err := queue.AcquirePermit(context.Background(), "worker-d")
	require.NoError(t, err)
	require.NotNil(t, replacement)
	reclaimedJob, err := queue.Claim(context.Background(), partition, "worker-d", replacement.Fence)
	require.NoError(t, err)
	require.NotNil(t, reclaimedJob)
	require.ErrorIs(t, queue.Finish(context.Background(), job, StatusSucceeded, "stale-result", ""), ErrLeaseLost)
	require.NoError(t, queue.Finish(context.Background(), reclaimedJob, StatusSucceeded, "current-result", ""))
	require.NoError(t, queue.ReleasePermit(context.Background(), third))
	require.NoError(t, queue.ReleasePermit(context.Background(), replacement))
}

func TestOAuthQueueUserLimitsAreIsolatedByApplicationAndUser(t *testing.T) {
	queue, _ := newOAuthQueueIntegrationFixture(t)
	queue.config.UserLimit = 1
	queue.config.UserBurst = 1
	queue.config.UserDuration = time.Minute

	first, err := queue.AllowUserOperation(context.Background(), "token", 1, 1)
	require.NoError(t, err)
	assert.True(t, first.Allowed)
	second, err := queue.AllowUserOperation(context.Background(), "token", 1, 1)
	require.NoError(t, err)
	assert.False(t, second.Allowed)
	assert.Greater(t, second.RetryAfter, int64(0))
	require.NoError(t, queue.RefundUserOperation(context.Background(), "token", 1, 1))
	afterRefund, err := queue.AllowUserOperation(context.Background(), "token", 1, 1)
	require.NoError(t, err)
	assert.True(t, afterRefund.Allowed)
	otherUser, err := queue.AllowUserOperation(context.Background(), "token", 1, 2)
	require.NoError(t, err)
	assert.True(t, otherUser.Allowed)
	otherApp, err := queue.AllowUserOperation(context.Background(), "token", 2, 1)
	require.NoError(t, err)
	assert.True(t, otherApp.Allowed)
	otherOperation, err := queue.AllowUserOperation(context.Background(), "userinfo", 1, 1)
	require.NoError(t, err)
	assert.True(t, otherOperation.Allowed)
}

func TestOAuthQueueMaintenanceExpiresPendingJobsWithoutAWorker(t *testing.T) {
	queue, _ := newOAuthQueueIntegrationFixture(t)
	deadline := time.Now().Add(-time.Second)
	created, err := queue.Enqueue(context.Background(), EnqueueInput{
		ID:        "exchange-expired-pending",
		Payload:   "encrypted-payload",
		PollHash:  "poll-hash",
		Deadline:  deadline,
		CleanupAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	assert.True(t, created)
	partition := queue.Partition("exchange-expired-pending")
	expired, err := queue.ExpirePending(context.Background(), partition, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), expired)
	snapshot, err := queue.Snapshot(context.Background(), "exchange-expired-pending", "poll-hash")
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, snapshot.Status)
}

func TestOAuthQueueAdaptiveConcurrencyUsesNegativeFeedback(t *testing.T) {
	queue, client := newOAuthQueueIntegrationFixture(t)
	ctx := context.Background()
	keys := queue.coordinatorKeys()
	require.NoError(t, client.Set(ctx, keys[1], queue.config.MaxConcurrency, 0).Err())
	now, err := client.Time(ctx).Result()
	require.NoError(t, err)
	bucket := now.UnixMilli()/queue.config.AdjustInterval.Milliseconds() - 1
	require.NoError(t, client.HSet(ctx, queue.metricKey(bucket), map[string]interface{}{
		"total":  10,
		"le_500": 10,
	}).Err())

	adjustment, err := queue.AdjustConcurrency(ctx, "adaptive-test")
	require.NoError(t, err)
	assert.Equal(t, queue.config.MaxConcurrency, adjustment.Previous)
	assert.Equal(t, queue.config.MaxConcurrency/2, adjustment.Next)
	target, err := client.Get(ctx, keys[1]).Int()
	require.NoError(t, err)
	assert.Equal(t, queue.config.MaxConcurrency/2, target)
}

func TestOAuthQueueAdaptiveConcurrencyDoesNotHideExtremeLatency(t *testing.T) {
	queue, client := newOAuthQueueIntegrationFixture(t)
	queue.config.TargetP95 = 10 * time.Second
	ctx := context.Background()
	keys := queue.coordinatorKeys()
	require.NoError(t, client.Set(ctx, keys[1], queue.config.MaxConcurrency, 0).Err())
	now, err := client.Time(ctx).Result()
	require.NoError(t, err)
	bucket := now.UnixMilli()/queue.config.AdjustInterval.Milliseconds() - 1
	require.NoError(t, client.HSet(ctx, queue.metricKey(bucket), map[string]interface{}{
		"total":    1,
		"gt_10000": 1,
	}).Err())

	adjustment, err := queue.AdjustConcurrency(ctx, "extreme-latency-test")
	require.NoError(t, err)
	assert.Equal(t, 11*time.Second, adjustment.P95)
	assert.Equal(t, queue.config.MaxConcurrency/2, adjustment.Next)
}

func TestOAuthQueueConfigRejectsConflictingRedisModes(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.QueueRedisURLs = []string{"redis://127.0.0.1:6379"}
	config.ClusterAddrs = []string{"127.0.0.1:7000"}
	require.Error(t, config.Validate())

	config = DefaultConfig()
	config.Enabled = true
	config.Namespace = "invalid:{slot}"
	require.Error(t, config.Validate())

	config = DefaultConfig()
	config.Enabled = true
	config.Partitions = 256
	config.Capacity = 100
	require.Error(t, config.Validate())

	config = DefaultConfig()
	config.Enabled = true
	config.Capacity = 100
	config.WorkersPerInstance = 101
	require.Error(t, config.Validate())

	requested, wait := ParsePreferWait("respond-async, wait=99", 55*time.Second)
	assert.True(t, requested)
	assert.Equal(t, 55*time.Second, wait)
	requested, wait = ParsePreferWait("x-respond-async=true, wait=1", 55*time.Second)
	assert.False(t, requested)
	assert.Zero(t, wait)
}

func TestOAuthQueueOperationalDefaultsAreNotEnvironmentTuning(t *testing.T) {
	assert.False(t, DefaultConfig().Enabled)
	t.Setenv("OAUTH_QUEUE_ENABLE", "true")
	t.Setenv("OAUTH_QUEUE_CAPACITY", "100")
	t.Setenv("OAUTH_QUEUE_MAX_CONCURRENCY", "1")
	t.Setenv("OAUTH_QUEUE_JOB_TTL_SECONDS", "1")
	t.Setenv("OAUTH_QUEUE_REDIS_URLS", "")
	t.Setenv("OAUTH_QUEUE_REDIS_CLUSTER_ADDRS", "")
	t.Setenv("OAUTH_QUEUE_REDIS_SENTINEL_ADDRS", "")

	config, err := LoadConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, config.Enabled)
	defaults := DefaultConfig()
	assert.Equal(t, defaults.Capacity, config.Capacity)
	assert.Equal(t, defaults.MaxConcurrency, config.MaxConcurrency)
	assert.Equal(t, defaults.JobTTL, config.JobTTL)
}
