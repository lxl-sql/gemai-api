package model

import (
	"errors"

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
	if input.RequestId == "" {
		input.RequestId = common.GetUUID()
	}
	now := common.GetTimestamp()
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
		Status:                  BillingSettlementStatusPending,
		Attempts:                0,
		LastError:               input.LastError,
		UpdatedAt:               now,
	}
	return DB.Clauses(clause.OnConflict{
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
			"status":                     BillingSettlementStatusPending,
			"last_error":                 failure.LastError,
			"updated_at":                 now,
		}),
	}).Create(&failure).Error
}

func HasPendingBillingSettlementFailures() bool {
	var id int64
	err := DB.Model(&BillingSettlementFailure{}).
		Where("status = ? AND delta != 0", BillingSettlementStatusPending).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
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
	retryBefore := common.GetTimestamp() - int64(retryDelaySeconds)
	var failures []*BillingSettlementFailure
	err := DB.Where("status = ? AND delta != 0 AND (attempts = 0 OR updated_at <= ?)", BillingSettlementStatusPending, retryBefore).
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
			"updated_at": common.GetTimestamp(),
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
			"updated_at": common.GetTimestamp(),
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
