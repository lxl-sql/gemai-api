package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) error {
	claim, claimed, err := ClaimBillingSubmissionAudit(info)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			var contents []string
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	other["billing_audit_key"] = BillingSubmissionAuditKey(info, "task-submit", info.PriceData.Quota)
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	attachQuotaSaturation(c, info, other)
	auditClaimExpiresAt := int64(0)
	if claim != nil {
		auditClaimExpiresAt = claim.ExpiresAt
	}
	logErr := model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId:           info.ChannelId,
		ModelName:           info.OriginModelName,
		TokenName:           tokenName,
		Quota:               info.PriceData.Quota,
		Content:             logContent,
		TokenId:             info.TokenId,
		Group:               info.UsingGroup,
		Other:               other,
		RequestId:           info.RequestId,
		AuditClaimExpiresAt: auditClaimExpiresAt,
	})
	if logErr != nil {
		ReleaseBillingSubmissionAudit(info, claim, logErr)
		return logErr
	}
	if err := CompleteBillingSubmissionAudit(info, claim, info.PriceData.Quota); err != nil {
		ReleaseBillingSubmissionAudit(info, claim, err)
		return err
	}
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
	return nil
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		targetQuota := task.Quota + delta
		if targetQuota < 0 {
			targetQuota = 0
		}
		idempotencyKey := fmt.Sprintf("task_subscription_delta:%s:%d", task.TaskID, targetQuota)
		return model.PostConsumeUserSubscriptionDeltaWithKey(task.PrivateData.SubscriptionId, int64(delta), idempotencyKey)
	}
	if delta > 0 {
		breakdown, err := model.DebitQuotaPreferGiftNoLedger(task.UserId, delta)
		if err != nil {
			return err
		}
		if breakdown != nil {
			task.PrivateData.WalletQuotaConsumed += -breakdown.QuotaDelta
			task.PrivateData.WalletGiftQuotaConsumed += -breakdown.GiftQuotaDelta
		}
		return nil
	}
	refundAmount := -delta
	if refundAmount <= 0 {
		return nil
	}
	consumed := task.PrivateData.WalletQuotaConsumed + task.PrivateData.WalletGiftQuotaConsumed
	if consumed <= 0 {
		_, err := model.RefundQuotaByBreakdownNoLedger(task.UserId, model.QuotaDelta{
			GiftQuotaDelta: refundAmount,
		})
		return err
	}
	if refundAmount > consumed {
		refundAmount = consumed
	}
	finalConsumed := consumed - refundAmount
	finalGiftConsumed := task.PrivateData.WalletGiftQuotaConsumed
	if finalGiftConsumed > finalConsumed {
		finalGiftConsumed = finalConsumed
	}
	finalQuotaConsumed := finalConsumed - finalGiftConsumed
	refundGift := task.PrivateData.WalletGiftQuotaConsumed - finalGiftConsumed
	refundQuota := task.PrivateData.WalletQuotaConsumed - finalQuotaConsumed

	_, err := model.RefundQuotaByBreakdownNoLedger(task.UserId, model.QuotaDelta{
		QuotaDelta:     refundQuota,
		GiftQuotaDelta: refundGift,
	})
	if err != nil {
		return err
	}
	task.PrivateData.WalletQuotaConsumed = finalQuotaConsumed
	task.PrivateData.WalletGiftQuotaConsumed = finalGiftConsumed
	return nil
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				other[k] = v
			}
		}
	}
	if task.PrivateData.WalletQuotaConsumed > 0 || task.PrivateData.WalletGiftQuotaConsumed > 0 {
		other["deducted_quota"] = task.PrivateData.WalletQuotaConsumed
		other["deducted_gift_quota"] = task.PrivateData.WalletGiftQuotaConsumed
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

func persistTaskBillingState(ctx context.Context, task *model.Task) {
	if task == nil || task.ID == 0 {
		return
	}
	if err := model.DB.Model(task).Select("quota", "private_data").Updates(task).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("保存任务计费状态失败 task %s: %s", task.TaskID, err.Error()))
	}
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// FinalizeTaskTransition commits terminal task state and its billing delta in
// one main-database transaction. The completed reservation receipt is removed
// only after the corresponding log-database audit succeeds.
func FinalizeTaskTransition(ctx context.Context, task *model.Task, fromStatus model.TaskStatus, actualQuota int, reason string, clamps ...*common.QuotaClamp) (bool, error) {
	won, settlement, err := model.FinalizeTaskBilling(task, fromStatus, actualQuota)
	if err != nil || !won {
		return won, err
	}
	financialDelta := actualQuota - settlement.PreConsumedQuota
	if financialDelta > 0 {
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, financialDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, financialDelta)
	}
	claim, claimed, err := model.ClaimBillingReservationAudit(settlement.RequestId, billingAuditClaimTTLSeconds())
	if err != nil {
		return true, err
	}
	if !claimed {
		return true, nil
	}
	if err := recordTaskBillingAudit(task, settlement, claim.ExpiresAt, reason, clamps...); err != nil {
		model.ReleaseBillingReservationAudit(settlement.RequestId, claim.ExpiresAt, err)
		return true, err
	}
	if err := model.AcknowledgeBillingReservationAudit(settlement.RequestId, claim.ExpiresAt); err != nil {
		model.ReleaseBillingReservationAudit(settlement.RequestId, claim.ExpiresAt, err)
		return true, err
	}
	return true, nil
}

func recordTaskBillingAudit(task *model.Task, settlement *model.BillingReservationSettlementResult, claimExpiresAt int64, reason string, clamps ...*common.QuotaClamp) error {
	if task == nil || settlement == nil {
		return errors.New("invalid task billing audit state")
	}
	auditedQuota := settlement.AuditedQuota
	if auditedQuota == 0 && task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.SubmittedQuota > 0 {
		submittedQuota := task.PrivateData.BillingContext.SubmittedQuota
		submitKey := billingAuditKey("task-submit", settlement.RequestId, settlement.ReservationId, 0, submittedQuota)
		if exists, err := model.HasTaskBillingAudit(settlement.RequestId, submitKey); err != nil {
			return err
		} else if exists {
			auditedQuota = submittedQuota
		}
	}
	auditDelta := settlement.ActualQuota - auditedQuota
	if auditDelta == 0 {
		return nil
	}
	auditKey := billingAuditKey("task", settlement.RequestId, settlement.ReservationId, auditedQuota, settlement.ActualQuota)
	if exists, err := model.HasTaskBillingAudit(settlement.RequestId, auditKey); err != nil {
		return err
	} else if exists {
		return nil
	}
	logType := model.LogTypeConsume
	logQuota := auditDelta
	if auditDelta < 0 {
		logType = model.LogTypeRefund
		logQuota = -auditDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = settlement.PreConsumedQuota
	other["audited_quota"] = auditedQuota
	other["actual_quota"] = settlement.ActualQuota
	other["reason"] = reason
	other["billing_audit_key"] = auditKey
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	return model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:              task.UserId,
		LogType:             logType,
		Content:             reason,
		ChannelId:           task.ChannelId,
		ModelName:           taskModelName(task),
		Quota:               logQuota,
		TokenId:             task.PrivateData.TokenId,
		Group:               task.Group,
		Other:               other,
		NodeName:            task.PrivateData.NodeName,
		RequestId:           settlement.RequestId,
		AuditClaimExpiresAt: claimExpiresAt,
	})
}

// FinalizeMidjourneyTransition atomically transitions a Midjourney task and
// its billing, then records and acknowledges the terminal audit.
func FinalizeMidjourneyTransition(task *model.Midjourney, fromStatus string, actualQuota int, reason string) (bool, error) {
	won, settlement, err := model.FinalizeMidjourneyBilling(task, fromStatus, actualQuota)
	if err != nil || !won {
		return won, err
	}
	financialDelta := settlement.ActualQuota - settlement.PreConsumedQuota
	if financialDelta > 0 {
		model.UpdateUserUsedQuotaAndRequestCount(settlement.UserId, financialDelta)
		model.UpdateChannelUsedQuota(settlement.ChannelId, financialDelta)
	}
	claim, claimed, err := model.ClaimBillingReservationAudit(settlement.RequestId, billingAuditClaimTTLSeconds())
	if err != nil {
		return true, err
	}
	if !claimed {
		return true, nil
	}
	if err := recordMidjourneyBillingAudit(task, settlement, claim.ExpiresAt, reason); err != nil {
		model.ReleaseBillingReservationAudit(settlement.RequestId, claim.ExpiresAt, err)
		return true, err
	}
	if err := model.AcknowledgeBillingReservationAudit(settlement.RequestId, claim.ExpiresAt); err != nil {
		model.ReleaseBillingReservationAudit(settlement.RequestId, claim.ExpiresAt, err)
		return true, err
	}
	return true, nil
}

func recordMidjourneyBillingAudit(task *model.Midjourney, settlement *model.BillingReservationSettlementResult, claimExpiresAt int64, reason string) error {
	if task == nil || settlement == nil {
		return errors.New("invalid Midjourney billing audit state")
	}
	auditedQuota := settlement.AuditedQuota
	if auditedQuota == 0 && settlement.PreConsumedQuota > 0 {
		submitKey := billingAuditKey("midjourney-submit", settlement.RequestId, settlement.ReservationId, 0, settlement.PreConsumedQuota)
		if exists, err := model.HasTaskBillingAudit(settlement.RequestId, submitKey); err != nil {
			return err
		} else if exists {
			auditedQuota = settlement.PreConsumedQuota
		}
	}
	auditDelta := settlement.ActualQuota - auditedQuota
	if auditDelta == 0 {
		return nil
	}
	auditKey := billingAuditKey("midjourney", settlement.RequestId, settlement.ReservationId, auditedQuota, settlement.ActualQuota)
	if exists, err := model.HasTaskBillingAudit(settlement.RequestId, auditKey); err != nil {
		return err
	} else if exists {
		return nil
	}
	logType := model.LogTypeConsume
	logQuota := auditDelta
	if auditDelta < 0 {
		logType = model.LogTypeRefund
		logQuota = -auditDelta
	}
	return model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:              settlement.UserId,
		LogType:             logType,
		Content:             reason,
		ChannelId:           settlement.ChannelId,
		ModelName:           CovertMjpActionToModelName(task.Action),
		Quota:               logQuota,
		TokenId:             settlement.TokenId,
		Group:               settlement.Group,
		RequestId:           settlement.RequestId,
		AuditClaimExpiresAt: claimExpiresAt,
		Other: map[string]interface{}{
			"task_id":            task.MjId,
			"pre_consumed_quota": settlement.PreConsumedQuota,
			"audited_quota":      auditedQuota,
			"actual_quota":       settlement.ActualQuota,
			"reason":             reason,
			"billing_audit_key":  auditKey,
		},
	})
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	quota := task.Quota
	if quota == 0 {
		return
	}

	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	task.Quota = 0
	persistTaskBillingState(ctx, task)

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 记录日志
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) {
	if actualQuota <= 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		return
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota
	persistTaskBillingState(ctx, task)

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	actualQuota, reason, clamp, ok := CalculateTaskQuotaByTokens(task, totalTokens)
	if !ok {
		return
	}
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}

// CalculateTaskQuotaByTokens calculates a terminal quota without mutating any
// balance. Callers can then include it in the atomic task transition.
func CalculateTaskQuotaByTokens(task *model.Task, totalTokens int) (int, string, *common.QuotaClamp, bool) {
	if task == nil || totalTokens <= 0 {
		return 0, "", nil, false
	}
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(taskModelName(task))
	if !hasRatioSetting || modelRatio <= 0 {
		return 0, "", nil, false
	}
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, true)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0, "", nil, false
	}
	groupRatio := ratio_setting.GetGroupRatio(group)
	if userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(group, group); ok {
		groupRatio = userGroupRatio
	}
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * groupRatio * otherMultiplier)
	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, groupRatio, otherMultiplier)
	return actualQuota, reason, clamp, true
}

type TaskCompletionBillingAdaptor interface {
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

func billingAuditClaimTTLSeconds() int64 {
	seconds := common.GetEnvOrDefault("BILLING_AUDIT_CLAIM_TTL_SECONDS", 60)
	minimum := model.BillingAuditLogTimeoutSeconds() + 15
	if seconds < minimum {
		seconds = minimum
	}
	if seconds > 3600 {
		seconds = 3600
	}
	return int64(seconds)
}

// ResolveTaskCompletionBilling determines terminal success quota without
// mutating billing state, so polling and realtime-fetch paths share one policy.
func ResolveTaskCompletionBilling(adaptor TaskCompletionBillingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) (int, string, *common.QuotaClamp) {
	if task == nil {
		return 0, "invalid task", nil
	}
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		quota := bc.SubmittedQuota
		if quota == 0 {
			quota = task.Quota
		}
		return quota, "per-call task billing", nil
	}
	if adaptor != nil {
		if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
			return actualQuota, "adaptor billing adjustment", nil
		}
	}
	if taskResult != nil {
		if actualQuota, reason, clamp, ok := CalculateTaskQuotaByTokens(task, taskResult.TotalTokens); ok && actualQuota > 0 {
			return actualQuota, reason, clamp
		}
	}
	return task.Quota, "task estimate retained", nil
}
