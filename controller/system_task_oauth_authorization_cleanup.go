package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	oauthAuthorizationCodeCleanupBatchSize  = 1000
	oauthAuthorizationCodeCleanupMaxBatches = 10
	oauthAuthorizationCodeTaskHistoryTTL    = 7 * 24 * time.Hour
)

type oauthAuthorizationCodeCleanupHandler struct{}

func (oauthAuthorizationCodeCleanupHandler) Type() string {
	return model.SystemTaskTypeOAuthAuthorizationCleanup
}

func (oauthAuthorizationCodeCleanupHandler) Enabled() bool { return true }

func (oauthAuthorizationCodeCleanupHandler) Interval() time.Duration { return 5 * time.Minute }

func (oauthAuthorizationCodeCleanupHandler) NewPayload() any { return nil }

func (oauthAuthorizationCodeCleanupHandler) Run(
	ctx context.Context,
	task *model.SystemTask,
	runnerID string,
) {
	var deletedCodes int64
	for batch := 0; batch < oauthAuthorizationCodeCleanupMaxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
			return
		}
		count, err := model.DeleteExpiredOAuthAuthorizationCodesBatch(
			ctx,
			time.Now(),
			oauthAuthorizationCodeCleanupBatchSize,
		)
		if err != nil {
			finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
			return
		}
		deletedCodes += count
		if count < oauthAuthorizationCodeCleanupBatchSize {
			break
		}
	}
	var deletedRefreshHistory int64
	for batch := 0; batch < oauthAuthorizationCodeCleanupMaxBatches; batch++ {
		count, err := model.DeleteExpiredOAuthRefreshTokenHistoryBatch(
			ctx,
			time.Now(),
			oauthAuthorizationCodeCleanupBatchSize,
		)
		if err != nil {
			finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
			return
		}
		deletedRefreshHistory += count
		if count < oauthAuthorizationCodeCleanupBatchSize {
			break
		}
	}
	finishSystemTaskHandler(
		task,
		runnerID,
		model.SystemTaskStatusSucceeded,
		map[string]int64{
			"deleted_authorization_codes": deletedCodes,
			"deleted_refresh_history":     deletedRefreshHistory,
		},
		nil,
	)
	if err := model.DeleteFinishedSystemTasksBefore(
		model.SystemTaskTypeOAuthAuthorizationCleanup,
		time.Now().Add(-oauthAuthorizationCodeTaskHistoryTTL).Unix(),
	); err != nil {
		common.SysLog("failed to prune OAuth authorization cleanup task history: " + err.Error())
	}
}
