package system_setting

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	logStatSettingPrefix        = "log_stat_setting."
	logStatRecentMinutesMinimum = 5
	logStatRecentMinutesMaximum = 30
)

type LogStatSettings struct {
	Enabled         bool `json:"enabled"`
	BackfillEnabled bool `json:"backfill_enabled"`
	RecentMinutes   int  `json:"recent_minutes"`
}

// Environment variables remain startup defaults for backward compatibility.
// Persisted system settings take precedence after options are loaded from DB.
var logStatSettings = LogStatSettings{
	Enabled:         common.GetEnvOrDefaultBool("LOG_STAT_ROLLUP_ENABLED", true),
	BackfillEnabled: common.GetEnvOrDefaultBool("LOG_STAT_BACKFILL_ENABLED", true),
	RecentMinutes: common.GetEnvOrDefault(
		"LOG_STAT_ROLLUP_RECENT_MINUTES",
		5,
	),
}

func init() {
	config.GlobalConfig.Register("log_stat_setting", &logStatSettings)
}

func GetLogStatSettings() LogStatSettings {
	// updateOptionMap holds the same mutex while applying registered config
	// updates, so readers cannot observe a partially updated settings struct.
	common.OptionMapRWMutex.RLock()
	settings := logStatSettings
	common.OptionMapRWMutex.RUnlock()
	if settings.RecentMinutes < logStatRecentMinutesMinimum {
		settings.RecentMinutes = logStatRecentMinutesMinimum
	}
	if settings.RecentMinutes > logStatRecentMinutesMaximum {
		settings.RecentMinutes = logStatRecentMinutesMaximum
	}
	return settings
}

func LogStatOptionValues() map[string]string {
	settings := GetLogStatSettings()
	return map[string]string{
		logStatSettingPrefix + "enabled":          strconv.FormatBool(settings.Enabled),
		logStatSettingPrefix + "backfill_enabled": strconv.FormatBool(settings.BackfillEnabled),
		logStatSettingPrefix + "recent_minutes":   strconv.Itoa(settings.RecentMinutes),
	}
}

func LogStatRollupEnabled() bool {
	return GetLogStatSettings().Enabled
}

func LogStatBackfillEnabled() bool {
	settings := GetLogStatSettings()
	return settings.Enabled && settings.BackfillEnabled
}

func LogStatRecentWindow() time.Duration {
	return time.Duration(GetLogStatSettings().RecentMinutes) * time.Minute
}

func ValidateLogStatOption(key string, value string) error {
	if !strings.HasPrefix(key, logStatSettingPrefix) {
		return nil
	}
	name := strings.TrimPrefix(key, logStatSettingPrefix)
	switch name {
	case "enabled", "backfill_enabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		return nil
	case "recent_minutes":
		parsed, err := strconv.Atoi(value)
		if err != nil ||
			parsed < logStatRecentMinutesMinimum ||
			parsed > logStatRecentMinutesMaximum {
			return fmt.Errorf(
				"%s must be between %d and %d",
				key,
				logStatRecentMinutesMinimum,
				logStatRecentMinutesMaximum,
			)
		}
		return nil
	default:
		return fmt.Errorf("unknown log stat setting: %s", key)
	}
}
