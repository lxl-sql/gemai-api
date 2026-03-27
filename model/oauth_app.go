package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type OAuthApp struct {
	Id               int       `json:"id" gorm:"primaryKey"`
	Name             string    `json:"name" gorm:"type:varchar(128);not null"`
	Description      string    `json:"description" gorm:"type:varchar(512)"`
	Logo             string    `json:"logo" gorm:"type:varchar(512)"`
	ClientId         string    `json:"client_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecretHash string    `json:"-" gorm:"column:client_secret_hash;type:varchar(128);not null"`
	RedirectUris     string    `json:"redirect_uris" gorm:"type:text;not null"`
	UserId           int       `json:"user_id" gorm:"index;not null"`
	Status           int       `json:"status" gorm:"type:int;default:1"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (OAuthApp) TableName() string {
	return "oauth_apps"
}

func (app *OAuthApp) GetRedirectUris() []string {
	var uris []string
	if app.RedirectUris == "" {
		return uris
	}
	_ = json.Unmarshal([]byte(app.RedirectUris), &uris)
	return uris
}

func (app *OAuthApp) SetRedirectUris(uris []string) error {
	data, err := json.Marshal(uris)
	if err != nil {
		return err
	}
	app.RedirectUris = string(data)
	return nil
}

func (app *OAuthApp) IsRedirectUriAllowed(uri string) bool {
	for _, allowed := range app.GetRedirectUris() {
		if allowed == uri {
			return true
		}
	}
	return false
}

func (app *OAuthApp) ValidateClientSecret(secret string) bool {
	return common.ValidatePasswordAndHash(secret, app.ClientSecretHash)
}

func GetOAuthAppByClientId(clientId string) (*OAuthApp, error) {
	var app OAuthApp
	err := DB.Where("client_id = ? AND status = ?", clientId, common.UserStatusEnabled).First(&app).Error
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func GetOAuthAppsByUserId(userId int, keyword string) ([]*OAuthApp, error) {
	var apps []*OAuthApp
	tx := DB.Where("user_id = ?", userId)
	if keyword != "" {
		tx = tx.Where("name LIKE ? OR client_id LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	err := tx.Order("id desc").Find(&apps).Error
	return apps, err
}

func GetOAuthAppById(id int) (*OAuthApp, error) {
	var app OAuthApp
	err := DB.First(&app, id).Error
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func CreateOAuthApp(app *OAuthApp) error {
	return DB.Create(app).Error
}

func UpdateOAuthApp(app *OAuthApp) error {
	return DB.Save(app).Error
}

func DeleteOAuthApp(id int, userId int) error {
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&OAuthApp{})
	if result.RowsAffected == 0 {
		return errors.New("oauth app not found or no permission")
	}
	return result.Error
}
