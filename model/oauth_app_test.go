package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type sqliteLegacyOAuthGrantForMigration struct {
	Id       int    `gorm:"primaryKey"`
	UserId   int    `gorm:"uniqueIndex:idx_oauth_grants_user_client;not null"`
	ClientId string `gorm:"type:varchar(64);uniqueIndex:idx_oauth_grants_user_client;not null"`
	Scopes   string `gorm:"type:varchar(512);not null"`
}

func (sqliteLegacyOAuthGrantForMigration) TableName() string { return "oauth_grants" }

type sqliteLegacyOAuthAppForMigration struct {
	Id               int    `gorm:"primaryKey"`
	Name             string `gorm:"type:varchar(128);not null"`
	ClientId         string `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecretHash string `gorm:"column:client_secret_hash;type:varchar(128);not null"`
	RedirectUris     string `gorm:"type:text;not null"`
	UserId           int    `gorm:"index;not null"`
}

func (sqliteLegacyOAuthAppForMigration) TableName() string { return "oauth_apps" }

func TestValidateOAuthRedirectUrisAllowsHttpsAndLoopbackHttp(t *testing.T) {
	uris, err := ValidateOAuthRedirectUris([]string{
		" https://tool.example.com/auth/callback ",
		"http://localhost:26999/auth/callback",
		"http://127.0.0.1:1455/auth/callback",
	})

	require.NoError(t, err)
	require.Equal(t, []string{
		"https://tool.example.com/auth/callback",
		"http://localhost:26999/auth/callback",
		"http://127.0.0.1:1455/auth/callback",
	}, uris)
}

func TestValidateOAuthRedirectUrisRejectsUnsafeValues(t *testing.T) {
	tests := []string{
		"http://tool.example.com/auth/callback",
		"javascript:alert(1)",
		"https://tool.example.com/auth/callback#token",
		"https://user:pass@tool.example.com/auth/callback",
	}

	for _, uri := range tests {
		t.Run(uri, func(t *testing.T) {
			_, err := ValidateOAuthRedirectUris([]string{uri})
			require.Error(t, err)
		})
	}
}

func TestOAuthClientTypeDefaultsPreserveExistingApps(t *testing.T) {
	clientType, err := NormalizeOAuthClientType("", OAuthClientTypeConfidential)
	require.NoError(t, err)
	require.Equal(t, OAuthClientTypeConfidential, clientType)
	require.Equal(t, OAuthClientTypeLegacy, (&OAuthApp{}).EffectiveClientType())
	require.Equal(t, OAuthClientTypeConfidential, (&OAuthApp{ClientType: "unexpected"}).EffectiveClientType())
	_, err = NormalizeOAuthClientType("unexpected", OAuthClientTypeConfidential)
	require.Error(t, err)
}

func TestOAuthLegacySchemaMigrationSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&sqliteLegacyOAuthGrantForMigration{}, &sqliteLegacyOAuthAppForMigration{}))
	require.NoError(t, db.Create(&sqliteLegacyOAuthGrantForMigration{
		UserId:   901,
		ClientId: "gai_sqlite_legacy_grant",
		Scopes:   "profile",
	}).Error)
	require.NoError(t, db.Create(&sqliteLegacyOAuthAppForMigration{
		Name:             "SQLite Legacy App",
		ClientId:         "gai_sqlite_legacy_app",
		ClientSecretHash: "legacy-hash",
		RedirectUris:     `["https://tool.example.com/callback"]`,
		UserId:           901,
	}).Error)

	require.NoError(t, db.AutoMigrate(&OAuthGrant{}, &OAuthApp{}))
	var grant OAuthGrant
	require.NoError(t, db.Where("client_id = ?", "gai_sqlite_legacy_grant").First(&grant).Error)
	require.Zero(t, grant.AuthorizationVersion)
	var app OAuthApp
	require.NoError(t, db.Where("client_id = ?", "gai_sqlite_legacy_app").First(&app).Error)
	require.Equal(t, OAuthClientTypeLegacy, app.ClientType)
}

func newOAuthGrantDedupeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&OAuthGrant{}))
	require.NoError(t, db.Migrator().DropIndex(&OAuthGrant{}, "idx_oauth_grants_user_client"))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestDedupeOAuthGrantsKeepsActiveRefreshGrant(t *testing.T) {
	db := newOAuthGrantDedupeTestDB(t)
	expiresAt := time.Now().Add(time.Hour)
	active := &OAuthGrant{
		UserId:                910,
		ClientId:              "gai_dedupe_active",
		Scopes:                "profile",
		AuthorizationVersion:  1,
		RefreshTokenHash:      "active-refresh-token",
		RefreshTokenExpiresAt: &expiresAt,
	}
	require.NoError(t, db.Create(active).Error)
	revoked := &OAuthGrant{
		UserId:               active.UserId,
		ClientId:             active.ClientId,
		Scopes:               "profile",
		AuthorizationVersion: 2,
		Revoked:              true,
	}
	require.NoError(t, db.Create(revoked).Error)

	require.NoError(t, dedupeOAuthGrantsForUniqueIndex())
	var grants []OAuthGrant
	require.NoError(t, db.Where("user_id = ? AND client_id = ?", active.UserId, active.ClientId).Find(&grants).Error)
	require.Len(t, grants, 1)
	require.Equal(t, active.Id, grants[0].Id)
}

func TestDedupeOAuthGrantsRollsBackAllPairsOnFailure(t *testing.T) {
	db := newOAuthGrantDedupeTestDB(t)
	for _, userId := range []int{911, 912} {
		for index := 0; index < 2; index++ {
			require.NoError(t, db.Create(&OAuthGrant{
				UserId:               userId,
				ClientId:             "gai_dedupe_rollback",
				Scopes:               "profile",
				AuthorizationVersion: int64(index + 1),
			}).Error)
		}
	}
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_second_oauth_grant_pair_delete
		BEFORE DELETE ON oauth_grants
		WHEN OLD.user_id = 912
		BEGIN
			SELECT RAISE(FAIL, 'forced oauth grant dedupe failure');
		END
	`).Error)

	err := dedupeOAuthGrantsForUniqueIndex()
	require.ErrorContains(t, err, "forced oauth grant dedupe failure")
	var count int64
	require.NoError(t, db.Model(&OAuthGrant{}).Count(&count).Error)
	require.Equal(t, int64(4), count)
}

func TestDeleteOAuthAppPreservesDatabaseError(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthApp{}))
	app := &OAuthApp{
		Name:             "Delete Error App",
		ClientId:         "gai_delete_error_" + common.GetRandomString(8),
		ClientSecretHash: "hash",
		ClientType:       OAuthClientTypeConfidential,
		RedirectUris:     `["https://tool.example.com/callback"]`,
		UserId:           902,
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(app).Error)
	require.NoError(t, DB.Exec(`
		CREATE TRIGGER reject_oauth_app_delete
		BEFORE DELETE ON oauth_apps
		BEGIN
			SELECT RAISE(FAIL, 'forced oauth app delete failure');
		END
	`).Error)
	t.Cleanup(func() {
		_ = DB.Exec("DROP TRIGGER IF EXISTS reject_oauth_app_delete").Error
		_ = DB.Where("id = ?", app.Id).Delete(&OAuthApp{}).Error
	})

	err := DeleteOAuthApp(app.Id, app.UserId)
	require.ErrorContains(t, err, "forced oauth app delete failure")
}

func TestConsumeOAuthAuthorizationCodeOnlyOnce(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthAuthorizationCode{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_authorization_codes")
	})

	code := &OAuthAuthorizationCode{
		Code:        "code-once",
		ClientId:    "gai_test",
		UserId:      1,
		RedirectUri: "https://tool.example.com/auth/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	consumed, err := ConsumeOAuthAuthorizationCode("code-once")
	require.NoError(t, err)
	require.True(t, consumed)

	consumed, err = ConsumeOAuthAuthorizationCode("code-once")
	require.NoError(t, err)
	require.False(t, consumed)
}

func TestOAuthGrantRevocation(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_grants")
	})

	grant, err := UpsertOAuthGrant(1, "gai_test", "profile api.token.manage")
	require.NoError(t, err)
	require.False(t, grant.Revoked)

	activeGrant, err := GetActiveOAuthGrant(grant.Id, 1, "gai_test", grant.AuthorizationVersion)
	require.NoError(t, err)
	require.Equal(t, "profile api.token.manage", activeGrant.Scopes)

	require.NoError(t, RevokeOAuthGrantForUser(grant.Id, 1))
	_, err = GetActiveOAuthGrant(grant.Id, 1, "gai_test", grant.AuthorizationVersion)
	require.Error(t, err)

	grant, err = UpsertOAuthGrant(1, "gai_test", "profile")
	require.NoError(t, err)
	require.False(t, grant.Revoked)
	require.Nil(t, grant.RevokedAt)
	require.Equal(t, "profile", grant.Scopes)
}

func TestUpsertOAuthGrantKeepsUserClientUnique(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_grants")
	})

	grant, err := UpsertOAuthGrant(1, "gai_test", "profile")
	require.NoError(t, err)
	require.NoError(t, RevokeOAuthGrantForUser(grant.Id, 1))

	updated, err := UpsertOAuthGrant(1, "gai_test", "profile api.token.manage")
	require.NoError(t, err)
	require.NotEqual(t, grant.Id, updated.Id)
	require.False(t, updated.Revoked)
	require.Nil(t, updated.RevokedAt)
	require.Equal(t, "profile api.token.manage", updated.Scopes)

	var count int64
	require.NoError(t, DB.Model(&OAuthGrant{}).Where("user_id = ? AND client_id = ?", 1, "gai_test").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestOAuthGrantRefreshTokenRotation(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}, &OAuthRefreshTokenHistory{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_refresh_token_histories")
		DB.Exec("DELETE FROM oauth_grants")
	})

	refreshToken := "refresh-token-old"
	grant, err := UpsertOAuthGrantWithRefreshTokenTx(DB, 1, "gai_test", "profile api.token.manage", refreshToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, grant.RefreshTokenHash)
	require.NotEqual(t, refreshToken, grant.RefreshTokenHash)

	loaded, err := GetActiveOAuthGrantByRefreshToken("gai_test", refreshToken)
	require.NoError(t, err)
	require.Equal(t, grant.Id, loaded.Id)

	nextRefreshToken := "refresh-token-next"
	rotated, err := RotateOAuthGrantRefreshToken(
		grant.Id,
		"gai_test",
		refreshToken,
		nextRefreshToken,
		time.Now().Add(2*time.Hour),
	)
	require.NoError(t, err)
	require.Equal(t, HashOAuthRefreshToken(nextRefreshToken), rotated.RefreshTokenHash)
	require.NotNil(t, rotated.LastRefreshAt)

	_, err = RotateOAuthGrantRefreshToken(
		grant.Id,
		"gai_test",
		refreshToken,
		"refresh-token-reuse",
		time.Now().Add(2*time.Hour),
	)
	require.Error(t, err)

	expiredGrant, err := UpsertOAuthGrantWithRefreshTokenTx(DB, 2, "gai_test", "profile", "expired-refresh", time.Now().Add(-time.Minute))
	require.NoError(t, err)

	_, err = RotateOAuthGrantRefreshToken(
		expiredGrant.Id,
		"gai_test",
		"expired-refresh",
		"refresh-token-after-expired",
		time.Now().Add(time.Hour),
	)
	require.Error(t, err)
}

func TestOAuthGrantReauthorizationInvalidatesOlderAccessTokenGeneration(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}))
	t.Cleanup(func() { DB.Exec("DELETE FROM oauth_grants") })

	first, err := UpsertOAuthGrant(41, "gai_generation", "profile")
	require.NoError(t, err)
	firstVersion := first.AuthorizationVersion
	require.NoError(t, RevokeOAuthGrantForUser(first.Id, first.UserId))

	second, err := UpsertOAuthGrant(first.UserId, first.ClientId, "profile")
	require.NoError(t, err)
	require.NotEqual(t, first.Id, second.Id)
	require.Greater(t, second.AuthorizationVersion, firstVersion)

	_, err = GetActiveOAuthGrant(first.Id, first.UserId, first.ClientId, firstVersion)
	require.Error(t, err)
	_, err = GetActiveOAuthGrant(second.Id, second.UserId, second.ClientId, second.AuthorizationVersion)
	require.NoError(t, err)
}

func TestOAuthAuthorizationExchangeStateRollsBackTogether(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthAuthorizationCode{}, &OAuthGrant{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_authorization_codes")
		DB.Exec("DELETE FROM oauth_grants")
	})
	code := &OAuthAuthorizationCode{
		Code:        "transaction-code",
		ClientId:    "gai_transaction",
		UserId:      43,
		RedirectUri: "https://tool.example.com/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))
	forcedErr := errors.New("forced rollback")
	err := DB.Transaction(func(tx *gorm.DB) error {
		consumed, consumeErr := ConsumeOAuthAuthorizationCodeTx(tx, code.Code)
		require.NoError(t, consumeErr)
		require.True(t, consumed)
		_, consumeErr = UpsertOAuthGrantWithRefreshTokenTx(
			tx,
			code.UserId,
			code.ClientId,
			code.Scope,
			"transaction-refresh",
			time.Now().Add(time.Hour),
		)
		require.NoError(t, consumeErr)
		return forcedErr
	})
	require.ErrorIs(t, err, forcedErr)

	storedCode, err := GetOAuthAuthorizationCode(code.Code)
	require.NoError(t, err)
	require.False(t, storedCode.Used)
	var grantCount int64
	require.NoError(t, DB.Model(&OAuthGrant{}).
		Where("user_id = ? AND client_id = ?", code.UserId, code.ClientId).
		Count(&grantCount).Error)
	require.Zero(t, grantCount)
}

func TestRefreshReplayHistoryRevokesAcrossMultipleGenerations(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}, &OAuthRefreshTokenHistory{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_refresh_token_histories")
		DB.Exec("DELETE FROM oauth_grants")
	})
	grant, err := UpsertOAuthGrantWithRefreshTokenTx(DB, 44, "gai_refresh_history", "profile", "refresh-0", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, RotateOAuthGrantRefreshTokenCAS(grant, "refresh-0", "refresh-1", time.Now().Add(time.Hour)))
	require.NoError(t, RotateOAuthGrantRefreshTokenCAS(grant, "refresh-1", "refresh-2", time.Now().Add(time.Hour)))
	require.NoError(t, DB.Model(&OAuthRefreshTokenHistory{}).
		Where("token_hash = ?", HashOAuthRefreshToken("refresh-0")).
		Update("rotated_at", time.Now().Add(-2*time.Minute)).Error)

	revoked, err := RevokeOAuthGrantByReplayedRefreshToken(grant.ClientId, "refresh-0")
	require.NoError(t, err)
	require.True(t, revoked)
}

func TestDeleteExpiredOAuthAuthorizationCodesBatchIsBounded(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthAuthorizationCode{}))
	t.Cleanup(func() { DB.Exec("DELETE FROM oauth_authorization_codes") })
	now := time.Now()
	for index := 0; index < 3; index++ {
		require.NoError(t, CreateOAuthAuthorizationCode(&OAuthAuthorizationCode{
			Code:        "expired-code-" + string(rune('a'+index)),
			ClientId:    "gai_cleanup",
			UserId:      50 + index,
			RedirectUri: "https://tool.example.com/callback",
			ExpiresAt:   now.Add(-time.Minute),
		}))
	}
	require.NoError(t, CreateOAuthAuthorizationCode(&OAuthAuthorizationCode{
		Code:        "active-code",
		ClientId:    "gai_cleanup",
		UserId:      60,
		RedirectUri: "https://tool.example.com/callback",
		ExpiresAt:   now.Add(time.Minute),
	}))

	deleted, err := DeleteExpiredOAuthAuthorizationCodesBatch(context.Background(), now, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	var remaining int64
	require.NoError(t, DB.Model(&OAuthAuthorizationCode{}).Count(&remaining).Error)
	require.Equal(t, int64(2), remaining)
}
