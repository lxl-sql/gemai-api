package controller

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTokenUsageSourceControllerSettings(t *testing.T, values map[string]string) {
	t.Helper()
	settings := config.GlobalConfig.Get("token_usage_source_setting")
	original, err := config.ConfigToMap(settings)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settings, values))
	t.Cleanup(func() {
		_ = config.UpdateConfigFromMap(settings, original)
	})
}

func TestGetTokenUsageSourcesReturnsOnlyOwnedTokenSummary(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.TokenUsageSource{},
		&model.TokenUsageSourceMeta{},
		&model.LogStatRollupState{},
	))
	token := seedToken(t, db, 81, "source-token", "source-token-key")
	require.NoError(t, db.Create(&model.TokenUsageSourceMeta{
		TokenID:         token.Id,
		UserID:          81,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, db.Create(&model.TokenUsageSource{
		UserID:        81,
		TokenID:       token.Id,
		SourceKey:     model.NewTokenUsageSourceKey("192.0.2.81", "client/1.0"),
		IP:            "192.0.2.81",
		UserAgent:     "client/1.0",
		FirstSeenAt:   110,
		LastSeenAt:    120,
		LastSuccessAt: 120,
	}).Error)
	require.NoError(t, model.SaveLogStatRollupState(context.Background(), &model.LogStatRollupState{
		Name:           model.TokenUsageSourceStateName,
		CoverageStart:  100,
		Watermark:      120,
		BackfillCursor: 110,
	}))
	setTokenUsageSourceControllerSettings(t, map[string]string{
		"enabled":          "true",
		"backfill_enabled": "false",
	})

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodGet,
		"/api/token/"+strconv.Itoa(token.Id)+"/usage-sources",
		nil,
		81,
	)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	GetTokenUsageSources(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var page struct {
		Items       []model.TokenUsageSource `json:"items"`
		Total       int64                    `json:"total"`
		Backfilling bool                     `json:"backfilling"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, "192.0.2.81", page.Items[0].IP)
	assert.Equal(t, "client/1.0", page.Items[0].UserAgent)
	assert.False(t, page.Backfilling)
}

func TestStatusPublishesTokenUsageSourceCapabilityFromSystemSetting(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		enabled  string
		expected bool
	}{
		{name: "enabled", enabled: "true", expected: true},
		{name: "disabled", enabled: "false", expected: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setTokenUsageSourceControllerSettings(t, map[string]string{
				"enabled": testCase.enabled,
			})
			ctx, recorder := newAuthenticatedContext(
				t,
				http.MethodGet,
				"/api/status",
				nil,
				81,
			)

			GetStatus(ctx)

			response := decodeAPIResponse(t, recorder)
			require.True(t, response.Success, response.Message)
			var status struct {
				TokenUsageSourceEnabled bool `json:"token_usage_source_enabled"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &status))
			assert.Equal(t, testCase.expected, status.TokenUsageSourceEnabled)
		})
	}
}
