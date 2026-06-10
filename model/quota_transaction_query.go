package model

import (
	"strings"

	"gorm.io/gorm"
)

type QuotaTransactionFilters struct {
	UserId        int
	Username      string
	Type          string
	Source        string
	ReferenceType string
	ReferenceId   string
	Direction     string
	Bucket        string
	StartTime     int64
	EndTime       int64
}

type QuotaTransactionRecord struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id"`
	Username          string `json:"username" gorm:"column:username"`
	Type              string `json:"type"`
	QuotaDelta        int    `json:"quota_delta"`
	GiftQuotaDelta    int    `json:"gift_quota_delta"`
	BalanceBefore     int    `json:"balance_before"`
	GiftBalanceBefore int    `json:"gift_balance_before"`
	BalanceAfter      int    `json:"balance_after"`
	GiftBalanceAfter  int    `json:"gift_balance_after"`
	TotalDelta        int    `json:"total_delta"`
	Source            string `json:"source"`
	ReferenceType     string `json:"reference_type"`
	ReferenceId       string `json:"reference_id"`
	RequestId         string `json:"request_id"`
	IdempotencyKey    string `json:"idempotency_key"`
	OperatorId        int    `json:"operator_id"`
	OperatorName      string `json:"operator_name" gorm:"column:operator_name"`
	Metadata          string `json:"metadata"`
	CreatedAt         int64  `json:"created_at"`
}

func applyQuotaTransactionFilters(tx *gorm.DB, filters QuotaTransactionFilters) *gorm.DB {
	if filters.UserId > 0 {
		tx = tx.Where("qt.user_id = ?", filters.UserId)
	}
	if username := strings.TrimSpace(filters.Username); username != "" {
		tx = tx.Where("u.username = ?", username)
	}
	if value := strings.TrimSpace(filters.Type); value != "" {
		tx = tx.Where("qt.type = ?", value)
	}
	if value := strings.TrimSpace(filters.Source); value != "" {
		tx = tx.Where("qt.source = ?", value)
	}
	if value := strings.TrimSpace(filters.ReferenceType); value != "" {
		tx = tx.Where("qt.reference_type = ?", value)
	}
	if value := strings.TrimSpace(filters.ReferenceId); value != "" {
		tx = tx.Where("qt.reference_id = ?", value)
	}
	switch strings.TrimSpace(filters.Direction) {
	case "income":
		tx = tx.Where("qt.total_delta > 0")
	case "expense":
		tx = tx.Where("qt.total_delta < 0")
	}
	switch strings.TrimSpace(filters.Bucket) {
	case QuotaBucketRecharge:
		tx = tx.Where("qt.quota_delta <> 0")
	case QuotaBucketGift:
		tx = tx.Where("qt.gift_quota_delta <> 0")
	case "both":
		tx = tx.Where("qt.quota_delta <> 0 AND qt.gift_quota_delta <> 0")
	}
	if filters.StartTime > 0 {
		tx = tx.Where("qt.created_at >= ?", filters.StartTime)
	}
	if filters.EndTime > 0 {
		tx = tx.Where("qt.created_at <= ?", filters.EndTime)
	}
	return tx
}

func quotaTransactionQueryBase() *gorm.DB {
	return DB.Table("quota_transactions AS qt").
		Joins("LEFT JOIN users AS u ON u.id = qt.user_id").
		Joins("LEFT JOIN users AS op ON op.id = qt.operator_id")
}

func GetQuotaTransactions(filters QuotaTransactionFilters, startIdx int, num int) (records []*QuotaTransactionRecord, total int64, err error) {
	base := applyQuotaTransactionFilters(quotaTransactionQueryBase(), filters)
	if err = base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = base.
		Select("qt.*, u.username AS username, op.username AS operator_name").
		Order("qt.id DESC").
		Limit(num).
		Offset(startIdx).
		Scan(&records).Error
	return records, total, err
}
