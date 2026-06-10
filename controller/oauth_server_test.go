package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOAuthScopesSupportsDelegatedTokenManage(t *testing.T) {
	scope, err := normalizeOAuthScopes("profile email api.token.manage")
	require.NoError(t, err)
	require.Equal(t, "profile email api.token.manage", scope)

	_, err = normalizeOAuthScopes("profile api")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported scope: api")

	scope, err = normalizeOAuthScopes("")
	require.NoError(t, err)
	require.Equal(t, common.OAuthScopeProfile, scope)
}

func TestAppendOAuthRedirectParamsEscapesState(t *testing.T) {
	redirectUrl, err := appendOAuthRedirectParams("https://tool.example.com/auth/callback?existing=1", map[string]string{
		"code":  "code-123",
		"state": "a=b&evil=1",
	})

	require.NoError(t, err)
	require.Equal(t, "https://tool.example.com/auth/callback?code=code-123&existing=1&state=a%3Db%26evil%3D1", redirectUrl)
}

func TestVerifyOAuthCodeVerifierS256(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	require.NoError(t, validateOAuthCodeChallenge(challenge, oauthCodeChallengeMethodS256))
	require.True(t, verifyOAuthCodeVerifier(verifier, challenge, oauthCodeChallengeMethodS256))
	require.False(t, verifyOAuthCodeVerifier("wrong-verifier", challenge, oauthCodeChallengeMethodS256))
}

func TestSignOAuthDelegatedAccessTokenIncludesExpectedClaims(t *testing.T) {
	now := time.Now()
	tokenString, expiresIn, err := signOAuthDelegatedAccessToken(
		7,
		"gai_test",
		9,
		"profile api.token.manage",
		now,
	)

	require.NoError(t, err)
	require.Equal(t, common.OAuthDefaultAccessTokenTTL, expiresIn)

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(common.CryptoSecret), nil
	})
	require.NoError(t, err)
	require.True(t, token.Valid)

	claims := token.Claims.(jwt.MapClaims)
	require.Equal(t, float64(7), claims["sub"])
	require.Equal(t, "gai_test", claims["client_id"])
	require.Equal(t, float64(9), claims["grant_id"])
	require.Equal(t, "profile api.token.manage", claims["scope"])
	require.Equal(t, common.OAuthAccessTokenType, claims["typ"])
	require.Equal(t, common.OAuthTokenUseDelegatedAPI, claims["token_use"])
	require.Equal(t, float64(now.Unix()+int64(common.OAuthDefaultAccessTokenTTL)), claims["exp"])
}
