package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

	activeGrant, err := GetActiveOAuthGrant(grant.Id, 1, "gai_test")
	require.NoError(t, err)
	require.Equal(t, "profile api.token.manage", activeGrant.Scopes)

	require.NoError(t, RevokeOAuthGrantForUser(grant.Id, 1))
	_, err = GetActiveOAuthGrant(grant.Id, 1, "gai_test")
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
	require.Equal(t, grant.Id, updated.Id)
	require.False(t, updated.Revoked)
	require.Nil(t, updated.RevokedAt)
	require.Equal(t, "profile api.token.manage", updated.Scopes)

	var count int64
	require.NoError(t, DB.Model(&OAuthGrant{}).Where("user_id = ? AND client_id = ?", 1, "gai_test").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestOAuthGrantRefreshTokenRotation(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&OAuthGrant{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM oauth_grants")
	})

	grant, err := UpsertOAuthGrant(1, "gai_test", "profile api.token.manage")
	require.NoError(t, err)

	refreshToken := "refresh-token-old"
	require.NoError(t, SaveOAuthGrantRefreshToken(grant, refreshToken, time.Now().Add(time.Hour)))
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

	expiredGrant, err := UpsertOAuthGrant(2, "gai_test", "profile")
	require.NoError(t, err)
	require.NoError(t, SaveOAuthGrantRefreshToken(expiredGrant, "expired-refresh", time.Now().Add(-time.Minute)))

	_, err = RotateOAuthGrantRefreshToken(
		expiredGrant.Id,
		"gai_test",
		"expired-refresh",
		"refresh-token-after-expired",
		time.Now().Add(time.Hour),
	)
	require.Error(t, err)
}
