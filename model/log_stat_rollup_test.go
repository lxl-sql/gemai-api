package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setLogStatSettingsForTest(t *testing.T, enabled bool, backfillEnabled bool) {
	t.Helper()
	original := system_setting.GetLogStatSettings()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"log_stat_setting.enabled":          fmt.Sprint(original.Enabled),
			"log_stat_setting.backfill_enabled": fmt.Sprint(original.BackfillEnabled),
			"log_stat_setting.recent_minutes":   fmt.Sprint(original.RecentMinutes),
		}))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"log_stat_setting.enabled":          fmt.Sprint(enabled),
		"log_stat_setting.backfill_enabled": fmt.Sprint(backfillEnabled),
	}))
}

func TestLogStatRollupBucketExpressionByDialect(t *testing.T) {
	original := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(original)
		initCol()
	})
	tests := []struct {
		databaseType common.DatabaseType
		expected     string
	}{
		{common.DatabaseTypeSQLite, "(created_at - (created_at % 60))"},
		{common.DatabaseTypeMySQL, "(created_at - MOD(created_at, 60))"},
		{common.DatabaseTypePostgreSQL, "(created_at - MOD(created_at, 60))"},
		{common.DatabaseTypeClickHouse, "(intDiv(created_at, 60) * 60)"},
	}
	for _, test := range tests {
		common.SetLogDatabaseType(test.databaseType)
		expression, err := logStatRollupBucketExpression()
		require.NoError(t, err)
		assert.Equal(t, test.expected, expression)
	}
}

func TestRecentLogRateWindowUsesSixtyCompletedSeconds(t *testing.T) {
	const now int64 = 1_720_000_060

	start, end := recentLogRateWindow(now)

	assert.Equal(t, now-60, start)
	assert.Equal(t, now, end)
	assert.Equal(t, int64(60), end-start)
}

func TestRecentLogRateCacheEvictsExpiredThenOldest(t *testing.T) {
	cache := newRecentLogRateCache(10, 2)
	cache.put("oldest", LogStatRollupAggregate{Quota: 1}, 100)
	cache.put("newer", LogStatRollupAggregate{Quota: 2}, 101)
	cache.put("latest", LogStatRollupAggregate{Quota: 3}, 102)

	_, ok := cache.get("oldest", 102)
	assert.False(t, ok)
	aggregate, ok := cache.get("newer", 102)
	require.True(t, ok)
	assert.Equal(t, int64(2), aggregate.Quota)
	aggregate, ok = cache.get("latest", 102)
	require.True(t, ok)
	assert.Equal(t, int64(3), aggregate.Quota)

	cache.put("after-expiry", LogStatRollupAggregate{Quota: 4}, 112)
	_, ok = cache.get("newer", 112)
	assert.False(t, ok)
	aggregate, ok = cache.get("after-expiry", 112)
	require.True(t, ok)
	assert.Equal(t, int64(4), aggregate.Quota)
}

func TestLogStatUsernameMatchModeAppliesToRollupAndRecentRawQuery(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	now := time.Now().Unix()
	bucketStart := now - now%60 - 120
	require.NoError(t, UpsertLogStatRollups(ctx, []LogStatRollup{
		{BucketStart: bucketStart, Username: "alice%ops", Quota: 3},
		{BucketStart: bucketStart, Username: "aliceXops", Quota: 4},
	}))

	exact, err := QueryLogStatRollups(ctx, LogStatRollupFilter{
		StartTimestamp: bucketStart,
		EndTimestamp:   bucketStart + 60,
		Username:       "alice%ops",
		UsernameMatch:  LogStatTextMatchExact,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), exact.Quota)

	pattern, err := QueryLogStatRollups(ctx, LogStatRollupFilter{
		StartTimestamp: bucketStart,
		EndTimestamp:   bucketStart + 60,
		Username:       "alice%ops",
		UsernameMatch:  LogStatTextMatchPattern,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), pattern.Quota)

	require.NoError(t, LOG_DB.Create(&[]Log{
		{
			CreatedAt:    now - 1,
			Type:         LogTypeConsume,
			Username:     "alice%ops",
			Quota:        5,
			PromptTokens: 6,
		},
		{
			CreatedAt:    now - 1,
			Type:         LogTypeConsume,
			Username:     "aliceXops",
			Quota:        7,
			PromptTokens: 8,
		},
	}).Error)
	originalCache := recentLogRateCache
	recentLogRateCache = newRecentLogRateCache(10, 2048)
	t.Cleanup(func() {
		recentLogRateCache = originalCache
	})

	recentExact, err := queryRecentLogRateStat(ctx, LogStatQuery{
		Username:      "alice%ops",
		UsernameMatch: LogStatTextMatchExact,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), recentExact.RequestCount)
	assert.Equal(t, int64(6), recentExact.TotalTokens())

	recentPattern, err := queryRecentLogRateStat(ctx, LogStatQuery{
		Username:      "alice%ops",
		UsernameMatch: LogStatTextMatchPattern,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), recentPattern.RequestCount)
	assert.Equal(t, int64(14), recentPattern.TotalTokens())
}

func TestLogStatRollupAggregateReplaceAndQuery(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const bucketStart int64 = 1_720_000_020
	rangeStart := bucketStart - bucketStart%60
	rangeEnd := rangeStart + 60

	logs := []Log{
		{
			CreatedAt:        rangeStart + 1,
			Type:             LogTypeConsume,
			Username:         "alice",
			TokenName:        "default",
			ModelName:        "gpt-test",
			ChannelId:        3,
			Group:            "vip",
			Quota:            12,
			PromptTokens:     5,
			CompletionTokens: 7,
		},
		{
			CreatedAt:        rangeStart + 59,
			Type:             LogTypeConsume,
			Username:         "alice",
			TokenName:        "default",
			ModelName:        "gpt-test",
			ChannelId:        3,
			Group:            "vip",
			Quota:            20,
			PromptTokens:     8,
			CompletionTokens: 9,
		},
		{
			CreatedAt: rangeStart + 10,
			Type:      LogTypeError,
			Username:  "alice",
			Quota:     999,
		},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	rollups, err := AggregateLogStatRollups(ctx, rangeStart, rangeEnd)
	require.NoError(t, err)
	require.Len(t, rollups, 1)
	assert.Equal(t, int64(2), rollups[0].RequestCount)
	assert.Equal(t, int64(32), rollups[0].Quota)
	assert.Equal(t, int64(13), rollups[0].PromptTokens)
	assert.Equal(t, int64(16), rollups[0].CompletionTokens)
	assert.Len(t, rollups[0].DimensionKey, 64)

	require.NoError(t, ReplaceLogStatRollups(ctx, rangeStart, rangeEnd, rollups))
	aggregate, err := QueryLogStatRollups(ctx, LogStatRollupFilter{
		StartTimestamp: rangeStart,
		EndTimestamp:   rangeEnd,
		Username:       "alice",
		TokenName:      "default",
		ModelName:      "gpt-test",
		ChannelID:      3,
		Group:          "vip",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(32), aggregate.Quota)
	assert.Equal(t, int64(2), aggregate.RequestCount)
	assert.Equal(t, int64(29), aggregate.TotalTokens())
	totalAggregate, err := QueryLogStatMinuteTotals(ctx, rangeStart, rangeEnd)
	require.NoError(t, err)
	assert.Equal(t, aggregate, totalAggregate)

	require.NoError(t, LOG_DB.Where("type = ?", LogTypeConsume).Delete(&Log{}).Error)
	replacement := Log{
		CreatedAt:        rangeStart + 30,
		Type:             LogTypeConsume,
		Username:         "alice",
		TokenName:        "default",
		ModelName:        "gpt-test",
		ChannelId:        3,
		Group:            "vip",
		Quota:            4,
		PromptTokens:     1,
		CompletionTokens: 2,
	}
	require.NoError(t, LOG_DB.Create(&replacement).Error)
	rollups, err = AggregateLogStatRollups(ctx, rangeStart, rangeEnd)
	require.NoError(t, err)
	require.NoError(t, ReplaceLogStatRollups(ctx, rangeStart, rangeEnd, rollups))

	aggregate, err = QueryLogStatRollups(ctx, LogStatRollupFilter{
		StartTimestamp: rangeStart,
		EndTimestamp:   rangeEnd,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), aggregate.Quota)
	assert.Equal(t, int64(1), aggregate.RequestCount)
	assert.Equal(t, int64(3), aggregate.TotalTokens())
}

func TestLogStatRollupDimensionKeyAndStateTransitions(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	firstKey := NewLogStatRollupDimensionKey("ab", "c", "", 1, "")
	secondKey := NewLogStatRollupDimensionKey("a", "bc", "", 1, "")
	assert.NotEqual(t, firstKey, secondKey)
	assert.Equal(t, firstKey, NewLogStatRollupDimensionKey("ab", "c", "", 1, ""))

	state := &LogStatRollupState{
		Name:           LogStatRollupStateName,
		CoverageStart:  60,
		Watermark:      600,
		BackfillCursor: 540,
	}
	require.NoError(t, SaveLogStatRollupState(ctx, state))

	// 同事务覆盖写 + 水位推进：只允许单调向前。
	require.NoError(t, ReplaceLogStatRollupsAndAdvanceWatermark(ctx, 600, 660, nil, LogStatRollupStateName, 660))
	require.NoError(t, ReplaceLogStatRollupsAndAdvanceWatermark(ctx, 540, 600, nil, LogStatRollupStateName, 600))

	// 回填由近及远：CAS 事务把 cursor 从当前值下压到块起点。
	require.NoError(t, ReplaceLogStatRollupsAndLowerCursor(ctx, 480, 540, nil, LogStatRollupStateName))

	stored, err := GetLogStatRollupState(ctx, LogStatRollupStateName)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, int64(60), stored.CoverageStart)
	assert.Equal(t, int64(660), stored.Watermark)
	assert.Equal(t, int64(480), stored.BackfillCursor)
}

func TestBackfillReplaceAndCursorAdvanceAreAtomic(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	seedLogStatCoverage(t, 60, 600, 900)
	rollups := []LogStatRollup{{BucketStart: 540, Username: "alice", Quota: 7}}

	require.NoError(t, ReplaceLogStatRollupsAndLowerCursor(
		ctx, 540, 600, rollups, LogStatRollupStateName,
	))
	state, err := GetLogStatRollupState(ctx, LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, int64(540), state.BackfillCursor)

	// A stale chunk with an old end cursor must not write any rows.
	err = ReplaceLogStatRollupsAndLowerCursor(
		ctx,
		420,
		480,
		[]LogStatRollup{{BucketStart: 420, Username: "stale", Quota: 99}},
		LogStatRollupStateName,
	)
	assert.ErrorIs(t, err, ErrLogStatRollupStateChanged)
	var count int64
	require.NoError(t, DB.Model(&LogStatRollup{}).Where("username = ?", "stale").Count(&count).Error)
	assert.Zero(t, count)
}

func TestAggregateLogStatRollupsLimitedBoundsMaterializedRows(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const start int64 = 600
	logs := []Log{
		{CreatedAt: start + 1, Type: LogTypeConsume, Username: "a", Quota: 1},
		{CreatedAt: start + 2, Type: LogTypeConsume, Username: "b", Quota: 1},
		{CreatedAt: start + 3, Type: LogTypeConsume, Username: "c", Quota: 1},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	rollups, truncated, err := AggregateLogStatRollupsLimited(ctx, start, start+60, 2)
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, rollups, 2)
}

func seedLogStatCoverage(t *testing.T, coverageStart int64, backfillCursor int64, watermark int64) {
	t.Helper()
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           LogStatRollupStateName,
		CoverageStart:  coverageStart,
		BackfillCursor: backfillCursor,
		Watermark:      watermark,
	}))
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           LogStatMinuteTotalStateName,
		CoverageStart:  coverageStart,
		BackfillCursor: backfillCursor,
		Watermark:      watermark,
	}))
}

func TestSumUsedQuotaUsesRollupsAndRawMinuteBoundaries(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const bucketStart int64 = 1_720_100_000
	rangeStart := bucketStart - bucketStart%60
	logs := []Log{
		{CreatedAt: rangeStart + 20, Type: LogTypeConsume, Username: "alice", ModelName: "gpt-test", Quota: 1},
		{CreatedAt: rangeStart + 70, Type: LogTypeConsume, Username: "alice", ModelName: "gpt-test", Quota: 2},
		{CreatedAt: rangeStart + 125, Type: LogTypeConsume, Username: "alice", ModelName: "gpt-test", Quota: 3},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)
	rollups, err := AggregateLogStatRollups(ctx, rangeStart, rangeStart+180)
	require.NoError(t, err)
	require.NoError(t, ReplaceLogStatRollups(ctx, rangeStart, rangeStart+180, rollups))
	seedLogStatCoverage(t, rangeStart, rangeStart, rangeStart+180)

	stat, err := SumUsedQuota(ctx, rangeStart+10, rangeStart+129, "gpt-%", "alice", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(6), stat.Quota)

	_, err = SumUsedQuota(ctx, rangeStart-1, rangeStart+10, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatRangeUnavailable)
}

func TestSumUsedQuotaHistoricalRangeSurvivesStaleWatermark(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	// watermark 停在很久以前（相对 now 滞后远超 raw tail 上限），
	// 但纯历史区间查询必须仍然成功。
	const rangeStart int64 = 1_720_200_000
	log := Log{CreatedAt: rangeStart + 30, Type: LogTypeConsume, Username: "alice", Quota: 7}
	require.NoError(t, LOG_DB.Create(&log).Error)
	rollups, err := AggregateLogStatRollups(ctx, rangeStart, rangeStart+60)
	require.NoError(t, err)
	require.NoError(t, ReplaceLogStatRollups(ctx, rangeStart, rangeStart+60, rollups))
	seedLogStatCoverage(t, rangeStart, rangeStart, rangeStart+60)
	// 全站查询必须只依赖每分钟总计；即使维度明细不可用，也不应退回大范围 SUM。
	require.NoError(t, DB.Where("bucket_start >= ? AND bucket_start < ?", rangeStart, rangeStart+60).
		Delete(&LogStatRollup{}).Error)

	stat, err := SumUsedQuota(ctx, rangeStart, rangeStart+59, "", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(7), stat.Quota)

	// 需要最新数据（end 钳到 now，超出水位数小时）的查询应报滞后而非扫全表。
	_, err = SumUsedQuota(ctx, rangeStart, time.Now().Unix(), "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatLagging)
}

func TestSumUsedQuotaWaitsForMinuteTotalUpgradeState(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const rangeStart int64 = 1_720_300_020
	bucketStart := rangeStart - rangeStart%60
	require.NoError(t, SaveLogStatRollupState(ctx, &LogStatRollupState{
		Name:           LogStatRollupStateName,
		CoverageStart:  bucketStart,
		BackfillCursor: bucketStart,
		Watermark:      bucketStart + 60,
	}))
	require.NoError(t, UpsertLogStatRollups(ctx, []LogStatRollup{
		{BucketStart: bucketStart, Username: "alice", Quota: 8},
	}))

	_, err := SumUsedQuota(ctx, bucketStart, bucketStart+59, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatInitializing)
}

func TestSumUsedQuotaGateDuringBackfill(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	base := int64(28_671_667) * 60
	coverage := base
	cursor := base + 3600 // 回填尚未到达 coverage
	watermark := base + 7200
	seedLogStatCoverage(t, coverage, cursor, watermark)
	require.NoError(t, UpsertLogStatRollups(ctx, []LogStatRollup{
		{BucketStart: cursor + 60, Username: "alice", Quota: 11},
	}))
	require.NoError(t, UpsertLogStatMinuteTotals(ctx, []LogStatMinuteTotal{
		{BucketStart: cursor + 60, Quota: 11},
	}))

	// 已覆盖区间（start >= cursor）可查，且返回聚合值。
	stat, err := SumUsedQuota(ctx, cursor+60, cursor+120, "", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(11), stat.Quota)

	// 未覆盖区间（start < cursor）在回填期间返回初始化中。
	_, err = SumUsedQuota(ctx, coverage+60, cursor+120, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatInitializing)

	// 回填被停用时下界不会推进，如实返回“范围暂无数据”而非“初始化中”。
	setLogStatSettingsForTest(t, true, false)
	_, err = SumUsedQuota(ctx, coverage+60, cursor+120, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatRangeUnavailable)
}

func TestSumUsedQuotaDisabledSwitchReturnsExplicitError(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const base int64 = 1_720_600_020
	seedLogStatCoverage(t, base, base, base+60)

	setLogStatSettingsForTest(t, false, true)
	_, err := SumUsedQuota(ctx, base, base+59, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatDisabled)
}

func TestSumUsedQuotaClampsOverflowingTimestamps(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	now := time.Now().Unix()
	watermark := now - now%60
	bucket := watermark - 120
	seedLogStatCoverage(t, now-3600, now-3600, watermark)
	require.NoError(t, UpsertLogStatRollups(ctx, []LogStatRollup{
		{BucketStart: bucket, Username: "alice", Quota: 9},
	}))
	require.NoError(t, UpsertLogStatMinuteTotals(ctx, []LogStatMinuteTotal{
		{BucketStart: bucket, Quota: 9},
	}))

	// 巨大的 end 被钳到 now：不溢出、不触发 ErrLogStatLagging（水位新鲜、
	// 尾部只有约 1 分钟），并且正常返回聚合结果。
	stat, err := SumUsedQuota(ctx, bucket, int64(1)<<62, "", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(9), stat.Quota)
}

func TestReconcileLogStatRollupsAfterLogCleanup(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	base := int64(28_673_334) * 60 // 整分钟对齐
	require.Zero(t, base%60)

	logs := []Log{
		{CreatedAt: base - 120, Type: LogTypeConsume, Username: "old", Quota: 100},
		{CreatedAt: base + 10, Type: LogTypeConsume, Username: "boundary-old", Quota: 50},
		{CreatedAt: base + 40, Type: LogTypeConsume, Username: "boundary-new", Quota: 5},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)
	rollups, err := AggregateLogStatRollups(ctx, base-120, base+60)
	require.NoError(t, err)
	require.NoError(t, ReplaceLogStatRollups(ctx, base-120, base+60, rollups))
	seedLogStatCoverage(t, base-3600, base-3600, base+60)

	// 模拟日志清理：删除 created_at < base+30 的原始日志。
	cleanupTarget := base + 30
	require.NoError(t, LOG_DB.Where("created_at < ?", cleanupTarget).Delete(&Log{}).Error)
	require.NoError(t, ReconcileLogStatRollupsAfterLogCleanup(ctx, cleanupTarget))

	// 清理点之前的分钟桶被删除，边界分钟按剩余日志重算。
	aggregate, err := QueryLogStatRollups(ctx, LogStatRollupFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(5), aggregate.Quota)
	bucketCut := cleanupTarget - cleanupTarget%60
	totalAggregate, err := QueryLogStatMinuteTotals(ctx, bucketCut, bucketCut+60)
	require.NoError(t, err)
	assert.Equal(t, int64(5), totalAggregate.Quota)

	state, err := GetLogStatRollupState(ctx, LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, bucketCut, state.CoverageStart)
	assert.Equal(t, bucketCut, state.BackfillCursor)
	assert.False(t, state.CleanupPending)
	totalState, err := GetLogStatRollupState(ctx, LogStatMinuteTotalStateName)
	require.NoError(t, err)
	assert.Equal(t, bucketCut, totalState.CoverageStart)
	assert.Equal(t, bucketCut, totalState.BackfillCursor)

	// 清理点之前的查询被明确拒绝，而不是返回偏高/偏低的结果。
	_, err = SumUsedQuota(ctx, base-120, base+59, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatRangeUnavailable)
}

func TestSumUsedQuotaRejectsWhileCleanupIsPending(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	const start int64 = 1_720_500_000
	seedLogStatCoverage(t, start, start, start+60)
	require.NoError(t, BeginLogStatCleanup(ctx, start+30))

	_, err := SumUsedQuota(ctx, start, start+59, "", "", "", 0, "")
	assert.ErrorIs(t, err, ErrLogStatLagging)
}

func TestPruneLogStatRollupsAdvancesCoverageAtomically(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	seedLogStatCoverage(t, 60, 60, 600)
	require.NoError(t, UpsertLogStatRollups(ctx, []LogStatRollup{
		{BucketStart: 60, Username: "old", Quota: 1},
		{BucketStart: 300, Username: "current", Quota: 2},
	}))
	require.NoError(t, UpsertLogStatMinuteTotals(ctx, []LogStatMinuteTotal{
		{BucketStart: 60, Quota: 1},
		{BucketStart: 300, Quota: 2},
	}))

	pruned, err := PruneLogStatRollupsBefore(ctx, LogStatRollupStateName, 300)
	require.NoError(t, err)
	assert.True(t, pruned)
	var rows []LogStatRollup
	require.NoError(t, DB.Order("bucket_start").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(300), rows[0].BucketStart)
	var totals []LogStatMinuteTotal
	require.NoError(t, DB.Order("bucket_start").Find(&totals).Error)
	require.Len(t, totals, 1)
	assert.Equal(t, int64(300), totals[0].BucketStart)
	state, err := GetLogStatRollupState(ctx, LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, int64(300), state.CoverageStart)
	assert.Equal(t, int64(300), state.BackfillCursor)

	// 回填未完成（cursor > coverage）时 prune 仍执行：保留边界之外的
	// 区间已经出窗，收缩回填目标防止关闭回填后聚合表无限增长。
	seedLogStatCoverage(t, 300, 480, 600)
	pruned, err = PruneLogStatRollupsBefore(ctx, LogStatRollupStateName, 420)
	require.NoError(t, err)
	assert.True(t, pruned)
	state, err = GetLogStatRollupState(ctx, LogStatRollupStateName)
	require.NoError(t, err)
	assert.Equal(t, int64(420), state.CoverageStart)
	// cursor 高于新边界时保持不变，回填继续向新目标推进。
	assert.Equal(t, int64(480), state.BackfillCursor)

	// 幂等：边界未前进时不重复删除。
	pruned, err = PruneLogStatRollupsBefore(ctx, LogStatRollupStateName, 420)
	require.NoError(t, err)
	assert.False(t, pruned)
}
