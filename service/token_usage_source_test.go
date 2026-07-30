package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTokenUsageSourceServiceSettings(t *testing.T, values map[string]string) {
	t.Helper()
	settings := config.GlobalConfig.Get("token_usage_source_setting")
	original, err := config.ConfigToMap(settings)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settings, values))
	t.Cleanup(func() {
		_ = config.UpdateConfigFromMap(settings, original)
	})
}

func TestTokenUsageSourceRollupRepairsRecentRangeAndAdvancesWatermark(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(
		&model.TokenUsageSource{},
		&model.TokenUsageSourceMeta{},
	))
	require.NoError(t, model.DB.Exec("DELETE FROM token_usage_sources").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM token_usage_source_meta").Error)
	t.Cleanup(func() {
		_ = model.DB.Exec("DELETE FROM token_usage_sources").Error
		_ = model.DB.Exec("DELETE FROM token_usage_source_meta").Error
	})
	setTokenUsageSourceServiceSettings(t, map[string]string{
		"enabled":          "true",
		"backfill_enabled": "false",
	})

	const tokenID = 95001
	const userID = 96001
	const targetEnd = int64(28_666_667) * 60
	require.Zero(t, targetEnd%60)
	require.NoError(t, model.DB.Create(&model.TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   targetEnd - 600,
	}).Error)
	require.NoError(t, model.SaveLogStatRollupState(context.Background(), &model.LogStatRollupState{
		Name:           model.TokenUsageSourceStateName,
		CoverageStart:  targetEnd - 600,
		Watermark:      targetEnd - 60,
		BackfillCursor: targetEnd - 60,
	}))
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId:    userID,
		TokenId:   tokenID,
		CreatedAt: targetEnd - 30,
		Type:      model.LogTypeConsume,
		Ip:        "198.51.100.95",
		UserAgent: "client/1.0",
	}).Error)

	const runnerID = "token-source-runner"
	task := claimLogStatTask(
		t,
		model.SystemTaskTypeTokenUsageSourceRollup,
		tokenUsageSourceRollupPayload{TargetEnd: targetEnd},
		runnerID,
	)
	runTokenUsageSourceRollupTask(context.Background(), task, runnerID)

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	state, err := model.GetLogStatRollupState(context.Background(), model.TokenUsageSourceStateName)
	require.NoError(t, err)
	assert.Equal(t, targetEnd, state.Watermark)
	var sources []model.TokenUsageSource
	require.NoError(t, model.DB.Where("token_id = ?", tokenID).Find(&sources).Error)
	require.Len(t, sources, 1)
	assert.Equal(t, "198.51.100.95", sources[0].IP)
}

func TestTokenUsageSourceChunkShrinksToMinimum(t *testing.T) {
	current := int64(300)
	expected := []int64{150, 75, 37, 18, 10}
	for _, nextExpected := range expected {
		next, ok := smallerTokenUsageSourceChunk(current)
		require.True(t, ok)
		assert.Equal(t, nextExpected, next)
		current = next
	}
	next, ok := smallerTokenUsageSourceChunk(current)
	assert.False(t, ok)
	assert.Equal(t, tokenUsageSourceMinChunk, next)
}

func TestAggregateTokenUsageSourceChunkClassifiesDeadline(t *testing.T) {
	truncate(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, _, err := aggregateTokenUsageSourceChunk(ctx, 100, 160)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errTokenUsageSourceChunkTimeout))
}

func TestTokenUsageSourceReconcileCanRunBeforeRollup(t *testing.T) {
	setTokenUsageSourceServiceSettings(t, map[string]string{
		"enabled":           "false",
		"reconcile_enabled": "true",
	})
	assert.True(t, tokenUsageSourceReconcileEnabled())
}
