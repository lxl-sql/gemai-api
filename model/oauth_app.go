package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const MaxOAuthRedirectUris = 20

const (
	OAuthClientTypeLegacy       = "legacy"
	OAuthClientTypeConfidential = "confidential"
	OAuthClientTypePublic       = "public"
)

type OAuthApp struct {
	Id               int       `json:"id" gorm:"primaryKey"`
	Name             string    `json:"name" gorm:"type:varchar(128);not null"`
	Description      string    `json:"description" gorm:"type:varchar(512)"`
	Logo             string    `json:"logo" gorm:"type:varchar(512)"`
	ClientId         string    `json:"client_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecretHash string    `json:"-" gorm:"column:client_secret_hash;type:varchar(128);not null"`
	ClientType       string    `json:"client_type" gorm:"type:varchar(32);not null;default:'legacy'"`
	RedirectUris     string    `json:"redirect_uris" gorm:"type:text;not null"`
	UserId           int       `json:"user_id" gorm:"index;not null"`
	Status           int       `json:"status" gorm:"type:int;default:1"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func NormalizeOAuthClientType(clientType string, fallback string) (string, error) {
	clientType = strings.ToLower(strings.TrimSpace(clientType))
	if clientType == "" {
		clientType = fallback
	}
	switch clientType {
	case OAuthClientTypeLegacy, OAuthClientTypeConfidential, OAuthClientTypePublic:
		return clientType, nil
	default:
		return "", fmt.Errorf("unsupported OAuth client type: %s", clientType)
	}
}

func (app *OAuthApp) EffectiveClientType() string {
	if app == nil || strings.TrimSpace(app.ClientType) == "" {
		return OAuthClientTypeLegacy
	}
	clientType, err := NormalizeOAuthClientType(app.ClientType, OAuthClientTypeLegacy)
	if err != nil {
		return OAuthClientTypeConfidential
	}
	return clientType
}

func (OAuthApp) TableName() string {
	return "oauth_apps"
}

func (app *OAuthApp) GetRedirectUris() []string {
	var uris []string
	if app.RedirectUris == "" {
		return uris
	}
	_ = common.Unmarshal([]byte(app.RedirectUris), &uris)
	return uris
}

func (app *OAuthApp) SetRedirectUris(uris []string) error {
	normalized, err := ValidateOAuthRedirectUris(uris)
	if err != nil {
		return err
	}
	data, err := common.Marshal(normalized)
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
	return app.ValidateClientSecretContext(context.Background(), secret)
}

func ValidateOAuthRedirectUris(uris []string) ([]string, error) {
	if len(uris) == 0 {
		return nil, errors.New("at least one redirect_uri is required")
	}
	if len(uris) > MaxOAuthRedirectUris {
		return nil, fmt.Errorf("too many redirect_uris, maximum is %d", MaxOAuthRedirectUris)
	}

	seen := make(map[string]struct{}, len(uris))
	normalized := make([]string, 0, len(uris))
	for _, uri := range uris {
		trimmed := strings.TrimSpace(uri)
		if err := ValidateOAuthRedirectUri(trimmed); err != nil {
			return nil, err
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one redirect_uri is required")
	}
	return normalized, nil
}

func ValidateOAuthRedirectUri(raw string) error {
	if raw == "" {
		return errors.New("redirect_uri cannot be empty")
	}
	if len(raw) > 512 {
		return errors.New("redirect_uri is too long")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid redirect_uri: %s", raw)
	}
	if parsed.Fragment != "" {
		return errors.New("redirect_uri must not contain fragment")
	}
	if parsed.User != nil {
		return errors.New("redirect_uri must not contain userinfo")
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackOAuthHost(parsed.Hostname()) {
			return nil
		}
		return errors.New("http redirect_uri is only allowed for localhost or loopback addresses")
	default:
		return errors.New("redirect_uri scheme must be https, or http for localhost")
	}
}

func isLoopbackOAuthHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func GetOAuthAppByClientId(clientId string) (*OAuthApp, error) {
	return GetOAuthAppByClientIdContext(context.Background(), clientId)
}

func GetOAuthAppByClientIdContext(ctx context.Context, clientId string) (*OAuthApp, error) {
	var app OAuthApp
	err := DB.WithContext(ctx).
		Where("client_id = ? AND status = ?", clientId, common.UserStatusEnabled).
		First(&app).Error
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func GetOAuthAppByClientIdAnyStatus(clientId string) (*OAuthApp, error) {
	var app OAuthApp
	err := DB.Where("client_id = ?", clientId).First(&app).Error
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
	if err := DB.Create(app).Error; err != nil {
		return err
	}
	if err := invalidateOAuthAppCache(app.ClientId); err != nil {
		common.SysLog("failed to invalidate OAuth app cache after create: " + err.Error())
	}
	return nil
}

func UpdateOAuthApp(app *OAuthApp) error {
	if err := DB.Save(app).Error; err != nil {
		return err
	}
	if err := invalidateOAuthAppCache(app.ClientId); err != nil {
		common.SysLog("failed to invalidate OAuth app cache after update: " + err.Error())
	}
	return nil
}

func DeleteOAuthApp(id int, userId int) error {
	var app OAuthApp
	if err := DB.Select("client_id").Where("id = ? AND user_id = ?", id, userId).First(&app).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("oauth app not found or no permission")
		}
		return err
	}
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&OAuthApp{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("oauth app not found or no permission")
	}
	if err := invalidateOAuthAppCache(app.ClientId); err != nil {
		common.SysLog("failed to invalidate OAuth app cache after delete: " + err.Error())
	}
	return nil
}
