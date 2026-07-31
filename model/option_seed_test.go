package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedMissingOptionValuesPreservesExistingValues(t *testing.T) {
	originalSettings := system_setting.GetLogStatSettings()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"log_stat_setting.enabled":          fmt.Sprint(originalSettings.Enabled),
			"log_stat_setting.backfill_enabled": fmt.Sprint(originalSettings.BackfillEnabled),
			"log_stat_setting.recent_minutes":   fmt.Sprint(originalSettings.RecentMinutes),
		}))
	})

	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" LIKE ?", "log_stat_setting.%").Delete(&Option{}).Error)
	t.Cleanup(func() {
		_ = DB.Where(commonKeyCol+" LIKE ?", "log_stat_setting.%").Delete(&Option{}).Error
	})

	require.NoError(t, DB.Create(&Option{
		Key:   "log_stat_setting.enabled",
		Value: "false",
	}).Error)
	seedMissingOptionValues(
		"log_stat_setting.",
		map[string]string{
			"log_stat_setting.enabled":          "true",
			"log_stat_setting.backfill_enabled": "true",
			"log_stat_setting.recent_minutes":   "10",
		},
		"log stat test",
	)

	var options []Option
	require.NoError(t, DB.
		Where(commonKeyCol+" LIKE ?", "log_stat_setting.%").
		Order(commonKeyCol).
		Find(&options).Error)
	require.Len(t, options, 3)

	values := make(map[string]string, len(options))
	for _, option := range options {
		values[option.Key] = option.Value
	}
	assert.Equal(t, "false", values["log_stat_setting.enabled"])
	assert.Equal(t, "true", values["log_stat_setting.backfill_enabled"])
	assert.Equal(t, "10", values["log_stat_setting.recent_minutes"])
}

func TestUpdateOptionReturnsDatabaseWriteError(t *testing.T) {
	const (
		key         = "test.update_option.persist_failure"
		triggerName = "fail_update_option_insert"
	)

	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" = ?", key).Delete(&Option{}).Error)
	require.NoError(t, DB.Exec(
		"CREATE TRIGGER "+triggerName+
			" BEFORE INSERT ON options WHEN NEW.key = '"+key+"'"+
			" BEGIN SELECT RAISE(ABORT, 'forced option insert failure'); END",
	).Error)
	t.Cleanup(func() {
		_ = DB.Exec("DROP TRIGGER IF EXISTS " + triggerName).Error
		_ = DB.Where(commonKeyCol+" = ?", key).Delete(&Option{}).Error
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, key)
		common.OptionMapRWMutex.Unlock()
	})

	err := UpdateOption(key, "new-value")

	require.ErrorContains(t, err, "forced option insert failure")
	common.OptionMapRWMutex.RLock()
	_, exists := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, exists)
}
