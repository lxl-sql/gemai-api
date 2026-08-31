package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const (
	oauthAppCachePrefix       = "oauth:app:"
	oauthClientAuthPrefix     = "oauth:client-auth:"
	oauthAppCacheTTL          = 5 * time.Minute
	oauthClientAuthSuccessTTL = 2 * time.Minute
	oauthCacheTimeout         = 200 * time.Millisecond
)

type oauthAppCacheDTO struct {
	Id           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Logo         string `json:"logo"`
	ClientId     string `json:"client_id"`
	ClientType   string `json:"client_type"`
	RedirectUris string `json:"redirect_uris"`
	Status       int    `json:"status"`
}

// GetCachedOAuthAppByClientIdContext is for public authorization metadata.
// Security-sensitive token, UserInfo, and delegated API checks intentionally
// use GetOAuthAppByClientIdContext so app disable/delete and secret rotation
// take effect immediately even when Redis invalidation is degraded.
func GetCachedOAuthAppByClientIdContext(ctx context.Context, clientId string) (*OAuthApp, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if app, found := getCachedOAuthApp(ctx, clientId); found {
		return app, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		return GetOAuthAppByClientIdContext(ctx, clientId)
	}
	app, err := coalesceAuthCacheLoad(
		ctx,
		authCacheLoadNamespaceOAuth,
		clientId,
		func() (OAuthApp, error) {
			if cached, found := getCachedOAuthApp(context.Background(), clientId); found {
				return *cached, nil
			}
			loaded, loadErr := GetOAuthAppByClientIdContext(context.Background(), clientId)
			if loadErr != nil {
				return OAuthApp{}, loadErr
			}
			writeOAuthAppCache(context.Background(), loaded)
			return *loaded, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func getCachedOAuthApp(ctx context.Context, clientId string) (*OAuthApp, bool) {
	if !common.RedisEnabled || common.RDB == nil || clientId == "" {
		return nil, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, oauthCacheTimeout)
	value, err := common.RDB.Get(cacheCtx, oauthAppCachePrefix+clientId).Bytes()
	cancel()
	if err != nil {
		return nil, false
	}
	var cached oauthAppCacheDTO
	if err := common.Unmarshal(value, &cached); err != nil || cached.ClientId != clientId || cached.Status != common.UserStatusEnabled {
		_ = invalidateOAuthAppCache(clientId)
		return nil, false
	}
	return cached.toModel(), true
}

func writeOAuthAppCache(ctx context.Context, app *OAuthApp) {
	if !common.RedisEnabled || common.RDB == nil || app == nil || app.ClientId == "" {
		return
	}
	value, err := common.Marshal(oauthAppCacheDTOFromModel(app))
	if err != nil {
		return
	}
	ttl := oauthAppCacheTTL + time.Duration(app.Id%60)*time.Second
	cacheCtx, cancel := context.WithTimeout(ctx, oauthCacheTimeout)
	_ = common.RDB.Set(cacheCtx, oauthAppCachePrefix+app.ClientId, value, ttl).Err()
	cancel()
}

func invalidateOAuthAppCache(clientId string) error {
	if !common.RedisEnabled || common.RDB == nil || clientId == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), oauthCacheTimeout)
	err := common.RDB.Del(ctx, oauthAppCachePrefix+clientId).Err()
	cancel()
	return err
}

func (app *OAuthApp) ValidateClientSecretContext(ctx context.Context, secret string) bool {
	if app == nil || secret == "" || app.ClientSecretHash == "" {
		return false
	}
	cacheKey := oauthClientAuthCacheKey(app, secret)
	if cachedOAuthClientAuth(ctx, cacheKey) {
		return true
	}
	validated, err := coalesceAuthCacheLoad(
		ctx,
		authCacheLoadNamespaceOAuthSecret,
		cacheKey,
		func() (bool, error) {
			if cachedOAuthClientAuth(context.Background(), cacheKey) {
				return true, nil
			}
			if !common.ValidatePasswordAndHash(secret, app.ClientSecretHash) {
				return false, nil
			}
			cacheOAuthClientAuthSuccess(context.Background(), cacheKey)
			return true, nil
		},
	)
	return err == nil && validated
}

func cachedOAuthClientAuth(ctx context.Context, cacheKey string) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, oauthCacheTimeout)
	cached, err := common.RDB.Get(cacheCtx, cacheKey).Result()
	cancel()
	if err == redis.Nil {
		return false
	}
	return err == nil && cached == "1"
}

func cacheOAuthClientAuthSuccess(ctx context.Context, cacheKey string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, oauthCacheTimeout)
	_ = common.RDB.Set(cacheCtx, cacheKey, "1", oauthClientAuthSuccessTTL).Err()
	cancel()
}

func oauthClientAuthCacheKey(app *OAuthApp, secret string) string {
	hashDigest := sha256.Sum256([]byte(app.ClientSecretHash))
	secretDigest := common.GenerateHMAC(secret)
	return fmt.Sprintf(
		"%s%d:%s:%s",
		oauthClientAuthPrefix,
		app.Id,
		hex.EncodeToString(hashDigest[:]),
		secretDigest,
	)
}

func oauthAppCacheDTOFromModel(app *OAuthApp) oauthAppCacheDTO {
	return oauthAppCacheDTO{
		Id:           app.Id,
		Name:         app.Name,
		Description:  app.Description,
		Logo:         app.Logo,
		ClientId:     app.ClientId,
		ClientType:   app.ClientType,
		RedirectUris: app.RedirectUris,
		Status:       app.Status,
	}
}

func (cached oauthAppCacheDTO) toModel() *OAuthApp {
	return &OAuthApp{
		Id:           cached.Id,
		Name:         cached.Name,
		Description:  cached.Description,
		Logo:         cached.Logo,
		ClientId:     cached.ClientId,
		ClientType:   cached.ClientType,
		RedirectUris: cached.RedirectUris,
		Status:       cached.Status,
	}
}
