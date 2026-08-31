package model

import (
	"context"
	"time"
)

type OAuthRefreshTokenHistory struct {
	Id                   int       `json:"id" gorm:"primaryKey"`
	GrantId              int       `json:"grant_id" gorm:"index;not null"`
	ClientId             string    `json:"client_id" gorm:"type:varchar(64);index:idx_oauth_refresh_history_lookup,priority:1;not null"`
	AuthorizationVersion int64     `json:"authorization_version" gorm:"index;not null"`
	TokenHash            string    `json:"-" gorm:"type:varchar(64);uniqueIndex;index:idx_oauth_refresh_history_lookup,priority:2;not null"`
	RotatedAt            time.Time `json:"rotated_at" gorm:"index;not null"`
	ExpiresAt            time.Time `json:"expires_at" gorm:"index:idx_oauth_refresh_history_expires_at;not null"`
}

func (OAuthRefreshTokenHistory) TableName() string {
	return "oauth_refresh_token_histories"
}

func getOAuthRefreshTokenHistory(clientId string, tokenHash string) (*OAuthRefreshTokenHistory, error) {
	var history OAuthRefreshTokenHistory
	err := DB.Where("client_id = ? AND token_hash = ?", clientId, tokenHash).First(&history).Error
	if err != nil {
		return nil, err
	}
	return &history, nil
}

func DeleteExpiredOAuthRefreshTokenHistoryBatch(ctx context.Context, now time.Time, limit int) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 1000
	}
	var ids []int
	if err := DB.WithContext(ctx).
		Model(&OAuthRefreshTokenHistory{}).
		Where("expires_at < ?", now).
		Order("expires_at asc, id asc").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := DB.WithContext(ctx).Where("id IN ?", ids).Delete(&OAuthRefreshTokenHistory{})
	return result.RowsAffected, result.Error
}
