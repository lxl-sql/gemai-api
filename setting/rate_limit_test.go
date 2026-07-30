package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRequestRateLimitConfigUsesGroupOverride(t *testing.T) {
	ModelRequestRateLimitMutex.Lock()
	originalEnabled := ModelRequestRateLimitEnabled
	originalDuration := ModelRequestRateLimitDurationMinutes
	originalTotal := ModelRequestRateLimitCount
	originalSuccess := ModelRequestRateLimitSuccessCount
	originalGroups := ModelRequestRateLimitGroup
	ModelRequestRateLimitMutex.Unlock()
	t.Cleanup(func() {
		ModelRequestRateLimitMutex.Lock()
		defer ModelRequestRateLimitMutex.Unlock()
		ModelRequestRateLimitEnabled = originalEnabled
		ModelRequestRateLimitDurationMinutes = originalDuration
		ModelRequestRateLimitCount = originalTotal
		ModelRequestRateLimitSuccessCount = originalSuccess
		ModelRequestRateLimitGroup = originalGroups
	})

	SetModelRequestRateLimitEnabled(true)
	SetModelRequestRateLimitDurationMinutes(5)
	SetModelRequestRateLimitCount(100)
	SetModelRequestRateLimitSuccessCount(80)
	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(`{"vip":[500,400]}`))

	defaultConfig := GetModelRequestRateLimitConfig("")
	vipConfig := GetModelRequestRateLimitConfig("vip")

	assert.Equal(t, ModelRequestRateLimitConfig{
		Enabled:         true,
		DurationMinutes: 5,
		TotalCount:      100,
		SuccessCount:    80,
	}, defaultConfig)
	assert.Equal(t, ModelRequestRateLimitConfig{
		Enabled:         true,
		DurationMinutes: 5,
		TotalCount:      500,
		SuccessCount:    400,
	}, vipConfig)
}

func TestInvalidGroupRateLimitUpdatePreservesCurrentConfig(t *testing.T) {
	ModelRequestRateLimitMutex.Lock()
	originalGroups := ModelRequestRateLimitGroup
	ModelRequestRateLimitGroup = map[string][2]int{"vip": {500, 400}}
	ModelRequestRateLimitMutex.Unlock()
	t.Cleanup(func() {
		ModelRequestRateLimitMutex.Lock()
		defer ModelRequestRateLimitMutex.Unlock()
		ModelRequestRateLimitGroup = originalGroups
	})

	require.Error(t, UpdateModelRequestRateLimitGroupByJSONString(`{"vip":`))

	total, success, found := GetGroupRateLimit("vip")
	assert.True(t, found)
	assert.Equal(t, 500, total)
	assert.Equal(t, 400, success)
}
