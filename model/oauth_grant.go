package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OAuthGrant struct {
	Id                    int        `json:"id" gorm:"primaryKey"`
	UserId                int        `json:"user_id" gorm:"uniqueIndex:idx_oauth_grants_user_client;not null"`
	ClientId              string     `json:"client_id" gorm:"type:varchar(64);uniqueIndex:idx_oauth_grants_user_client;index:idx_oauth_grants_refresh_lookup,priority:1;not null"`
	Scopes                string     `json:"scopes" gorm:"type:varchar(512);not null"`
	Revoked               bool       `json:"revoked" gorm:"default:false;index;index:idx_oauth_grants_refresh_lookup,priority:3"`
	RevokedAt             *time.Time `json:"revoked_at"`
	RefreshTokenHash      string     `json:"-" gorm:"type:varchar(64);index;index:idx_oauth_grants_refresh_lookup,priority:2"`
	RefreshTokenExpiresAt *time.Time `json:"refresh_token_expires_at" gorm:"index:idx_oauth_grants_refresh_lookup,priority:4"`
	// PreviousRefreshTokenHash keeps the hash of the immediately rotated-out refresh token.
	// Presenting that token again is a replay signal (RFC 9700): the whole grant is revoked.
	PreviousRefreshTokenHash string     `json:"-" gorm:"type:varchar(64);index"`
	LastRefreshAt            *time.Time `json:"last_refresh_at"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

func (OAuthGrant) TableName() string {
	return "oauth_grants"
}

func UpsertOAuthGrant(userId int, clientId string, scopes string) (*OAuthGrant, error) {
	grant := OAuthGrant{
		UserId:   userId,
		ClientId: clientId,
		Scopes:   scopes,
		Revoked:  false,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "client_id"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"scopes":     scopes,
			"revoked":    false,
			"revoked_at": nil,
		}),
	}).Create(&grant).Error; err != nil {
		return nil, err
	}

	var saved OAuthGrant
	if err := DB.Where("user_id = ? AND client_id = ?", userId, clientId).First(&saved).Error; err != nil {
		return nil, err
	}
	return &saved, nil
}

func GetActiveOAuthGrant(id int, userId int, clientId string) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where("id = ? AND user_id = ? AND client_id = ? AND revoked = ?", id, userId, clientId, false).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func HashOAuthRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func SaveOAuthGrantRefreshToken(grant *OAuthGrant, refreshToken string, expiresAt time.Time) error {
	grant.RefreshTokenHash = HashOAuthRefreshToken(refreshToken)
	grant.RefreshTokenExpiresAt = &expiresAt
	// 重新授权开启新的 token 家族，旧链不再视为重放信号
	grant.PreviousRefreshTokenHash = ""
	grant.LastRefreshAt = nil
	return DB.Save(grant).Error
}

func GetActiveOAuthGrantByRefreshToken(clientId string, refreshToken string) (*OAuthGrant, error) {
	now := time.Now()
	var grant OAuthGrant
	err := DB.Where(
		"client_id = ? AND revoked = ? AND refresh_token_hash = ? AND refresh_token_expires_at > ?",
		clientId,
		false,
		HashOAuthRefreshToken(refreshToken),
		now,
	).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func RotateOAuthGrantRefreshToken(grantId int, clientId string, refreshToken string, nextRefreshToken string, nextExpiresAt time.Time) (*OAuthGrant, error) {
	now := time.Now()
	lastRefreshAt := now
	result := DB.Model(&OAuthGrant{}).
		Where("id = ? AND client_id = ? AND revoked = ? AND refresh_token_hash = ? AND refresh_token_expires_at > ?", grantId, clientId, false, HashOAuthRefreshToken(refreshToken), now).
		Updates(map[string]interface{}{
			"refresh_token_hash":          HashOAuthRefreshToken(nextRefreshToken),
			"refresh_token_expires_at":    nextExpiresAt,
			"previous_refresh_token_hash": HashOAuthRefreshToken(refreshToken),
			"last_refresh_at":             &lastRefreshAt,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	var grant OAuthGrant
	if err := DB.Where("client_id = ? AND refresh_token_hash = ? AND revoked = ?", clientId, HashOAuthRefreshToken(nextRefreshToken), false).First(&grant).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}

// oauthRefreshReplayGraceSeconds 是上一代 refresh token 重放的宽限期：
// 同一客户端多实例/多标签页并发刷新、或刷新响应丢失后的立即重试，都属于
// 紧跟轮换发生的合法重用，不应触发授权撤销；超过宽限期仍出现的旧 token
// 才视为泄露重放（RFC 9700）。宽限期内的旧 token 同样不会换发新凭证。
const oauthRefreshReplayGraceSeconds = 60

// RevokeOAuthGrantByReplayedRefreshToken detects reuse of an already-rotated refresh
// token and revokes the whole grant (token family revocation per RFC 9700): a rotated
// token being presented again after the grace window means it leaked to a second party.
// Returns true when a replay was detected and the grant was revoked.
func RevokeOAuthGrantByReplayedRefreshToken(clientId string, refreshToken string) (bool, error) {
	if clientId == "" || refreshToken == "" {
		return false, nil
	}
	now := time.Now()
	graceCutoff := now.Add(-oauthRefreshReplayGraceSeconds * time.Second)
	result := DB.Model(&OAuthGrant{}).
		Where(
			"client_id = ? AND revoked = ? AND previous_refresh_token_hash = ? AND last_refresh_at IS NOT NULL AND last_refresh_at <= ?",
			clientId, false, HashOAuthRefreshToken(refreshToken), graceCutoff,
		).
		Updates(map[string]interface{}{
			"revoked":                     true,
			"revoked_at":                  &now,
			"refresh_token_hash":          "",
			"refresh_token_expires_at":    nil,
			"previous_refresh_token_hash": "",
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func GetOAuthGrantsByUserId(userId int) ([]*OAuthGrant, error) {
	var grants []*OAuthGrant
	err := DB.Where("user_id = ?", userId).Order("id desc").Find(&grants).Error
	return grants, err
}

func GetOAuthGrantForUser(id int, userId int) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where("id = ? AND user_id = ?", id, userId).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func RevokeOAuthGrantForUser(id int, userId int) error {
	now := time.Now()
	result := DB.Model(&OAuthGrant{}).
		Where("id = ? AND user_id = ? AND revoked = ?", id, userId, false).
		Updates(map[string]interface{}{
			"revoked":                     true,
			"revoked_at":                  &now,
			"refresh_token_hash":          "",
			"refresh_token_expires_at":    nil,
			"previous_refresh_token_hash": "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RevokeAllOAuthGrantsForUserTx invalidates every delegated authorization for
// a user. It is shared by password recovery and "revoke all credentials" so
// those security boundaries cannot drift apart.
func RevokeAllOAuthGrantsForUserTx(tx *gorm.DB, userId int) error {
	if tx == nil || userId <= 0 {
		return errors.New("invalid user security revocation request")
	}
	now := time.Now()
	return tx.Model(&OAuthGrant{}).
		Where("user_id = ? AND revoked = ?", userId, false).
		Updates(map[string]interface{}{
			"revoked":                     true,
			"revoked_at":                  &now,
			"refresh_token_hash":          "",
			"refresh_token_expires_at":    nil,
			"previous_refresh_token_hash": "",
		}).Error
}
