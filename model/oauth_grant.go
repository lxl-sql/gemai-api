package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OAuthGrant struct {
	Id                    int        `json:"id" gorm:"primaryKey"`
	UserId                int        `json:"user_id" gorm:"uniqueIndex:idx_oauth_grants_user_client;not null"`
	ClientId              string     `json:"client_id" gorm:"type:varchar(64);uniqueIndex:idx_oauth_grants_user_client;index:idx_oauth_grants_refresh_lookup,priority:1;not null"`
	Scopes                string     `json:"scopes" gorm:"type:varchar(512);not null"`
	AuthorizationVersion  int64      `json:"authorization_version" gorm:"not null;default:0"`
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
	var grant *OAuthGrant
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		grant, err = upsertOAuthGrantTx(tx, userId, clientId, scopes, "", nil)
		return err
	})
	return grant, err
}

// UpsertOAuthGrantWithRefreshTokenTx creates a new authorization generation
// and persists its initial refresh token in the caller's transaction. Every
// successful authorization increments AuthorizationVersion so access tokens
// issued for an older, later-revoked generation can never become valid again.
func UpsertOAuthGrantWithRefreshTokenTx(
	tx *gorm.DB,
	userId int,
	clientId string,
	scopes string,
	refreshToken string,
	expiresAt time.Time,
) (*OAuthGrant, error) {
	return upsertOAuthGrantTx(tx, userId, clientId, scopes, HashOAuthRefreshToken(refreshToken), &expiresAt)
}

func upsertOAuthGrantTx(
	tx *gorm.DB,
	userId int,
	clientId string,
	scopes string,
	refreshTokenHash string,
	refreshTokenExpiresAt *time.Time,
) (*OAuthGrant, error) {
	if tx == nil {
		return nil, errors.New("oauth grant transaction is nil")
	}
	for {
		var previous OAuthGrant
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND client_id = ?", userId, clientId).
			First(&previous).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			grant := OAuthGrant{
				UserId:                userId,
				ClientId:              clientId,
				Scopes:                scopes,
				AuthorizationVersion:  1,
				Revoked:               false,
				RefreshTokenHash:      refreshTokenHash,
				RefreshTokenExpiresAt: refreshTokenExpiresAt,
			}
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "client_id"}},
				DoNothing: true,
			}).Create(&grant)
			if result.Error != nil {
				return nil, result.Error
			}
			if result.RowsAffected == 1 {
				return &grant, nil
			}
			// A concurrent authorization inserted the first row after our
			// locking read. Load that generation and serialize the replacement.
			continue
		}
		if err != nil {
			return nil, err
		}

		authorizationVersion := previous.AuthorizationVersion + 1
		// Keep the old row alive until after the replacement is inserted. This
		// both frees the user/client unique key and prevents SQLite from reusing
		// the deleted integer primary key when this is the only grant row.
		retiredClientId := fmt.Sprintf("retired:%d:%x", previous.Id, time.Now().UnixNano())
		if updateErr := tx.Model(&OAuthGrant{}).
			Where("id = ?", previous.Id).
			Updates(map[string]any{
				"client_id":                   retiredClientId,
				"revoked":                     true,
				"refresh_token_hash":          "",
				"previous_refresh_token_hash": "",
				"refresh_token_expires_at":    nil,
			}).Error; updateErr != nil {
			return nil, updateErr
		}
		grant := OAuthGrant{
			UserId:                userId,
			ClientId:              clientId,
			Scopes:                scopes,
			AuthorizationVersion:  authorizationVersion,
			Revoked:               false,
			RefreshTokenHash:      refreshTokenHash,
			RefreshTokenExpiresAt: refreshTokenExpiresAt,
		}
		if err := tx.Create(&grant).Error; err != nil {
			return nil, err
		}
		if err := tx.Delete(&OAuthGrant{}, previous.Id).Error; err != nil {
			return nil, err
		}
		return &grant, nil
	}
}

func GetActiveOAuthGrant(id int, userId int, clientId string, authorizationVersion int64) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where(
		"id = ? AND user_id = ? AND client_id = ? AND authorization_version = ? AND revoked = ?",
		id, userId, clientId, authorizationVersion, false,
	).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func GetActiveOAuthGrantLegacy(id int, userId int, clientId string) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where(
		"id = ? AND user_id = ? AND client_id = ? AND revoked = ?",
		id, userId, clientId, false,
	).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func HashOAuthRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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
	grant := &OAuthGrant{}
	if err := DB.Where("id = ? AND client_id = ?", grantId, clientId).First(grant).Error; err != nil {
		return nil, err
	}
	if err := RotateOAuthGrantRefreshTokenCAS(grant, refreshToken, nextRefreshToken, nextExpiresAt); err != nil {
		return nil, err
	}
	return grant, nil
}

// RotateOAuthGrantRefreshTokenCAS rotates the token family without a trailing
// SELECT. The authorization version in the predicate prevents a stale request
// from overwriting a concurrent revoke or reauthorization.
func RotateOAuthGrantRefreshTokenCAS(grant *OAuthGrant, refreshToken string, nextRefreshToken string, nextExpiresAt time.Time) error {
	if grant == nil {
		return errors.New("oauth grant is nil")
	}
	now := time.Now()
	lastRefreshAt := now
	oldRefreshTokenHash := HashOAuthRefreshToken(refreshToken)
	nextRefreshTokenHash := HashOAuthRefreshToken(nextRefreshToken)
	oldExpiresAt := now
	if grant.RefreshTokenExpiresAt != nil {
		oldExpiresAt = *grant.RefreshTokenExpiresAt
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&OAuthGrant{}).
			Where(
				"id = ? AND client_id = ? AND authorization_version = ? AND revoked = ? AND refresh_token_hash = ? AND refresh_token_expires_at > ?",
				grant.Id, grant.ClientId, grant.AuthorizationVersion, false, oldRefreshTokenHash, now,
			).
			Updates(map[string]interface{}{
				"refresh_token_hash":          nextRefreshTokenHash,
				"refresh_token_expires_at":    nextExpiresAt,
				"previous_refresh_token_hash": oldRefreshTokenHash,
				"last_refresh_at":             &lastRefreshAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&OAuthRefreshTokenHistory{
			GrantId:              grant.Id,
			ClientId:             grant.ClientId,
			AuthorizationVersion: grant.AuthorizationVersion,
			TokenHash:            oldRefreshTokenHash,
			RotatedAt:            now,
			ExpiresAt:            oldExpiresAt,
		}).Error
	}); err != nil {
		return err
	}
	grant.RefreshTokenHash = nextRefreshTokenHash
	grant.RefreshTokenExpiresAt = &nextExpiresAt
	grant.PreviousRefreshTokenHash = oldRefreshTokenHash
	grant.LastRefreshAt = &lastRefreshAt
	return nil
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
	tokenHash := HashOAuthRefreshToken(refreshToken)
	history, historyErr := getOAuthRefreshTokenHistory(clientId, tokenHash)
	if historyErr == nil {
		if history.RotatedAt.After(graceCutoff) {
			return false, nil
		}
		result := DB.Model(&OAuthGrant{}).
			Where(
				"id = ? AND client_id = ? AND authorization_version = ? AND revoked = ?",
				history.GrantId, clientId, history.AuthorizationVersion, false,
			).
			Updates(map[string]interface{}{
				"revoked":                     true,
				"revoked_at":                  &now,
				"authorization_version":       gorm.Expr("authorization_version + ?", 1),
				"refresh_token_hash":          "",
				"refresh_token_expires_at":    nil,
				"previous_refresh_token_hash": "",
			})
		if result.Error != nil {
			return false, result.Error
		}
		return result.RowsAffected > 0, nil
	}
	if !errors.Is(historyErr, gorm.ErrRecordNotFound) {
		return false, historyErr
	}
	result := DB.Model(&OAuthGrant{}).
		Where(
			"client_id = ? AND revoked = ? AND previous_refresh_token_hash = ? AND last_refresh_at IS NOT NULL AND last_refresh_at <= ?",
			clientId, false, tokenHash, graceCutoff,
		).
		Updates(map[string]interface{}{
			"revoked":                     true,
			"revoked_at":                  &now,
			"authorization_version":       gorm.Expr("authorization_version + ?", 1),
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
			"authorization_version":       gorm.Expr("authorization_version + ?", 1),
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
			"authorization_version":       gorm.Expr("authorization_version + ?", 1),
			"refresh_token_hash":          "",
			"refresh_token_expires_at":    nil,
			"previous_refresh_token_hash": "",
		}).Error
}
