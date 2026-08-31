package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

func TestTokenAuthMissingUserIsInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	tests := []struct {
		name       string
		middleware gin.HandlerFunc
		openAI     bool
	}{
		{
			name:       "relay token auth",
			middleware: TokenAuth(),
			openAI:     true,
		},
		{
			name:       "read-only token auth",
			middleware: TokenAuthReadOnly(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previousDB := model.DB
			previousRedisEnabled := common.RedisEnabled
			previousMainDatabaseType := common.MainDatabaseType()
			previousLogDatabaseType := common.LogDatabaseType()

			common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
			common.RedisEnabled = false
			dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(&model.Token{}, &model.User{}))
			model.DB = db
			t.Cleanup(func() {
				model.DB = previousDB
				common.RedisEnabled = previousRedisEnabled
				common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
				sqlDB, dbErr := db.DB()
				if dbErr == nil {
					_ = sqlDB.Close()
				}
			})

			const tokenKey = "orphanusertoken123456"
			keyHash := common.GenerateHMAC(tokenKey)
			require.NoError(t, db.Create(&model.Token{
				UserId:      424242,
				KeyHash:     &keyHash,
				Name:        "orphan-user-token",
				Status:      common.TokenStatusEnabled,
				ExpiredTime: -1,
				RemainQuota: 100,
				Group:       "default",
			}).Error)

			request := func() (int, string) {
				recorder := httptest.NewRecorder()
				router := gin.New()
				router.GET("/", test.middleware, func(c *gin.Context) {
					c.Status(http.StatusNoContent)
				})
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer sk-"+tokenKey)
				req.Header.Set("Accept-Language", "en")
				router.ServeHTTP(recorder, req)

				var response struct {
					Message string `json:"message"`
					Error   struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				require.NoError(t, common.DecodeJson(recorder.Body, &response))
				if test.openAI {
					return recorder.Code, response.Error.Message
				}
				return recorder.Code, response.Message
			}

			status, message := request()
			assert.Equal(t, http.StatusUnauthorized, status)
			assert.Contains(t, message, "Invalid token")

			require.NoError(t, db.Migrator().DropTable(&model.User{}))
			status, message = request()
			assert.Equal(t, http.StatusInternalServerError, status)
			assert.Contains(t, message, "Database error")
		})
	}
}
