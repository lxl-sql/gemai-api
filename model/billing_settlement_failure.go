package model

import (
	"context"
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingSettlementStatusPending = "pending"
	BillingSettlementStatusSettled = "settled"
)

type BillingSettlementFailure struct {
	Id                      int64  `json:"id" gorm:"primary_key;index:idx_billing_settle_status_updated,priority:3"`
	RequestId               string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId                  int    `json:"user_id" gorm:"index"`
	TokenId                 int    `json:"token_id" gorm:"index"`
	ChannelId               int    `json:"channel_id" gorm:"index"`
	BillingSource           string `json:"billing_source" gorm:"type:varchar(32);index;default:''"`
	SubscriptionId          int    `json:"subscription_id" gorm:"index;default:0"`
	ActualQuota             int    `json:"actual_quota" gorm:"type:int;default:0"`
	PreConsumedQuota        int    `json:"pre_consumed_quota" gorm:"type:int;default:0"`
	Delta                   int    `json:"delta" gorm:"type:int;default:0"`
	WalletQuotaConsumed     int    `json:"wallet_quota_consumed" gorm:"type:int;default:0"`
	WalletGiftQuotaConsumed int    `json:"wallet_gift_quota_consumed" gorm:"type:int;default:0"`
	FundingSettled          bool   `json:"funding_settled" gorm:"default:false"`
	ReservationManaged      bool   `json:"reservation_managed"`
	ReservationStatus       string `json:"reservation_status" gorm:"type:varchar(32);default:''"`
	Status                  string `json:"status" gorm:"type:varchar(32);index;index:idx_billing_settle_status_updated,priority:1;default:'pending'"`
	Attempts                int    `json:"attempts" gorm:"type:int;default:0"`
	LastError               string `json:"last_error" gorm:"type:text"`
	CreatedAt               int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt               int64  `json:"updated_at" gorm:"bigint;index;index:idx_billing_settle_status_updated,priority:2"`
}

type BillingSettlementFailureInput struct {
	RequestId               string
	UserId                  int
	TokenId                 int
	ChannelId               int
	BillingSource           string
	SubscriptionId          int
	ActualQuota             int
	PreConsumedQuota        int
	Delta                   int
	WalletQuotaConsumed     int
	WalletGiftQuotaConsumed int
	FundingSettled          bool
	ReservationManaged      bool
	ReservationStatus       string
	LastError               string
}

func (failure *BillingSettlementFailure) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if failure.CreatedAt == 0 {
		failure.CreatedAt = now
	}
	if failure.UpdatedAt == 0 {
		failure.UpdatedAt = now
	}
	if failure.Status == "" {
		failure.Status = BillingSettlementStatusPending
	}
	return nil
}

func RecordBillingSettlementFailure(input BillingSettlementFailureInput) error {
	if input.ReservationManaged && (input.ActualQuota < 0 || input.ActualQuota > math.MaxInt32) {
		return errors.New("managed billing settlement quota is outside database range")
	}
	if input.RequestId == "" {
		input.RequestId = common.GetUUID()
	}
	return withBoundedQuotaTransaction(context.Background(), func(tx *gorm.DB) error {
		now, err := queryDBTimestampTx(tx)
		if err != nil {
			return err
		}
		if input.ReservationManaged {
			if input.ReservationStatus != BillingReservationStatusSettling && input.ReservationStatus != BillingReservationStatusRefunding {
				return errors.New("invalid managed billing reservation status")
			}
			var reservation BillingReservation
			query := lockForUpdate(tx).Where("request_id = ?", input.RequestId).Limit(1).Find(&reservation)
			if query.Error != nil {
				return query.Error
			}
			if query.RowsAffected == 0 {
				return tx.Model(&BillingSettlementFailure{}).
					Where("request_id = ? AND status = ?", input.RequestId, BillingSettlementStatusPending).
					Updates(map[string]interface{}{
						"status":     BillingSettlementStatusSettled,
						"updated_at": now,
					}).Error
			}
			if reservation.Status == BillingReservationStatusReserved || reservation.Status == BillingReservationStatusDispatched {
				if err := tx.Model(&BillingReservation{}).Where("id = ?", reservation.Id).Updates(map[string]interface{}{
					"status":        input.ReservationStatus,
					"desired_quota": input.ActualQuota,
					"expires_at":    now,
					"last_error":    input.LastError,
					"updated_at":    now,
				}).Error; err != nil {
					return err
				}
			} else if reservation.Status == BillingReservationStatusCompleted && reservation.DesiredQuota == input.ActualQuota {
				return tx.Model(&BillingSettlementFailure{}).
					Where("request_id = ? AND status = ?", input.RequestId, BillingSettlementStatusPending).
					Updates(map[string]interface{}{
						"status":     BillingSettlementStatusSettled,
						"updated_at": now,
					}).Error
			} else if reservation.Status != input.ReservationStatus || reservation.DesiredQuota != input.ActualQuota {
				return ErrBillingReservationIntentConflict
			}
		}

		failure := BillingSettlementFailure{
			RequestId:               input.RequestId,
			UserId:                  input.UserId,
			TokenId:                 input.TokenId,
			ChannelId:               input.ChannelId,
			BillingSource:           input.BillingSource,
			SubscriptionId:          input.SubscriptionId,
			ActualQuota:             input.ActualQuota,
			PreConsumedQuota:        input.PreConsumedQuota,
			Delta:                   input.Delta,
			WalletQuotaConsumed:     input.WalletQuotaConsumed,
			WalletGiftQuotaConsumed: input.WalletGiftQuotaConsumed,
			FundingSettled:          input.FundingSettled,
			ReservationManaged:      input.ReservationManaged,
			ReservationStatus:       input.ReservationStatus,
			Status:                  BillingSettlementStatusPending,
			Attempts:                0,
			LastError:               input.LastError,
			CreatedAt:               now,
			UpdatedAt:               now,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "request_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"user_id":                    failure.UserId,
				"token_id":                   failure.TokenId,
				"channel_id":                 failure.ChannelId,
				"billing_source":             failure.BillingSource,
				"subscription_id":            failure.SubscriptionId,
				"actual_quota":               failure.ActualQuota,
				"pre_consumed_quota":         failure.PreConsumedQuota,
				"delta":                      failure.Delta,
				"wallet_quota_consumed":      failure.WalletQuotaConsumed,
				"wallet_gift_quota_consumed": failure.WalletGiftQuotaConsumed,
				"funding_settled":            failure.FundingSettled,
				"reservation_managed":        failure.ReservationManaged,
				"reservation_status":         failure.ReservationStatus,
				"status":                     BillingSettlementStatusPending,
				"last_error":                 failure.LastError,
				"updated_at":                 now,
			}),
		}).Create(&failure).Error
	})
}

func HasPendingBillingSettlementFailures() bool {
	failures, err := FindPendingBillingSettlementFailures(1)
	return err == nil && len(failures) > 0
}

func FindPendingBillingSettlementFailures(limit int) ([]*BillingSettlementFailure, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	retryDelaySeconds := common.GetEnvOrDefault("BILLING_SETTLEMENT_RETRY_DELAY_SECONDS", 60)
	if retryDelaySeconds < 0 {
		retryDelaySeconds = 0
	}
	now := GetDBTimestamp()
	retryDelay := int64(retryDelaySeconds)
	delay2 := retryDelay * 2
	delay4 := retryDelay * 4
	delay8 := retryDelay * 8
	delay16 := retryDelay * 16
	delay32 := retryDelay * 32
	delay64 := retryDelay * 64
	delay128 := retryDelay * 128
	retryCutoffSQL := `? - CASE
			WHEN attempts >= 8 THEN ?
			WHEN attempts = 7 THEN ?
			WHEN attempts = 6 THEN ?
			WHEN attempts = 5 THEN ?
			WHEN attempts = 4 THEN ?
			WHEN attempts = 3 THEN ?
			WHEN attempts = 2 THEN ?
			ELSE ? END`
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		// pgx can infer CASE bind values as jsonb in this mixed arithmetic
		// expression. Cast them explicitly so PostgreSQL keeps bigint semantics.
		retryCutoffSQL = `CAST(? AS BIGINT) - CASE
			WHEN attempts >= 8 THEN CAST(? AS BIGINT)
			WHEN attempts = 7 THEN CAST(? AS BIGINT)
			WHEN attempts = 6 THEN CAST(? AS BIGINT)
			WHEN attempts = 5 THEN CAST(? AS BIGINT)
			WHEN attempts = 4 THEN CAST(? AS BIGINT)
			WHEN attempts = 3 THEN CAST(? AS BIGINT)
			WHEN attempts = 2 THEN CAST(? AS BIGINT)
			ELSE CAST(? AS BIGINT) END`
	}
	var failures []*BillingSettlementFailure
	err := DB.Where(`status = ? AND (delta != 0 OR reservation_managed = ?) AND
		(attempts = 0 OR updated_at <= (`+retryCutoffSQL+`))`,
		BillingSettlementStatusPending, true, now,
		delay128, delay64, delay32, delay16, delay8, delay4, delay2, retryDelay).
		Order("id asc").
		Limit(limit).
		Find(&failures).Error
	return failures, err
}

func MarkBillingSettlementFailureSettled(id int64) error {
	if id == 0 {
		return nil
	}
	return DB.Model(&BillingSettlementFailure{}).
		Where("id = ? AND status = ?", id, BillingSettlementStatusPending).
		Updates(map[string]interface{}{
			"status":     BillingSettlementStatusSettled,
			"updated_at": GetDBTimestamp(),
		}).Error
}

func MarkBillingSettlementFailureAttempt(id int64, err error) error {
	if id == 0 {
		return nil
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	return DB.Model(&BillingSettlementFailure{}).
		Where("id = ? AND status = ?", id, BillingSettlementStatusPending).
		Updates(map[string]interface{}{
			"attempts":   gorm.Expr("attempts + ?", 1),
			"last_error": errText,
			"updated_at": GetDBTimestamp(),
		}).Error
}

func GetBillingSettlementFailure(id int64) (*BillingSettlementFailure, error) {
	if id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var failure BillingSettlementFailure
	if err := DB.Where("id = ?", id).First(&failure).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &failure, nil
}
