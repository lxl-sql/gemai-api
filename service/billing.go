package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
)

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	var securityBudget *TokenBudgetReservation
	if !relayInfo.IsPlayground {
		var err error
		securityBudget, err = ReserveTokenSecurityBudget(c, relayInfo.TokenId, preConsumedQuota)
		if err != nil {
			RecordTokenSecurityRejection(c, err, relayInfo.OriginModelName, preConsumedQuota)
			return types.NewErrorWithStatusCode(
				fmt.Errorf("%s", TokenSecurityErrorMessage(err)),
				TokenSecurityErrorCode(err),
				TokenSecurityHTTPStatus(err),
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		securityBudget.Finalize(0)
		return apiErr
	}
	session.securityBudget = securityBudget
	relayInfo.Billing = session
	return nil
}

// CommitTaskSubmission atomically inserts an accepted asynchronous task and
// settles its submission quota. Free tasks have no billing reservation and are
// inserted directly.
func CommitTaskSubmission(task *model.Task, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if task == nil || relayInfo == nil {
		return fmt.Errorf("invalid task submission billing state")
	}
	if relayInfo.Billing == nil {
		if relayInfo.PriceData.FreeModel {
			actualQuota = 0
		}
		task.Quota = actualQuota
		return task.Insert()
	}
	session, ok := relayInfo.Billing.(*BillingSession)
	if !ok {
		return fmt.Errorf("unsupported billing session type %T", relayInfo.Billing)
	}
	if err := session.commitTask(task, actualQuota); err != nil {
		recordBillingSettlementFailure(relayInfo, actualQuota, session.GetPreConsumedQuota(), model.BillingReservationStatusSettling, err)
		return err
	}
	return nil
}

// CommitMidjourneySubmission is the Midjourney equivalent of
// CommitTaskSubmission.
func CommitMidjourneySubmission(task *model.Midjourney, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if task == nil || relayInfo == nil {
		return fmt.Errorf("invalid Midjourney submission billing state")
	}
	if relayInfo.Billing == nil {
		if relayInfo.PriceData.FreeModel {
			actualQuota = 0
		}
		task.Quota = actualQuota
		return task.Insert()
	}
	session, ok := relayInfo.Billing.(*BillingSession)
	if !ok {
		return fmt.Errorf("unsupported billing session type %T", relayInfo.Billing)
	}
	if err := session.commitMidjourneyTask(task, actualQuota); err != nil {
		recordBillingSettlementFailure(relayInfo, actualQuota, session.GetPreConsumedQuota(), model.BillingReservationStatusSettling, err)
		return err
	}
	return nil
}

// AcknowledgeBilling removes the completed reservation receipt after the
// consumption log is durable. Failed or incomplete settlements are left for
// the repair worker.
func AcknowledgeBilling(relayInfo *relaycommon.RelayInfo) error {
	if relayInfo == nil || relayInfo.Billing == nil {
		return nil
	}
	session, ok := relayInfo.Billing.(*BillingSession)
	if !ok {
		return nil
	}
	session.mu.Lock()
	settled := session.settled
	session.mu.Unlock()
	if !settled {
		return nil
	}
	return model.AcknowledgeBillingReservation(relayInfo.RequestId)
}

// RecordConsumeLogAndAcknowledge writes the immutable consumption log before
// removing the completed reservation receipt.
func RecordConsumeLogAndAcknowledge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params model.RecordConsumeLogParams) error {
	if relayInfo == nil {
		return fmt.Errorf("relay info is nil")
	}
	claim, claimed, err := ClaimBillingSubmissionAudit(relayInfo)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	if params.Other == nil {
		params.Other = make(map[string]interface{})
	}
	params.RequestId = relayInfo.RequestId
	params.Other["billing_audit_key"] = BillingSubmissionAuditKey(relayInfo, "request", params.Quota)
	if claim != nil {
		params.AuditClaimExpiresAt = claim.ExpiresAt
	}
	if err := model.RecordConsumeLog(ctx, relayInfo.UserId, params); err != nil {
		ReleaseBillingSubmissionAudit(relayInfo, claim, err)
		return err
	}
	if claim != nil {
		if err := model.AcknowledgeBillingReservationAudit(relayInfo.RequestId, claim.ExpiresAt); err != nil {
			ReleaseBillingSubmissionAudit(relayInfo, claim, err)
			return err
		}
		return nil
	}
	return AcknowledgeBilling(relayInfo)
}

func BillingSubmissionAuditKey(relayInfo *relaycommon.RelayInfo, kind string, actualQuota int) string {
	if relayInfo == nil {
		return ""
	}
	var reservationId int64
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		session.mu.Lock()
		reservationId = session.reservationId
		session.mu.Unlock()
	}
	return billingAuditKey(kind, relayInfo.RequestId, reservationId, 0, actualQuota)
}

// ClaimBillingSubmissionAudit serializes an asynchronous task's initial log
// with terminal billing audits. A nil claim means the request has no durable
// reservation and can be logged directly.
func ClaimBillingSubmissionAudit(relayInfo *relaycommon.RelayInfo) (*model.BillingReservation, bool, error) {
	if relayInfo == nil || relayInfo.Billing == nil {
		return nil, true, nil
	}
	return model.ClaimBillingReservationAudit(relayInfo.RequestId, billingAuditClaimTTLSeconds())
}

func CompleteBillingSubmissionAudit(relayInfo *relaycommon.RelayInfo, claim *model.BillingReservation, quota int) error {
	if relayInfo == nil || claim == nil {
		return nil
	}
	return model.CompleteBillingReservationAuditClaim(relayInfo.RequestId, claim.ExpiresAt, quota)
}

func ReleaseBillingSubmissionAudit(relayInfo *relaycommon.RelayInfo, claim *model.BillingReservation, auditErr error) {
	if relayInfo == nil || claim == nil {
		return
	}
	model.ReleaseBillingReservationAudit(relayInfo.RequestId, claim.ExpiresAt, auditErr)
}

func billingAuditKey(kind string, requestId string, reservationId int64, auditedQuota int, actualQuota int) string {
	return fmt.Sprintf("%s:%s:%d:%d:%d", kind, requestId, reservationId, auditedQuota, actualQuota)
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 否则回退到旧的 PostConsumeQuota 路径（兼容按次计费等场景）。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if delta > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费与实际消耗一致，无需调整：%s（按次计费）",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			recordBillingSettlementFailure(relayInfo, actualQuota, preConsumed, model.BillingReservationStatusSettling, err)
			return err
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
			} else {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	// 回退：无 BillingSession 时使用旧路径
	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	if quotaDelta != 0 {
		if err := PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true); err != nil {
			recordBillingSettlementFailure(relayInfo, actualQuota, relayInfo.FinalPreConsumedQuota, "", err)
			return err
		}
	}
	return nil
}

func recordBillingSettlementFailure(relayInfo *relaycommon.RelayInfo, actualQuota int, preConsumed int, reservationStatus string, settleErr error) {
	if relayInfo == nil || settleErr == nil {
		return
	}
	delta := actualQuota - preConsumed
	if delta == 0 && relayInfo.Billing == nil {
		return
	}
	if err := model.RecordBillingSettlementFailure(model.BillingSettlementFailureInput{
		RequestId:               relayInfo.RequestId,
		UserId:                  relayInfo.UserId,
		TokenId:                 relayInfo.TokenId,
		ChannelId:               relayInfo.ChannelId,
		BillingSource:           relayInfo.BillingSource,
		SubscriptionId:          relayInfo.SubscriptionId,
		ActualQuota:             actualQuota,
		PreConsumedQuota:        preConsumed,
		Delta:                   delta,
		WalletQuotaConsumed:     relayInfo.WalletConsumedQuota,
		WalletGiftQuotaConsumed: relayInfo.WalletConsumedGiftQuota,
		FundingSettled:          false,
		ReservationManaged:      relayInfo.Billing != nil,
		ReservationStatus:       reservationStatus,
		LastError:               settleErr.Error(),
	}); err != nil {
		common.SysLog(fmt.Sprintf("failed to record billing settlement failure (request_id=%s user_id=%d delta=%d): %v",
			relayInfo.RequestId, relayInfo.UserId, delta, err))
	}
}
