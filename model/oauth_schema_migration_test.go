package model

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitOAuthSchemaMigrationDoesNotMigrateUnrelatedModels(t *testing.T) {
	previousDB := DB
	previousSQLitePath := common.SQLitePath
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	common.SQLitePath = filepath.Join(t.TempDir(), "oauth-migration.db")
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "")
	t.Cleanup(func() {
		if DB != nil && DB != previousDB {
			if sqlDB, err := DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		DB = previousDB
		common.SQLitePath = previousSQLitePath
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})

	require.NoError(t, InitOAuthSchemaMigration())
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	assert.Equal(t, 1, sqlDB.Stats().MaxOpenConnections)
	assert.True(t, DB.Migrator().HasTable(&OAuthApp{}))
	assert.True(t, DB.Migrator().HasTable(&OAuthAuthorizationCode{}))
	assert.True(t, DB.Migrator().HasIndex(&OAuthAuthorizationCode{}, "idx_oauth_authorization_codes_expires_at"))
	assert.True(t, DB.Migrator().HasTable(&OAuthGrant{}))
	assert.True(t, DB.Migrator().HasTable(&OAuthRefreshTokenHistory{}))
	assert.False(t, DB.Migrator().HasTable(&User{}))
	assert.False(t, DB.Migrator().HasTable(&Redemption{}))
}
