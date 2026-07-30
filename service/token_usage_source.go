package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	tokenUsageSourceRollupInterval = time.Minute
	tokenUsageSourceRepairSeconds  = int64(5 * 60)
	tokenUsageSourceMinChunk       = int64(10)
)

var errTokenUsageSourceChunkTimeout = errors.New("token usage source chunk timed out")

type tokenUsageSourceRollupPayload struct {
	TargetEnd int64 `json:"target_end"`
}

type tokenUsageSourceTaskState struct {
	RangeStart      int64 `json:"range_start"`
	RangeEnd        int64 `json:"range_end"`
	ProcessedChunks int   `json:"processed_chunks"`
}

type tokenUsageSourceTaskResult struct {
	Watermark      int64 `json:"watermark"`
	BackfillCursor int64 `json:"backfill_cursor"`
}

type tokenUsageSourceRollupHandler struct{}
type tokenUsageSourceBackfillHandler struct{}
type tokenUsageSourceReconcileHandler struct{}

func tokenUsageSourceRollupEnabled() bool {
	return system_setting.TokenUsageSourceRollupEnabled()
}

func tokenUsageSourceBackfillEnabled() bool {
	return system_setting.TokenUsageSourceBackfillEnabled()
}

func tokenUsageSourceReconcileEnabled() bool {
	return system_setting.TokenUsageSourceReconcileEnabled()
}

func tokenUsageSourceReconcileBatchSize() int {
	return system_setting.GetTokenUsageSourceSettings().ReconcileBatchSize
}

func tokenUsageSourceReconcileMaxBatches() int {
	return system_setting.GetTokenUsageSourceSettings().ReconcileMaxBatchesPerRun
}

func tokenUsageSourceLateLogLag() int64 {
	return int64(system_setting.GetTokenUsageSourceSettings().LateLogLagSeconds)
}

func tokenUsageSourceBackfillDays() int64 {
	return int64(system_setting.GetTokenUsageSourceSettings().BackfillDays)
}

func tokenUsageSourceBackfillChunkSeconds() int64 {
	return int64(system_setting.GetTokenUsageSourceSettings().BackfillChunkSeconds)
}

func tokenUsageSourceChunkTimeout() time.Duration {
	return time.Duration(system_setting.GetTokenUsageSourceSettings().ChunkTimeoutSeconds) * time.Second
}

func tokenUsageSourceMaxGroups() int {
	return system_setting.GetTokenUsageSourceSettings().MaxGroupsPerChunk
}

func tokenUsageSourceLimit() int {
	return system_setting.GetTokenUsageSourceSettings().MaxSourcesPerToken
}

func tokenUsageSourceMaxLiveChunks() int {
	return system_setting.GetTokenUsageSourceSettings().MaxLiveChunksPerRun
}

func tokenUsageSourceMaxWatermarkLag() int64 {
	return int64(system_setting.GetTokenUsageSourceSettings().MaxWatermarkLagSeconds)
}

func tokenUsageSourceTargetEnd(now time.Time) int64 {
	target := completeMinuteEnd(now) - tokenUsageSourceLateLogLag()
	return target - target%60
}

func (tokenUsageSourceRollupHandler) Type() string {
	return model.SystemTaskTypeTokenUsageSourceRollup
}

func (tokenUsageSourceRollupHandler) Enabled() bool {
	return tokenUsageSourceRollupEnabled()
}

func (tokenUsageSourceRollupHandler) Interval() time.Duration {
	return tokenUsageSourceRollupInterval
}

func (tokenUsageSourceRollupHandler) NewPayload() any {
	return tokenUsageSourceRollupPayload{TargetEnd: tokenUsageSourceTargetEnd(time.Now())}
}

func (tokenUsageSourceRollupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runTokenUsageSourceRollupTask(ctx, task, runnerID)
}

func (tokenUsageSourceBackfillHandler) Type() string {
	return model.SystemTaskTypeTokenUsageSourceBackfill
}

func (tokenUsageSourceBackfillHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runTokenUsageSourceBackfillTask(ctx, task, runnerID)
}

func (tokenUsageSourceReconcileHandler) Type() string {
	return model.SystemTaskTypeTokenUsageSourceReconcile
}

func (tokenUsageSourceReconcileHandler) Enabled() bool {
	return tokenUsageSourceReconcileEnabled()
}

func (tokenUsageSourceReconcileHandler) Interval() time.Duration {
	return tokenUsageSourceRollupInterval
}

func (tokenUsageSourceReconcileHandler) NewPayload() any {
	return struct{}{}
}

func (tokenUsageSourceReconcileHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !tokenUsageSourceReconcileEnabled() {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, map[string]bool{"disabled": true}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	result := &model.TokenUsageSourceReconcileResult{}
	for batch := 0; batch < tokenUsageSourceReconcileMaxBatches(); batch++ {
		batchResult, err := model.ReconcileTokenUsageSourceMetaBatch(
			ctx,
			tokenUsageSourceReconcileBatchSize(),
		)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		result.Processed += batchResult.Processed
		result.Created += batchResult.Created
		result.Enabled += batchResult.Enabled
		result.Disabled += batchResult.Disabled
		result.Purged += batchResult.Purged
		result.DeletedSources += batchResult.DeletedSources
		result.NextCursor = batchResult.NextCursor
		result.CycleCompleted = batchResult.CycleCompleted
		result.ReconciledAt = batchResult.ReconciledAt
		if batchResult.CycleCompleted {
			break
		}
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(tokenUsageSourceRollupHandler{})
	RegisterSystemTaskHandler(tokenUsageSourceBackfillHandler{})
	RegisterSystemTaskHandler(tokenUsageSourceReconcileHandler{})
}

func runTokenUsageSourceRollupTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !tokenUsageSourceRollupEnabled() {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, map[string]bool{"disabled": true}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	var payload tokenUsageSourceRollupPayload
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if payload.TargetEnd <= 0 {
		payload.TargetEnd = tokenUsageSourceTargetEnd(time.Now())
	}

	maintenanceCtx, releaseMaintenance, err := acquireLogStatMaintenanceLock(ctx, task.TaskID, runnerID)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	defer releaseMaintenance()
	ctx = maintenanceCtx

	state, err := model.GetLogStatRollupState(ctx, model.TokenUsageSourceStateName)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if state == nil {
		coverageStart := payload.TargetEnd - tokenUsageSourceBackfillDays()*24*60*60
		coverageStart -= coverageStart % 60
		earliestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		earliestLog, earliestErr := model.GetEarliestTokenUsageSourceLogTimestamp(earliestCtx)
		cancel()
		if earliestErr != nil {
			failSystemTask(task, runnerID, earliestErr)
			return
		}
		if earliestLog > coverageStart {
			coverageStart = earliestLog
		}
		if coverageStart >= payload.TargetEnd {
			coverageStart = payload.TargetEnd - 60
		}
		state, err = model.InitializeTokenUsageSourceState(ctx, coverageStart, payload.TargetEnd)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}
	if payload.TargetEnd < state.Watermark {
		payload.TargetEnd = state.Watermark
	}

	rangeStart := state.Watermark - tokenUsageSourceRepairSeconds
	if state.Watermark < payload.TargetEnd-tokenUsageSourceRepairSeconds {
		rangeStart = state.Watermark
	}
	if rangeStart < state.CoverageStart {
		rangeStart = state.CoverageStart
	}
	taskState := tokenUsageSourceTaskState{
		RangeStart: rangeStart,
		RangeEnd:   payload.TargetEnd,
	}
	currentWatermark := state.Watermark
	chunkStart := rangeStart
	chunkSeconds := int64(60)
	for chunkStart < payload.TargetEnd && taskState.ProcessedChunks < tokenUsageSourceMaxLiveChunks() {
		chunkEnd := chunkStart + chunkSeconds
		if chunkEnd > payload.TargetEnd {
			chunkEnd = payload.TargetEnd
		}
		groups, truncated, queryErr := aggregateTokenUsageSourceChunk(ctx, chunkStart, chunkEnd)
		if queryErr != nil {
			if errors.Is(queryErr, errTokenUsageSourceChunkTimeout) {
				smallerChunk, canShrink := smallerTokenUsageSourceChunk(chunkSeconds)
				if canShrink {
					chunkSeconds = smallerChunk
					continue
				}
			}
			failSystemTask(task, runnerID, queryErr)
			return
		}
		if truncated {
			smallerChunk, canShrink := smallerTokenUsageSourceChunk(chunkSeconds)
			if !canShrink {
				failSystemTask(task, runnerID, fmt.Errorf(
					"token usage source chunk [%d,%d) exceeds %d groups at minimum chunk size",
					chunkStart,
					chunkEnd,
					tokenUsageSourceMaxGroups(),
				))
				return
			}
			chunkSeconds = smallerChunk
			continue
		}
		if err := model.MergeTokenUsageSourceGroups(ctx, groups, tokenUsageSourceLimit()); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if chunkEnd > currentWatermark {
			if err := model.AdvanceTokenUsageSourceWatermark(ctx, currentWatermark, chunkEnd); err != nil {
				if errors.Is(err, model.ErrLogStatRollupStateChanged) {
					break
				}
				failSystemTask(task, runnerID, err)
				return
			}
			currentWatermark = chunkEnd
		}
		chunkStart = chunkEnd
		taskState.ProcessedChunks++
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, taskState); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}

	state, err = model.GetLogStatRollupState(ctx, model.TokenUsageSourceStateName)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if state != nil &&
		state.Watermark >= payload.TargetEnd &&
		state.BackfillCursor > state.CoverageStart &&
		tokenUsageSourceBackfillEnabled() {
		if _, _, err := EnqueueSystemTask(model.SystemTaskTypeTokenUsageSourceBackfill, struct{}{}); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}

	result := tokenUsageSourceTaskResult{}
	if state != nil {
		result.Watermark = state.Watermark
		result.BackfillCursor = state.BackfillCursor
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func runTokenUsageSourceBackfillTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	if !tokenUsageSourceRollupEnabled() || !tokenUsageSourceBackfillEnabled() {
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

	state, err := model.GetLogStatRollupState(ctx, model.TokenUsageSourceStateName)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if state == nil || state.BackfillCursor <= state.CoverageStart {
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, tokenUsageSourceTaskResult{}, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
	if tokenUsageSourceTargetEnd(time.Now())-state.Watermark > tokenUsageSourceMaxWatermarkLag() {
		result := tokenUsageSourceTaskResult{
			Watermark:      state.Watermark,
			BackfillCursor: state.BackfillCursor,
		}
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}

	chunkEnd := state.BackfillCursor
	chunkSeconds := tokenUsageSourceBackfillChunkSeconds()
	for {
		chunkStart := chunkEnd - chunkSeconds
		if chunkStart < state.CoverageStart {
			chunkStart = state.CoverageStart
		}
		groups, truncated, queryErr := aggregateTokenUsageSourceChunk(ctx, chunkStart, chunkEnd)
		if queryErr != nil {
			if errors.Is(queryErr, errTokenUsageSourceChunkTimeout) {
				smallerChunk, canShrink := smallerTokenUsageSourceChunk(chunkSeconds)
				if canShrink {
					chunkSeconds = smallerChunk
					continue
				}
			}
			failSystemTask(task, runnerID, queryErr)
			return
		}
		if truncated {
			smallerChunk, canShrink := smallerTokenUsageSourceChunk(chunkSeconds)
			if !canShrink {
				failSystemTask(task, runnerID, fmt.Errorf(
					"token usage source backfill chunk [%d,%d) exceeds %d groups at minimum chunk size",
					chunkStart,
					chunkEnd,
					tokenUsageSourceMaxGroups(),
				))
				return
			}
			chunkSeconds = smallerChunk
			continue
		}
		if err := model.MergeTokenUsageSourceGroups(ctx, groups, tokenUsageSourceLimit()); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if err := model.LowerTokenUsageSourceBackfillCursor(ctx, chunkEnd, chunkStart); err != nil {
			if !errors.Is(err, model.ErrLogStatRollupStateChanged) {
				failSystemTask(task, runnerID, err)
				return
			}
		}
		result := tokenUsageSourceTaskResult{
			Watermark:      state.Watermark,
			BackfillCursor: chunkStart,
		}
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}
}

func aggregateTokenUsageSourceChunk(
	ctx context.Context,
	startTimestamp int64,
	endTimestamp int64,
) ([]model.TokenUsageSourceGroup, bool, error) {
	chunkCtx, cancel := context.WithTimeout(ctx, tokenUsageSourceChunkTimeout())
	defer cancel()
	groups, truncated, err := model.AggregateTokenUsageSourceGroups(
		chunkCtx,
		startTimestamp,
		endTimestamp,
		tokenUsageSourceMaxGroups(),
	)
	if err != nil &&
		(errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(chunkCtx.Err(), context.DeadlineExceeded)) {
		return nil, false, fmt.Errorf("%w: %v", errTokenUsageSourceChunkTimeout, err)
	}
	return groups, truncated, err
}

func smallerTokenUsageSourceChunk(current int64) (int64, bool) {
	if current <= tokenUsageSourceMinChunk {
		return current, false
	}
	next := current / 2
	if next < tokenUsageSourceMinChunk {
		next = tokenUsageSourceMinChunk
	}
	return next, true
}
