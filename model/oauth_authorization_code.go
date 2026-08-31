package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrOAuthAuthorizationCodeInvalid = errors.New("oauth authorization code is invalid")

type OAuthAuthorizationCode struct {
	Id                  int       `json:"id" gorm:"primaryKey"`
	Code                string    `json:"code" gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientId            string    `json:"client_id" gorm:"type:varchar(64);index;not null"`
	UserId              int       `json:"user_id" gorm:"index;not null"`
	RedirectUri         string    `json:"redirect_uri" gorm:"type:varchar(512);not null"`
	Scope               string    `json:"scope" gorm:"type:varchar(256)"`
	CodeChallenge       string    `json:"code_challenge" gorm:"type:varchar(128)"`
	CodeChallengeMethod string    `json:"code_challenge_method" gorm:"type:varchar(16)"`
	ExpiresAt           time.Time `json:"expires_at" gorm:"not null;index:idx_oauth_authorization_codes_expires_at"`
	Used                bool      `json:"used" gorm:"default:false"`
	CreatedAt           time.Time `json:"created_at"`
}

func (OAuthAuthorizationCode) TableName() string {
	return "oauth_authorization_codes"
}

func CreateOAuthAuthorizationCode(code *OAuthAuthorizationCode) error {
	return DB.Create(code).Error
}

func GetOAuthAuthorizationCode(code string) (*OAuthAuthorizationCode, error) {
	return GetOAuthAuthorizationCodeContext(context.Background(), code)
}

func GetOAuthAuthorizationCodeContext(ctx context.Context, code string) (*OAuthAuthorizationCode, error) {
	var authCode OAuthAuthorizationCode
	err := DB.WithContext(ctx).Where("code = ?", code).First(&authCode).Error
	if err != nil {
		return nil, err
	}
	return &authCode, nil
}

func ConsumeOAuthAuthorizationCode(code string) (bool, error) {
	return ConsumeOAuthAuthorizationCodeTx(DB, code)
}

func ConsumeOAuthAuthorizationCodeTx(tx *gorm.DB, code string) (bool, error) {
	if tx == nil {
		return false, errors.New("oauth authorization code transaction is nil")
	}
	result := tx.Model(&OAuthAuthorizationCode{}).
		Where("code = ? AND used = ? AND expires_at > ?", code, false, time.Now()).
		Update("used", true)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func ConsumeOAuthAuthorizationCodeRecordTx(tx *gorm.DB, code *OAuthAuthorizationCode, now time.Time) (bool, error) {
	if tx == nil || code == nil {
		return false, errors.New("oauth authorization code transaction is invalid")
	}
	result := tx.Model(&OAuthAuthorizationCode{}).
		Where(
			"code = ? AND client_id = ? AND user_id = ? AND redirect_uri = ? AND scope = ? AND code_challenge = ? AND code_challenge_method = ? AND used = ? AND expires_at > ?",
			code.Code,
			code.ClientId,
			code.UserId,
			code.RedirectUri,
			code.Scope,
			code.CodeChallenge,
			code.CodeChallengeMethod,
			false,
			now,
		).
		Update("used", true)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// DeleteExpiredOAuthAuthorizationCodesBatch deletes one bounded batch using a
// portable SELECT-ids-then-DELETE sequence that works on SQLite, MySQL, and
// PostgreSQL. Used codes stay until their original expiry so the request path
// never launches cleanup work and replay evidence remains available briefly.
func DeleteExpiredOAuthAuthorizationCodesBatch(ctx context.Context, now time.Time, limit int) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 1000
	}
	var ids []int
	if err := DB.WithContext(ctx).
		Model(&OAuthAuthorizationCode{}).
		Where("expires_at < ?", now).
		Order("expires_at asc, id asc").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := DB.WithContext(ctx).Where("id IN ?", ids).Delete(&OAuthAuthorizationCode{})
	return result.RowsAffected, result.Error
}
