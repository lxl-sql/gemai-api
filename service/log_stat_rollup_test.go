package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claimLogStatTask(t *testing.T, taskType string, payload any, runnerID string) *model.SystemTask {
	t.Helper()
	task, err := model.CreateSystemTask(taskType, payload, nil)
	require.NoError(t, err)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, taskType, runnerID, common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	return claimedTask
}

func TestLogStatRollupTaskRepairsWatermarkGapAndEnqueuesBackfill(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	targetEnd := int64(28_666_667) * 60 // 分钟对齐
	const runnerID = "rollup-runner"

	// 水位停滞在 10 分钟前（超出 5 分钟实时窗），任务应从旧水位续算补缺口。
	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  targetEnd - 3600,
		BackfillCursor: targetEnd - 1800,
		Watermark:      targetEnd - 600,
	}))
	gapLog := model.Log{
		CreatedAt:        targetEnd - 400, // 落在水位缺口内
		Type:             model.LogTypeConsume,
		Username:         "alice",
		TokenName:        "token",
		ModelName:        "model",
		ChannelId:        7,
		Group:            "vip",
		Quota:            9,
		PromptTokens:     4,
		CompletionTokens: 5,
	}
	require.NoError(t, model.LOG_DB.Create(&gapLog).Error)

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogStatRollup, LogStatRollupPayload{TargetEnd: targetEnd}, runnerID)
	runLogStatRollupTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, targetEnd, state.Watermark)
	assert.Equal(t, targetEnd-1800, state.BackfillCursor)

	// 缺口内的日志被补算进聚合表。
	aggregate, err := model.QueryLogStatRollups(ctx, model.LogStatRollupFilter{
		StartTimestamp: targetEnd - 600,
		EndTimestamp:   targetEnd,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(9), aggregate.Quota)
	assert.Equal(t, int64(1), aggregate.RequestCount)

	// 覆盖下界未到目标，回填任务已入队。
	backfillTask, err := model.GetActiveSystemTask(model.SystemTaskTypeLogStatBackfill)
	require.NoError(t, err)
	require.NotNil(t, backfillTask)
}

func TestLogStatBackfillTaskWalksBackwardToCoverage(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	base := int64(28_666_000) * 60
	const runnerID = "backfill-runner"

	// 覆盖目标为 base，当前下界为 base+3600：需要向下回填 1 小时。
	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  base,
		BackfillCursor: base + 3600,
		Watermark:      base + 7200,
	}))
	oldLog := model.Log{
		CreatedAt: base + 1800,
		Type:      model.LogTypeConsume,
		Username:  "bob",
		Quota:     11,
	}
	require.NoError(t, model.LOG_DB.Create(&oldLog).Error)

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogStatBackfill, LogStatBackfillPayload{}, runnerID)
	runLogStatBackfillTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, base, state.BackfillCursor) // 回填到 coverage 目标

	aggregate, err := model.QueryLogStatRollups(ctx, model.LogStatRollupFilter{
		StartTimestamp: base,
		EndTimestamp:   base + 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(11), aggregate.Quota)
}

func TestRollupTaskSelfHealsInterruptedCleanup(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	targetEnd := int64(28_666_700) * 60
	const runnerID = "selfheal-runner"

	// 模拟清理进程崩溃：cleanup_pending 残留、目标已记录、维护锁空闲。
	cleanupTarget := targetEnd - 7200
	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  targetEnd - 86400,
		BackfillCursor: targetEnd - 86400,
		Watermark:      targetEnd - 60,
		CleanupPending: true,
		CleanupTarget:  cleanupTarget,
	}))
	staleRollup := model.LogStatRollup{BucketStart: cleanupTarget - 600, Username: "stale", Quota: 5}
	require.NoError(t, model.UpsertLogStatRollups(ctx, []model.LogStatRollup{staleRollup}))

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogStatRollup, LogStatRollupPayload{TargetEnd: targetEnd}, runnerID)
	runLogStatRollupTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	assert.False(t, state.CleanupPending)
	assert.Zero(t, state.CleanupTarget)
	bucketCut := cleanupTarget - cleanupTarget%60
	assert.Equal(t, bucketCut, state.BackfillCursor)
	assert.Equal(t, bucketCut, state.CoverageStart)

	// 清理点之前的过期聚合已被自愈对账清除。
	var count int64
	require.NoError(t, model.DB.Model(&model.LogStatRollup{}).Where("username = ?", "stale").Count(&count).Error)
	assert.Zero(t, count)
}

func TestRollupTaskClearsStaleCleanupFlagWithoutTarget(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	targetEnd := int64(28_666_800) * 60
	const runnerID = "selfheal-no-target"

	// 旧版本升级遗留：标志置位但无目标记录。自愈只清标志不对账。
	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  targetEnd - 86400,
		BackfillCursor: targetEnd - 86400,
		Watermark:      targetEnd - 60,
		CleanupPending: true,
		CleanupTarget:  0,
	}))

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogStatRollup, LogStatRollupPayload{TargetEnd: targetEnd}, runnerID)
	runLogStatRollupTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	assert.False(t, state.CleanupPending)
}

func TestShrinkLogStatChunkOnTruncationHalvesDownToOneMinute(t *testing.T) {
	truncatedErr := fmt.Errorf("chunk [0,1800) exceeded limit: %w", errLogStatChunkTruncated)

	shrunk, next := shrinkLogStatChunkOnTruncation(truncatedErr, 1800)
	require.True(t, shrunk)
	assert.Equal(t, int64(900), next)

	// 到达一分钟下限后不再缩块，交由任务失败并提示调参。
	shrunk, next = shrinkLogStatChunkOnTruncation(truncatedErr, 60)
	assert.False(t, shrunk)
	assert.Equal(t, int64(60), next)

	// 非截断错误不触发缩块。
	shrunk, _ = shrinkLogStatChunkOnTruncation(errors.New("db down"), 1800)
	assert.False(t, shrunk)
}

func TestAggregateLogStatChunkHonorsCancellation(t *testing.T) {
	truncate(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := aggregateLogStatChunk(ctx, 60, 120)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBackfillShrinksChunkOnTruncationAndCompletes(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	base := int64(28_665_500) * 60
	const runnerID = "shrink-runner"
	// 上限取最小值 1000；30 分钟块内 1200 个 (分钟,用户) 组合触发截断，
	// 减半到 15 分钟后每块 ≤900 个组合可正常落库。
	t.Setenv("LOG_STAT_MAX_GROUPS_PER_CHUNK", "1000")

	logs := make([]model.Log, 0, 1200)
	for i := 0; i < 1200; i++ {
		logs = append(logs, model.Log{
			CreatedAt: base + int64(i), // 每秒一条，分布在 20 分钟内
			Type:      model.LogTypeConsume,
			Username:  fmt.Sprintf("user-%04d", i),
			Quota:     1,
		})
	}
	require.NoError(t, model.LOG_DB.CreateInBatches(&logs, 500).Error)

	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  base,
		BackfillCursor: base + 1800,
		Watermark:      base + 3600,
	}))

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogStatBackfill, LogStatBackfillPayload{}, runnerID)
	runLogStatBackfillTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, base, state.BackfillCursor)

	// 缩块重试后数据完整：1200 条消费日志全部进入聚合。
	aggregate, err := model.QueryLogStatRollups(ctx, model.LogStatRollupFilter{
		StartTimestamp: base,
		EndTimestamp:   base + 1800,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1200), aggregate.Quota)
	assert.Equal(t, int64(1200), aggregate.RequestCount)
}

func TestLogStatMaintenanceLockSerializesTaskTypes(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	lockCtx, release, err := acquireLogStatMaintenanceLock(ctx, "rollup-owner", "runner-a")
	require.NoError(t, err)
	require.NotNil(t, lockCtx)

	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, _, err = acquireLogStatMaintenanceLock(waitCtx, "cleanup-owner", "runner-b")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	release()
	secondCtx, secondRelease, err := acquireLogStatMaintenanceLock(ctx, "cleanup-owner", "runner-b")
	require.NoError(t, err)
	require.NotNil(t, secondCtx)
	secondRelease()
}

func TestLogCleanupTaskReconcilesRollups(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	base := int64(28_665_000) * 60
	const runnerID = "cleanup-runner"

	logs := []model.Log{
		{CreatedAt: base - 120, Type: model.LogTypeConsume, Username: "old", Quota: 40},
		{CreatedAt: base + 30, Type: model.LogTypeConsume, Username: "kept", Quota: 3},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	rollups, err := model.AggregateLogStatRollups(ctx, base-120, base+60)
	require.NoError(t, err)
	require.NoError(t, model.ReplaceLogStatRollups(ctx, base-120, base+60, rollups))
	require.NoError(t, model.SaveLogStatRollupState(ctx, &model.LogStatRollupState{
		Name:           model.LogStatRollupStateName,
		CoverageStart:  base - 3600,
		BackfillCursor: base - 3600,
		Watermark:      base + 60,
	}))

	claimedTask := claimLogStatTask(t, model.SystemTaskTypeLogCleanup, LogCleanupPayload{TargetTimestamp: base, BatchSize: 10}, runnerID)
	runLogCleanupTask(ctx, claimedTask, runnerID)

	finished, err := model.GetSystemTaskByTaskID(claimedTask.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)

	// 原始日志与聚合数据同步：清理点之前的聚合被删除。
	aggregate, err := model.QueryLogStatRollups(ctx, model.LogStatRollupFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), aggregate.Quota)

	state, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, base, state.CoverageStart)
	assert.Equal(t, base, state.BackfillCursor)
	assert.False(t, state.CleanupPending)
}
