//go:build integration

package model

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type legacyOAuthGrantForMigration struct {
	Id       int    `gorm:"primaryKey"`
	UserId   int    `gorm:"uniqueIndex:idx_oauth_grants_user_client;not null"`
	ClientId string `gorm:"type:varchar(64);uniqueIndex:idx_oauth_grants_user_client;not null"`
	Scopes   string `gorm:"type:varchar(512);not null"`
	Revoked  bool
}

func (legacyOAuthGrantForMigration) TableName() string { return "oauth_grants" }

type legacyOAuthAppForMigration struct {
	Id               int    `gorm:"primaryKey"`
	Name             string `gorm:"type:varchar(128);not null"`
	ClientId         string `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecretHash string `gorm:"column:client_secret_hash;type:varchar(128);not null"`
	RedirectUris     string `gorm:"type:text;not null"`
	UserId           int    `gorm:"index;not null"`
}

func (legacyOAuthAppForMigration) TableName() string { return "oauth_apps" }

type legacyOAuthAuthorizationCodeForMigration struct {
	Id                  int       `gorm:"primaryKey"`
	Code                string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientId            string    `gorm:"type:varchar(64);index;not null"`
	UserId              int       `gorm:"index;not null"`
	RedirectUri         string    `gorm:"type:varchar(512);not null"`
	Scope               string    `gorm:"type:varchar(256)"`
	CodeChallenge       string    `gorm:"type:varchar(128)"`
	CodeChallengeMethod string    `gorm:"type:varchar(16)"`
	ExpiresAt           time.Time `gorm:"not null"`
	Used                bool      `gorm:"default:false"`
	CreatedAt           time.Time
}

func (legacyOAuthAuthorizationCodeForMigration) TableName() string {
	return "oauth_authorization_codes"
}

func migrateOAuthIntegrationSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&legacyOAuthAppForMigration{},
		&legacyOAuthAuthorizationCodeForMigration{},
		&legacyOAuthGrantForMigration{},
		&OAuthRefreshTokenHistory{},
	))
	require.NoError(t, db.Create(&legacyOAuthGrantForMigration{
		UserId:   999,
		ClientId: "gai_legacy_migration",
		Scopes:   "profile",
	}).Error)
	require.NoError(t, db.Create(&legacyOAuthAppForMigration{
		Name:             "Legacy OAuth App",
		ClientId:         "gai_legacy_app_migration",
		ClientSecretHash: "legacy-hash",
		RedirectUris:     `["https://tool.example.com/callback"]`,
		UserId:           999,
	}).Error)
	require.NoError(t, db.AutoMigrate(&OAuthGrant{}, &OAuthApp{}, &OAuthAuthorizationCode{}))
	var migrated OAuthGrant
	require.NoError(t, db.Where("user_id = ? AND client_id = ?", 999, "gai_legacy_migration").First(&migrated).Error)
	require.Zero(t, migrated.AuthorizationVersion)
	var migratedApp OAuthApp
	require.NoError(t, db.Where("client_id = ?", "gai_legacy_app_migration").First(&migratedApp).Error)
	require.Equal(t, OAuthClientTypeLegacy, migratedApp.ClientType)
	require.True(t, db.Migrator().HasIndex(&OAuthAuthorizationCode{}, "idx_oauth_authorization_codes_expires_at"))
}

func requireConcurrentOAuthGrantUpsert(t *testing.T, userId int, clientId string) {
	t.Helper()
	start := make(chan struct{})
	grants := make(chan *OAuthGrant, 2)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			grant, err := UpsertOAuthGrant(userId, clientId, "profile")
			grants <- grant
			errorsCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(grants)
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	ids := make(map[int]struct{}, 2)
	for grant := range grants {
		require.NotNil(t, grant)
		ids[grant.Id] = struct{}{}
	}
	require.Len(t, ids, 2)
	var count int64
	require.NoError(t, DB.Model(&OAuthGrant{}).
		Where("user_id = ? AND client_id = ?", userId, clientId).
		Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestOAuthPostgreSQLAuthorizationLifecycle(t *testing.T) {
	dsn := os.Getenv("OAUTH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OAUTH_TEST_POSTGRES_DSN is not configured")
	}
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQLDB, err := adminDB.DB()
	require.NoError(t, err)
	require.NoError(t, adminSQLDB.Ping())
	schema := "oauth_test_" + strings.ToLower(strings.ReplaceAll(common.GetUUID(), "-", ""))
	require.NoError(t, adminDB.Exec(fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)).Error)
	t.Cleanup(func() {
		require.NoError(t, adminDB.Exec(fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)).Error)
		require.NoError(t, adminSQLDB.Close())
	})

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	postgresDB, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := postgresDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping())

	previousDB := DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	DB = postgresDB
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, previousLogType)
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
		Code:        "postgres-oauth-code",
		ClientId:    "gai_postgres",
		UserId:      71,
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
			"postgres-refresh-0",
			time.Now().Add(time.Hour),
		)
		return consumeErr
	}))
	require.Equal(t, int64(1), grant.AuthorizationVersion)
	consumedAgain, err := ConsumeOAuthAuthorizationCode(code.Code)
	require.NoError(t, err)
	require.False(t, consumedAgain)

	require.NoError(t, RotateOAuthGrantRefreshTokenCAS(
		grant,
		"postgres-refresh-0",
		"postgres-refresh-1",
		time.Now().Add(time.Hour),
	))
	require.NoError(t, DB.Model(&OAuthRefreshTokenHistory{}).
		Where("token_hash = ?", HashOAuthRefreshToken("postgres-refresh-0")).
		Update("rotated_at", time.Now().Add(-2*time.Minute)).Error)
	revoked, err := RevokeOAuthGrantByReplayedRefreshToken(grant.ClientId, "postgres-refresh-0")
	require.NoError(t, err)
	require.True(t, revoked)
	reauthorized, err := UpsertOAuthGrant(grant.UserId, grant.ClientId, "profile")
	require.NoError(t, err)
	require.NotEqual(t, grant.Id, reauthorized.Id)
	require.Greater(t, reauthorized.AuthorizationVersion, grant.AuthorizationVersion)
	requireConcurrentOAuthGrantUpsert(t, 73, "gai_postgres_concurrent")
}
