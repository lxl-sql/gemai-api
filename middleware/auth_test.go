package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbortTokenAuthError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
		wantCode    types.ErrorCode
	}{
		{
			name:        "exhausted token",
			err:         model.ErrTokenExhausted,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This token quota is exhausted",
			wantCode:    types.ErrorCodeInsufficientTokenQuota,
		},
		{
			name:        "invalid token",
			err:         model.ErrTokenInvalid,
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "Invalid token",
		},
		{
			name:        "database failure",
			err:         fmt.Errorf("%w: unavailable", model.ErrDatabase),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Database error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			context.Request.Header.Set("Accept-Language", "en")
			abortTokenAuthError(context, test.err)

			var response struct {
				Error struct {
					Message string          `json:"message"`
					Code    types.ErrorCode `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, common.DecodeJson(recorder.Body, &response))
			assert.Equal(t, test.wantStatus, recorder.Code)
			assert.Contains(t, response.Error.Message, test.wantMessage)
			assert.Equal(t, test.wantCode, response.Error.Code)
			assert.True(t, context.IsAborted())
		})
	}
}
