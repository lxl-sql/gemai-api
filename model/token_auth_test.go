package model

import (
	"context"
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
