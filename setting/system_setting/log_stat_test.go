package system_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveLogStatSettings(t *testing.T) {
	t.Helper()
	originalSettings := logStatSettings
	t.Cleanup(func() {
		logStatSettings = originalSettings
	})
}

func TestLogStatSettingsNormalizeRecentWindow(t *testing.T) {
	preserveLogStatSettings(t)

	logStatSettings = LogStatSettings{
		Enabled:         true,
		BackfillEnabled: true,
		RecentMinutes:   2,
	}
	assert.True(t, LogStatRollupEnabled())
	assert.True(t, LogStatBackfillEnabled())
	assert.Equal(t, 5*time.Minute, LogStatRecentWindow())
	assert.Equal(t, "5", LogStatOptionValues()["log_stat_setting.recent_minutes"])

	logStatSettings.RecentMinutes = 31
	assert.Equal(t, 30*time.Minute, LogStatRecentWindow())

	logStatSettings.Enabled = false
	assert.False(t, LogStatBackfillEnabled())
}

func TestValidateLogStatOption(t *testing.T) {
	require.NoError(t, ValidateLogStatOption("log_stat_setting.enabled", "true"))
	require.NoError(t, ValidateLogStatOption("log_stat_setting.recent_minutes", "10"))
	assert.Error(t, ValidateLogStatOption("log_stat_setting.recent_minutes", "4"))
	assert.Error(t, ValidateLogStatOption("log_stat_setting.recent_minutes", "31"))
	assert.Error(t, ValidateLogStatOption("log_stat_setting.unknown", "1"))
	require.NoError(t, ValidateLogStatOption("unrelated.setting", "anything"))
}
