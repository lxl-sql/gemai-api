package oauthqueue

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusUnknown    Status = "unknown"
	StatusExpired    Status = "expired"
)

var (
	ErrQueueFull        = errors.New("OAuth exchange queue is full")
	ErrJobNotFound      = errors.New("OAuth exchange job is not found")
	ErrPollTokenInvalid = errors.New("OAuth exchange poll token is invalid")
	ErrLeaseLost        = errors.New("OAuth exchange lease is lost")
)

type EnqueueInput struct {
	ID        string
	Payload   string
	PollHash  string
	Deadline  time.Time
	CleanupAt time.Time
}

type ClaimedJob struct {
	ID        string
	Payload   string
	Attempt   int
	Partition int
	Owner     string
	Fence     int64
}

type Snapshot struct {
	ID       string
	Status   Status
	Result   string
	Error    string
	Deadline time.Time
	Attempt  int
}

type Queue struct {
	config  Config
	clients *ClientSet
}

var enqueueScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local status = redis.call('HGET', KEYS[3], ARGV[1])
if status then
  local deadline = tonumber(redis.call('HGET', KEYS[11], ARGV[1]) or '0')
  if deadline > now_ms then
    return 0
  end
  redis.call('ZREM', KEYS[1], ARGV[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  for i = 3, 11 do redis.call('HDEL', KEYS[i], ARGV[1]) end
  redis.call('ZREM', KEYS[12], ARGV[1])
  redis.call('ZREM', KEYS[13], ARGV[1])
end
local queued = redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2])
if queued >= tonumber(ARGV[6]) then return -1 end
redis.call('HSET', KEYS[3], ARGV[1], 'pending')
redis.call('HSET', KEYS[4], ARGV[1], ARGV[2])
redis.call('HSET', KEYS[5], ARGV[1], ARGV[3])
redis.call('HSET', KEYS[8], ARGV[1], 0)
redis.call('HSET', KEYS[11], ARGV[1], ARGV[4])
redis.call('ZADD', KEYS[1], now_ms, ARGV[1])
redis.call('ZADD', KEYS[12], ARGV[5], ARGV[1])
redis.call('ZADD', KEYS[13], ARGV[4], ARGV[1])
for i = 1, 13 do redis.call('PEXPIRE', KEYS[i], tonumber(ARGV[7])) end
return 1
`)

var claimScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
for i = 1, 32 do
  local ids = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', now_ms, 'LIMIT', 0, 1)
  if #ids == 0 then return {} end
  local id = ids[1]
  redis.call('ZREM', KEYS[1], id)
  local status = redis.call('HGET', KEYS[3], id)
  if status == 'pending' then
    local deadline = tonumber(redis.call('HGET', KEYS[9], id) or '0')
    if deadline <= now_ms then
      redis.call('HSET', KEYS[3], id, 'expired')
      redis.call('HDEL', KEYS[4], id)
    else
      local attempt = redis.call('HINCRBY', KEYS[8], id, 1)
      redis.call('HSET', KEYS[3], id, 'processing')
      redis.call('HSET', KEYS[5], id, ARGV[1])
      redis.call('HSET', KEYS[6], id, ARGV[2])
      redis.call('HDEL', KEYS[7], id)
      redis.call('ZADD', KEYS[2], now_ms + tonumber(ARGV[3]), id)
      local payload = redis.call('HGET', KEYS[4], id)
      return {id, payload or '', tostring(attempt)}
    end
  end
end
return {}
`)

var renewScript = redis.NewScript(`
if redis.call('HGET', KEYS[2], ARGV[1]) ~= 'processing' then return 0 end
if redis.call('HGET', KEYS[3], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[4], ARGV[1]) ~= ARGV[3] then return 0 end
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZADD', KEYS[1], now_ms + tonumber(ARGV[4]), ARGV[1])
return 1
`)

var finishScript = redis.NewScript(`
if redis.call('HGET', KEYS[2], ARGV[1]) ~= 'processing' then return 0 end
if redis.call('HGET', KEYS[3], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[4], ARGV[1]) ~= ARGV[3] then return 0 end
redis.call('HSET', KEYS[2], ARGV[1], ARGV[4])
redis.call('HSET', KEYS[5], ARGV[1], ARGV[5])
redis.call('HSET', KEYS[6], ARGV[1], ARGV[6])
redis.call('HDEL', KEYS[7], ARGV[1])
redis.call('HDEL', KEYS[3], ARGV[1])
redis.call('HDEL', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[1], ARGV[1])
return 1
`)

var reclaimScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local ids = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', now_ms, 'LIMIT', 0, ARGV[1])
local reclaimed = 0
for _, id in ipairs(ids) do
  redis.call('ZREM', KEYS[2], id)
  if redis.call('HGET', KEYS[3], id) == 'processing' then
    local deadline = tonumber(redis.call('HGET', KEYS[6], id) or '0')
    if deadline <= now_ms then
      redis.call('HSET', KEYS[3], id, 'expired')
      redis.call('HDEL', KEYS[7], id)
    else
      redis.call('HSET', KEYS[3], id, 'pending')
      redis.call('ZADD', KEYS[1], now_ms, id)
    end
    redis.call('HDEL', KEYS[4], id)
    redis.call('HDEL', KEYS[5], id)
    reclaimed = reclaimed + 1
  end
end
return reclaimed
`)

var expirePendingScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local ids = redis.call('ZRANGEBYSCORE', KEYS[4], '-inf', now_ms, 'LIMIT', 0, ARGV[1])
local expired = 0
for _, id in ipairs(ids) do
  redis.call('ZREM', KEYS[4], id)
  if redis.call('HGET', KEYS[2], id) == 'pending' then
    redis.call('ZREM', KEYS[1], id)
    redis.call('HSET', KEYS[2], id, 'expired')
    redis.call('HDEL', KEYS[3], id)
    expired = expired + 1
  end
end
return expired
`)

var cleanupScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local ids = redis.call('ZRANGEBYSCORE', KEYS[12], '-inf', now_ms, 'LIMIT', 0, ARGV[1])
for _, id in ipairs(ids) do
  redis.call('ZREM', KEYS[1], id)
  redis.call('ZREM', KEYS[2], id)
  for i = 3, 11 do redis.call('HDEL', KEYS[i], id) end
  redis.call('ZREM', KEYS[12], id)
  redis.call('ZREM', KEYS[13], id)
end
return #ids
`)

func New(config Config, clients *ClientSet) (*Queue, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return &Queue{config: config, clients: clients}, nil
	}
	if clients == nil || len(clients.Shards) == 0 || clients.Coordinator == nil {
		return nil, fmt.Errorf("OAuth queue Redis clients are required")
	}
	return &Queue{config: config, clients: clients}, nil
}

func (queue *Queue) Config() Config { return queue.config }

func (queue *Queue) Partition(id string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(id))
	return int(hasher.Sum32() % uint32(queue.config.Partitions))
}

func (queue *Queue) Enqueue(ctx context.Context, input EnqueueInput) (bool, error) {
	partition := queue.Partition(input.ID)
	keys := queue.partitionKeys(partition)
	result, err := enqueueScript.Run(
		ctx,
		queue.shard(partition),
		keys,
		input.ID,
		input.Payload,
		input.PollHash,
		input.Deadline.UnixMilli(),
		input.CleanupAt.UnixMilli(),
		queue.partitionCapacity(),
		(queue.config.ResultTTL * 2).Milliseconds(),
	).Int()
	if err != nil {
		return false, err
	}
	if result < 0 {
		return false, ErrQueueFull
	}
	return result == 1, nil
}

func (queue *Queue) Pending(ctx context.Context, partition int) (int64, error) {
	keys := queue.partitionKeys(partition)
	return queue.shard(partition).ZCard(ctx, keys[0]).Result()
}

func (queue *Queue) Claim(ctx context.Context, partition int, owner string, fence int64) (*ClaimedJob, error) {
	keys := queue.partitionKeys(partition)
	values, err := claimScript.Run(
		ctx,
		queue.shard(partition),
		[]string{keys[0], keys[1], keys[2], keys[3], keys[5], keys[6], keys[8], keys[7], keys[10]},
		owner,
		strconv.FormatInt(fence, 10),
		queue.config.LeaseTTL.Milliseconds(),
	).StringSlice()
	if err == redis.Nil || len(values) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("unexpected OAuth queue claim response")
	}
	attempt, err := strconv.Atoi(values[2])
	if err != nil {
		return nil, err
	}
	return &ClaimedJob{
		ID:        values[0],
		Payload:   values[1],
		Attempt:   attempt,
		Partition: partition,
		Owner:     owner,
		Fence:     fence,
	}, nil
}

func (queue *Queue) Renew(ctx context.Context, job *ClaimedJob) error {
	keys := queue.partitionKeys(job.Partition)
	result, err := renewScript.Run(
		ctx,
		queue.shard(job.Partition),
		[]string{keys[1], keys[2], keys[5], keys[6]},
		job.ID,
		job.Owner,
		strconv.FormatInt(job.Fence, 10),
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

func (queue *Queue) Finish(ctx context.Context, job *ClaimedJob, status Status, resultValue string, errorValue string) error {
	keys := queue.partitionKeys(job.Partition)
	result, err := finishScript.Run(
		ctx,
		queue.shard(job.Partition),
		[]string{keys[1], keys[2], keys[5], keys[6], keys[9], keys[8], keys[3]},
		job.ID,
		job.Owner,
		strconv.FormatInt(job.Fence, 10),
		string(status),
		resultValue,
		errorValue,
	).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (queue *Queue) Snapshot(ctx context.Context, id string, pollHash string) (*Snapshot, error) {
	partition := queue.Partition(id)
	keys := queue.partitionKeys(partition)
	client := queue.shard(partition)
	pipe := client.Pipeline()
	statusCmd := pipe.HGet(ctx, keys[2], id)
	pollCmd := pipe.HGet(ctx, keys[4], id)
	resultCmd := pipe.HGet(ctx, keys[9], id)
	errorCmd := pipe.HGet(ctx, keys[8], id)
	deadlineCmd := pipe.HGet(ctx, keys[10], id)
	attemptCmd := pipe.HGet(ctx, keys[7], id)
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	status, err := statusCmd.Result()
	if err == redis.Nil || status == "" {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}
	storedPollHash, err := pollCmd.Result()
	if err != nil {
		return nil, ErrPollTokenInvalid
	}
	if subtle.ConstantTimeCompare([]byte(storedPollHash), []byte(pollHash)) != 1 {
		return nil, ErrPollTokenInvalid
	}
	deadlineMillis, _ := deadlineCmd.Int64()
	attempt, _ := attemptCmd.Int()
	resultValue, _ := resultCmd.Result()
	errorValue, _ := errorCmd.Result()
	return &Snapshot{
		ID:       id,
		Status:   Status(status),
		Result:   resultValue,
		Error:    errorValue,
		Deadline: time.UnixMilli(deadlineMillis),
		Attempt:  attempt,
	}, nil
}

func (queue *Queue) Reclaim(ctx context.Context, partition int, limit int) (int64, error) {
	keys := queue.partitionKeys(partition)
	return reclaimScript.Run(
		ctx,
		queue.shard(partition),
		[]string{keys[0], keys[1], keys[2], keys[5], keys[6], keys[10], keys[3]},
		limit,
	).Int64()
}

func (queue *Queue) ExpirePending(ctx context.Context, partition int, limit int) (int64, error) {
	keys := queue.partitionKeys(partition)
	return expirePendingScript.Run(
		ctx,
		queue.shard(partition),
		[]string{keys[0], keys[2], keys[3], keys[12]},
		limit,
	).Int64()
}

func (queue *Queue) Cleanup(ctx context.Context, partition int, limit int) (int64, error) {
	keys := queue.partitionKeys(partition)
	return cleanupScript.Run(ctx, queue.shard(partition), keys, limit).Int64()
}

func (queue *Queue) partitionCapacity() int {
	return (queue.config.Capacity + queue.config.Partitions - 1) / queue.config.Partitions
}

func (queue *Queue) shard(partition int) redis.UniversalClient {
	return queue.clients.Shards[partition%len(queue.clients.Shards)]
}

func (queue *Queue) partitionKeys(partition int) []string {
	tag := fmt.Sprintf("{%s-q-%03d}", queue.config.Namespace, partition)
	prefix := queue.config.Namespace + ":" + tag + ":"
	return []string{
		prefix + "pending",
		prefix + "processing",
		prefix + "status",
		prefix + "payload",
		prefix + "poll",
		prefix + "owner",
		prefix + "fence",
		prefix + "attempt",
		prefix + "error",
		prefix + "result",
		prefix + "deadline",
		prefix + "expiry",
		prefix + "job_deadline",
	}
}

func ParsePreferWait(prefer string, maximum time.Duration) (bool, time.Duration) {
	prefer = strings.ToLower(prefer)
	wait := maximum
	asyncRequested := false
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if part == "respond-async" {
			asyncRequested = true
			continue
		}
		if !strings.HasPrefix(part, "wait=") {
			continue
		}
		seconds, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(part, "wait=")))
		if err == nil && seconds >= 0 {
			wait = time.Duration(seconds) * time.Second
		}
	}
	if !asyncRequested {
		return false, 0
	}
	if wait > maximum {
		wait = maximum
	}
	return true, wait
}
