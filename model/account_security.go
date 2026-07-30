package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const accountAccessTokenLength = 32

func newAccountAccessToken() (string, error) {
	return common.GenerateRandomCharsKey(accountAccessTokenLength)
}

func RotateAccountAccessToken(userId int) (string, error) {
	if userId <= 0 {
		return "", errors.New("invalid user id")
	}
	accessToken, err := newAccountAccessToken()
	if err != nil {
		return "", err
	}
	if err := DB.Model(&User{}).
		Where("id = ?", userId).
		Update("access_token", accessToken).Error; err != nil {
		return "", err
	}
	return accessToken, nil
}

// applyPasswordSecurityTx is the single password-security boundary. Password
// changes must revoke dashboard sessions, account access tokens, and delegated
// OAuth grants in the same database transaction.
func applyPasswordSecurityTx(tx *gorm.DB, userId int, plainPassword string) error {
	if tx == nil || userId <= 0 || plainPassword == "" {
		return errors.New("invalid password security update")
	}
	passwordHash, err := common.Password2Hash(plainPassword)
	if err != nil {
		return err
	}
	accessToken, err := newAccountAccessToken()
	if err != nil {
		return err
	}
	if err := tx.Model(&User{}).
		Where("id = ?", userId).
		Updates(map[string]interface{}{
			"password":         passwordHash,
			"access_token":     accessToken,
			"security_version": gorm.Expr("COALESCE(security_version, 0) + ?", 1),
		}).Error; err != nil {
		return err
	}
	return RevokeAllOAuthGrantsForUserTx(tx, userId)
}

// RevokeUserSecurityCredentials invalidates every dashboard session and
// account-level credential while leaving API tokens untouched. Incident
// response can revoke API tokens separately after preserving evidence.
func RevokeUserSecurityCredentials(userId int) error {
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	accessToken, err := newAccountAccessToken()
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).
			Where("id = ?", userId).
			Updates(map[string]interface{}{
				"access_token":     accessToken,
				"security_version": gorm.Expr("COALESCE(security_version, 0) + ?", 1),
			}).Error; err != nil {
			return err
		}
		return RevokeAllOAuthGrantsForUserTx(tx, userId)
	}); err != nil {
		return err
	}
	return InvalidateUserCache(userId)
}
