package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestValidateUserTokenDistinguishesExhaustedQuota(t *testing.T) {
	previousDB := DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}))
	DB = db
	var tokenLookupQueries []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("capture_token_auth_queries", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tokenLookupQueries = append(tokenLookupQueries, tx.Statement.SQL.String())
		}
	}))

	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		initCol()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	const exhaustedKey = "exhaustedtokenkey123456"
	require.NoError(t, db.Create(&Token{
		UserId:      1,
		Key:         exhaustedKey,
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 0,
	}).Error)

	token, err := ValidateUserToken(exhaustedKey)

	require.NotNil(t, token)
	assert.ErrorIs(t, err, ErrTokenExhausted)
	assert.NotErrorIs(t, err, ErrTokenInvalid)
	require.Len(t, tokenLookupQueries, 2)
	assert.Contains(t, tokenLookupQueries[0], "key_hash")
	assert.Contains(t, tokenLookupQueries[1], "key")
	for _, query := range tokenLookupQueries {
		assert.NotContains(t, query, " OR ")
		assert.NotContains(t, query, "ORDER BY")
	}

	tokenLookupQueries = nil
	_, err = ValidateUserToken("unknowntokenkey123456")
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.NotErrorIs(t, err, ErrTokenExhausted)
	require.Len(t, tokenLookupQueries, 2)

	const hashedKey = "hashedtokenkey123456789"
	hashedKeyFingerprint := common.GenerateHMAC(hashedKey)
	require.NoError(t, db.Create(&Token{
		UserId:      2,
		KeyHash:     &hashedKeyFingerprint,
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}).Error)

	tokenLookupQueries = nil
	hashedToken, err := GetTokenByKey(hashedKey, true)
	require.NoError(t, err)
	assert.Equal(t, 2, hashedToken.UserId)
	assert.Equal(t, hashedKey, hashedToken.GetFullKey())
	require.Len(t, tokenLookupQueries, 1)
	assert.Contains(t, tokenLookupQueries[0], "key_hash")
	assert.NotContains(t, tokenLookupQueries[0], " OR ")
	assert.NotContains(t, tokenLookupQueries[0], "ORDER BY")
}

func TestBackfillTokenKeyMetadataBatchIsBounded(t *testing.T) {
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	for i := 0; i < 3; i++ {
		require.NoError(t, db.Create(&Token{
			UserId: 1,
			Key:    fmt.Sprintf("bounded-backfill-key-%d", i),
			Status: common.TokenStatusEnabled,
		}).Error)
	}

	processed, err := BackfillTokenKeyMetadataBatch(context.Background(), 2)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	var remaining int64
	require.NoError(t, db.Model(&Token{}).Where("key_hash IS NULL").Count(&remaining).Error)
	assert.Equal(t, int64(1), remaining)
}

func TestTokenEffectiveStatusUsesExpirationAndQuota(t *testing.T) {
	now := int64(1_000)
	tests := []struct {
		name  string
		token Token
		want  int
	}{
		{
			name:  "enabled",
			token: Token{Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
			want:  common.TokenStatusEnabled,
		},
		{
			name:  "expired while stored status remains enabled",
			token: Token{Status: common.TokenStatusEnabled, ExpiredTime: now - 1, UnlimitedQuota: true},
			want:  common.TokenStatusExpired,
		},
		{
			name:  "exhausted while stored status remains enabled",
			token: Token{Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 0},
			want:  common.TokenStatusExhausted,
		},
		{
			name:  "explicitly disabled takes precedence",
			token: Token{Status: common.TokenStatusDisabled, ExpiredTime: now - 1, RemainQuota: 0},
			want:  common.TokenStatusDisabled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.token.EffectiveStatus(now))
		})
	}
}

func TestCommittedTokenCacheSynchronizationDistinguishesRevocationFromActivation(t *testing.T) {
	deleteErr := errors.New("cache delete failed")

	assert.NoError(t, committedTokenCacheSynchronizationResult("delete", deleteErr, nil, true))

	err := committedTokenCacheSynchronizationResult("enable", deleteErr, nil, false)
	assert.Error(t, err)
	assert.True(t, TokenMutationCommitted(err))
	assert.ErrorIs(t, err, deleteErr)

	disableErr := errors.New("cache disable failed")
	err = committedTokenCacheSynchronizationResult("delete", deleteErr, disableErr, true)
	assert.Error(t, err)
	assert.True(t, TokenMutationCommitted(err))
	assert.ErrorIs(t, err, deleteErr)
	assert.ErrorIs(t, err, disableErr)
}

func TestInsertWithSecurityPolicyLimitRejectsSecondToken(t *testing.T) {
	previousDB := DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
	DB = db
	require.NoError(t, db.Create(&User{Id: 1, Username: "token-limit-user"}).Error)
	t.Cleanup(func() {
		DB = previousDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		initCol()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	first := &Token{
		UserId:         1,
		Key:            "limited-token-key-1",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, first.InsertWithSecurityPolicyLimit(nil, 1))

	second := &Token{
		UserId:         1,
		Key:            "limited-token-key-2",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.ErrorIs(t, second.InsertWithSecurityPolicyLimit(nil, 1), ErrTokenLimitExceeded)

	var count int64
	require.NoError(t, db.Model(&Token{}).Where("user_id = ?", 1).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
