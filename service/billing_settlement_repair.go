package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	// Per-phase, not per-pass: one pass runs billingSettlementRepairPhases of
	// these windows back to back. Keep the product comfortably inside what the
	// task lease heartbeat can renew, or a slow database costs the whole pass.
	billingSettlementRepairDefaultRunLimitSeconds = 30
	billingAuditRepairDefaultBatchLimit           = 200
	billingReservationManualReviewAttempts        = 8
	// billingSettlementRepairPhases counts the dispatching phases in one pass
	// (settlement failures, expired reservations, submission audits, completed
	// request audits) and bounds the whole pass at one window each.
	billingSettlementRepairPhases = 4
)

// billingSettlementRepairRunLimit bounds one repair pass. The lease heartbeat
// renews every systemTaskLockTTL/3, so a pass may safely outlive the original
// 15 seconds; on a loaded database a single settlement costs seconds, and too
// small a budget lets the pass expire before it reaches its later phases.
func billingSettlementRepairRunLimit() time.Duration {
	seconds := common.GetEnvOrDefault("BILLING_SETTLEMENT_REPAIR_RUN_LIMIT_SECONDS", billingSettlementRepairDefaultRunLimitSeconds)
	if seconds < 15 {
		seconds = 15
	}
	return time.Duration(seconds) * time.Second
}

func billingSettlementNeedsManualReview(err error) bool {
	return errors.Is(err, model.ErrInsufficientUserQuota) ||
		errors.Is(err, model.ErrBillingSettlementRequiresManual)
}

// forEachBillingRepairItem applies fn to each item with bounded concurrency and
// stops dispatching as soon as ctx expires.
//
// One repair item spends most of its wall time waiting on row locks rather than
// on CPU, so a serial pass drains a backlog far more slowly than the database
// can absorb. Items are keyed by request ID and each one locks only its own
// user's rows — the per-user serialization inside the quota transaction keeps
// same-user items ordered — so concurrent items cannot deadlock each other.
func forEachBillingRepairItem[T any](ctx context.Context, items []T, fn func(item T)) {
	workers := common.GetEnvOrDefault("BILLING_SETTLEMENT_REPAIR_CONCURRENCY", 4)
	if workers < 1 {
		workers = 1
	}
	if workers > 32 {
		workers = 32
	}

	slots := make(chan struct{}, workers)
	var wg sync.WaitGroup
dispatch:
	for _, item := range items {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		current := item
		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			defer func() { <-slots }()
			fn(current)
		})
	}
	wg.Wait()
}

type BillingSettlementRepairSummary struct {
	Scanned                    int `json:"scanned"`
	Settled                    int `json:"settled"`
	Failed                     int `json:"failed"`
	ReservationsScanned        int `json:"reservations_scanned"`
	ReservationsRepaired       int `json:"reservations_repaired"`
	ReservationsFailed         int `json:"reservations_failed"`
	ReservationsManualRequired int `json:"reservations_manual_required"`
	AuditsScanned              int `json:"audits_scanned"`
	AuditsRepaired             int `json:"audits_repaired"`
	AuditsFailed               int `json:"audits_failed"`
	AuditMarkersDeleted        int `json:"audit_markers_deleted"`
	InfrastructureFailed       int `json:"infrastructure_failed"`
}

func (summary BillingSettlementRepairSummary) FailureCount() int {
	return summary.Failed + summary.ReservationsFailed + summary.AuditsFailed + summary.InfrastructureFailed
}

func RunBillingSettlementRepairOnce(ctx context.Context) BillingSettlementRepairSummary {
	summary := BillingSettlementRepairSummary{}
	defer refreshBillingRepairWakeup()
	if ctx == nil {
		ctx = context.Background()
	}
	// Each phase dispatches within its own window, measured from when that phase
	// starts. Sharing one run deadline starved the later phases: a phase stops
	// dispatching when its window closes but still waits for its in-flight items,
	// and a single item can outlast the whole run budget, so the reservation
	// phase was never reached while a settlement backlog existed.
	phaseLimit := billingSettlementRepairRunLimit()
	ctx, cancel := context.WithTimeout(ctx, billingSettlementRepairPhases*phaseLimit)
	defer cancel()
	if ctx.Err() != nil {
		return summary
	}

	failureCtx, cancelFailures := context.WithTimeout(ctx, phaseLimit)
	defer cancelFailures()

	limit := common.GetEnvOrDefault("BILLING_SETTLEMENT_REPAIR_BATCH_SIZE", 1000)
	failures, err := model.FindPendingBillingSettlementFailures(limit)
	if err != nil {
		summary.InfrastructureFailed++
		common.SysLog("failed to find pending billing settlement failures: " + err.Error())
	} else {
		var mu sync.Mutex
		forEachBillingRepairItem(failureCtx, failures, func(failure *model.BillingSettlementFailure) {
			retryErr := retryBillingSettlementFailure(failure)
			var markErr error
			var manualErr error
			manualRequired := false
			if retryErr != nil && failure.ReservationManaged && billingSettlementNeedsManualReview(retryErr) {
				reservation, reservationErr := model.GetBillingReservationByRequestId(failure.RequestId)
				if reservationErr != nil {
					manualErr = reservationErr
				} else if reservation != nil &&
					reservation.Status == model.BillingReservationStatusSettling &&
					reservation.DesiredQuota > reservation.ReservedQuota &&
					failure.Attempts+1 >= billingReservationManualReviewAttempts {
					manualRequired, manualErr = model.MarkBillingReservationManualRequired(reservation.Id, retryErr)
				}
			}
			if manualRequired {
				common.SysLog(fmt.Sprintf("billing settlement failure requires manual reconciliation (request_id=%s): %v",
					failure.RequestId, retryErr))
			} else if manualErr != nil {
				common.SysLog(fmt.Sprintf("failed to mark billing settlement failure for manual reconciliation (request_id=%s): %v",
					failure.RequestId, manualErr))
			}
			if retryErr != nil {
				// Only a successful retry may close the record. Marking it settled
				// first would strand the unrepaired delta: the attempt update is
				// scoped to pending rows and would silently match nothing.
				if !manualRequired {
					markErr = model.MarkBillingSettlementFailureAttempt(failure.Id, retryErr)
				}
				if !manualRequired {
					common.SysLog(fmt.Sprintf("failed to retry billing settlement (request_id=%s): %v", failure.RequestId, retryErr))
				}
			} else {
				markErr = model.MarkBillingSettlementFailureSettled(failure.Id)
			}
			if markErr != nil {
				common.SysLog("failed to mark billing settlement outcome: " + markErr.Error())
			}

			mu.Lock()
			defer mu.Unlock()
			summary.Scanned++
			if manualRequired {
				summary.ReservationsManualRequired++
			} else if retryErr != nil {
				summary.Failed++
			} else {
				summary.Settled++
			}
			if markErr != nil {
				summary.InfrastructureFailed++
			}
			if manualErr != nil {
				summary.InfrastructureFailed++
			}
		})
	}
	if ctx.Err() != nil {
		return summary
	}

	reservationLimit := common.GetEnvOrDefault("BILLING_RESERVATION_REPAIR_BATCH_SIZE", 1000)
	reservations, err := model.FindDueBillingReservations(reservationLimit)
	if err != nil {
		summary.InfrastructureFailed++
		common.SysLog("failed to find due billing reservations: " + err.Error())
		return summary
	}
	reservationCtx, cancelReservations := context.WithTimeout(ctx, phaseLimit)
	defer cancelReservations()

	var reservationMu sync.Mutex
	forEachBillingRepairItem(reservationCtx, reservations, func(reservation *model.BillingReservation) {
		repaired, repairErr := model.RepairExpiredBillingReservation(reservation.RequestId)
		manualRequired := false
		if billingSettlementNeedsManualReview(repairErr) &&
			reservation.Status == model.BillingReservationStatusSettling &&
			reservation.DesiredQuota > reservation.ReservedQuota &&
			reservation.Attempts+1 >= billingReservationManualReviewAttempts {
			var markErr error
			manualRequired, markErr = model.MarkBillingReservationManualRequired(reservation.Id, repairErr)
			if markErr != nil {
				common.SysLog(fmt.Sprintf("failed to mark billing reservation for manual reconciliation (request_id=%s): %v",
					reservation.RequestId, markErr))
			}
			if manualRequired {
				common.SysLog(fmt.Sprintf("billing reservation requires manual reconciliation (request_id=%s attempts=%d): %v",
					reservation.RequestId, reservation.Attempts+1, repairErr))
				repairErr = nil
			} else if markErr != nil {
				repairErr = fmt.Errorf("mark billing reservation for manual reconciliation: %w", markErr)
			}
		}
		if repairErr != nil {
			model.RecordBillingReservationAttempt(reservation.RequestId, repairErr)
			common.SysLog(fmt.Sprintf("failed to repair billing reservation (request_id=%s status=%s): %v",
				reservation.RequestId, reservation.Status, repairErr))
		}

		reservationMu.Lock()
		defer reservationMu.Unlock()
		summary.ReservationsScanned++
		if manualRequired {
			summary.ReservationsManualRequired++
			return
		}
		if repairErr != nil {
			summary.ReservationsFailed++
			return
		}
		if repaired {
			summary.ReservationsRepaired++
		}
	})
	if ctx.Err() != nil {
		return summary
	}

	// Receipts settle far faster than they are audited, so the audit backlog is
	// what accumulates once the reservation phase is healthy. The three audit
	// passes share one window and one concurrency bound.
	auditLimit := common.GetEnvOrDefault("BILLING_AUDIT_REPAIR_BATCH_SIZE", billingAuditRepairDefaultBatchLimit)
	if auditLimit <= 0 {
		auditLimit = billingAuditRepairDefaultBatchLimit
	}
	auditCtx, cancelAudits := context.WithTimeout(ctx, phaseLimit)
	defer cancelAudits()
	var auditMu sync.Mutex
	recordAuditOutcome := func(repaired bool, auditErr error, requestId string, kind string) {
		if auditErr != nil {
			common.SysLog(fmt.Sprintf("failed to repair %s billing audit (request_id=%s): %v", kind, requestId, auditErr))
		}
		auditMu.Lock()
		defer auditMu.Unlock()
		summary.AuditsScanned++
		if auditErr != nil {
			summary.AuditsFailed++
			return
		}
		if repaired {
			summary.AuditsRepaired++
		}
	}

	submissionReceipts, err := model.FindPendingTaskSubmissionBillingReservations(auditLimit)
	if err != nil {
		summary.InfrastructureFailed++
		common.SysLog("failed to find pending task submission billing audits: " + err.Error())
		return summary
	}
	forEachBillingRepairItem(auditCtx, submissionReceipts, func(reservation *model.BillingReservation) {
		repaired, auditErr := repairPendingTaskSubmissionBillingAudit(reservation)
		recordAuditOutcome(repaired, auditErr, reservation.RequestId, "task submission")
	})
	if ctx.Err() != nil {
		return summary
	}

	auditReceipts, err := model.FindCompletedTaskBillingReservations(auditLimit)
	if err != nil {
		summary.InfrastructureFailed++
		common.SysLog("failed to find completed task billing audits: " + err.Error())
		return summary
	}
	forEachBillingRepairItem(auditCtx, auditReceipts, func(reservation *model.BillingReservation) {
		repaired, auditErr := repairCompletedTaskBillingAudit(reservation)
		recordAuditOutcome(repaired, auditErr, reservation.RequestId, "completed task")
	})
	if ctx.Err() != nil {
		return summary
	}
	standaloneGrace := common.GetEnvOrDefault("BILLING_STANDALONE_AUDIT_GRACE_SECONDS", 60)
	if standaloneGrace < 15 {
		standaloneGrace = 15
	}
	standaloneReceipts, err := model.FindCompletedStandaloneBillingReservations(
		auditLimit,
		model.GetDBTimestamp()-int64(standaloneGrace),
	)
	if err != nil {
		summary.InfrastructureFailed++
		common.SysLog("failed to find completed request billing audits: " + err.Error())
		return summary
	}
	forEachBillingRepairItem(auditCtx, standaloneReceipts, func(reservation *model.BillingReservation) {
		repaired, auditErr := repairCompletedStandaloneBillingAudit(reservation)
		recordAuditOutcome(repaired, auditErr, reservation.RequestId, "completed request")
	})

	// Marker cleanup is maintenance work. Run it only on an otherwise idle pass
	// so a large marker backlog cannot delay or obscure financial/audit recovery.
	if ctx.Err() == nil && summary.Scanned == 0 && summary.ReservationsScanned == 0 &&
		summary.AuditsScanned == 0 && summary.FailureCount() == 0 {
		markerCutoff := model.GetDBTimestamp() - int64(model.BillingAuditMarkerRetentionSeconds())
		deletedMarkers, markerErr := model.DeleteExpiredBillingAuditMarkers(
			limit,
			markerCutoff,
		)
		if markerErr != nil {
			summary.InfrastructureFailed++
			common.SysLog("failed to delete expired billing audit markers: " + markerErr.Error())
		} else {
			summary.AuditMarkersDeleted = deletedMarkers
		}
	}
	return summary
}

func repairCompletedStandaloneBillingAudit(reservation *model.BillingReservation) (bool, error) {
	if reservation == nil {
		return false, nil
	}
	claimed, won, err := model.ClaimBillingReservationAudit(reservation.RequestId, billingAuditClaimTTLSeconds())
	if err != nil || !won {
		return false, err
	}
	auditKey := billingAuditKey("request", claimed.RequestId, claimed.Id, 0, claimed.DesiredQuota)
	exists := false
	if claimed.DesiredQuota > 0 {
		exists, err = model.HasTaskBillingAudit(claimed.RequestId, auditKey)
		if err != nil {
			model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
			return false, err
		}
	}
	if claimed.DesiredQuota > 0 && !exists {
		err = model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
			UserId:              claimed.UserId,
			LogType:             model.LogTypeConsume,
			Content:             "billing audit repair",
			ChannelId:           claimed.ChannelId,
			ModelName:           claimed.ModelName,
			Quota:               claimed.DesiredQuota,
			TokenId:             claimed.TokenId,
			Group:               claimed.Group,
			RequestId:           claimed.RequestId,
			AuditClaimExpiresAt: claimed.ExpiresAt,
			Other: map[string]interface{}{
				"actual_quota":      claimed.DesiredQuota,
				"billing_audit_key": auditKey,
				"billing_source":    claimed.BillingSource,
				"audit_repair":      true,
			},
		})
		if err != nil {
			model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
			return false, err
		}
	}
	if err := model.AcknowledgeBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt); err != nil {
		model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
		return false, err
	}
	return true, nil
}

func repairPendingTaskSubmissionBillingAudit(reservation *model.BillingReservation) (bool, error) {
	if reservation == nil {
		return false, nil
	}
	claimed, won, err := model.ClaimBillingReservationAudit(reservation.RequestId, billingAuditClaimTTLSeconds())
	if err != nil || !won {
		return false, err
	}

	submittedQuota := claimed.ReservedQuota
	kind := ""
	params := model.RecordTaskBillingLogParams{
		UserId:              claimed.UserId,
		LogType:             model.LogTypeConsume,
		Content:             "task submission billing audit repair",
		ChannelId:           claimed.ChannelId,
		ModelName:           claimed.ModelName,
		Quota:               submittedQuota,
		TokenId:             claimed.TokenId,
		Group:               claimed.Group,
		RequestId:           claimed.RequestId,
		AuditClaimExpiresAt: claimed.ExpiresAt,
		Other: map[string]interface{}{
			"audit_repair":   true,
			"billing_source": claimed.BillingSource,
		},
	}
	if claimed.TaskId > 0 {
		var task model.Task
		if err := model.DB.Where("id = ?", claimed.TaskId).First(&task).Error; err != nil {
			model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
			return false, err
		}
		kind = "task-submit"
		if task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.SubmittedQuota > 0 {
			submittedQuota = task.PrivateData.BillingContext.SubmittedQuota
		}
		params.ChannelId = task.ChannelId
		params.ModelName = taskModelName(&task)
		params.Group = task.Group
		params.NodeName = task.PrivateData.NodeName
		params.Other = taskBillingOther(&task)
		params.Other["task_id"] = task.TaskID
		params.Other["audit_repair"] = true
		params.Other["billing_source"] = claimed.BillingSource
	} else if claimed.MidjourneyId > 0 {
		var task model.Midjourney
		if err := model.DB.Where("id = ?", claimed.MidjourneyId).First(&task).Error; err != nil {
			model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
			return false, err
		}
		kind = "midjourney-submit"
		params.ChannelId = task.ChannelId
		params.ModelName = CovertMjpActionToModelName(task.Action)
		params.Other["task_id"] = task.MjId
	} else {
		err := fmt.Errorf("billing reservation %s is not bound to an asynchronous task", claimed.RequestId)
		model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
		return false, err
	}
	if submittedQuota <= 0 {
		err := fmt.Errorf("billing reservation %s has invalid submitted quota %d", claimed.RequestId, submittedQuota)
		model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
		return false, err
	}
	params.Quota = submittedQuota
	auditKey := billingAuditKey(kind, claimed.RequestId, claimed.Id, 0, submittedQuota)
	params.Other["actual_quota"] = submittedQuota
	params.Other["billing_audit_key"] = auditKey
	exists, err := model.HasTaskBillingAudit(claimed.RequestId, auditKey)
	if err != nil {
		model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
		return false, err
	}
	if !exists {
		if err := model.RecordTaskBillingLog(params); err != nil {
			model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
			return false, err
		}
	}
	if err := model.CompleteBillingReservationAuditClaim(claimed.RequestId, claimed.ExpiresAt, submittedQuota); err != nil {
		model.ReleaseBillingReservationAudit(claimed.RequestId, claimed.ExpiresAt, err)
		return false, err
	}
	return true, nil
}

func repairCompletedTaskBillingAudit(reservation *model.BillingReservation) (bool, error) {
	if reservation == nil {
		return false, nil
	}
	claimedReservation, claimed, err := model.ClaimBillingReservationAudit(reservation.RequestId, billingAuditClaimTTLSeconds())
	if err != nil || !claimed {
		return false, err
	}
	reservation = claimedReservation
	settlement := &model.BillingReservationSettlementResult{
		Completed:               true,
		ReservationId:           reservation.Id,
		RequestId:               reservation.RequestId,
		TaskId:                  reservation.TaskId,
		MidjourneyId:            reservation.MidjourneyId,
		UserId:                  reservation.UserId,
		TokenId:                 reservation.TokenId,
		ChannelId:               reservation.ChannelId,
		ModelName:               reservation.ModelName,
		Group:                   reservation.Group,
		BillingSource:           reservation.BillingSource,
		SubscriptionId:          reservation.SubscriptionId,
		PreConsumedQuota:        reservation.ReservedQuota,
		ActualQuota:             reservation.DesiredQuota,
		AuditedQuota:            reservation.AuditedQuota,
		WalletQuotaConsumed:     reservation.WalletQuotaReserved,
		WalletGiftQuotaConsumed: reservation.WalletGiftQuotaReserved,
	}
	if reservation.TaskId > 0 {
		var task model.Task
		if err := model.DB.Where("id = ?", reservation.TaskId).First(&task).Error; err != nil {
			model.ReleaseBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt, err)
			return false, err
		}
		if err := recordTaskBillingAudit(&task, settlement, reservation.ExpiresAt, "task billing audit repair"); err != nil {
			model.ReleaseBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt, err)
			return false, err
		}
	} else if reservation.MidjourneyId > 0 {
		var task model.Midjourney
		if err := model.DB.Where("id = ?", reservation.MidjourneyId).First(&task).Error; err != nil {
			model.ReleaseBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt, err)
			return false, err
		}
		if err := recordMidjourneyBillingAudit(&task, settlement, reservation.ExpiresAt, "Midjourney billing audit repair"); err != nil {
			model.ReleaseBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt, err)
			return false, err
		}
	}
	if err := model.AcknowledgeBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt); err != nil {
		model.ReleaseBillingReservationAudit(reservation.RequestId, reservation.ExpiresAt, err)
		return false, err
	}
	return true, nil
}

func retryBillingSettlementFailure(failure *model.BillingSettlementFailure) error {
	if failure == nil {
		return nil
	}
	if failure.Delta == 0 {
		if !failure.ReservationManaged {
			return nil
		}
	}
	if failure.ReservationManaged {
		status := failure.ReservationStatus
		if status != model.BillingReservationStatusSettling && status != model.BillingReservationStatusRefunding {
			return fmt.Errorf("invalid managed billing reservation status: %s", status)
		}
		_, err := model.FinalizeBillingReservation(failure.RequestId, failure.ActualQuota, status)
		if err != nil {
			model.RecordBillingReservationAttempt(failure.RequestId, err)
		}
		return err
	}

	tokenKey := ""
	tokenMissing := failure.TokenId == 0
	if failure.TokenId > 0 {
		token, err := model.GetTokenById(failure.TokenId)
		if err != nil {
			tokenMissing = true
			common.SysLog(fmt.Sprintf("billing settlement retry cannot load token %d, will repair funding only: %v", failure.TokenId, err))
		} else {
			tokenKey = token.Key
		}
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:                  failure.UserId,
		TokenId:                 failure.TokenId,
		TokenKey:                tokenKey,
		BillingSource:           failure.BillingSource,
		SubscriptionId:          failure.SubscriptionId,
		RequestId:               failure.RequestId,
		WalletConsumedQuota:     failure.WalletQuotaConsumed,
		WalletConsumedGiftQuota: failure.WalletGiftQuotaConsumed,
		IsPlayground:            tokenMissing,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: failure.ChannelId,
		},
	}

	if failure.FundingSettled {
		return retryBillingSettlementTokenOnly(relayInfo, failure.Delta)
	}
	return PostConsumeQuota(relayInfo, failure.Delta, failure.PreConsumedQuota, false)
}

func retryBillingSettlementTokenOnly(relayInfo *relaycommon.RelayInfo, delta int) error {
	if relayInfo == nil || delta == 0 || relayInfo.IsPlayground {
		return nil
	}
	if delta > 0 {
		return model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, delta)
	}
	return model.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -delta)
}
