package system_setting

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

const tokenUsageSourceSettingPrefix = "token_usage_source_setting."

type TokenUsageSourceSettings struct {
	Enabled                   bool `json:"enabled"`
	ReconcileEnabled          bool `json:"reconcile_enabled"`
	ReconcileBatchSize        int  `json:"reconcile_batch_size"`
	ReconcileMaxBatchesPerRun int  `json:"reconcile_max_batches_per_run"`
	MaxSourcesPerToken        int  `json:"max_sources_per_token"`

	// Legacy log-rollup tuning remains internal so stale pre-release tasks can
	// finish as disabled. These values are not exposed as options, and both
	// historical handlers are hard-disabled.
	BackfillEnabled        bool `json:"backfill_enabled"`
	BackfillDays           int  `json:"backfill_days"`
	BackfillChunkSeconds   int  `json:"backfill_chunk_seconds"`
	ChunkTimeoutSeconds    int  `json:"chunk_timeout_seconds"`
	MaxGroupsPerChunk      int  `json:"max_groups_per_chunk"`
	MaxLiveChunksPerRun    int  `json:"max_live_chunks_per_run"`
	LateLogLagSeconds      int  `json:"late_log_lag_seconds"`
	MaxWatermarkLagSeconds int  `json:"max_watermark_lag_seconds"`
}

var tokenUsageSourceSettings = TokenUsageSourceSettings{
	Enabled:                   false,
	ReconcileEnabled:          false,
	BackfillEnabled:           false,
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

func init() {
	config.GlobalConfig.Register("token_usage_source_setting", &tokenUsageSourceSettings)
}

func normalizeTokenUsageSourceSettings(settings TokenUsageSourceSettings) TokenUsageSourceSettings {
	settings.ReconcileBatchSize = clampTokenUsageSourceSetting(settings.ReconcileBatchSize, 25, 500)
	settings.ReconcileMaxBatchesPerRun = clampTokenUsageSourceSetting(
		settings.ReconcileMaxBatchesPerRun,
		1,
		50,
	)
	settings.BackfillDays = clampTokenUsageSourceSetting(settings.BackfillDays, 1, 3650)
	settings.BackfillChunkSeconds = clampTokenUsageSourceSetting(
		settings.BackfillChunkSeconds,
		60,
		1800,
	)
	settings.ChunkTimeoutSeconds = clampTokenUsageSourceSetting(settings.ChunkTimeoutSeconds, 5, 300)
	settings.MaxGroupsPerChunk = clampTokenUsageSourceSetting(
		settings.MaxGroupsPerChunk,
		1000,
		200000,
	)
	settings.MaxLiveChunksPerRun = clampTokenUsageSourceSetting(
		settings.MaxLiveChunksPerRun,
		1,
		360,
	)
	settings.LateLogLagSeconds = clampTokenUsageSourceSetting(
		settings.LateLogLagSeconds,
		60,
		3600,
	)
	if settings.MaxWatermarkLagSeconds < 300 {
		settings.MaxWatermarkLagSeconds = 300
	}
	settings.MaxSourcesPerToken = clampTokenUsageSourceSetting(
		settings.MaxSourcesPerToken,
		10,
		5000,
	)
	return settings
}

func clampTokenUsageSourceSetting(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func GetTokenUsageSourceSettings() TokenUsageSourceSettings {
	return normalizeTokenUsageSourceSettings(tokenUsageSourceSettings)
}

func TokenUsageSourceOptionValues() map[string]string {
	settings := GetTokenUsageSourceSettings()
	return map[string]string{
		tokenUsageSourceSettingPrefix + "enabled": strconv.FormatBool(settings.Enabled),
		tokenUsageSourceSettingPrefix + "reconcile_enabled": strconv.FormatBool(
			settings.ReconcileEnabled,
		),
		tokenUsageSourceSettingPrefix + "reconcile_batch_size": strconv.Itoa(
			settings.ReconcileBatchSize,
		),
		tokenUsageSourceSettingPrefix + "reconcile_max_batches_per_run": strconv.Itoa(
			settings.ReconcileMaxBatchesPerRun,
		),
		tokenUsageSourceSettingPrefix + "max_sources_per_token": strconv.Itoa(
			settings.MaxSourcesPerToken,
		),
	}
}

func TokenUsageSourceRollupEnabled() bool {
	return false
}

func TokenUsageSourceUIEnabled() bool {
	return GetTokenUsageSourceSettings().Enabled
}

func TokenUsageSourceReconcileEnabled() bool {
	settings := GetTokenUsageSourceSettings()
	return settings.Enabled || settings.ReconcileEnabled
}

func TokenUsageSourceBackfillEnabled() bool {
	return false
}

func TokenUsageSourceDirectCountingEnabledAt(_ int64) bool {
	return GetTokenUsageSourceSettings().Enabled
}

func ValidateTokenUsageSourceOption(key string, value string) error {
	if !strings.HasPrefix(key, tokenUsageSourceSettingPrefix) {
		return nil
	}
	name := strings.TrimPrefix(key, tokenUsageSourceSettingPrefix)
	switch name {
	case "enabled", "reconcile_enabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		return nil
	case "reconcile_batch_size":
		return validateTokenUsageSourceInteger(key, value, 25, 500)
	case "reconcile_max_batches_per_run":
		return validateTokenUsageSourceInteger(key, value, 1, 50)
	case "max_sources_per_token":
		return validateTokenUsageSourceInteger(key, value, 10, 5000)
	default:
		return fmt.Errorf("unknown token usage source setting: %s", key)
	}
}

func validateTokenUsageSourceInteger(key string, value string, minimum int, maximum int) error {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return nil
}
