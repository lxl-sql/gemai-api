package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signDelegatedOAuthTestToken(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":           7,
		"client_id":     "gai_test",
		"grant_id":      9,
		"grant_version": 3,
		"aud":           "gai_test",
		"scope":         "profile",
		"iat":           now.Unix(),
		"exp":           now.Add(time.Hour).Unix(),
		"iss":           common.OAuthTokenIssuerGemaiAPI,
		"jti":           "test-jti",
		"typ":           common.OAuthAccessTokenType,
		"token_use":     common.OAuthTokenUseDelegatedAPI,
	}
	if mutate != nil {
		mutate(claims)
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(common.CryptoSecret))
	require.NoError(t, err)
	return token
}

func TestParseDelegatedOAuthAccessTokenValidatesSecurityClaims(t *testing.T) {
	parsed, err := ParseDelegatedOAuthAccessToken(signDelegatedOAuthTestToken(t, nil))
	require.NoError(t, err)
	assert.Equal(t, 7, parsed.UserID)
	assert.Equal(t, 9, parsed.GrantID)
	assert.Equal(t, int64(3), parsed.GrantVersion)
	assert.True(t, parsed.GrantVersionPresent)

	_, err = ParseDelegatedOAuthAccessToken(signDelegatedOAuthTestToken(t, func(claims jwt.MapClaims) {
		claims["iss"] = "other-issuer"
	}))
	require.Error(t, err)

	_, err = ParseDelegatedOAuthAccessToken(signDelegatedOAuthTestToken(t, func(claims jwt.MapClaims) {
		delete(claims, "jti")
	}))
	require.Error(t, err)

	_, err = ParseDelegatedOAuthAccessToken(signDelegatedOAuthTestToken(t, func(claims jwt.MapClaims) {
		claims["aud"] = "other-client"
	}))
	require.Error(t, err)
}

func TestParseDelegatedOAuthAccessTokenAcceptsLegacyGrantVersionZero(t *testing.T) {
	parsed, err := ParseDelegatedOAuthAccessToken(signDelegatedOAuthTestToken(t, func(claims jwt.MapClaims) {
		delete(claims, "grant_version")
	}))
	require.NoError(t, err)
	assert.Zero(t, parsed.GrantVersion)
	assert.False(t, parsed.GrantVersionPresent)
}

func TestAcceptLegacyOAuthGrantTokensUsesExplicitCompatibilityGate(t *testing.T) {
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "")
	assert.True(t, AcceptLegacyOAuthGrantTokens())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "false")
	assert.False(t, AcceptLegacyOAuthGrantTokens())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "invalid")
	assert.True(t, AcceptLegacyOAuthGrantTokens())
	require.Error(t, ValidateOAuthCompatibilityConfig())
}

func TestValidateOAuthCompatibilityConfigAcceptsBooleanValues(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", value)
			require.NoError(t, ValidateOAuthCompatibilityConfig())
		})
	}
}
