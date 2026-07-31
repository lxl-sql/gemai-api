package model

import (
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

	_, err = ValidateUserToken("unknowntokenkey123456")
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.NotErrorIs(t, err, ErrTokenExhausted)
}
