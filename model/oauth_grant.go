package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type OAuthGrant struct {
	Id        int        `json:"id" gorm:"primaryKey"`
	UserId    int        `json:"user_id" gorm:"index;not null"`
	ClientId  string     `json:"client_id" gorm:"type:varchar(64);index;not null"`
	Scopes    string     `json:"scopes" gorm:"type:varchar(512);not null"`
	Revoked   bool       `json:"revoked" gorm:"default:false;index"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (OAuthGrant) TableName() string {
	return "oauth_grants"
}

func UpsertOAuthGrant(userId int, clientId string, scopes string) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where("user_id = ? AND client_id = ?", userId, clientId).First(&grant).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		grant = OAuthGrant{
			UserId:   userId,
			ClientId: clientId,
			Scopes:   scopes,
			Revoked:  false,
		}
		return &grant, DB.Create(&grant).Error
	}

	grant.Scopes = scopes
	grant.Revoked = false
	grant.RevokedAt = nil
	return &grant, DB.Save(&grant).Error
}

func GetActiveOAuthGrant(id int, userId int, clientId string) (*OAuthGrant, error) {
	var grant OAuthGrant
	err := DB.Where("id = ? AND user_id = ? AND client_id = ? AND revoked = ?", id, userId, clientId, false).First(&grant).Error
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func GetOAuthGrantsByUserId(userId int) ([]*OAuthGrant, error) {
	var grants []*OAuthGrant
	err := DB.Where("user_id = ?", userId).Order("id desc").Find(&grants).Error
	return grants, err
}

func RevokeOAuthGrantForUser(id int, userId int) error {
	now := time.Now()
	result := DB.Model(&OAuthGrant{}).
		Where("id = ? AND user_id = ? AND revoked = ?", id, userId, false).
		Updates(map[string]interface{}{
			"revoked":    true,
			"revoked_at": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
