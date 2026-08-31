package model

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthAppCacheDTOExcludesClientSecretMaterial(t *testing.T) {
	app := &OAuthApp{
		Id:               7,
		ClientId:         "gai_cache_test",
		ClientSecretHash: "sensitive-client-secret-hash",
		Status:           common.UserStatusEnabled,
	}
	encoded, err := common.Marshal(oauthAppCacheDTOFromModel(app))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), app.ClientSecretHash)
}

func TestOAuthClientAuthCacheKeyDoesNotContainPresentedSecret(t *testing.T) {
	app := &OAuthApp{Id: 8, ClientSecretHash: "stored-hash"}
	presentedSecret := "presented-client-secret"
	key := oauthClientAuthCacheKey(app, presentedSecret)
	assert.True(t, strings.HasPrefix(key, oauthClientAuthPrefix))
	assert.NotContains(t, key, presentedSecret)
	assert.NotContains(t, key, app.ClientSecretHash)
}

func TestOAuthClientAuthCacheKeyChangesAfterSecretReset(t *testing.T) {
	redisURL := os.Getenv("RATE_LIMIT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("RATE_LIMIT_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	previousRDB := common.RDB
	previousRedisEnabled := common.RedisEnabled
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = previousRDB
		common.RedisEnabled = previousRedisEnabled
		_ = client.Close()
	})

	oldSecret := "old-oauth-client-secret"
	newSecret := "new-oauth-client-secret"
	oldHash, err := common.Password2Hash(oldSecret)
	require.NoError(t, err)
	newHash, err := common.Password2Hash(newSecret)
	require.NoError(t, err)
	app := &OAuthApp{Id: 81, ClientSecretHash: oldHash}
	oldKey := oauthClientAuthCacheKey(app, oldSecret)
	t.Cleanup(func() {
		_ = client.Del(context.Background(), oldKey, oauthClientAuthCacheKey(&OAuthApp{Id: app.Id, ClientSecretHash: newHash}, newSecret)).Err()
	})

	assert.True(t, app.ValidateClientSecret(oldSecret))
	app.ClientSecretHash = newHash
	assert.False(t, app.ValidateClientSecret(oldSecret))
	assert.True(t, app.ValidateClientSecret(newSecret))
}
