package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// BillingSession owns one request's durable pre-consume, settlement and refund
// lifecycle. PostgreSQL remains the source of truth; local fields only mirror
// the active billing_reservations row for logging and compatibility.
type BillingSession struct {
	relayInfo           *relaycommon.RelayInfo
	funding             FundingSource
	preConsumedQuota    int
	tokenConsumed       int
	reservationId       int64
	reservationActive   bool
	finalizationStarted bool
	settled             bool
	refunded            bool
	leaseTTL            time.Duration
	leaseStop           chan struct{}
	leaseStopOnce       sync.Once
	securityBudget      *TokenBudgetReservation
	mu                  sync.Mutex
}

func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.refunded {
		return errors.New("billing reservation was already refunded")
	}
	// Reaching Settle means the provider path succeeded. Even invalid local
	// quota output must retain the dispatched estimate for repair rather than
	// turn the successful upstream call into a refund.
	s.finalizationStarted = true
	s.stopLeaseHeartbeat()
	if actualQuota < 0 {
		return errors.New("actual billing quota cannot be negative")
	}

	// Once an upstream success reaches precise settlement, a later error must
	// never turn that successful provider call into a refund. The durable
	// settling intent (or, at minimum, the dispatched lease) is repaired by a
	// different instance if this process cannot finish it.
	result, err := s.finalizeReservation(actualQuota, model.BillingReservationStatusSettling)
	if err != nil {
		return err
	}
	s.applySettlementResult(result, actualQuota)
	s.securityBudget.Finalize(int64(actualQuota))
	s.reservationActive = false
	s.settled = true
	return nil
}

func (s *BillingSession) commitTask(task *model.Task, actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.refunded || !s.reservationActive {
		return errors.New("billing reservation is not active")
	}
	if task == nil {
		return errors.New("task is nil")
	}

	s.finalizationStarted = true
	s.stopLeaseHeartbeat()
	result, err := model.InsertTaskWithBillingReservation(task, s.relayInfo.RequestId, actualQuota)
	if err != nil {
		return err
	}
	s.applySettlementResult(result, actualQuota)
	s.securityBudget.Finalize(int64(actualQuota))
	s.reservationActive = false
	s.settled = true
	return nil
}

func (s *BillingSession) commitMidjourneyTask(task *model.Midjourney, actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.refunded || !s.reservationActive {
		return errors.New("billing reservation is not active")
	}
	if task == nil {
		return errors.New("midjourney task is nil")
	}
	s.finalizationStarted = true
	s.stopLeaseHeartbeat()
	result, err := model.InsertMidjourneyWithBillingReservation(task, s.relayInfo.RequestId, actualQuota)
	if err != nil {
		return err
	}
	s.applySettlementResult(result, actualQuota)
	s.securityBudget.Finalize(int64(actualQuota))
	s.reservationActive = false
	s.settled = true
	return nil
}

// Refund synchronously persists the refund intent and applies it. If the
// bounded database operation fails, the reservation row is retained for the
// cross-instance repair task.
func (s *BillingSession) Refund(c *gin.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || !s.reservationActive || s.finalizationStarted {
		return nil
	}

	s.stopLeaseHeartbeat()
	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败，返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	result, err := s.finalizeReservation(0, model.BillingReservationStatusRefunding)
	if err != nil {
		recordBillingSettlementFailure(s.relayInfo, 0, s.preConsumedQuota, model.BillingReservationStatusRefunding, err)
		return err
	}
	s.applySettlementResult(result, 0)
	s.securityBudget.Finalize(0)
	s.reservationActive = false
	s.refunded = true
	if err := model.AcknowledgeBillingReservation(s.relayInfo.RequestId); err != nil {
		common.SysLog(fmt.Sprintf("failed to acknowledge refunded billing reservation (request_id=%s): %v", s.relayInfo.RequestId, err))
	}
	return nil
}

func (s *BillingSession) finalizeReservation(actualQuota int, status string) (*model.BillingReservationSettlementResult, error) {
	seconds := common.GetEnvOrDefault("BILLING_ONLINE_SETTLEMENT_TIMEOUT_SECONDS", 8)
	if seconds < 3 {
		seconds = 3
	}
	if seconds > 30 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	return model.FinalizeBillingReservationContext(ctx, s.relayInfo.RequestId, actualQuota, status)
}

func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reservationActive && !s.finalizationStarted && !s.settled && !s.refunded
}

func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || targetQuota <= s.preConsumedQuota {
		return nil
	}
	if !s.reservationActive {
		return errors.New("billing reservation is not active")
	}

	previous := s.preConsumedQuota
	reservation, err := model.IncreaseBillingReservation(
		s.relayInfo.RequestId,
		targetQuota,
		int64(s.leaseTTL.Seconds()),
	)
	if err != nil {
		return err
	}
	s.applyReservationSnapshot(reservation)
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		sub.AmountUsedAfter += int64(s.preConsumedQuota - previous)
	}
	s.syncRelayInfo()
	return nil
}

func (s *BillingSession) MarkDispatched() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded {
		return errors.New("billing reservation is already finalized")
	}
	if !s.reservationActive {
		return errors.New("billing reservation is not active")
	}
	return model.MarkBillingReservationDispatched(s.relayInfo.RequestId, int64(s.leaseTTL.Seconds()), s.relayInfo.ChannelId)
}

func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota
	if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)",
			s.relayInfo.UserId,
			logger.FormatQuota(effectiveQuota),
			s.funding.Source(),
		))
	}

	s.leaseTTL = billingReservationTTL()
	result, err := model.CreateBillingReservationContext(c.Request.Context(), model.BillingReservationCreateInput{
		RequestId:     s.relayInfo.RequestId,
		UserId:        s.relayInfo.UserId,
		TokenId:       s.relayInfo.TokenId,
		TokenKey:      s.relayInfo.TokenKey,
		ChannelId:     common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		BillingSource: s.funding.Source(),
		ModelName:     s.relayInfo.OriginModelName,
		Group:         s.relayInfo.UsingGroup,
		Quota:         effectiveQuota,
		IsPlayground:  s.relayInfo.IsPlayground,
		LeaseSeconds:  int64(s.leaseTTL.Seconds()),
	})
	if err != nil {
		if errors.Is(err, model.ErrBillingReservationTokenNotFound) || strings.Contains(err.Error(), "token quota") {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, model.ErrInsufficientUserQuota) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if strings.Contains(err.Error(), "no active subscription") || strings.Contains(err.Error(), "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	if result == nil || result.Reservation == nil {
		return types.NewError(errors.New("billing reservation result is empty"), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.reservationActive = true
	s.applyReservationSnapshot(result.Reservation)
	if sub, ok := s.funding.(*SubscriptionFunding); ok && result.Subscription != nil {
		sub.subscriptionId = result.Subscription.UserSubscriptionId
		sub.preConsumed = int64(result.Reservation.ReservedQuota)
		sub.AmountTotal = result.Subscription.AmountTotal
		sub.AmountUsedAfter = result.Subscription.AmountUsedAfter
		if result.SubscriptionPlanInfo != nil {
			sub.PlanId = result.SubscriptionPlanInfo.PlanId
			sub.PlanTitle = result.SubscriptionPlanInfo.PlanTitle
		}
	}
	s.syncRelayInfo()
	if c != nil && c.Request != nil {
		s.startLeaseHeartbeat(c.Request.Context())
	}
	return nil
}

func billingReservationTTL() time.Duration {
	seconds := common.GetEnvOrDefault("BILLING_RESERVATION_TTL_SECONDS", 15*60)
	if seconds < 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func (s *BillingSession) startLeaseHeartbeat(ctx context.Context) {
	if !s.reservationActive || s.leaseTTL <= 0 || s.leaseStop != nil {
		return
	}
	interval := s.leaseTTL / 3
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	s.leaseStop = make(chan struct{})
	stop := s.leaseStop
	requestId := s.relayInfo.RequestId
	ttl := s.leaseTTL
	gopool.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if err := model.TouchBillingReservation(requestId, int64(ttl.Seconds())); err != nil {
					common.SysLog(fmt.Sprintf("failed to renew billing reservation (request_id=%s): %v", requestId, err))
				}
			}
		}
	})
}

func (s *BillingSession) stopLeaseHeartbeat() {
	s.leaseStopOnce.Do(func() {
		if s.leaseStop != nil {
			close(s.leaseStop)
		}
	})
}

func (s *BillingSession) applyReservationSnapshot(reservation *model.BillingReservation) {
	if reservation == nil {
		return
	}
	s.reservationId = reservation.Id
	s.preConsumedQuota = reservation.ReservedQuota
	s.tokenConsumed = reservation.TokenQuotaReserved
	if wallet, ok := s.funding.(*WalletFunding); ok {
		wallet.consumed = reservation.ReservedQuota
		wallet.consumedQuota = reservation.WalletQuotaReserved
		wallet.consumedGiftQuota = reservation.WalletGiftQuotaReserved
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		sub.subscriptionId = reservation.SubscriptionId
		sub.preConsumed = int64(reservation.ReservedQuota)
	}
}

func (s *BillingSession) applySettlementResult(result *model.BillingReservationSettlementResult, actualQuota int) {
	if result != nil && result.ReservationId > 0 {
		s.reservationId = result.ReservationId
	}
	if result != nil && result.BillingSource == BillingSourceWallet {
		if wallet, ok := s.funding.(*WalletFunding); ok {
			wallet.consumed = actualQuota
			wallet.consumedQuota = result.WalletQuotaConsumed
			wallet.consumedGiftQuota = result.WalletGiftQuotaConsumed
		}
	}
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(actualQuota - s.preConsumedQuota)
	}
	s.syncRelayInfo()
}

func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
		return
	}
	info.SubscriptionId = 0
	info.SubscriptionPreConsumed = 0
	if wallet, ok := s.funding.(*WalletFunding); ok {
		info.WalletConsumedQuota = wallet.consumedQuota
		info.WalletConsumedGiftQuota = wallet.consumedGiftQuota
	}
}

func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(errors.New("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if preConsumedQuota < 0 || preConsumedQuota > math.MaxInt32 {
		return nil, types.NewError(errors.New("pre-consumed quota is outside database bounds"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if relayInfo.RequestId == "" {
		relayInfo.RequestId = common.GetUUID()
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足，剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		if userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败，用户剩余额度: %s，需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		relayInfo.UserQuota = userQuota
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WalletFunding{},
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &SubscriptionFunding{},
		}
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, apiErr := tryWallet()
		if apiErr != nil && apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
			return trySubscription()
		}
		return session, apiErr
	case "subscription_first":
		fallthrough
	default:
		hasSub, err := model.HasActiveUserSubscription(relayInfo.UserId)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeInsufficientUserQuota {
			return session, apiErr
		}
		allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
		if overflowErr != nil {
			return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if allowOverflow {
			return tryWallet()
		}
		return nil, apiErr
	}
}
