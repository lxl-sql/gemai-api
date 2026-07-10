package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type BillingSettlementRepairSummary struct {
	Scanned int `json:"scanned"`
	Settled int `json:"settled"`
	Failed  int `json:"failed"`
}

func RunBillingSettlementRepairOnce(ctx context.Context) BillingSettlementRepairSummary {
	summary := BillingSettlementRepairSummary{}
	limit := common.GetEnvOrDefault("BILLING_SETTLEMENT_REPAIR_BATCH_SIZE", 100)
	failures, err := model.FindPendingBillingSettlementFailures(limit)
	if err != nil {
		common.SysLog("failed to find pending billing settlement failures: " + err.Error())
		return summary
	}
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
	return summary
}

func retryBillingSettlementFailure(failure *model.BillingSettlementFailure) error {
	if failure == nil {
		return nil
	}
	if failure.Delta == 0 {
		return nil
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
