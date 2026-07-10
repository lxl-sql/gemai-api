package service

import (
	"errors"
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
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
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
			recordBillingSettlementFailure(relayInfo, actualQuota, preConsumed, err)
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
			recordBillingSettlementFailure(relayInfo, actualQuota, relayInfo.FinalPreConsumedQuota, err)
			return err
		}
	}
	return nil
}

func recordBillingSettlementFailure(relayInfo *relaycommon.RelayInfo, actualQuota int, preConsumed int, settleErr error) {
	if relayInfo == nil || settleErr == nil {
		return
	}
	delta := actualQuota - preConsumed
	if delta == 0 {
		return
	}
	var settlementErr *BillingSettlementError
	fundingSettled := false
	if errors.As(settleErr, &settlementErr) {
		fundingSettled = settlementErr.FundingSettled
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
		FundingSettled:          fundingSettled,
		LastError:               settleErr.Error(),
	}); err != nil {
		common.SysLog(fmt.Sprintf("failed to record billing settlement failure (request_id=%s user_id=%d delta=%d): %v",
			relayInfo.RequestId, relayInfo.UserId, delta, err))
	}
}
