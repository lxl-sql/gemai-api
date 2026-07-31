package service

import (
	"testing"

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

func TestTokenUsageSourceHistoricalTasksStayDisabled(t *testing.T) {
	setTokenUsageSourceServiceSettings(t, map[string]string{
		"enabled":          "true",
		"backfill_enabled": "true",
	})

	assert.False(t, tokenUsageSourceRollupEnabled())
	assert.False(t, tokenUsageSourceBackfillEnabled())
	assert.False(t, (tokenUsageSourceRollupHandler{}).Enabled())
}

func TestTokenUsageSourceReconcileCanRunBeforeDirectCounting(t *testing.T) {
	setTokenUsageSourceServiceSettings(t, map[string]string{
		"enabled":           "false",
		"reconcile_enabled": "true",
	})

	assert.True(t, tokenUsageSourceReconcileEnabled())
}
