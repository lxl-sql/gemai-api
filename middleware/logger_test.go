package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeLoggedPathRedactsOAuthQueryParameters(t *testing.T) {
	assert.Equal(
		t,
		"/api/oauth-server/authorize?<redacted>",
		sanitizeLoggedPath("/api/oauth-server/authorize?client_id=gai&state=secret&code_challenge=challenge"),
	)
	assert.Equal(
		t,
		"/api/oauth/provider?<redacted>",
		sanitizeLoggedPath("/api/oauth/provider?code=authorization-code&state=secret"),
	)
	assert.Equal(t, "/api/status?verbose=true", sanitizeLoggedPath("/api/status?verbose=true"))
}

func TestSanitizeLoggedPathRedactsSensitiveQueryParameters(t *testing.T) {
	assert.Equal(
		t,
		"/v1beta/models/gemini:generateContent?alt=sse&key=<redacted>&api_key=<redacted>",
		sanitizeLoggedPath("/v1beta/models/gemini:generateContent?alt=sse&key=gemini-secret&api_key=api-secret"),
	)
	assert.Equal(
		t,
		"/v1/models/gemini:streamGenerateContent?Access_Token=<redacted>&%74oken=<redacted>&prettyPrint=true",
		sanitizeLoggedPath("/v1/models/gemini:streamGenerateContent?Access_Token=access-secret&%74oken=token-secret&prettyPrint=true"),
	)
}
