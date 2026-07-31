package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenUsageSourceRecordsOneExplicitTerminalOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open("file:middleware_token_usage_source?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.TokenUsageSource{},
		&model.TokenUsageSourceMeta{},
	))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = model.StopTokenUsageSourceBatchUpdater(stopCtx)
		cancel()
		model.DB = previousDB
	})

	settings := config.GlobalConfig.Get("token_usage_source_setting")
	originalSettings, err := config.ConfigToMap(settings)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settings, map[string]string{
		"enabled": "true",
	}))
	t.Cleanup(func() {
		_ = config.UpdateConfigFromMap(settings, originalSettings)
	})

	const tokenID = 98101
	const userID = 98201
	require.NoError(t, db.Create(&model.TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   common.GetTimestamp() - 10,
	}).Error)

	engine := gin.New()
	engine.Use(TokenUsageSource())
	engine.GET("/", func(c *gin.Context) {
		c.Set("id", userID)
		c.Set("token_id", tokenID)
		SetTokenUsageSourceOutcome(c, false)
		c.Status(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.90:1234"
	request.Header.Set("User-Agent", "client/1.0")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)
	require.NoError(t, model.FlushTokenUsageSourceBatch(context.Background()))

	var source model.TokenUsageSource
	require.NoError(t, db.First(&source, "token_id = ?", tokenID).Error)
	assert.Equal(t, "192.0.2.90", source.IP)
	assert.Zero(t, source.SuccessCount)
	assert.Equal(t, int64(1), source.ErrorCount)
}

func TestTokenUsageSourceOccurredAtMatchesTerminalOutcomeLog(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyTokenUsageSourceSuccessAt, int64(101))
	common.SetContextKey(c, constant.ContextKeyTokenUsageSourceErrorAt, int64(202))

	assert.Equal(t, int64(101), tokenUsageSourceOccurredAt(c, true, 303))
	assert.Equal(t, int64(202), tokenUsageSourceOccurredAt(c, false, 303))

	empty, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, int64(303), tokenUsageSourceOccurredAt(empty, true, 303))
	assert.Equal(t, int64(303), tokenUsageSourceOccurredAt(empty, false, 303))
}
