package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureVerificationChallengeRoundTrip(t *testing.T) {
	originalSecret := SessionSecret
	SessionSecret = "secure-verification-test-secret"
	t.Cleanup(func() {
		SessionSecret = originalSecret
	})

	token, err := GenerateSecureVerificationChallenge(7, 3, "api_key_security", 200)
	require.NoError(t, err)

	challenge, err := ParseSecureVerificationChallenge(token, 100)
	require.NoError(t, err)
	assert.Equal(t, 7, challenge.UserId)
	assert.Equal(t, int64(3), challenge.SecurityVersion)
	assert.Equal(t, "api_key_security", challenge.Purpose)
	assert.Equal(t, int64(200), challenge.ExpiresAt)
	assert.NotEmpty(t, challenge.Nonce)
}

func TestSecureVerificationChallengeRejectsTamperingAndExpiry(t *testing.T) {
	originalSecret := SessionSecret
	SessionSecret = "secure-verification-test-secret"
	t.Cleanup(func() {
		SessionSecret = originalSecret
	})

	token, err := GenerateSecureVerificationChallenge(7, 3, "api_key_security", 200)
	require.NoError(t, err)

	_, err = ParseSecureVerificationChallenge(token+"tampered", 100)
	assert.Error(t, err)
	_, err = ParseSecureVerificationChallenge(token, 200)
	assert.Error(t, err)
}
