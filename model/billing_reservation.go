package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingReservationStatusReserved   = "reserved"
	BillingReservationStatusDispatched = "dispatched"
	BillingReservationStatusSettling   = "settling"
	BillingReservationStatusRefunding  = "refunding"
	BillingReservationStatusCompleted  = "completed"
	BillingReservationStatusAuditing   = "auditing"

	BillingReservationSourceWallet       = "wallet"
	BillingReservationSourceSubscription = "subscription"
	billingReservationNoExpiry           = int64(math.MaxInt64)
)

var (
	ErrBillingReservationNotReady       = errors.New("billing reservation has no settlement intent")
	ErrBillingReservationIntentConflict = errors.New("billing reservation settlement intent conflicts with existing intent")
	ErrBillingReservationTokenNotFound  = errors.New("billing reservation token not found")
	ErrBillingReservationAuditClaimLost = errors.New("billing reservation audit claim was lost")
)

// BillingReservation is a durable record for one request's billing lifecycle.
// Active rows carry the pre-consume lease and settlement intent. A successfully
// charged row briefly remains in completed state until its consumption audit is
// recorded; this closes the crash window between the main and log databases.
type BillingReservation struct {
	Id                      int64  `json:"id" gorm:"primaryKey;index:idx_billing_reservation_due,priority:2;index:idx_billing_reservation_audit,priority:3"`
	RequestId               string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId                  int    `json:"user_id"`
	TokenId                 int    `json:"token_id"`
	ChannelId               int    `json:"channel_id"`
	TaskId                  int64  `json:"task_id" gorm:"index"`
	MidjourneyId            int    `json:"midjourney_id" gorm:"index"`
	BillingSource           string `json:"billing_source" gorm:"type:varchar(32)"`
	SubscriptionId          int    `json:"subscription_id"`
	ModelName               string `json:"model_name" gorm:"type:varchar(191)"`
	Group                   string `json:"group" gorm:"type:varchar(64)"`
	ReservedQuota           int    `json:"reserved_quota" gorm:"type:int"`
	WalletQuotaReserved     int    `json:"wallet_quota_reserved" gorm:"type:int"`
	WalletGiftQuotaReserved int    `json:"wallet_gift_quota_reserved" gorm:"type:int"`
	TokenQuotaReserved      int    `json:"token_quota_reserved" gorm:"type:int"`
	TokenQuotaEnabled       *bool  `json:"token_quota_enabled"`
	DesiredQuota            int    `json:"desired_quota" gorm:"type:int"`
	AuditedQuota            int    `json:"audited_quota" gorm:"type:int"`
	Status                  string `json:"status" gorm:"type:varchar(32);index:idx_billing_reservation_audit,priority:1"`
	Attempts                int    `json:"attempts" gorm:"type:int"`
	LastError               string `json:"last_error" gorm:"type:text"`
	ExpiresAt               int64  `json:"expires_at" gorm:"type:bigint;index:idx_billing_reservation_due,priority:1"`
	CreatedAt               int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt               int64  `json:"updated_at" gorm:"type:bigint;index:idx_billing_reservation_audit,priority:2"`
}

func (reservation *BillingReservation) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if reservation.CreatedAt == 0 {
		reservation.CreatedAt = now
	}
	if reservation.UpdatedAt == 0 {
		reservation.UpdatedAt = now
	}
	if reservation.Status == "" {
		reservation.Status = BillingReservationStatusReserved
	}
	return nil
}

type BillingReservationCreateInput struct {
	RequestId     string
	UserId        int
	TokenId       int
	TokenKey      string
	ChannelId     int
	BillingSource string
	ModelName     string
	Group         string
	Quota         int
	IsPlayground  bool
	ExpiresAt     int64
	LeaseSeconds  int64
}

type BillingReservationCreateResult struct {
	Reservation          *BillingReservation
	Subscription         *SubscriptionPreConsumeResult
	SubscriptionPlanInfo *SubscriptionPlanInfo
	Reused               bool
}

type BillingReservationSettlementResult struct {
	Completed               bool
	ReservationId           int64
	RequestId               string
	TaskId                  int64
	MidjourneyId            int
	UserId                  int
	TokenId                 int
	ChannelId               int
	ModelName               string
	Group                   string
	BillingSource           string
	SubscriptionId          int
	PreConsumedQuota        int
	ActualQuota             int
	AuditedQuota            int
	WalletQuotaConsumed     int
	WalletGiftQuotaConsumed int
}

func CreateBillingReservation(input BillingReservationCreateInput) (*BillingReservationCreateResult, error) {
	input.RequestId = strings.TrimSpace(input.RequestId)
	if input.RequestId == "" {
		return nil, errors.New("request_id is empty")
	}
	if len(input.RequestId) > 64 {
		return nil, errors.New("request_id is too long")
	}
	if len(input.ModelName) > 191 {
		return nil, errors.New("billing reservation model_name is too long")
	}
	if len(input.Group) > 64 {
		return nil, errors.New("billing reservation group is too long")
	}
	if input.UserId <= 0 {
		return nil, errors.New("invalid user_id")
	}
	if input.Quota < 0 {
		return nil, errors.New("billing reservation quota cannot be negative")
	}
	if input.Quota > math.MaxInt32 {
		return nil, errors.New("billing reservation quota exceeds database limit")
	}
	if input.BillingSource != BillingReservationSourceWallet && input.BillingSource != BillingReservationSourceSubscription {
		return nil, fmt.Errorf("unsupported billing source: %s", input.BillingSource)
	}
	if input.BillingSource == BillingReservationSourceSubscription && input.Quota <= 0 {
		return nil, errors.New("subscription reservation quota must be positive")
	}
	if input.ExpiresAt <= 0 && input.LeaseSeconds <= 0 {
		return nil, errors.New("billing reservation expiry is required")
	}
	if !input.IsPlayground && input.TokenId <= 0 {
		return nil, errors.New("token_id is required for billing reservation")
	}

	result := &BillingReservationCreateResult{}
	created := false
	unlock := lockQuotaUser(input.UserId)
	defer unlock()

	err := DB.Transaction(func(tx *gorm.DB) error {
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		expiresAt := input.ExpiresAt
		if input.LeaseSeconds > 0 {
			expiresAt = now + input.LeaseSeconds
		}
		tokenQuotaEnabled := !input.IsPlayground
		reservation := &BillingReservation{
			RequestId:         input.RequestId,
			UserId:            input.UserId,
			TokenId:           input.TokenId,
			ChannelId:         input.ChannelId,
			BillingSource:     input.BillingSource,
			ModelName:         input.ModelName,
			Group:             input.Group,
			Status:            BillingReservationStatusReserved,
			ExpiresAt:         expiresAt,
			TokenQuotaEnabled: &tokenQuotaEnabled,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		var insert *gorm.DB
		if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
			insert = tx.Clauses(clause.Insert{Modifier: "IGNORE"}).Create(reservation)
		} else {
			insert = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(reservation)
		}
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			var existing BillingReservation
			if err := tx.Where("request_id = ?", input.RequestId).First(&existing).Error; err != nil {
				return err
			}
			if err := validateReusedBillingReservation(&existing, input); err != nil {
				return err
			}
			result.Reservation = &existing
			result.Reused = true
			return nil
		}
		created = true
		if tokenQuotaEnabled && input.Quota == 0 {
			var tokenCount int64
			if err := tx.Model(&Token{}).Where("id = ? AND user_id = ?", input.TokenId, input.UserId).Count(&tokenCount).Error; err != nil {
				return err
			}
			if tokenCount == 0 {
				return ErrBillingReservationTokenNotFound
			}
		}

		switch input.BillingSource {
		case BillingReservationSourceWallet:
			if input.Quota > 0 {
				breakdown, err := DebitQuotaPreferGiftNoLedgerTx(tx, input.UserId, input.Quota)
				if err != nil {
					return err
				}
				if breakdown != nil {
					reservation.WalletQuotaReserved = -breakdown.QuotaDelta
					reservation.WalletGiftQuotaReserved = -breakdown.GiftQuotaDelta
				}
			}
		case BillingReservationSourceSubscription:
			subscription, err := preConsumeUserSubscriptionTx(tx, input.RequestId, input.UserId, input.ModelName, 0, int64(input.Quota))
			if err != nil {
				return err
			}
			if subscription.IdempotencyReused {
				return fmt.Errorf("request_id %s was already used by a completed subscription reservation", input.RequestId)
			}
			reservation.SubscriptionId = subscription.UserSubscriptionId
			result.Subscription = subscription
		}

		if input.Quota > 0 && tokenQuotaEnabled {
			if err := adjustBillingReservationTokenTx(tx, input.UserId, input.TokenId, input.Quota, false); err != nil {
				return err
			}
			reservation.TokenQuotaReserved = input.Quota
		}

		reservation.ReservedQuota = input.Quota
		reservation.DesiredQuota = input.Quota
		reservation.UpdatedAt = now
		if err := tx.Model(&BillingReservation{}).
			Where("id = ?", reservation.Id).
			Updates(map[string]interface{}{
				"subscription_id":            reservation.SubscriptionId,
				"reserved_quota":             reservation.ReservedQuota,
				"wallet_quota_reserved":      reservation.WalletQuotaReserved,
				"wallet_gift_quota_reserved": reservation.WalletGiftQuotaReserved,
				"token_quota_reserved":       reservation.TokenQuotaReserved,
				"desired_quota":              reservation.DesiredQuota,
				"updated_at":                 reservation.UpdatedAt,
			}).Error; err != nil {
			return err
		}
		result.Reservation = reservation
		return nil
	})
	if err != nil {
		if created {
			// A commit error can be ambiguous. Cache eviction is harmless after a
			// rollback and necessary when the transaction actually committed.
			invalidateBillingReservationCaches(input.UserId, input.TokenKey)
		}
		return nil, err
	}
	if result.Reservation == nil {
		return nil, errors.New("billing reservation was not created")
	}

	if result.Subscription == nil && result.Reservation.SubscriptionId > 0 {
		var sub UserSubscription
		if err := DB.Where("id = ?", result.Reservation.SubscriptionId).First(&sub).Error; err == nil {
			result.Subscription = &SubscriptionPreConsumeResult{
				UserSubscriptionId: sub.Id,
				PreConsumed:        int64(result.Reservation.ReservedQuota),
				AmountTotal:        sub.AmountTotal,
				AmountUsedBefore:   sub.AmountUsed,
				AmountUsedAfter:    sub.AmountUsed,
			}
		}
	}
	if result.Reservation.SubscriptionId > 0 {
		result.SubscriptionPlanInfo, _ = GetSubscriptionPlanInfoByUserSubscriptionId(result.Reservation.SubscriptionId)
	}
	// Also invalidate on idempotent reuse. A previous process may have committed
	// the reservation and crashed before evicting Redis, and this retry is the
	// first safe opportunity to repair that cache state.
	invalidateBillingReservationCaches(input.UserId, input.TokenKey)
	return result, nil
}

func validateReusedBillingReservation(existing *BillingReservation, input BillingReservationCreateInput) error {
	if existing == nil {
		return errors.New("existing billing reservation is nil")
	}
	if existing.UserId != input.UserId || existing.TokenId != input.TokenId ||
		existing.BillingSource != input.BillingSource || existing.ModelName != input.ModelName || existing.Group != input.Group {
		return fmt.Errorf("request_id %s is already used by another billing reservation", input.RequestId)
	}
	if billingReservationTokenQuotaEnabled(existing) != !input.IsPlayground {
		return fmt.Errorf("billing reservation %s has inconsistent token quota policy", input.RequestId)
	}
	if existing.Status != BillingReservationStatusReserved {
		return fmt.Errorf("billing reservation %s is already finalizing", input.RequestId)
	}
	if existing.ReservedQuota < input.Quota {
		return fmt.Errorf("billing reservation %s has inconsistent reserved quota", input.RequestId)
	}
	return nil
}

func billingReservationTokenQuotaEnabled(reservation *BillingReservation) bool {
	if reservation == nil {
		return false
	}
	if reservation.TokenQuotaEnabled != nil {
		return *reservation.TokenQuotaEnabled
	}
	// Compatibility for rows created before the policy column existed. The
	// playground currently uses a temporary token with id 0.
	return reservation.TokenId > 0
}

func IncreaseBillingReservation(requestId string, targetQuota int, leaseSeconds int64) (*BillingReservation, error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, errors.New("request_id is empty")
	}
	if targetQuota < 0 {
		return nil, errors.New("billing reservation target quota cannot be negative")
	}
	if targetQuota > math.MaxInt32 {
		return nil, errors.New("billing reservation target quota exceeds database limit")
	}
	if leaseSeconds <= 0 {
		return nil, errors.New("billing reservation lease must be positive")
	}
	reservationUserId, found, err := findBillingReservationUserId(requestId)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	unlock := lockQuotaUser(reservationUserId)
	defer unlock()

	var reservation BillingReservation
	changed := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.Status != BillingReservationStatusReserved && reservation.Status != BillingReservationStatusDispatched {
			return fmt.Errorf("billing reservation %s is already finalizing", requestId)
		}
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		expiresAt := now + leaseSeconds
		if targetQuota <= reservation.ReservedQuota {
			if expiresAt > reservation.ExpiresAt {
				reservation.ExpiresAt = expiresAt
				reservation.UpdatedAt = now
				return tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
					"expires_at": reservation.ExpiresAt,
					"updated_at": reservation.UpdatedAt,
				}).Error
			}
			return nil
		}

		delta := targetQuota - reservation.ReservedQuota
		switch reservation.BillingSource {
		case BillingReservationSourceWallet:
			breakdown, err := DebitQuotaPreferGiftNoLedgerTx(tx, reservation.UserId, delta)
			if err != nil {
				return err
			}
			if breakdown != nil {
				reservation.WalletQuotaReserved += -breakdown.QuotaDelta
				reservation.WalletGiftQuotaReserved += -breakdown.GiftQuotaDelta
			}
		case BillingReservationSourceSubscription:
			if err := adjustUserSubscriptionDeltaTx(tx, reservation.SubscriptionId, int64(delta), ""); err != nil {
				return err
			}
			preConsumeUpdate := tx.Model(&SubscriptionPreConsumeRecord{}).
				Where("request_id = ? AND user_id = ? AND user_subscription_id = ? AND status = ?",
					reservation.RequestId, reservation.UserId, reservation.SubscriptionId, "consumed").
				Updates(map[string]interface{}{
					"pre_consumed": int64(targetQuota),
					"updated_at":   now,
				})
			if preConsumeUpdate.Error != nil {
				return preConsumeUpdate.Error
			}
			if preConsumeUpdate.RowsAffected == 0 {
				return errors.New("subscription pre-consume record is missing or inconsistent")
			}
		default:
			return fmt.Errorf("unsupported billing source: %s", reservation.BillingSource)
		}

		if billingReservationTokenQuotaEnabled(&reservation) {
			if err := adjustBillingReservationTokenTx(tx, reservation.UserId, reservation.TokenId, delta, false); err != nil {
				return err
			}
			reservation.TokenQuotaReserved += delta
		}
		reservation.ReservedQuota = targetQuota
		reservation.DesiredQuota = targetQuota
		if expiresAt > reservation.ExpiresAt {
			reservation.ExpiresAt = expiresAt
		}
		reservation.UpdatedAt = now
		changed = true
		return tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
			"reserved_quota":             reservation.ReservedQuota,
			"wallet_quota_reserved":      reservation.WalletQuotaReserved,
			"wallet_gift_quota_reserved": reservation.WalletGiftQuotaReserved,
			"token_quota_reserved":       reservation.TokenQuotaReserved,
			"desired_quota":              reservation.DesiredQuota,
			"expires_at":                 reservation.ExpiresAt,
			"updated_at":                 reservation.UpdatedAt,
		}).Error
	})
	if err != nil {
		if changed {
			invalidateBillingReservationCachesByTokenId(reservation.UserId, reservation.TokenId)
		}
		return nil, err
	}
	if changed {
		invalidateBillingReservationCachesByTokenId(reservation.UserId, reservation.TokenId)
	}
	return &reservation, nil
}

// MarkBillingReservationDispatched durably records that an upstream attempt is
// about to start. An expired dispatched reservation is conservatively settled
// at its reserved amount because a process crash can no longer prove whether
// the provider completed the request.
func MarkBillingReservationDispatched(requestId string, leaseSeconds int64, channelId int) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return errors.New("request_id is empty")
	}
	if leaseSeconds <= 0 {
		return errors.New("billing reservation lease must be positive")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		updates := map[string]interface{}{
			"status":     BillingReservationStatusDispatched,
			"expires_at": now + leaseSeconds,
			"updated_at": now,
		}
		if channelId > 0 {
			updates["channel_id"] = channelId
		}
		result := tx.Model(&BillingReservation{}).
			Where("request_id = ? AND status IN ?", requestId, []string{
				BillingReservationStatusReserved,
				BillingReservationStatusDispatched,
			}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var reservation BillingReservation
			query := tx.Where("request_id = ?", requestId).Limit(1).Find(&reservation)
			if query.Error != nil {
				return query.Error
			}
			if query.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("billing reservation %s is already finalizing", requestId)
		}
		return nil
	})
}

func FinalizeBillingReservation(requestId string, actualQuota int, status string) (*BillingReservationSettlementResult, error) {
	if actualQuota < 0 {
		return nil, errors.New("actual billing quota cannot be negative")
	}
	if actualQuota > math.MaxInt32 {
		return nil, errors.New("actual billing quota exceeds database limit")
	}
	if status != BillingReservationStatusSettling && status != BillingReservationStatusRefunding {
		return nil, fmt.Errorf("invalid billing reservation final status: %s", status)
	}
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, errors.New("request_id is empty")
	}

	found, err := persistBillingReservationIntent(requestId, actualQuota, status)
	if err != nil {
		return nil, err
	}
	if !found {
		return &BillingReservationSettlementResult{Completed: true, ActualQuota: actualQuota}, nil
	}
	result, err := ApplyBillingReservationIntent(requestId)
	if err != nil {
		RecordBillingReservationAttempt(requestId, err)
		return nil, err
	}
	return result, nil
}

func persistBillingReservationIntent(requestId string, actualQuota int, status string) (bool, error) {
	found := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		query := lockForUpdate(tx).Where("request_id = ?", requestId).Limit(1).Find(&reservation)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			return nil
		}
		found = true
		if reservation.Status == BillingReservationStatusCompleted {
			if reservation.DesiredQuota == actualQuota {
				return nil
			}
			return fmt.Errorf("%w: request_id=%s completed_quota=%d requested_status=%s requested_quota=%d",
				ErrBillingReservationIntentConflict, requestId, reservation.DesiredQuota, status, actualQuota)
		}
		if reservation.Status != BillingReservationStatusReserved && reservation.Status != BillingReservationStatusDispatched {
			if reservation.Status == status && reservation.DesiredQuota == actualQuota {
				return nil
			}
			return fmt.Errorf("%w: request_id=%s existing_status=%s existing_quota=%d requested_status=%s requested_quota=%d",
				ErrBillingReservationIntentConflict, requestId, reservation.Status, reservation.DesiredQuota, status, actualQuota)
		}
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		return tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
			"status":        status,
			"desired_quota": actualQuota,
			"expires_at":    now,
			"last_error":    "",
			"updated_at":    now,
		}).Error
	})
	return found, err
}

func ApplyBillingReservationIntent(requestId string) (*BillingReservationSettlementResult, error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, errors.New("request_id is empty")
	}

	reservationUserId, found, err := findBillingReservationUserId(requestId)
	if err != nil {
		return nil, err
	}
	result := &BillingReservationSettlementResult{Completed: true}
	if !found {
		return result, nil
	}
	unlock := lockQuotaUser(reservationUserId)
	defer unlock()

	var userId int
	var tokenId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		query := lockForUpdate(tx).Where("request_id = ?", requestId).Limit(1).Find(&reservation)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			return nil
		}
		userId = reservation.UserId
		tokenId = reservation.TokenId
		return applyBillingReservationIntentTx(tx, &reservation, result, false)
	})
	if err != nil {
		if userId > 0 {
			invalidateBillingReservationCachesByTokenId(userId, tokenId)
		}
		return nil, err
	}
	if userId > 0 {
		invalidateBillingReservationCachesByTokenId(userId, tokenId)
	}
	return result, nil
}

// InsertTaskWithBillingReservation atomically settles the task submission and
// inserts its durable task row. The completed reservation is kept as a receipt
// and bound to the task until terminal billing is audited.
func InsertTaskWithBillingReservation(task *Task, requestId string, actualQuota int) (*BillingReservationSettlementResult, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, errors.New("request_id is empty")
	}
	if actualQuota < 0 || actualQuota > math.MaxInt32 {
		return nil, errors.New("task billing quota is outside database bounds")
	}
	reservationUserId, found, err := findBillingReservationUserId(requestId)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	unlock := lockQuotaUser(reservationUserId)
	defer unlock()

	result := &BillingReservationSettlementResult{Completed: true}
	var userId int
	var tokenId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.TaskId != 0 {
			var existingTask Task
			if err := tx.Where("id = ?", reservation.TaskId).First(&existingTask).Error; err != nil {
				return err
			}
			if existingTask.TaskID != task.TaskID || existingTask.UserId != task.UserId {
				return fmt.Errorf("billing reservation %s is already bound to another task %d", requestId, reservation.TaskId)
			}
			if existingTask.Quota != actualQuota || reservation.DesiredQuota != actualQuota {
				return fmt.Errorf("billing reservation %s task quota conflicts with idempotent replay: stored=%d requested=%d", requestId, existingTask.Quota, actualQuota)
			}
			*task = existingTask
			fillBillingReservationSettlementResult(&reservation, result)
			userId = reservation.UserId
			tokenId = reservation.TokenId
			return nil
		}
		if reservation.Status != BillingReservationStatusReserved &&
			reservation.Status != BillingReservationStatusDispatched &&
			reservation.Status != BillingReservationStatusCompleted {
			return fmt.Errorf("billing reservation %s cannot be bound in status %s", requestId, reservation.Status)
		}
		if task.ChannelId > 0 {
			reservation.ChannelId = task.ChannelId
		}
		reservation.Status = BillingReservationStatusSettling
		if actualQuota == 0 {
			reservation.Status = BillingReservationStatusRefunding
		}
		reservation.DesiredQuota = actualQuota
		if err := applyBillingReservationIntentTx(tx, &reservation, result, false); err != nil {
			return err
		}

		task.Quota = actualQuota
		task.PrivateData.BillingRequestId = requestId
		task.PrivateData.WalletQuotaConsumed = result.WalletQuotaConsumed
		task.PrivateData.WalletGiftQuotaConsumed = result.WalletGiftQuotaConsumed
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		reservation.TaskId = task.ID
		if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
			"task_id":    task.ID,
			"channel_id": reservation.ChannelId,
		}).Error; err != nil {
			return err
		}
		result.TaskId = task.ID
		userId = reservation.UserId
		tokenId = reservation.TokenId
		return nil
	})
	if userId > 0 {
		invalidateBillingReservationCachesByTokenId(userId, tokenId)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// FinalizeTaskBilling atomically applies the final task quota and performs the
// terminal task-status CAS. A billing failure rolls back the status transition,
// allowing a later polling pass to retry without double charging or refunding.
func FinalizeTaskBilling(task *Task, fromStatus TaskStatus, actualQuota int) (bool, *BillingReservationSettlementResult, error) {
	if task == nil || task.ID <= 0 {
		return false, nil, errors.New("invalid task")
	}
	if actualQuota < 0 || actualQuota > math.MaxInt32 {
		return false, nil, errors.New("task billing quota is outside database bounds")
	}
	requestId := strings.TrimSpace(task.PrivateData.BillingRequestId)
	if requestId == "" {
		requestId = fmt.Sprintf("task:%d", task.ID)
	}
	unlock := lockQuotaUser(task.UserId)
	defer unlock()

	result := &BillingReservationSettlementResult{Completed: true}
	won := false
	var tokenId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		reservationQuery := lockForUpdate(tx).Where("request_id = ?", requestId).Limit(1).Find(&reservation)
		if reservationQuery.Error != nil {
			return reservationQuery.Error
		}

		var current Task
		query := lockForUpdate(tx).Where("id = ?", task.ID).First(&current)
		if query.Error != nil {
			return query.Error
		}
		if current.Status != fromStatus {
			return nil
		}
		won = true

		legacySubscription := false
		if reservationQuery.RowsAffected == 0 {
			tokenQuotaEnabled := current.PrivateData.TokenId > 0
			billingSource := current.PrivateData.BillingSource
			if billingSource == "" {
				billingSource = BillingReservationSourceWallet
			}
			reservation = BillingReservation{
				RequestId:               requestId,
				TaskId:                  current.ID,
				UserId:                  current.UserId,
				TokenId:                 current.PrivateData.TokenId,
				ChannelId:               current.ChannelId,
				BillingSource:           billingSource,
				SubscriptionId:          current.PrivateData.SubscriptionId,
				ModelName:               taskBillingModelName(&current),
				Group:                   current.Group,
				ReservedQuota:           current.Quota,
				WalletQuotaReserved:     current.PrivateData.WalletQuotaConsumed,
				WalletGiftQuotaReserved: current.PrivateData.WalletGiftQuotaConsumed,
				TokenQuotaReserved:      current.Quota,
				TokenQuotaEnabled:       &tokenQuotaEnabled,
				DesiredQuota:            actualQuota,
				AuditedQuota:            current.Quota,
				Status:                  BillingReservationStatusSettling,
				ExpiresAt:               0,
			}
			if actualQuota == 0 {
				reservation.Status = BillingReservationStatusRefunding
			}
			legacySubscription = billingSource == BillingReservationSourceSubscription
			if err := tx.Create(&reservation).Error; err != nil {
				return err
			}
		} else {
			if reservation.TaskId != 0 && reservation.TaskId != current.ID {
				return fmt.Errorf("billing reservation %s belongs to task %d, not %d", requestId, reservation.TaskId, current.ID)
			}
			if reservation.Status == BillingReservationStatusAuditing {
				now, err := queryDBTimestampTx(tx)
				if err != nil {
					return err
				}
				if reservation.ExpiresAt > now-int64(BillingAuditLogTimeoutSeconds()) {
					return fmt.Errorf("task billing reservation %s has an active submission audit claim", requestId)
				}
				// The submission logger died or stalled beyond its lease. Continue
				// terminal billing; the deterministic audit key will detect a late
				// submission log, while lease ownership prevents the stale worker
				// from releasing or deleting this receipt.
				reservation.Status = BillingReservationStatusCompleted
			}
			if reservation.Status != BillingReservationStatusCompleted {
				return fmt.Errorf("task billing reservation %s is in unexpected status %s", requestId, reservation.Status)
			}
			if reservation.ReservedQuota != current.Quota {
				return fmt.Errorf("task %d quota %d differs from reservation quota %d", current.ID, current.Quota, reservation.ReservedQuota)
			}
			reservation.TaskId = current.ID
			reservation.DesiredQuota = actualQuota
			reservation.Status = BillingReservationStatusSettling
			if actualQuota == 0 {
				reservation.Status = BillingReservationStatusRefunding
			}
		}

		if err := applyBillingReservationIntentTx(tx, &reservation, result, legacySubscription); err != nil {
			return err
		}
		task.Quota = actualQuota
		task.PrivateData.BillingRequestId = requestId
		task.PrivateData.BillingSource = reservation.BillingSource
		task.PrivateData.SubscriptionId = reservation.SubscriptionId
		task.PrivateData.TokenId = reservation.TokenId
		task.PrivateData.WalletQuotaConsumed = result.WalletQuotaConsumed
		task.PrivateData.WalletGiftQuotaConsumed = result.WalletGiftQuotaConsumed
		update := tx.Model(&Task{}).
			Where("id = ? AND status = ?", current.ID, fromStatus).
			Select("*").
			Updates(task)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return errors.New("task terminal compare-and-swap updated no rows")
		}
		if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Update("task_id", current.ID).Error; err != nil {
			return err
		}
		result.TaskId = current.ID
		tokenId = reservation.TokenId
		return nil
	})
	if won {
		invalidateBillingReservationCachesByTokenId(task.UserId, tokenId)
	}
	if err != nil {
		return false, nil, err
	}
	return won, result, nil
}

func taskBillingModelName(task *Task) string {
	if task == nil {
		return ""
	}
	if task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.OriginModelName != "" {
		return task.PrivateData.BillingContext.OriginModelName
	}
	return task.Properties.OriginModelName
}

// InsertMidjourneyWithBillingReservation atomically settles and stores an
// accepted Midjourney task, retaining the receipt for terminal refunds.
func InsertMidjourneyWithBillingReservation(task *Midjourney, requestId string, actualQuota int) (*BillingReservationSettlementResult, error) {
	if task == nil {
		return nil, errors.New("midjourney task is nil")
	}
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, errors.New("request_id is empty")
	}
	if actualQuota < 0 || actualQuota > math.MaxInt32 {
		return nil, errors.New("midjourney billing quota is outside database bounds")
	}
	userId, found, err := findBillingReservationUserId(requestId)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	unlock := lockQuotaUser(userId)
	defer unlock()

	result := &BillingReservationSettlementResult{Completed: true}
	var tokenId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.MidjourneyId != 0 {
			var existing Midjourney
			if err := tx.Where("id = ?", reservation.MidjourneyId).First(&existing).Error; err != nil {
				return err
			}
			if existing.MjId != task.MjId || existing.UserId != task.UserId {
				return fmt.Errorf("billing reservation %s is already bound to another Midjourney task %d", requestId, reservation.MidjourneyId)
			}
			if existing.Quota != actualQuota || reservation.DesiredQuota != actualQuota {
				return fmt.Errorf("billing reservation %s Midjourney quota conflicts with idempotent replay: stored=%d requested=%d", requestId, existing.Quota, actualQuota)
			}
			*task = existing
			fillBillingReservationSettlementResult(&reservation, result)
			tokenId = reservation.TokenId
			return nil
		}
		if reservation.TaskId != 0 {
			return fmt.Errorf("billing reservation %s is already bound to task %d", requestId, reservation.TaskId)
		}
		if reservation.Status != BillingReservationStatusReserved &&
			reservation.Status != BillingReservationStatusDispatched &&
			reservation.Status != BillingReservationStatusCompleted {
			return fmt.Errorf("billing reservation %s cannot be bound in status %s", requestId, reservation.Status)
		}
		if task.ChannelId > 0 {
			reservation.ChannelId = task.ChannelId
		}
		reservation.Status = BillingReservationStatusSettling
		if actualQuota == 0 {
			reservation.Status = BillingReservationStatusRefunding
		}
		reservation.DesiredQuota = actualQuota
		if err := applyBillingReservationIntentTx(tx, &reservation, result, false); err != nil {
			return err
		}
		task.Quota = actualQuota
		task.WalletQuotaConsumed = result.WalletQuotaConsumed
		task.WalletGiftQuotaConsumed = result.WalletGiftQuotaConsumed
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		reservation.MidjourneyId = task.Id
		if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
			"midjourney_id": task.Id,
			"channel_id":    reservation.ChannelId,
		}).Error; err != nil {
			return err
		}
		result.MidjourneyId = task.Id
		tokenId = reservation.TokenId
		return nil
	})
	invalidateBillingReservationCachesByTokenId(userId, tokenId)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// FinalizeMidjourneyBilling atomically combines the Midjourney status CAS with
// its funding and token refund/settlement.
func FinalizeMidjourneyBilling(task *Midjourney, fromStatus string, actualQuota int) (bool, *BillingReservationSettlementResult, error) {
	if task == nil || task.Id <= 0 {
		return false, nil, errors.New("invalid midjourney task")
	}
	if actualQuota < 0 || actualQuota > math.MaxInt32 {
		return false, nil, errors.New("midjourney billing quota is outside database bounds")
	}
	unlock := lockQuotaUser(task.UserId)
	defer unlock()

	result := &BillingReservationSettlementResult{Completed: true}
	won := false
	requestId := ""
	var tokenId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		reservationQuery := lockForUpdate(tx).Where("midjourney_id = ?", task.Id).Limit(1).Find(&reservation)
		if reservationQuery.Error != nil {
			return reservationQuery.Error
		}
		var current Midjourney
		if err := lockForUpdate(tx).Where("id = ?", task.Id).First(&current).Error; err != nil {
			return err
		}
		if current.Status != fromStatus {
			return nil
		}
		won = true
		if reservationQuery.RowsAffected == 0 {
			requestId = fmt.Sprintf("mj:%d", current.Id)
			tokenQuotaEnabled := false
			reservation = BillingReservation{
				RequestId:               requestId,
				MidjourneyId:            current.Id,
				UserId:                  current.UserId,
				ChannelId:               current.ChannelId,
				BillingSource:           BillingReservationSourceWallet,
				ModelName:               current.Action,
				ReservedQuota:           current.Quota,
				WalletQuotaReserved:     current.WalletQuotaConsumed,
				WalletGiftQuotaReserved: current.WalletGiftQuotaConsumed,
				TokenQuotaEnabled:       &tokenQuotaEnabled,
				DesiredQuota:            actualQuota,
				AuditedQuota:            current.Quota,
				Status:                  BillingReservationStatusSettling,
			}
			if actualQuota == 0 {
				reservation.Status = BillingReservationStatusRefunding
			}
			if err := tx.Create(&reservation).Error; err != nil {
				return err
			}
		} else {
			requestId = reservation.RequestId
			if reservation.Status == BillingReservationStatusAuditing {
				now, err := queryDBTimestampTx(tx)
				if err != nil {
					return err
				}
				if reservation.ExpiresAt > now-int64(BillingAuditLogTimeoutSeconds()) {
					return fmt.Errorf("Midjourney billing reservation %s has an active submission audit claim", requestId)
				}
				reservation.Status = BillingReservationStatusCompleted
			}
			if reservation.Status != BillingReservationStatusCompleted {
				return fmt.Errorf("Midjourney billing reservation %s is in unexpected status %s", requestId, reservation.Status)
			}
			if reservation.ReservedQuota != current.Quota {
				return fmt.Errorf("Midjourney task %d quota %d differs from reservation quota %d", current.Id, current.Quota, reservation.ReservedQuota)
			}
			reservation.DesiredQuota = actualQuota
			reservation.Status = BillingReservationStatusSettling
			if actualQuota == 0 {
				reservation.Status = BillingReservationStatusRefunding
			}
		}
		if err := applyBillingReservationIntentTx(tx, &reservation, result, false); err != nil {
			return err
		}
		task.Quota = actualQuota
		task.WalletQuotaConsumed = result.WalletQuotaConsumed
		task.WalletGiftQuotaConsumed = result.WalletGiftQuotaConsumed
		update := tx.Model(&Midjourney{}).
			Where("id = ? AND status = ?", current.Id, fromStatus).
			Select("*").
			Updates(task)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return errors.New("Midjourney terminal compare-and-swap updated no rows")
		}
		result.MidjourneyId = current.Id
		tokenId = reservation.TokenId
		return nil
	})
	if won {
		invalidateBillingReservationCachesByTokenId(task.UserId, tokenId)
	}
	if err != nil {
		return false, nil, err
	}
	return won, result, nil
}

func applyBillingReservationIntentTx(tx *gorm.DB, reservation *BillingReservation, result *BillingReservationSettlementResult, legacySubscription bool) error {
	if tx == nil || reservation == nil || result == nil {
		return errors.New("invalid billing reservation settlement state")
	}
	if reservation.Status == BillingReservationStatusCompleted {
		fillBillingReservationSettlementResult(reservation, result)
		return nil
	}
	if reservation.Status != BillingReservationStatusSettling && reservation.Status != BillingReservationStatusRefunding {
		return ErrBillingReservationNotReady
	}

	actualQuota := reservation.DesiredQuota
	delta := actualQuota - reservation.ReservedQuota
	switch reservation.BillingSource {
	case BillingReservationSourceWallet:
		// A user can be soft-deleted while an upstream request is in flight.
		// Finalization must still restore or settle that row.
		walletTx := tx.Unscoped()
		if delta > 0 {
			breakdown, err := DebitQuotaPreferGiftNoLedgerTx(walletTx, reservation.UserId, delta)
			if err != nil {
				return err
			}
			if breakdown != nil {
				reservation.WalletQuotaReserved += -breakdown.QuotaDelta
				reservation.WalletGiftQuotaReserved += -breakdown.GiftQuotaDelta
			}
		} else if delta < 0 {
			if err := refundBillingReservationWalletTx(walletTx, reservation, -delta); err != nil {
				return err
			}
		}
	case BillingReservationSourceSubscription:
		if legacySubscription {
			// Tasks created before durable reservations do not retain the original
			// subscription pre-consume request ID. Preserve the legacy delta path
			// for those already-running tasks; new reservations always validate and
			// update their exact pre-consume record below.
			if delta != 0 {
				if err := adjustUserSubscriptionDeltaTx(tx, reservation.SubscriptionId, int64(delta), ""); err != nil {
					return err
				}
			}
			break
		}
		var preConsumeRecord SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).
			Where("request_id = ?", reservation.RequestId).
			First(&preConsumeRecord).Error; err != nil {
			return err
		}
		if preConsumeRecord.UserId != reservation.UserId || preConsumeRecord.UserSubscriptionId != reservation.SubscriptionId {
			return errors.New("subscription pre-consume record does not match billing reservation")
		}
		if preConsumeRecord.Status != "consumed" || preConsumeRecord.PreConsumed != int64(reservation.ReservedQuota) {
			return fmt.Errorf("subscription pre-consume record is inconsistent: status=%s quota=%d reserved=%d",
				preConsumeRecord.Status, preConsumeRecord.PreConsumed, reservation.ReservedQuota)
		}
		if delta != 0 {
			if err := adjustUserSubscriptionDeltaTx(tx, reservation.SubscriptionId, int64(delta), ""); err != nil {
				return err
			}
		}
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		subscriptionStatus := "consumed"
		if actualQuota == 0 {
			subscriptionStatus = "refunded"
		}
		if err := tx.Model(&SubscriptionPreConsumeRecord{}).
			Where("id = ?", preConsumeRecord.Id).
			Updates(map[string]interface{}{
				"pre_consumed": int64(actualQuota),
				"status":       subscriptionStatus,
				"updated_at":   now,
			}).Error; err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported billing source: %s", reservation.BillingSource)
	}

	tokenDelta := actualQuota - reservation.TokenQuotaReserved
	if billingReservationTokenQuotaEnabled(reservation) && tokenDelta != 0 {
		if err := adjustBillingReservationTokenTx(tx, reservation.UserId, reservation.TokenId, tokenDelta, true); err != nil {
			return err
		}
	}

	preConsumedQuota := reservation.ReservedQuota
	reservation.ReservedQuota = actualQuota
	if billingReservationTokenQuotaEnabled(reservation) {
		reservation.TokenQuotaReserved = actualQuota
	} else {
		reservation.TokenQuotaReserved = 0
	}
	reservation.DesiredQuota = actualQuota
	reservation.Status = BillingReservationStatusCompleted
	reservation.ExpiresAt = billingReservationNoExpiry
	now, err := queryDBTimestampTx(tx)
	if err != nil {
		return err
	}
	reservation.UpdatedAt = now
	if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
		"reserved_quota":             reservation.ReservedQuota,
		"wallet_quota_reserved":      reservation.WalletQuotaReserved,
		"wallet_gift_quota_reserved": reservation.WalletGiftQuotaReserved,
		"token_quota_reserved":       reservation.TokenQuotaReserved,
		"desired_quota":              reservation.DesiredQuota,
		"status":                     reservation.Status,
		"expires_at":                 reservation.ExpiresAt,
		"last_error":                 "",
		"updated_at":                 reservation.UpdatedAt,
	}).Error; err != nil {
		return err
	}
	fillBillingReservationSettlementResult(reservation, result)
	result.PreConsumedQuota = preConsumedQuota
	return nil
}

func fillBillingReservationSettlementResult(reservation *BillingReservation, result *BillingReservationSettlementResult) {
	if reservation == nil || result == nil {
		return
	}
	result.Completed = true
	result.ReservationId = reservation.Id
	result.RequestId = reservation.RequestId
	result.TaskId = reservation.TaskId
	result.MidjourneyId = reservation.MidjourneyId
	result.UserId = reservation.UserId
	result.TokenId = reservation.TokenId
	result.ChannelId = reservation.ChannelId
	result.ModelName = reservation.ModelName
	result.Group = reservation.Group
	result.BillingSource = reservation.BillingSource
	result.SubscriptionId = reservation.SubscriptionId
	result.PreConsumedQuota = reservation.ReservedQuota
	result.ActualQuota = reservation.DesiredQuota
	result.AuditedQuota = reservation.AuditedQuota
	result.WalletQuotaConsumed = reservation.WalletQuotaReserved
	result.WalletGiftQuotaConsumed = reservation.WalletGiftQuotaReserved
}

// CompleteBillingReservationAuditClaim records how much quota is represented
// by the immutable submission log and releases that claim. The lease timestamp
// is an ownership token: a stale worker cannot release a claim that another
// instance reclaimed after expiry.
func CompleteBillingReservationAuditClaim(requestId string, claimExpiresAt int64, quota int) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return errors.New("request_id is empty")
	}
	if claimExpiresAt <= 0 {
		return errors.New("billing audit claim expiry is invalid")
	}
	if quota < 0 || quota > math.MaxInt32 {
		return errors.New("audited billing quota is outside database bounds")
	}
	result := DB.Model(&BillingReservation{}).
		Where("request_id = ? AND status = ? AND expires_at = ?",
			requestId, BillingReservationStatusAuditing, claimExpiresAt).
		Updates(map[string]interface{}{
			"audited_quota": quota,
			"status":        BillingReservationStatusCompleted,
			"expires_at":    billingReservationNoExpiry,
			"last_error":    "",
			"updated_at":    GetDBTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrBillingReservationAuditClaimLost
	}
	return nil
}

// ValidateBillingReservationAuditClaim prevents a paused worker from starting
// a log-database transaction after its main-database lease has expired. Claim
// reclamation has an additional bounded log-write grace period, closing the
// race between this validation and the independent log database commit.
func ValidateBillingReservationAuditClaim(requestId string, claimExpiresAt int64) error {
	return validateBillingReservationAuditClaimContext(context.Background(), requestId, claimExpiresAt)
}

func validateBillingReservationAuditClaimContext(ctx context.Context, requestId string, claimExpiresAt int64) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" || claimExpiresAt <= 0 {
		return ErrBillingReservationAuditClaimLost
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return validateBillingReservationAuditClaimTx(tx, requestId, claimExpiresAt, false)
	})
}

func validateBillingReservationAuditClaimTx(tx *gorm.DB, requestId string, claimExpiresAt int64, lock bool) error {
	now, err := queryDBTimestampTx(tx)
	if err != nil {
		return err
	}
	var reservation BillingReservation
	query := tx.Select("id").Where(
		"request_id = ? AND status = ? AND expires_at = ? AND expires_at > ?",
		requestId, BillingReservationStatusAuditing, claimExpiresAt, now,
	)
	if lock {
		query = lockForUpdate(query)
	}
	result := query.Limit(1).Find(&reservation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrBillingReservationAuditClaimLost
	}
	return nil
}

// AcknowledgeBillingReservation removes a completed receipt only after the
// corresponding consumption audit is durable (or after a zero-charge refund).
func AcknowledgeBillingReservation(requestId string) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil
	}
	return DB.Where("request_id = ? AND status = ? AND task_id = 0 AND midjourney_id = 0",
		requestId, BillingReservationStatusCompleted).
		Delete(&BillingReservation{}).Error
}

// AcknowledgeBillingReservationAudit deletes a receipt only when the caller
// still owns its audit claim.
func AcknowledgeBillingReservationAudit(requestId string, claimExpiresAt int64) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil
	}
	if claimExpiresAt <= 0 {
		return errors.New("billing audit claim expiry is invalid")
	}
	result := DB.Where("request_id = ? AND status = ? AND expires_at = ?",
		requestId, BillingReservationStatusAuditing, claimExpiresAt).
		Delete(&BillingReservation{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrBillingReservationAuditClaimLost
	}
	return nil
}

// ClaimBillingReservationAudit serializes log-database audit writes across
// instances. An expired auditing lease can be reclaimed by the repair worker.
func ClaimBillingReservationAudit(requestId string, leaseSeconds int64) (*BillingReservation, bool, error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return nil, false, errors.New("request_id is empty")
	}
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}
	var reservation BillingReservation
	claimed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := lockForUpdate(tx).Where("request_id = ?", requestId).Limit(1).Find(&reservation)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			return nil
		}
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		if reservation.Status == BillingReservationStatusAuditing &&
			reservation.ExpiresAt > now-int64(BillingAuditLogTimeoutSeconds()) {
			return nil
		}
		if reservation.Status != BillingReservationStatusCompleted && reservation.Status != BillingReservationStatusAuditing {
			return nil
		}
		reservation.Status = BillingReservationStatusAuditing
		reservation.ExpiresAt = now + leaseSeconds
		reservation.UpdatedAt = now
		if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
			"status":     reservation.Status,
			"expires_at": reservation.ExpiresAt,
			"updated_at": reservation.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return nil, claimed, err
	}
	return &reservation, true, nil
}

func ReleaseBillingReservationAudit(requestId string, claimExpiresAt int64, auditErr error) {
	lastError := ""
	if auditErr != nil {
		lastError = auditErr.Error()
	}
	now := GetDBTimestamp()
	if err := DB.Model(&BillingReservation{}).
		Where("request_id = ? AND status = ? AND expires_at = ?",
			strings.TrimSpace(requestId), BillingReservationStatusAuditing, claimExpiresAt).
		Updates(map[string]interface{}{
			"status":     BillingReservationStatusCompleted,
			"expires_at": billingReservationNoExpiry,
			"last_error": lastError,
			"updated_at": now,
		}).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to release billing audit claim (request_id=%s): %v", requestId, err))
	}
}

// RepairExpiredBillingReservation atomically rechecks and finalizes one due
// reservation. The row is locked before expiry is evaluated, so a heartbeat
// that renewed the lease after the scheduler's initial scan is never undone.
//
// A reservation that was never dispatched is refunded. Once dispatch was
// durably recorded, a crash leaves the provider outcome ambiguous, so the
// reserved estimate is retained unless a more precise settlement/refund intent
// was already persisted.
func RepairExpiredBillingReservation(requestId string) (bool, error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return false, errors.New("request_id is empty")
	}
	reservationUserId, found, err := findBillingReservationUserId(requestId)
	if err != nil || !found {
		return false, err
	}
	unlock := lockQuotaUser(reservationUserId)
	defer unlock()

	repaired := false
	var userId int
	var tokenId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		query := lockForUpdate(tx).Where("request_id = ?", requestId).Limit(1).Find(&reservation)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			return nil
		}
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		if reservation.ExpiresAt > now {
			return nil
		}
		if reservation.Status == BillingReservationStatusCompleted {
			return nil
		}

		if reservation.Status == BillingReservationStatusReserved || reservation.Status == BillingReservationStatusDispatched {
			var pendingManaged int64
			if err := tx.Model(&BillingSettlementFailure{}).
				Where("request_id = ? AND status = ? AND reservation_managed = ?", requestId, BillingSettlementStatusPending, true).
				Count(&pendingManaged).Error; err != nil {
				return err
			}
			if pendingManaged > 0 {
				return nil
			}

			status := BillingReservationStatusRefunding
			desiredQuota := 0
			if reservation.Status == BillingReservationStatusDispatched {
				status = BillingReservationStatusSettling
				desiredQuota = reservation.ReservedQuota
			}
			reservation.Status = status
			reservation.DesiredQuota = desiredQuota
			reservation.UpdatedAt = now
			if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
				"status":        status,
				"desired_quota": desiredQuota,
				"last_error":    "",
				"updated_at":    now,
			}).Error; err != nil {
				return err
			}
		} else if reservation.Status != BillingReservationStatusSettling && reservation.Status != BillingReservationStatusRefunding {
			return fmt.Errorf("unsupported billing reservation status: %s", reservation.Status)
		}

		userId = reservation.UserId
		tokenId = reservation.TokenId
		result := &BillingReservationSettlementResult{Completed: true}
		if err := applyBillingReservationIntentTx(tx, &reservation, result, false); err != nil {
			return err
		}
		if result.ActualQuota == 0 && reservation.TaskId == 0 && reservation.MidjourneyId == 0 {
			if err := tx.Delete(&BillingReservation{}, reservation.Id).Error; err != nil {
				return err
			}
		}
		repaired = true
		return nil
	})
	if userId > 0 {
		invalidateBillingReservationCachesByTokenId(userId, tokenId)
	}
	return repaired, err
}

func refundBillingReservationWalletTx(tx *gorm.DB, reservation *BillingReservation, amount int) error {
	if amount <= 0 {
		return nil
	}
	consumed := reservation.WalletQuotaReserved + reservation.WalletGiftQuotaReserved
	if consumed <= 0 {
		_, err := RefundQuotaByBreakdownNoLedgerTx(tx, reservation.UserId, QuotaDelta{GiftQuotaDelta: amount})
		return err
	}
	if amount > consumed {
		return fmt.Errorf("wallet reservation refund exceeds reserved quota: refund=%d reserved=%d", amount, consumed)
	}
	finalConsumed := consumed - amount
	finalGiftConsumed := reservation.WalletGiftQuotaReserved
	if finalGiftConsumed > finalConsumed {
		finalGiftConsumed = finalConsumed
	}
	finalQuotaConsumed := finalConsumed - finalGiftConsumed
	refundGift := reservation.WalletGiftQuotaReserved - finalGiftConsumed
	refundQuota := reservation.WalletQuotaReserved - finalQuotaConsumed
	if _, err := RefundQuotaByBreakdownNoLedgerTx(tx, reservation.UserId, QuotaDelta{
		QuotaDelta:     refundQuota,
		GiftQuotaDelta: refundGift,
	}); err != nil {
		return err
	}
	reservation.WalletQuotaReserved = finalQuotaConsumed
	reservation.WalletGiftQuotaReserved = finalGiftConsumed
	return nil
}

// adjustBillingReservationTokenTx applies a billing delta to the token inside
// the caller's transaction. A positive delta consumes more; a negative delta
// returns previously reserved quota.
func adjustBillingReservationTokenTx(tx *gorm.DB, userId int, tokenId int, delta int, allowMissing bool) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}
	if tokenId <= 0 || delta == 0 {
		return nil
	}
	if userId <= 0 {
		return errors.New("billing reservation token user is invalid")
	}
	if delta > 0 {
		// used_quota/remain_quota 为 bigint，长期使用的令牌累计用量可以
		// 合法超过 int32，守卫仅需防 int64 溢出。
		result := tx.Model(&Token{}).
			Where("id = ? AND user_id = ? AND used_quota <= ? AND ((unlimited_quota = ? AND remain_quota >= ?) OR remain_quota >= ?)",
				tokenId, userId, math.MaxInt64-delta, true, math.MinInt64+delta, delta).
			Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota - ?", delta),
				"used_quota":    gorm.Expr("used_quota + ?", delta),
				"accessed_time": common.GetTimestamp(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var owner struct {
				UserId int
			}
			query := tx.Model(&Token{}).Select("user_id").Where("id = ?", tokenId).Limit(1).Find(&owner)
			if query.Error != nil {
				return query.Error
			}
			if query.RowsAffected == 0 {
				if allowMissing {
					// A token deleted after dispatch must not strand the funding
					// settlement or refund.
					return nil
				}
				return ErrBillingReservationTokenNotFound
			}
			if owner.UserId != userId {
				return errors.New("billing reservation token belongs to another user")
			}
			return errors.New("insufficient token quota or token quota exceeds database limit")
		}
		return nil
	}
	refund := -delta
	result := tx.Model(&Token{}).
		Where("id = ? AND user_id = ? AND used_quota >= ? AND remain_quota <= ?", tokenId, userId, refund, math.MaxInt64-refund).
		Updates(map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota + ?", refund),
			"used_quota":    gorm.Expr("used_quota - ?", refund),
			"accessed_time": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var owner struct {
			UserId int
		}
		query := tx.Model(&Token{}).Select("user_id").Where("id = ?", tokenId).Limit(1).Find(&owner)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			// The token may have been deleted while the upstream request was in
			// flight. There is then no token row to restore, but the funding refund
			// and reservation cleanup must still commit.
			return nil
		}
		if owner.UserId != userId {
			return errors.New("billing reservation token belongs to another user")
		}
		return errors.New("token quota refund would exceed database bounds")
	}
	return nil
}

func TouchBillingReservation(requestId string, leaseSeconds int64) error {
	if strings.TrimSpace(requestId) == "" || leaseSeconds <= 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		expiresAt := now + leaseSeconds
		return tx.Model(&BillingReservation{}).
			Where("request_id = ? AND status IN ? AND expires_at < ?", requestId, []string{
				BillingReservationStatusReserved,
				BillingReservationStatusDispatched,
			}, expiresAt).
			Updates(map[string]interface{}{
				"expires_at": expiresAt,
				"updated_at": now,
			}).Error
	})
}

func HasDueBillingReservations() bool {
	var id int64
	err := DB.Model(&BillingReservation{}).
		Where("status IN ? AND expires_at <= ?", []string{
			BillingReservationStatusReserved,
			BillingReservationStatusDispatched,
			BillingReservationStatusSettling,
			BillingReservationStatusRefunding,
		}, GetDBTimestamp()).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

func FindDueBillingReservations(limit int) ([]*BillingReservation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var reservations []*BillingReservation
	err := DB.Where("status IN ? AND expires_at <= ?", []string{
		BillingReservationStatusReserved,
		BillingReservationStatusDispatched,
		BillingReservationStatusSettling,
		BillingReservationStatusRefunding,
	}, GetDBTimestamp()).
		Order("expires_at asc, id asc").
		Limit(limit).
		Find(&reservations).Error
	return reservations, err
}

func FindCompletedTaskBillingReservations(limit int) ([]*BillingReservation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	statusClause := "((status = ?) OR (status = ? AND expires_at <= ?))"
	statusArgs := []interface{}{
		BillingReservationStatusCompleted,
		BillingReservationStatusAuditing,
		GetDBTimestamp() - int64(BillingAuditLogTimeoutSeconds()),
	}
	reservations := make([]*BillingReservation, 0, limit)
	if DB.Migrator().HasTable(&Task{}) {
		var taskReservations []*BillingReservation
		taskArgs := append(append([]interface{}{}, statusArgs...), []TaskStatus{TaskStatusSuccess, TaskStatusFailure})
		err := DB.Where(statusClause+` AND task_id > 0 AND EXISTS (
			SELECT 1 FROM tasks WHERE tasks.id = billing_reservations.task_id AND tasks.status IN ?
		)`, taskArgs...).
			Order("updated_at asc, id asc").
			Limit(limit).
			Find(&taskReservations).Error
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, taskReservations...)
	}
	remaining := limit - len(reservations)
	if remaining > 0 && DB.Migrator().HasTable(&Midjourney{}) {
		var midjourneyReservations []*BillingReservation
		midjourneyArgs := append(append([]interface{}{}, statusArgs...), "100%", []string{"SUCCESS", "FAILURE"})
		err := DB.Where(statusClause+` AND midjourney_id > 0 AND EXISTS (
			SELECT 1 FROM midjourneys WHERE midjourneys.id = billing_reservations.midjourney_id AND midjourneys.progress = ? AND midjourneys.status IN ?
		)`, midjourneyArgs...).
			Order("updated_at asc, id asc").
			Limit(remaining).
			Find(&midjourneyReservations).Error
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, midjourneyReservations...)
	}
	return reservations, nil
}

// FindPendingTaskSubmissionBillingReservations finds accepted asynchronous
// tasks whose initial consumption audit was never acknowledged. These receipts
// must be repaired even while the provider task is still running; otherwise a
// crash immediately after the main-database commit can leave the usage log
// missing until a potentially long-running task reaches terminal state.
func FindPendingTaskSubmissionBillingReservations(limit int) ([]*BillingReservation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	graceSeconds := common.GetEnvOrDefault("BILLING_TASK_SUBMISSION_AUDIT_GRACE_SECONDS", 60)
	if graceSeconds < 15 {
		graceSeconds = 15
	}
	now := GetDBTimestamp()
	var reservations []*BillingReservation
	err := DB.Where(`updated_at <= ? AND reserved_quota > 0 AND audited_quota = 0 AND
		(task_id > 0 OR midjourney_id > 0) AND
		((status = ?) OR (status = ? AND expires_at <= ?))`,
		now-int64(graceSeconds),
		BillingReservationStatusCompleted,
		BillingReservationStatusAuditing,
		now-int64(BillingAuditLogTimeoutSeconds())).
		Order("updated_at asc, id asc").
		Limit(limit).
		Find(&reservations).Error
	return reservations, err
}

func FindCompletedStandaloneBillingReservations(limit int, olderThan int64) ([]*BillingReservation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var reservations []*BillingReservation
	err := DB.Where(`task_id = 0 AND midjourney_id = 0 AND updated_at <= ? AND
		((status = ?) OR (status = ? AND expires_at <= ?))`,
		olderThan, BillingReservationStatusCompleted, BillingReservationStatusAuditing,
		GetDBTimestamp()-int64(BillingAuditLogTimeoutSeconds())).
		Order("updated_at asc, id asc").
		Limit(limit).
		Find(&reservations).Error
	return reservations, err
}

func HasPendingBillingReservationAudits() bool {
	reservations, err := FindPendingTaskSubmissionBillingReservations(1)
	if err != nil {
		return false
	}
	if len(reservations) > 0 {
		return true
	}
	reservations, err = FindCompletedTaskBillingReservations(1)
	if err != nil {
		return false
	}
	if len(reservations) > 0 {
		return true
	}
	graceSeconds := common.GetEnvOrDefault("BILLING_STANDALONE_AUDIT_GRACE_SECONDS", 60)
	if graceSeconds < 15 {
		graceSeconds = 15
	}
	reservations, err = FindCompletedStandaloneBillingReservations(1, GetDBTimestamp()-int64(graceSeconds))
	return err == nil && len(reservations) > 0
}

func GetBillingReservationByRequestId(requestId string) (*BillingReservation, error) {
	var reservation BillingReservation
	query := DB.Where("request_id = ?", strings.TrimSpace(requestId)).Limit(1).Find(&reservation)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		return nil, nil
	}
	return &reservation, nil
}

func findBillingReservationUserId(requestId string) (int, bool, error) {
	var reservation struct {
		UserId int
	}
	query := DB.Model(&BillingReservation{}).
		Select("user_id").
		Where("request_id = ?", strings.TrimSpace(requestId)).
		Limit(1).
		Find(&reservation)
	if query.Error != nil {
		return 0, false, query.Error
	}
	if query.RowsAffected == 0 {
		return 0, false, nil
	}
	return reservation.UserId, true, nil
}

func RecordBillingReservationAttempt(requestId string, settleErr error) {
	if settleErr == nil {
		return
	}
	retryDelay := common.GetEnvOrDefault("BILLING_RESERVATION_RETRY_DELAY_SECONDS", 60)
	if retryDelay < 1 {
		retryDelay = 1
	}
	now := GetDBTimestamp()
	if err := DB.Model(&BillingReservation{}).
		Where("request_id = ? AND status NOT IN ?", requestId, []string{
			BillingReservationStatusCompleted,
			BillingReservationStatusAuditing,
		}).
		Updates(map[string]interface{}{
			"attempts":   gorm.Expr("attempts + ?", 1),
			"last_error": settleErr.Error(),
			"expires_at": now + int64(retryDelay),
			"updated_at": now,
		}).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to mark billing reservation attempt (request_id=%s): %v", requestId, err))
	}
}

func invalidateBillingReservationCaches(userId int, tokenKey string) {
	if userId > 0 {
		if err := invalidateUserCache(userId); err != nil {
			common.SysLog("failed to invalidate user cache after billing reservation: " + err.Error())
		}
	}
	if common.RedisEnabled && tokenKey != "" {
		if err := cacheDeleteToken(tokenKey); err != nil {
			common.SysLog("failed to invalidate token cache after billing reservation: " + err.Error())
		}
	}
}

func invalidateBillingReservationCachesByTokenId(userId int, tokenId int) {
	tokenKey := ""
	if common.RedisEnabled && tokenId > 0 {
		var token Token
		if err := DB.Select(commonKeyCol).Where("id = ?", tokenId).First(&token).Error; err == nil {
			tokenKey = token.Key
		}
	}
	invalidateBillingReservationCaches(userId, tokenKey)
}
