package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newOAuthTokenExchangeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.OAuthApp{},
		&model.OAuthAuthorizationCode{},
		&model.OAuthGrant{},
	))
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	return db
}

func createOAuthTokenExchangeFixture(t *testing.T, db *gorm.DB, userStatus int, code string) *model.OAuthAuthorizationCode {
	t.Helper()
	user := &model.User{
		Username:    "oauth-exchange-" + code,
		DisplayName: "OAuth Exchange",
		Role:        common.RoleCommonUser,
		Status:      userStatus,
		Group:       "default",
	}
	require.NoError(t, db.Create(user).Error)
	application := &model.OAuthApp{
		Name:         "OAuth Exchange Test",
		ClientId:     "gai_exchange_test",
		ClientType:   model.OAuthClientTypePublic,
		RedirectUris: `["https://tool.example.com/callback"]`,
		UserId:       user.Id,
		Status:       common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(application).Error)
	authorizationCode := &model.OAuthAuthorizationCode{
		Code:        code,
		ClientId:    "gai_exchange_test",
		UserId:      user.Id,
		RedirectUri: "https://tool.example.com/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, db.Create(authorizationCode).Error)
	return authorizationCode
}

func requireOAuthExchangeRolledBack(t *testing.T, code string) {
	t.Helper()
	stored, err := model.GetOAuthAuthorizationCode(code)
	require.NoError(t, err)
	require.False(t, stored.Used)
	var grantCount int64
	require.NoError(t, model.DB.Model(&model.OAuthGrant{}).Count(&grantCount).Error)
	require.Zero(t, grantCount)
}

func TestExchangeOAuthAuthorizationCodeRollsBackWhenUserIsDisabled(t *testing.T) {
	db := newOAuthTokenExchangeTestDB(t)
	authorizationCode := createOAuthTokenExchangeFixture(t, db, common.UserStatusDisabled, "disabled-user-code")

	_, err := ExchangeOAuthAuthorizationCode(
		context.Background(),
		authorizationCode.Code,
		authorizationCode.ClientId,
		authorizationCode.RedirectUri,
		authorizationCode.CodeChallenge,
		authorizationCode.CodeChallengeMethod,
		time.Now(),
	)
	require.ErrorIs(t, err, ErrOAuthTokenUserUnavailable)
	requireOAuthExchangeRolledBack(t, authorizationCode.Code)
}

func TestExchangeOAuthAuthorizationCodeRollsBackWhenClientIsDisabled(t *testing.T) {
	db := newOAuthTokenExchangeTestDB(t)
	authorizationCode := createOAuthTokenExchangeFixture(t, db, common.UserStatusEnabled, "disabled-client-code")
	require.NoError(t, db.Model(&model.OAuthApp{}).
		Where("client_id = ?", authorizationCode.ClientId).
		Update("status", common.UserStatusDisabled).Error)

	_, err := ExchangeOAuthAuthorizationCode(
		context.Background(),
		authorizationCode.Code,
		authorizationCode.ClientId,
		authorizationCode.RedirectUri,
		authorizationCode.CodeChallenge,
		authorizationCode.CodeChallengeMethod,
		time.Now(),
	)
	require.ErrorIs(t, err, ErrOAuthTokenClientUnavailable)
	requireOAuthExchangeRolledBack(t, authorizationCode.Code)
}

func TestExchangeOAuthAuthorizationCodeRollsBackWhenGrantInsertFails(t *testing.T) {
	db := newOAuthTokenExchangeTestDB(t)
	authorizationCode := createOAuthTokenExchangeFixture(t, db, common.UserStatusEnabled, "grant-write-failure-code")
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_oauth_grant_insert
		BEFORE INSERT ON oauth_grants
		BEGIN
			SELECT RAISE(FAIL, 'forced oauth grant insert failure');
		END
	`).Error)

	_, err := ExchangeOAuthAuthorizationCode(
		context.Background(),
		authorizationCode.Code,
		authorizationCode.ClientId,
		authorizationCode.RedirectUri,
		authorizationCode.CodeChallenge,
		authorizationCode.CodeChallengeMethod,
		time.Now(),
	)
	require.Error(t, err)
	requireOAuthExchangeRolledBack(t, authorizationCode.Code)
}

func TestExchangeOAuthAuthorizationCodeRejectsChangedPKCEFacts(t *testing.T) {
	db := newOAuthTokenExchangeTestDB(t)
	authorizationCode := createOAuthTokenExchangeFixture(t, db, common.UserStatusEnabled, "changed-pkce-code")
	authorizationCode.CodeChallenge = "stored-challenge"
	authorizationCode.CodeChallengeMethod = "S256"
	require.NoError(t, db.Save(authorizationCode).Error)

	_, err := ExchangeOAuthAuthorizationCode(
		context.Background(),
		authorizationCode.Code,
		authorizationCode.ClientId,
		authorizationCode.RedirectUri,
		"previously-validated-challenge",
		authorizationCode.CodeChallengeMethod,
		time.Now(),
	)
	require.ErrorIs(t, err, model.ErrOAuthAuthorizationCodeInvalid)
	requireOAuthExchangeRolledBack(t, authorizationCode.Code)
}

func TestExchangeOAuthAuthorizationCodeChecksExpiryAtTransactionTime(t *testing.T) {
	db := newOAuthTokenExchangeTestDB(t)
	authorizationCode := createOAuthTokenExchangeFixture(t, db, common.UserStatusEnabled, "expired-transaction-code")
	authorizationCode.ExpiresAt = time.Now().Add(-time.Second)
	require.NoError(t, db.Save(authorizationCode).Error)

	_, err := ExchangeOAuthAuthorizationCode(
		context.Background(),
		authorizationCode.Code,
		authorizationCode.ClientId,
		authorizationCode.RedirectUri,
		authorizationCode.CodeChallenge,
		authorizationCode.CodeChallengeMethod,
		time.Now().Add(-time.Minute),
	)
	require.ErrorIs(t, err, model.ErrOAuthAuthorizationCodeInvalid)
	requireOAuthExchangeRolledBack(t, authorizationCode.Code)
}
