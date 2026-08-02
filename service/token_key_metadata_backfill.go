package service

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const (
	tokenKeyMetadataBackfillInterval      = time.Minute
	tokenKeyMetadataBackfillBatchSize     = 200
	tokenKeyMetadataBackfillBatchesPerRun = 5
)

var tokenKeyMetadataBackfillComplete atomic.Bool

type tokenKeyMetadataBackfillHandler struct{}

type tokenKeyMetadataBackfillResult struct {
	Processed int  `json:"processed"`
	Completed bool `json:"completed"`
}

func (tokenKeyMetadataBackfillHandler) Type() string {
	return model.SystemTaskTypeTokenKeyMetadataBackfill
}

func (tokenKeyMetadataBackfillHandler) Enabled() bool {
	return !tokenKeyMetadataBackfillComplete.Load()
}

func (tokenKeyMetadataBackfillHandler) Interval() time.Duration {
	return tokenKeyMetadataBackfillInterval
}

func (tokenKeyMetadataBackfillHandler) NewPayload() any {
	return struct{}{}
}

func (tokenKeyMetadataBackfillHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result := tokenKeyMetadataBackfillResult{}
	for batch := 0; batch < tokenKeyMetadataBackfillBatchesPerRun; batch++ {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		processed, err := model.BackfillTokenKeyMetadataBatch(ctx, tokenKeyMetadataBackfillBatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		result.Processed += processed
		if processed < tokenKeyMetadataBackfillBatchSize {
			result.Completed = true
			tokenKeyMetadataBackfillComplete.Store(true)
			break
		}
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(tokenKeyMetadataBackfillHandler{})
}
