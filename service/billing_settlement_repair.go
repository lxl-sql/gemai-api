package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type BillingSettlementRepairSummary struct {
	Scanned              int `json:"scanned"`
	Settled              int `json:"settled"`
	Failed               int `json:"failed"`
	ReservationsScanned  int `json:"reservations_scanned"`
	ReservationsRepaired int `json:"reservations_repaired"`
	ReservationsFailed   int `json:"reservations_failed"`
	AuditsScanned        int `json:"audits_scanned"`
	AuditsRepaired       int `json:"audits_repaired"`
	AuditsFailed         int `json:"audits_failed"`
	AuditMarkersDeleted  int `json:"audit_markers_deleted"`
}

func RunBillingSettlementRepairOnce(ctx context.Context) BillingSettlementRepairSummary {
	summary := BillingSettlementRepairSummary{}
	limit := common.GetEnvOrDefault("BILLING_SETTLEMENT_REPAIR_BATCH_SIZE", 1000)
	failures, err := model.FindPendingBillingSettlementFailures(limit)
	if err != nil {
		common.SysLog("failed to find pending billing settlement failures: " + err.Error())
	} else {
		for _, failure := range failures {
			if ctx != nil && ctx.Err() != nil {
				break
			}
			summary.Scanned++
			if err := retryBillingSettlementFailure(failure); err != nil {
				summary.Failed++
				if markErr := model.MarkBillingSettlementFailureAttempt(failure.Id, err); markErr != nil {
					common.SysLog("failed to mark billing settlement retry attempt: " + markErr.Error())
				}
				continue
			}
			summary.Settled++
			if err := model.MarkBillingSettlementFailureSettled(failure.Id); err != nil {
				common.SysLog("failed to mark billing settlement settled: " + err.Error())
			}
		}
	}
	markerCutoff := model.GetDBTimestamp() - int64(model.BillingAuditMarkerRetentionSeconds())
	deletedMarkers, markerErr := model.DeleteExpiredBillingAuditMarkers(limit, markerCutoff)
	if markerErr != nil {
		common.SysLog("failed to delete expired billing audit markers: " + markerErr.Error())
	} else {
		summary.AuditMarkersDeleted = deletedMarkers
	}

	reservationLimit := common.GetEnvOrDefault("BILLING_RESERVATION_REPAIR_BATCH_SIZE", 1000)
	reservations, err := model.FindDueBillingReservations(reservationLimit)
	if err != nil {
		common.SysLog("failed to find due billing reservations: " + err.Error())
		return summary
	}
	for _, reservation := range reservations {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		summary.ReservationsScanned++
		repaired, repairErr := model.RepairExpiredBillingReservation(reservation.RequestId)
		if repairErr != nil {
			summary.ReservationsFailed++
			model.RecordBillingReservationAttempt(reservation.RequestId, repairErr)
			common.SysLog(fmt.Sprintf("failed to repair billing reservation (request_id=%s status=%s): %v",
				reservation.RequestId, reservation.Status, repairErr))
			continue
		}
		if repaired {
			summary.ReservationsRepaired++
		}
	}

	submissionReceipts, err := model.FindPendingTaskSubmissionBillingReservations(reservationLimit)
	if err != nil {
		common.SysLog("failed to find pending task submission billing audits: " + err.Error())
		return summary
	}
	for _, reservation := range submissionReceipts {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		summary.AuditsScanned++
		repaired, err := repairPendingTaskSubmissionBillingAudit(reservation)
		if err != nil {
			summary.AuditsFailed++
			common.SysLog(fmt.Sprintf("failed to repair task submission billing audit (request_id=%s): %v", reservation.RequestId, err))
			continue
		}
		if repaired {
			summary.AuditsRepaired++
		}
	}

	auditReceipts, err := model.FindCompletedTaskBillingReservations(reservationLimit)
	if err != nil {
		common.SysLog("failed to find completed task billing audits: " + err.Error())
		return summary
	}
	for _, reservation := range auditReceipts {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		summary.AuditsScanned++
		repaired, err := repairCompletedTaskBillingAudit(reservation)
		if err != nil {
			summary.AuditsFailed++
			common.SysLog(fmt.Sprintf("failed to repair completed task billing audit (request_id=%s): %v", reservation.RequestId, err))
			continue
		}
		if repaired {
			summary.AuditsRepaired++
		}
	}
	standaloneGrace := common.GetEnvOrDefault("BILLING_STANDALONE_AUDIT_GRACE_SECONDS", 60)
	if standaloneGrace < 15 {
		standaloneGrace = 15
	}
	standaloneReceipts, err := model.FindCompletedStandaloneBillingReservations(
		reservationLimit,
		model.GetDBTimestamp()-int64(standaloneGrace),
	)
	if err != nil {
		common.SysLog("failed to find completed request billing audits: " + err.Error())
		return summary
	}
	for _, reservation := range standaloneReceipts {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		summary.AuditsScanned++
		repaired, err := repairCompletedStandaloneBillingAudit(reservation)
		if err != nil {
			summary.AuditsFailed++
			common.SysLog(fmt.Sprintf("failed to repair completed request billing audit (request_id=%s): %v", reservation.RequestId, err))
			continue
		}
		if repaired {
			summary.AuditsRepaired++
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
