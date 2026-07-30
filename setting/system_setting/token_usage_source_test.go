package system_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveTokenUsageSourceSettings(t *testing.T) {
	t.Helper()
	originalSettings := tokenUsageSourceSettings
	t.Cleanup(func() {
		tokenUsageSourceSettings = originalSettings
	})
}

func TestTokenUsageSourceDatabaseSettings(t *testing.T) {
	preserveTokenUsageSourceSettings(t)
	tokenUsageSourceSettings = TokenUsageSourceSettings{
		Enabled:                   true,
		ReconcileEnabled:          false,
		BackfillEnabled:           true,
		ReconcileBatchSize:        100,
		ReconcileMaxBatchesPerRun: 10,
		BackfillDays:              90,
		BackfillChunkSeconds:      300,
		ChunkTimeoutSeconds:       30,
		MaxGroupsPerChunk:         20000,
		MaxLiveChunksPerRun:       60,
		LateLogLagSeconds:         300,
		MaxWatermarkLagSeconds:    1800,
		MaxSourcesPerToken:        500,
	}

	assert.True(t, TokenUsageSourceRollupEnabled())
	assert.True(t, TokenUsageSourceUIEnabled())
	assert.True(t, TokenUsageSourceReconcileEnabled())
	assert.True(t, TokenUsageSourceBackfillEnabled())
	values := TokenUsageSourceOptionValues()
	assert.Equal(t, "true", values["token_usage_source_setting.enabled"])
	assert.Equal(t, "false", values["token_usage_source_setting.reconcile_enabled"])
}

func TestValidateTokenUsageSourceOption(t *testing.T) {
	require.NoError(t, ValidateTokenUsageSourceOption(
		"token_usage_source_setting.max_sources_per_token",
		"500",
	))
	assert.Error(t, ValidateTokenUsageSourceOption(
		"token_usage_source_setting.max_sources_per_token",
		"5001",
	))
	assert.Error(t, ValidateTokenUsageSourceOption(
		"token_usage_source_setting.unknown",
		"1",
	))
	require.NoError(t, ValidateTokenUsageSourceOption("unrelated.setting", "anything"))
}
