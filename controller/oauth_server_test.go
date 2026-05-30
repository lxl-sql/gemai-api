package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
