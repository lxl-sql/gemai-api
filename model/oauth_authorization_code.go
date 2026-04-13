package model

import (
	"time"
)

type OAuthAuthorizationCode struct {
	Id           int       `json:"id" gorm:"primaryKey"`
	Code         string    `json:"code" gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientId     string    `json:"client_id" gorm:"type:varchar(64);index;not null"`
	UserId       int       `json:"user_id" gorm:"index;not null"`
	RedirectUri  string    `json:"redirect_uri" gorm:"type:varchar(512);not null"`
	Scope        string    `json:"scope" gorm:"type:varchar(256)"`
	SessionValue string    `json:"-" gorm:"type:text"`
	ExpiresAt    time.Time `json:"expires_at" gorm:"not null"`
	Used         bool      `json:"used" gorm:"default:false"`
	CreatedAt    time.Time `json:"created_at"`
}

func (OAuthAuthorizationCode) TableName() string {
	return "oauth_authorization_codes"
}

func CreateOAuthAuthorizationCode(code *OAuthAuthorizationCode) error {
	return DB.Create(code).Error
}

func GetOAuthAuthorizationCode(code string) (*OAuthAuthorizationCode, error) {
	var authCode OAuthAuthorizationCode
	err := DB.Where("code = ?", code).First(&authCode).Error
	if err != nil {
		return nil, err
	}
	return &authCode, nil
}

func MarkOAuthAuthorizationCodeUsed(code string) error {
	return DB.Model(&OAuthAuthorizationCode{}).Where("code = ?", code).Update("used", true).Error
}

func CleanExpiredOAuthAuthorizationCodes() error {
	return DB.Where("expires_at < ? OR used = ?", time.Now(), true).Delete(&OAuthAuthorizationCode{}).Error
}
