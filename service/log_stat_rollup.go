package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	logStatRollupInterval = time.Minute
	// logStatRollupBackfillWindow 是聚合覆盖的目标历史窗口。
	logStatRollupBackfillWindow = 7 * 24 * time.Hour
	// logStatRollupPruneBuffer 让 coverage 下界比 7 天窗口再多留一段，
	// 避免默认“最近 7 天”查询恰好踩在 prune 边界上被拒绝。
	logStatRollupPruneBuffer = time.Hour
	// logStatRollupMaxRepairChunk 限制实时任务修复水位缺口时单次聚合的跨度。
	logStatRollupMaxRepairChunk = 30 * time.Minute
	logStatMaintenanceLockType  = "log_stat_maintenance"
)

// acquireLogStatMaintenanceLock serializes live rollup, historical backfill,
// and log cleanup across all master nodes. Waiting is intentional: allowing
// cleanup to overlap an already-running aggregate can resurrect deleted
// rollups or move the coverage cursor behind the cleanup boundary.
func acquireLogStatMaintenanceLock(ctx context.Context, ownerID string, runnerID string) (context.Context, func(), error) {
	lockID := ownerID + ":maintenance"
	for {
		acquired, err := model.TryAcquireNamedSystemTaskLock(
			logStatMaintenanceLockType,
			lockID,
			runnerID,
			systemTaskLockUntil(),
		)
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}

	lockCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(systemTaskLockTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-lockCtx.Done():
				return
			case <-ticker.C:
				if err := model.RenewSystemTaskLock(lockID, runnerID, systemTaskLockUntil()); err != nil {
					common.SysLog("log stat maintenance lock renewal failed: " + err.Error())
					cancel()
					return
				}
			}
		}
	}()
	release := func() {
		close(done)
		cancel()
		if err := model.ReleaseSystemTaskLock(lockID, runnerID); err != nil {
			common.SysLog("failed to release log stat maintenance lock: " + err.Error())
		}
	}
	return lockCtx, release, nil
}

func logStatBackfillChunkSeconds() int64 {
	minutes := common.GetEnvOrDefault("LOG_STAT_BACKFILL_CHUNK_MINUTES", 30)
	if minutes < 5 {
		minutes = 5
	}
	if minutes > 120 {
		minutes = 120
	}
	return int64(minutes) * 60
}

func logStatChunkTimeout() time.Duration {
	seconds := common.GetEnvOrDefault("LOG_STAT_CHUNK_TIMEOUT_SECONDS", 120)
	if seconds < 10 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

func logStatMaxGroupsPerChunk() int {
	limit := common.GetEnvOrDefault("LOG_STAT_MAX_GROUPS_PER_CHUNK", 100000)
	if limit < 1000 {
		limit = 1000
	}
	return limit
}

func logStatRepairMaxChunksPerRun() int {
	limit := common.GetEnvOrDefault("LOG_STAT_REPAIR_MAX_CHUNKS_PER_RUN", 2)
	if limit < 1 {
		return 1
	}
	if limit > 12 {
		return 12
	}
	return limit
}

func logStatBackfillMaxChunksPerRun() int {
	limit := common.GetEnvOrDefault("LOG_STAT_BACKFILL_MAX_CHUNKS_PER_RUN", 2)
	if limit < 1 {
		return 1
	}
	if limit > 12 {
		return 12
	}
	return limit
}

type LogStatRollupPayload struct {
	TargetEnd int64 `json:"target_end"`
}

type LogStatRollupTaskState struct {
	RecentStart int64 `json:"recent_start"`
	RecentEnd   int64 `json:"recent_end"`
}

type LogStatBackfillPayload struct{}

type LogStatBackfillTaskState struct {
	BackfillCursor  int64 `json:"backfill_cursor"`
	BackfillTarget  int64 `json:"backfill_target"`
	ProcessedChunks int   `json:"processed_chunks"`
}

// LogStatRollupResult 是实时任务的结果快照；BackfillCursor 仅为观测值，
// 实时任务本身不移动覆盖下界。
type LogStatRollupResult struct {
	Watermark      int64 `json:"watermark"`
	BackfillCursor int64 `json:"backfill_cursor"`
}

type LogStatBackfillResult struct {
	BackfillCursor int64 `json:"backfill_cursor"`
}

type logStatRollupHandler struct{}
type logStatBackfillHandler struct{}
type logStatTotalBackfillHandler struct{}

func (logStatRollupHandler) Type() string {
	return model.SystemTaskTypeLogStatRollup
}

func (logStatRollupHandler) Enabled() bool {
	return system_setting.LogStatRollupEnabled()
}

func (logStatRollupHandler) Interval() time.Duration {
	return logStatRollupInterval
}

func (logStatRollupHandler) NewPayload() any {
	return LogStatRollupPayload{TargetEnd: completeMinuteEnd(time.Now())}
}

func (logStatRollupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runLogStatRollupTask(ctx, task, runnerID)
}

func (logStatBackfillHandler) Type() string {
	return model.SystemTaskTypeLogStatBackfill
}

func (logStatBackfillHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runLogStatBackfillTask(ctx, task, runnerID)
}

func (logStatTotalBackfillHandler) Type() string {
	return model.SystemTaskTypeLogStatTotalBackfill
}

func (logStatTotalBackfillHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runLogStatTotalBackfillTask(ctx, task, runnerID)
}

func init() {
	RegisterSystemTaskHandler(logStatRollupHandler{})
	RegisterSystemTaskHandler(logStatBackfillHandler{})
	RegisterSystemTaskHandler(logStatTotalBackfillHandler{})
}

// runLogStatRollupTask 每分钟推进一次连续覆盖区间的上界：
//  1. 重算 [max(旧水位, TargetEnd-最近窗口), TargetEnd)（水位停滞时自动补缺口，
//     按最多 30 分钟分块、每轮限块数，避免单条超大聚合长期占锁）；
//  2. 聚合覆盖写与水位推进在同一事务提交；
//  3. 覆盖下界未到目标时入队由近及远的回填任务；
//  4. 整点时按 7 天窗口（外加缓冲）prune 过期分钟桶。
func runLogStatRollupTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !system_setting.LogStatRollupEnabled() {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, map[string]bool{"disabled": true}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	payload := LogStatRollupPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if payload.TargetEnd == 0 {
		payload.TargetEnd = completeMinuteEnd(time.Now())
	}
	if payload.TargetEnd <= 0 || payload.TargetEnd%60 != 0 {
		failSystemTask(task, runnerID, errors.New("log stat rollup target end must be a complete minute boundary"))
		return
	}
	maintenanceCtx, releaseMaintenance, err := acquireLogStatMaintenanceLock(ctx, task.TaskID, runnerID)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	defer releaseMaintenance()
	ctx = maintenanceCtx

	persistedState, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	// 崩溃自愈：能拿到维护锁说明没有清理任务在运行，此时 cleanup_pending
	// 仍为真只能是清理进程中断或对账失败遗留。按记录的目标补齐对账并清
	// 除标志，保证统计不会因一次清理事故永久停在“滞后”。
	if persistedState != nil && persistedState.CleanupPending {
		if persistedState.CleanupTarget > 0 {
			if err := model.ReconcileLogStatRollupsAfterLogCleanup(ctx, persistedState.CleanupTarget); err != nil {
				failSystemTask(task, runnerID, fmt.Errorf("failed to self-heal interrupted log cleanup: %w", err))
				return
			}
		} else {
			common.SysLog("clearing stale log stat cleanup flag without recorded target")
			if err := model.ClearLogStatCleanupPending(ctx); err != nil {
				failSystemTask(task, runnerID, err)
				return
			}
		}
		persistedState, err = model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}
	recentStart := payload.TargetEnd - int64(system_setting.LogStatRecentWindow().Seconds())
	if persistedState == nil {
		// 首次运行：覆盖区间从最近窗口起步，回填任务再把下界向 7 天前推进。
		persistedState = &model.LogStatRollupState{
			Name:           model.LogStatRollupStateName,
			CoverageStart:  payload.TargetEnd - int64(logStatRollupBackfillWindow.Seconds()),
			BackfillCursor: recentStart,
		}
		if err := model.SaveLogStatRollupState(ctx, persistedState); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}

	// 水位停滞（任务中断、master 切换）时从旧水位续算，保证覆盖区间连续。
	rebuildStart := recentStart
	if persistedState.Watermark > 0 && persistedState.Watermark < rebuildStart {
		rebuildStart = persistedState.Watermark
	}
	totalState, err := model.GetLogStatRollupState(ctx, model.LogStatMinuteTotalStateName)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if totalState != nil {
		totalRepairStart := totalState.Watermark
		if totalRepairStart == 0 {
			totalRepairStart = totalState.BackfillCursor
		}
		if totalRepairStart > 0 && totalRepairStart < rebuildStart {
			rebuildStart = totalRepairStart
		}
	}
	if rebuildStart >= payload.TargetEnd {
		rebuildStart = payload.TargetEnd - 60
	}
	if totalState == nil {
		// 独立状态避免升级时沿用维度聚合的历史 watermark，从而把尚未回填的
		// 新总计表误判为完整。最近窗口先实时生成，历史区间随后从维度表回填。
		totalState = &model.LogStatRollupState{
			Name:           model.LogStatMinuteTotalStateName,
			CoverageStart:  persistedState.CoverageStart,
			BackfillCursor: rebuildStart,
		}
		if err := model.SaveLogStatRollupState(ctx, totalState); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}

	taskState := LogStatRollupTaskState{RecentStart: rebuildStart, RecentEnd: payload.TargetEnd}
	chunkStart := rebuildStart
	processedChunks := 0
	repairChunkSeconds := int64(logStatRollupMaxRepairChunk.Seconds())
	for chunkStart < payload.TargetEnd && processedChunks < logStatRepairMaxChunksPerRun() {
		chunkEnd := chunkStart + repairChunkSeconds
		if chunkEnd > payload.TargetEnd {
			chunkEnd = payload.TargetEnd
		}
		rollups, err := aggregateLogStatChunk(ctx, chunkStart, chunkEnd)
		if err != nil {
			if shrunk, next := shrinkLogStatChunkOnTruncation(err, repairChunkSeconds); shrunk {
				repairChunkSeconds = next
				continue // 用更小的块重试同一起点，不计入本轮块数
			}
			failSystemTask(task, runnerID, err)
			return
		}
		if err := model.ReplaceLogStatRollupsAndAdvanceWatermark(ctx, chunkStart, chunkEnd, rollups, persistedState.Name, chunkEnd); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		chunkStart = chunkEnd
		processedChunks++
		if chunkStart < payload.TargetEnd {
			pauseMs := common.GetEnvOrDefault("LOG_STAT_REPAIR_PAUSE_MS", 250)
			if pauseMs > 0 {
				timer := time.NewTimer(time.Duration(pauseMs) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					failSystemTask(task, runnerID, ctx.Err())
					return
				case <-timer.C:
				}
			}
		}
	}
	persistedState.Watermark = chunkStart
	taskState.RecentEnd = chunkStart
	if err := model.UpdateSystemTaskState(task.TaskID, runnerID, taskState); err != nil {
		logSystemTaskLockError(ctx, task, err)
		return
	}

	// prune 先于回填入队：整点收缩覆盖边界后再用最新状态判断是否还需回填，
	// 避免“回填刚完成又被 prune 拉开缺口”时延迟一分钟才入队。
	if chunkStart == payload.TargetEnd && payload.TargetEnd%3600 == 0 {
		pruneCutoff := payload.TargetEnd - int64((logStatRollupBackfillWindow + logStatRollupPruneBuffer).Seconds())
		pruned, err := model.PruneLogStatRollupsBefore(ctx, persistedState.Name, pruneCutoff)
		if err != nil {
			common.SysLog("failed to prune old log stat rollups: " + err.Error())
		}
		if pruned {
			refreshed, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
			if err == nil && refreshed != nil {
				persistedState = refreshed
			}
		}
	}

	if chunkStart == payload.TargetEnd &&
		system_setting.LogStatBackfillEnabled() &&
		persistedState.BackfillCursor > persistedState.CoverageStart {
		if _, _, err := EnqueueSystemTask(model.SystemTaskTypeLogStatBackfill, LogStatBackfillPayload{}); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}
	if chunkStart == payload.TargetEnd && system_setting.LogStatBackfillEnabled() {
		refreshedMainState, mainErr := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
		refreshedTotalState, totalErr := model.GetLogStatRollupState(ctx, model.LogStatMinuteTotalStateName)
		if mainErr != nil || totalErr != nil {
			if mainErr != nil {
				err = mainErr
			} else {
				err = totalErr
			}
			failSystemTask(task, runnerID, err)
			return
		}
		if refreshedMainState != nil && refreshedTotalState != nil {
			availableLowerBound := refreshedMainState.BackfillCursor
			if refreshedMainState.CoverageStart > availableLowerBound {
				availableLowerBound = refreshedMainState.CoverageStart
			}
			if refreshedTotalState.BackfillCursor > availableLowerBound {
				if _, _, err := EnqueueSystemTask(model.SystemTaskTypeLogStatTotalBackfill, LogStatBackfillPayload{}); err != nil {
					failSystemTask(task, runnerID, err)
					return
				}
			}
		}
	}

	result := LogStatRollupResult{
		Watermark:      persistedState.Watermark,
		BackfillCursor: persistedState.BackfillCursor,
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
		return
	}
	pruneLogStatTaskHistory(payload.TargetEnd, model.SystemTaskTypeLogStatRollup)
}

// runLogStatBackfillTask 由近及远回填：每块处理 [cursor-chunk, cursor)，
// 成功后把覆盖下界压低，因此最近的历史最先可查。每块重新读取状态，
// 日志清理抬升下界后立即停止；块间按耗时动态退避，限制对日志库的持续 IO。
func runLogStatBackfillTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !system_setting.LogStatBackfillEnabled() {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, map[string]bool{"disabled": true}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	maintenanceCtx, releaseMaintenance, err := acquireLogStatMaintenanceLock(ctx, task.TaskID, runnerID)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	defer releaseMaintenance()
	ctx = maintenanceCtx

	chunkSeconds := logStatBackfillChunkSeconds()
	pauseFloorMs := common.GetEnvOrDefault("LOG_STAT_BACKFILL_PAUSE_MS", 250)
	taskState := LogStatBackfillTaskState{}

	for taskState.ProcessedChunks < logStatBackfillMaxChunksPerRun() {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		persistedState, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if persistedState == nil {
			failSystemTask(task, runnerID, errors.New("log stat rollup state is not initialized"))
			return
		}
		taskState.BackfillCursor = persistedState.BackfillCursor
		taskState.BackfillTarget = persistedState.CoverageStart
		if persistedState.BackfillCursor <= persistedState.CoverageStart {
			break // 回填完成
		}

		chunkEnd := persistedState.BackfillCursor
		chunkStart := chunkEnd - chunkSeconds
		if chunkStart < persistedState.CoverageStart {
			chunkStart = persistedState.CoverageStart
		}

		chunkBegan := time.Now()
		rollups, err := aggregateLogStatChunk(ctx, chunkStart, chunkEnd)
		if err != nil {
			if shrunk, next := shrinkLogStatChunkOnTruncation(err, chunkSeconds); shrunk {
				chunkSeconds = next
				continue // 用更小的块重试同一 cursor，不计入本轮块数
			}
			failSystemTask(task, runnerID, err)
			return
		}
		if err := model.ReplaceLogStatRollupsAndLowerCursor(ctx, chunkStart, chunkEnd, rollups, persistedState.Name); err != nil {
			if errors.Is(err, model.ErrLogStatRollupStateChanged) {
				// 状态被清理/prune 并发调整，视为本轮正常结束；
				// 下一分钟实时任务会按新状态重新入队回填。
				break
			}
			failSystemTask(task, runnerID, err)
			return
		}
		chunkElapsed := time.Since(chunkBegan)

		taskState.BackfillCursor = chunkStart
		taskState.ProcessedChunks++
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, taskState); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
		common.SysLog(fmt.Sprintf("log stat backfill chunk done: range=[%d,%d) groups=%d elapsed=%s remaining=%ds",
			chunkStart, chunkEnd, len(rollups), chunkElapsed.Round(time.Millisecond), chunkStart-persistedState.CoverageStart))

		// 动态退避：块间暂停不少于块本身耗时（占空比 <= 50%），下限可配。
		// 暂停期间持有维护锁会阻塞每分钟的实时聚合，因此本次运行的最后一块
		// 之后不再暂停，尽快释放锁让水位继续推进。
		hasNextChunkThisRun := taskState.ProcessedChunks < logStatBackfillMaxChunksPerRun() &&
			chunkStart > persistedState.CoverageStart
		pause := time.Duration(pauseFloorMs) * time.Millisecond
		if chunkElapsed > pause {
			pause = chunkElapsed
		}
		if hasNextChunkThisRun && pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				failSystemTask(task, runnerID, ctx.Err())
				return
			case <-timer.C:
			}
		}
	}

	result := LogStatBackfillResult{BackfillCursor: taskState.BackfillCursor}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
		return
	}
}

// runLogStatTotalBackfillTask upgrades the all-site minute totals from the
// existing dimension rollups. It shares the maintenance lock with live
// aggregation and cleanup, and never rescans the raw logs table.
func runLogStatTotalBackfillTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !system_setting.LogStatBackfillEnabled() {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, map[string]bool{"disabled": true}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	maintenanceCtx, releaseMaintenance, err := acquireLogStatMaintenanceLock(ctx, task.TaskID, runnerID)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	defer releaseMaintenance()
	ctx = maintenanceCtx

	chunkSeconds := logStatBackfillChunkSeconds()
	pauseFloorMs := common.GetEnvOrDefault("LOG_STAT_BACKFILL_PAUSE_MS", 250)
	taskState := LogStatBackfillTaskState{}

	for taskState.ProcessedChunks < logStatBackfillMaxChunksPerRun() {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		mainState, err := model.GetLogStatRollupState(ctx, model.LogStatRollupStateName)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		totalState, err := model.GetLogStatRollupState(ctx, model.LogStatMinuteTotalStateName)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if mainState == nil || totalState == nil {
			failSystemTask(task, runnerID, errors.New("log stat minute total state is not initialized"))
			return
		}
		if mainState.CleanupPending {
			failSystemTask(task, runnerID, errors.New("log stat cleanup is pending"))
			return
		}

		availableLowerBound := mainState.BackfillCursor
		if mainState.CoverageStart > availableLowerBound {
			availableLowerBound = mainState.CoverageStart
		}
		if totalState.CoverageStart > availableLowerBound {
			availableLowerBound = totalState.CoverageStart
		}
		taskState.BackfillCursor = totalState.BackfillCursor
		taskState.BackfillTarget = availableLowerBound
		if totalState.BackfillCursor <= availableLowerBound {
			break
		}

		chunkEnd := totalState.BackfillCursor
		chunkStart := chunkEnd - chunkSeconds
		if chunkStart < availableLowerBound {
			chunkStart = availableLowerBound
		}

		chunkBegan := time.Now()
		chunkCtx, cancel := context.WithTimeout(ctx, logStatChunkTimeout())
		totals, queryErr := model.AggregateLogStatMinuteTotalsFromRollups(chunkCtx, chunkStart, chunkEnd)
		cancel()
		if queryErr != nil {
			failSystemTask(task, runnerID, queryErr)
			return
		}
		if err := model.ReplaceLogStatMinuteTotalsAndLowerCursor(
			ctx,
			chunkStart,
			chunkEnd,
			totals,
			model.LogStatMinuteTotalStateName,
		); err != nil {
			if errors.Is(err, model.ErrLogStatRollupStateChanged) {
				break
			}
			failSystemTask(task, runnerID, err)
			return
		}
		chunkElapsed := time.Since(chunkBegan)

		taskState.BackfillCursor = chunkStart
		taskState.ProcessedChunks++
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, taskState); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
		common.SysLog(fmt.Sprintf("log stat total backfill chunk done: range=[%d,%d) minutes=%d elapsed=%s remaining=%ds",
			chunkStart, chunkEnd, len(totals), chunkElapsed.Round(time.Millisecond), chunkStart-availableLowerBound))

		hasNextChunkThisRun := taskState.ProcessedChunks < logStatBackfillMaxChunksPerRun() &&
			chunkStart > availableLowerBound
		pause := time.Duration(pauseFloorMs) * time.Millisecond
		if chunkElapsed > pause {
			pause = chunkElapsed
		}
		if hasNextChunkThisRun && pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				failSystemTask(task, runnerID, ctx.Err())
				return
			case <-timer.C:
			}
		}
	}

	result := LogStatBackfillResult{BackfillCursor: taskState.BackfillCursor}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func pruneLogStatTaskHistory(targetEnd int64, taskType string) {
	if targetEnd%3600 != 0 {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	if err := model.DeleteFinishedSystemTasksBefore(taskType, cutoff); err != nil {
		common.SysLog("failed to prune log stat task history: " + err.Error())
	}
}

// errLogStatChunkTruncated 表示单块维度组合数超过内存上限；调用方应缩小
// 时间块自动重试，而不是让任务反复失败等待人工调参。
var errLogStatChunkTruncated = errors.New("log stat chunk truncated")

const logStatMinChunkSeconds = int64(60)

// shrinkLogStatChunkOnTruncation 在块被截断时把块长减半（下限一分钟）。
// 返回是否可以继续用更小的块重试。一分钟内仍超上限属于病态维度爆炸，
// 只能失败并提示调大 LOG_STAT_MAX_GROUPS_PER_CHUNK。
func shrinkLogStatChunkOnTruncation(err error, currentChunkSeconds int64) (bool, int64) {
	if !errors.Is(err, errLogStatChunkTruncated) || currentChunkSeconds <= logStatMinChunkSeconds {
		return false, currentChunkSeconds
	}
	next := currentChunkSeconds / 2
	if next < logStatMinChunkSeconds {
		next = logStatMinChunkSeconds
	}
	common.SysLog(fmt.Sprintf("log stat chunk truncated, shrinking chunk from %ds to %ds", currentChunkSeconds, next))
	return true, next
}

// aggregateLogStatChunk 对单个时间块做聚合，带独立超时与分组数上限，
// 防止单块长时间占用日志库或超大结果集拖垮内存。截断时不落库，由调用方
// 缩块重试。
func aggregateLogStatChunk(ctx context.Context, startTimestamp int64, endTimestamp int64) ([]model.LogStatRollup, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	chunkCtx, cancel := context.WithTimeout(ctx, logStatChunkTimeout())
	defer cancel()
	maxGroups := logStatMaxGroupsPerChunk()
	rollups, truncated, err := model.AggregateLogStatRollupsLimited(chunkCtx, startTimestamp, endTimestamp, maxGroups)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("log stat chunk [%d,%d) exceeded %d groups: %w",
			startTimestamp, endTimestamp, maxGroups, errLogStatChunkTruncated)
	}
	return rollups, nil
}

func completeMinuteEnd(now time.Time) int64 {
	return now.Unix() - now.Unix()%60
}

// DeleteOldLogsWithStatMaintenance preserves the legacy synchronous cleanup
// endpoint while sharing the same cross-task maintenance lock as scheduled
// cleanup and rollup jobs.
func DeleteOldLogsWithStatMaintenance(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	ownerID := "legacy-cleanup-" + common.NewRequestId()
	runnerID := common.NodeName + "-legacy-cleanup"
	maintenanceCtx, releaseMaintenance, err := acquireLogStatMaintenanceLock(ctx, ownerID, runnerID)
	if err != nil {
		return 0, err
	}
	defer releaseMaintenance()
	if err := model.BeginLogStatCleanup(maintenanceCtx, targetTimestamp); err != nil {
		return 0, err
	}
	count, err := model.DeleteOldLog(maintenanceCtx, targetTimestamp, limit)
	if err != nil {
		// 删除中途失败时同样要清掉 cleanup_pending，避免统计永久滞后；
		// 对账幂等，剩余未删日志只影响列表展示。
		reconcileCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if reconcileErr := model.ReconcileLogStatRollupsAfterLogCleanup(reconcileCtx, targetTimestamp); reconcileErr != nil {
			common.SysLog("failed to settle log stat cleanup flag after legacy cleanup failure: " + reconcileErr.Error())
		}
	}
	return count, err
}
