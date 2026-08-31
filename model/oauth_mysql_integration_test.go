//go:build integration

package model

import (
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestOAuthMySQLAuthorizationLifecycle(t *testing.T) {
	dsn := os.Getenv("OAUTH_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("OAUTH_TEST_MYSQL_DSN is not configured")
	}
	mysqlDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := mysqlDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping())
	previousDB := DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	DB = mysqlDB
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, previousLogType)
	t.Cleanup(func() {
		DB = previousDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, sqlDB.Close())
	})
	migrateOAuthIntegrationSchema(t, DB)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var application OAuthApp
		return tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("client_id = ? AND status = ?", "gai_legacy_app_migration", common.UserStatusEnabled).
			First(&application).Error
	}))

	code := &OAuthAuthorizationCode{
		Code:        "mysql-oauth-code",
		ClientId:    "gai_mysql",
		UserId:      72,
		RedirectUri: "https://tool.example.com/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))
	var grant *OAuthGrant
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		consumed, consumeErr := ConsumeOAuthAuthorizationCodeTx(tx, code.Code)
		if consumeErr != nil {
			return consumeErr
		}
		if !consumed {
			return ErrOAuthAuthorizationCodeInvalid
		}
		grant, consumeErr = UpsertOAuthGrantWithRefreshTokenTx(
			tx,
			code.UserId,
			code.ClientId,
			code.Scope,
			"mysql-refresh-0",
			time.Now().Add(time.Hour),
		)
		return consumeErr
	}))
	require.Equal(t, int64(1), grant.AuthorizationVersion)
	require.NoError(t, RotateOAuthGrantRefreshTokenCAS(
		grant,
		"mysql-refresh-0",
		"mysql-refresh-1",
		time.Now().Add(time.Hour),
	))
	require.NoError(t, DB.Model(&OAuthRefreshTokenHistory{}).
		Where("token_hash = ?", HashOAuthRefreshToken("mysql-refresh-0")).
		Update("rotated_at", time.Now().Add(-2*time.Minute)).Error)
	revoked, err := RevokeOAuthGrantByReplayedRefreshToken(grant.ClientId, "mysql-refresh-0")
	require.NoError(t, err)
	require.True(t, revoked)
	reauthorized, err := UpsertOAuthGrant(grant.UserId, grant.ClientId, "profile")
	require.NoError(t, err)
	require.NotEqual(t, grant.Id, reauthorized.Id)
	require.Greater(t, reauthorized.AuthorizationVersion, grant.AuthorizationVersion)
	requireConcurrentOAuthGrantUpsert(t, 74, "gai_mysql_concurrent")
}
