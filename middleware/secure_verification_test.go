package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureVerificationChallengesKeepIndependentPurposes(t *testing.T) {
	originalSecret := common.SessionSecret
	common.SessionSecret = "secure-verification-middleware-test-secret"
	t.Cleanup(func() {
		common.SessionSecret = originalSecret
	})

	createChallenge := func(purpose string) string {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Set("id", 7)
		context.Set("security_version", int64(3))

		abortForSecureVerification(context, purpose, "verification required", "VERIFICATION_REQUIRED")
		require.Equal(t, 403, recorder.Code)

		response := struct {
			Challenge string `json:"verification_challenge"`
		}{}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.NotEmpty(t, response.Challenge)
		return response.Challenge
	}

	apiKeyToken := createChallenge(common.SecureVerificationPurposeAPIKey)
	genericToken := createChallenge("")

	apiKeyChallenge, err := common.ParseSecureVerificationChallenge(apiKeyToken, time.Now().Unix())
	require.NoError(t, err)
	genericChallenge, err := common.ParseSecureVerificationChallenge(genericToken, time.Now().Unix())
	require.NoError(t, err)

	assert.Equal(t, common.SecureVerificationPurposeAPIKey, apiKeyChallenge.Purpose)
	assert.Empty(t, genericChallenge.Purpose)
	assert.NotEqual(t, apiKeyChallenge.Nonce, genericChallenge.Nonce)
}
