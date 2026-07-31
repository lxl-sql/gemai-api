package model

import (
	"context"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type migrationLegacyTokenUsageSource struct {
	ID            int64  `gorm:"primaryKey"`
	UserID        int    `gorm:"not null;index:idx_token_usage_source_user_token,priority:1"`
	TokenID       int    `gorm:"not null;uniqueIndex:idx_token_usage_source_identity,priority:1;index:idx_token_usage_source_user_token,priority:2;index:idx_token_usage_source_recent,priority:1"`
	SourceKey     string `gorm:"type:char(64);not null;uniqueIndex:idx_token_usage_source_identity,priority:2"`
	IP            string `gorm:"type:varchar(64);not null"`
	UserAgent     string `gorm:"type:varchar(512);not null"`
	FirstSeenAt   int64  `gorm:"bigint;not null"`
	LastSeenAt    int64  `gorm:"bigint;not null;index:idx_token_usage_source_recent,priority:2"`
	LastSuccessAt int64  `gorm:"bigint;not null"`
	LastErrorAt   int64  `gorm:"bigint;not null"`
	UpdatedAt     int64  `gorm:"bigint;not null"`
}

func (migrationLegacyTokenUsageSource) TableName() string {
	return "token_usage_sources"
}

func TestTokenUsageSourceAutoMigrateAddsCountColumnsToExistingSQLiteTable(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:token_usage_source_migration?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&migrationLegacyTokenUsageSource{}))
	require.NoError(t, db.Create(&migrationLegacyTokenUsageSource{
		UserID:        1,
		TokenID:       2,
		SourceKey:     "source",
		IP:            "192.0.2.1",
		UserAgent:     "client/1.0",
		FirstSeenAt:   10,
		LastSeenAt:    20,
		LastSuccessAt: 20,
		UpdatedAt:     20,
	}).Error)

	require.NoError(t, migrateTokenUsageSourceCountColumns(db))
	require.NoError(t, migrateTokenUsageSourceCountColumns(db))
	require.NoError(t, db.AutoMigrate(&TokenUsageSource{}))

	var source TokenUsageSource
	require.NoError(t, db.First(&source).Error)
	assert.Zero(t, source.SuccessCount)
	assert.Zero(t, source.ErrorCount)
	assert.Zero(t, source.ForwardCountedThrough)
	assert.Zero(t, source.BackfillCountedFrom)
	assert.Zero(t, source.BackfillCounted)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration, source.CountGeneration)
}

func prepareTokenUsageSourceTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&TokenUsageSource{},
		&TokenUsageSourceMeta{},
		&TokenUsageSourceReconcileState{},
		&TokenUsageSourceCountProgress{},
	))
	require.NoError(t, DB.Exec("DELETE FROM token_usage_sources").Error)
	require.NoError(t, DB.Exec("DELETE FROM token_usage_source_meta").Error)
	require.NoError(t, DB.Exec("DELETE FROM token_usage_source_reconcile_states").Error)
	require.NoError(t, DB.Exec("DELETE FROM token_usage_source_count_progresses").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM token_usage_sources").Error
		_ = DB.Exec("DELETE FROM token_usage_source_meta").Error
		_ = DB.Exec("DELETE FROM token_usage_source_reconcile_states").Error
		_ = DB.Exec("DELETE FROM token_usage_source_count_progresses").Error
	})
}

func tokenUsageSourceTestGroup(tokenID int, userID int, identity string, first int64, last int64) TokenUsageSourceGroup {
	return TokenUsageSourceGroup{
		TokenID:       tokenID,
		UserID:        userID,
		SourceKey:     NewTokenUsageSourceKey(identity, identity),
		IP:            identity,
		UserAgent:     identity,
		FirstSeenAt:   first,
		LastSeenAt:    last,
		LastSuccessAt: last,
		SuccessCount:  1,
	}
}

func TestMergeTokenUsageSourceGroupsIsIdempotentAndKeepsRecentTopK(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const tokenID = 93001
	const userID = 94001
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)

	groups := []TokenUsageSourceGroup{
		tokenUsageSourceTestGroup(tokenID, userID, "198.51.100.1", 110, 210),
		tokenUsageSourceTestGroup(tokenID, userID, "198.51.100.2", 120, 220),
		tokenUsageSourceTestGroup(tokenID, userID, "198.51.100.3", 130, 230),
	}
	require.NoError(t, MergeTokenUsageSourceGroups(context.Background(), groups, 2))
	require.NoError(t, MergeTokenUsageSourceGroups(context.Background(), groups, 2))

	var sources []TokenUsageSource
	require.NoError(t, DB.Where("token_id = ?", tokenID).Order("last_seen_at DESC").Find(&sources).Error)
	require.Len(t, sources, 2)
	assert.Equal(t, int64(230), sources[0].LastSeenAt)
	assert.Equal(t, int64(220), sources[1].LastSeenAt)

	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.True(t, meta.Truncated)

	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  100,
		Watermark:      300,
		BackfillCursor: 100,
	}))
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})
	page, err := GetTokenUsageSourcePage(context.Background(), userID, tokenID, 0, 10)
	require.NoError(t, err)
	assert.False(t, page.CountsComplete)
}

func TestTokenUsageSourcePageDoesNotClaimExactCountsAfterCoverageCompletes(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const tokenID = 93020
	const userID = 94020
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  100,
		Watermark:      300,
		BackfillCursor: 100,
	}))
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})

	page, err := GetTokenUsageSourcePage(context.Background(), userID, tokenID, 0, 10)
	require.NoError(t, err)
	assert.False(t, page.Backfilling)
	assert.False(t, page.Truncated)
	assert.False(t, page.CountsComplete)
}

func TestPurgeTokenUsageSourcesPreventsHistoricalReplay(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	const tokenID = 93002
	const userID = 94002
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	group := tokenUsageSourceTestGroup(tokenID, userID, "203.0.113.10", 110, 210)
	require.NoError(t, MergeTokenUsageSourceGroups(context.Background(), []TokenUsageSourceGroup{group}, 10))

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return PurgeTokenUsageSourcesTx(tx, tokenID, userID)
	}))
	require.NoError(t, MergeTokenUsageSourceGroups(context.Background(), []TokenUsageSourceGroup{group}, 10))

	var count int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("token_id = ?", tokenID).Count(&count).Error)
	assert.Zero(t, count)

	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.Positive(t, meta.PurgedAt)
	assert.False(t, meta.TrackingEnabled)
}

func TestAggregateTokenUsageSourceGroupsNormalizesHistoricalClientIdentity(t *testing.T) {
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	const tokenID = 93003
	const userID = 94003
	require.NoError(t, LOG_DB.Where("token_id = ?", tokenID).Delete(&Log{}).Error)
	t.Cleanup(func() {
		_ = LOG_DB.Where("token_id = ?", tokenID).Delete(&Log{}).Error
	})
	require.NoError(t, LOG_DB.Create(&[]Log{
		{
			UserId:    userID,
			TokenId:   tokenID,
			CreatedAt: 110,
			Type:      LogTypeConsume,
			Ip:        "2001:0db8::1",
			UserAgent: "client/1.0   desktop",
		},
		{
			UserId:    userID,
			TokenId:   tokenID,
			CreatedAt: 120,
			Type:      LogTypeError,
			Ip:        "2001:db8::1",
			UserAgent: "client/1.0 desktop",
		},
	}).Error)

	groups, truncated, err := AggregateTokenUsageSourceGroups(context.Background(), 100, 130, 100)
	require.NoError(t, err)
	require.False(t, truncated)
	require.Len(t, groups, 1)
	assert.Equal(t, "2001:db8::1", groups[0].IP)
	assert.Equal(t, "client/1.0 desktop", groups[0].UserAgent)
	assert.Equal(t, int64(110), groups[0].FirstSeenAt)
	assert.Equal(t, int64(120), groups[0].LastSeenAt)
	assert.Equal(t, int64(110), groups[0].LastSuccessAt)
	assert.Equal(t, int64(120), groups[0].LastErrorAt)
	assert.Equal(t, int64(1), groups[0].SuccessCount)
	assert.Equal(t, int64(1), groups[0].ErrorCount)
}

func TestTokenUsageSourceCountProgressMakesBatchReplayIdempotent(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const firstTokenID = 93014
	const secondTokenID = 93015
	const userID = 94014
	require.NoError(t, DB.Create(&[]TokenUsageSourceMeta{
		{
			TokenID:         firstTokenID,
			UserID:          userID,
			TrackingEnabled: true,
			TrackingStart:   100,
		},
		{
			TokenID:         secondTokenID,
			UserID:          userID,
			TrackingEnabled: true,
			TrackingStart:   100,
		},
	}).Error)
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  100,
		Watermark:      200,
		BackfillCursor: 200,
	}))
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})

	progress, err := ClaimTokenUsageSourceCountProgress(
		context.Background(),
		TokenUsageSourceCountDirectionForward,
		200,
		260,
	)
	require.NoError(t, err)
	progress, err = MarkTokenUsageSourceCountProgressStarted(context.Background(), *progress)
	require.NoError(t, err)

	first := tokenUsageSourceTestGroup(firstTokenID, userID, "192.0.2.14", 210, 220)
	first.SuccessCount = 3
	first.ErrorCount = 1
	second := tokenUsageSourceTestGroup(secondTokenID, userID, "192.0.2.15", 230, 240)
	second.SuccessCount = 2
	second.ErrorCount = 2
	countRange := TokenUsageSourceCountRange{
		Direction:       TokenUsageSourceCountDirectionForward,
		Start:           200,
		End:             260,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
	}

	// Simulate one committed token batch followed by a process crash.
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{first},
		10,
		countRange,
	))
	resumed, err := ClaimTokenUsageSourceCountProgress(
		context.Background(),
		TokenUsageSourceCountDirectionForward,
		200,
		260,
	)
	require.NoError(t, err)
	assert.True(t, resumed.MergeStarted)
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{first, second},
		10,
		countRange,
	))
	require.NoError(t, CompleteTokenUsageSourceCountProgress(context.Background(), *resumed))

	var sources []TokenUsageSource
	require.NoError(t, DB.Order("token_id").Find(&sources).Error)
	require.Len(t, sources, 2)
	assert.Equal(t, int64(3), sources[0].SuccessCount)
	assert.Equal(t, int64(1), sources[0].ErrorCount)
	assert.Equal(t, int64(2), sources[1].SuccessCount)
	assert.Equal(t, int64(2), sources[1].ErrorCount)

	state, err := GetLogStatRollupState(context.Background(), TokenUsageSourceStateName)
	require.NoError(t, err)
	assert.Equal(t, int64(260), state.Watermark)
	var persisted TokenUsageSourceCountProgress
	require.NoError(t, DB.First(&persisted, "id = ?", tokenUsageSourceCountProgressStateID).Error)
	assert.Empty(t, persisted.Direction)
	assert.False(t, persisted.MergeStarted)
}

func TestTokenUsageSourceCountProgressCanOnlyShrinkBeforeMerge(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:            TokenUsageSourceStateName,
		CoverageStart:   50,
		Watermark:       100,
		BackfillCursor:  100,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
	}))
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})
	progress, err := ClaimTokenUsageSourceCountProgress(
		context.Background(),
		TokenUsageSourceCountDirectionForward,
		100,
		160,
	)
	require.NoError(t, err)
	progress, err = ResizeTokenUsageSourceCountProgress(
		context.Background(),
		*progress,
		100,
		130,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(130), progress.RangeEnd)

	progress, err = MarkTokenUsageSourceCountProgressStarted(context.Background(), *progress)
	require.NoError(t, err)
	_, err = ResizeTokenUsageSourceCountProgress(
		context.Background(),
		*progress,
		100,
		120,
	)
	assert.ErrorIs(t, err, ErrTokenUsageSourceCountProgressBusy)
}

func TestTokenUsageSourceForwardAndBackfillCountsDoNotOverlap(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	const tokenID = 93016
	const userID = 94016
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)

	forward := tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.16", 200, 220)
	forward.SuccessCount = 4
	backfill := tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.16", 160, 190)
	backfill.SuccessCount = 2
	backfill.ErrorCount = 1
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{forward},
		10,
		TokenUsageSourceCountRange{
			Direction:       TokenUsageSourceCountDirectionForward,
			Start:           200,
			End:             260,
			CountGeneration: tokenUsageSourceInitialCountGeneration,
		},
	))
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{backfill},
		10,
		TokenUsageSourceCountRange{
			Direction:       TokenUsageSourceCountDirectionBackfill,
			Start:           140,
			End:             200,
			CountGeneration: tokenUsageSourceInitialCountGeneration,
		},
	))
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{backfill},
		10,
		TokenUsageSourceCountRange{
			Direction:       TokenUsageSourceCountDirectionBackfill,
			Start:           140,
			End:             200,
			CountGeneration: tokenUsageSourceInitialCountGeneration,
		},
	))
	err := MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{forward},
		10,
		TokenUsageSourceCountRange{
			Direction:       TokenUsageSourceCountDirectionForward,
			Start:           240,
			End:             300,
			CountGeneration: tokenUsageSourceInitialCountGeneration,
		},
	)
	assert.ErrorIs(t, err, ErrTokenUsageSourceCountRangeOverlap)

	var source TokenUsageSource
	require.NoError(t, DB.First(&source, "token_id = ?", tokenID).Error)
	assert.Equal(t, int64(6), source.SuccessCount)
	assert.Equal(t, int64(1), source.ErrorCount)
	assert.Equal(t, int64(260), source.ForwardCountedThrough)
	assert.Equal(t, int64(1), source.BackfillCounted)
	assert.Equal(t, int64(140), source.BackfillCountedFrom)
}

func TestDeleteTokenPurgesSourcesAndLeavesPermanentMetaTombstone(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const tokenID = 93004
	const userID = 94004
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id = ?", tokenID).Delete(&Token{}).Error
		_ = DB.Unscoped().Where("id = ?", userID).Delete(&User{}).Error
	})
	require.NoError(t, DB.Create(&User{Id: userID, Username: "usage-source-owner"}).Error)
	require.NoError(t, DB.Create(&Token{
		Id:          tokenID,
		UserId:      userID,
		Key:         "usage-source-delete-key",
		Name:        "delete-source-token",
		CreatedTime: 100,
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, MergeTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.44", 110, 120)},
		10,
	))

	require.NoError(t, DeleteTokenById(tokenID, userID))

	var sourceCount int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("token_id = ?", tokenID).Count(&sourceCount).Error)
	assert.Zero(t, sourceCount)
	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.Positive(t, meta.PurgedAt)
	assert.False(t, meta.TrackingEnabled)
	var tokenCount int64
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tokenID).Count(&tokenCount).Error)
	assert.Zero(t, tokenCount)
}

func TestDeleteUserPurgesOrphanedUsageSourceMeta(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const tokenID = 93019
	const userID = 94019
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id = ?", userID).Delete(&User{}).Error
	})
	user := User{
		Id:       userID,
		Username: "usage-source-delete-orphan-owner",
		AffCode:  "usage-source-delete-orphan-aff",
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSource{
		UserID:      userID,
		TokenID:     tokenID,
		SourceKey:   NewTokenUsageSourceKey("192.0.2.49", "client/1.0"),
		IP:          "192.0.2.49",
		UserAgent:   "client/1.0",
		FirstSeenAt: 110,
		LastSeenAt:  120,
	}).Error)

	require.NoError(t, user.Delete())

	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.False(t, meta.TrackingEnabled)
	assert.Zero(t, meta.TrackingStart)
	assert.Positive(t, meta.PurgedAt)
	require.NoError(t, MergeTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.50", 130, 140)},
		10,
	))
	var sourceCount int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("user_id = ?", userID).Count(&sourceCount).Error)
	assert.Zero(t, sourceCount)
}

func TestRecordIPLogSettingDoesNotControlUsageSources(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const tokenID = 93005
	const userID = 94005
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id = ?", tokenID).Delete(&Token{}).Error
		_ = DB.Unscoped().Where("id = ?", userID).Delete(&User{}).Error
	})
	user := User{Id: userID, Username: "usage-source-setting-owner"}
	user.SetSetting(dto.UserSetting{RecordIpLog: true})
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&Token{
		Id:          tokenID,
		UserId:      userID,
		Key:         "usage-source-setting-key",
		Name:        "setting-source-token",
		CreatedTime: 100,
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, MergeTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.45", 110, 120)},
		10,
	))

	require.NoError(t, UpdateUserSetting(userID, dto.UserSetting{RecordIpLog: false}))

	var sourceCount int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("token_id = ?", tokenID).Count(&sourceCount).Error)
	assert.Equal(t, int64(1), sourceCount)
	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.True(t, meta.TrackingEnabled)
	assert.Equal(t, int64(100), meta.TrackingStart)
	assert.Zero(t, meta.PurgedAt)

	require.NoError(t, UpdateUserSetting(userID, dto.UserSetting{RecordIpLog: true}))
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.True(t, meta.TrackingEnabled)
	assert.Positive(t, meta.TrackingStart)
	assert.Zero(t, meta.PurgedAt)

	require.NoError(t, MergeTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.45", 110, 120)},
		10,
	))
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("token_id = ?", tokenID).Count(&sourceCount).Error)
	assert.Equal(t, int64(1), sourceCount)

	require.NoError(t, MergeTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.46", 121, 121)},
		10,
	))
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("token_id = ?", tokenID).Count(&sourceCount).Error)
	assert.Equal(t, int64(2), sourceCount)
}

func TestTokenCreationTracksUsageSourcesIndependentlyOfRecordIPLog(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	for index, enabled := range []bool{false, true} {
		userID := 94020 + index
		user := User{
			Id:       userID,
			Username: "usage-source-create-owner-" + strconv.Itoa(index),
			AffCode:  "usage-source-create-aff-" + strconv.Itoa(index),
		}
		user.SetSetting(dto.UserSetting{RecordIpLog: enabled})
		require.NoError(t, DB.Create(&user).Error)
		token := &Token{
			UserId: userID,
			Name:   "usage-source-create-token-" + strconv.Itoa(index),
			Key:    "created-usage-source-key-" + strconv.Itoa(index),
			Status: common.TokenStatusEnabled,
		}
		require.NoError(t, token.Insert())
		t.Cleanup(func() {
			_ = DB.Unscoped().Where("id = ?", token.Id).Delete(&Token{}).Error
			_ = DB.Unscoped().Where("id = ?", userID).Delete(&User{}).Error
		})

		var meta TokenUsageSourceMeta
		require.NoError(t, DB.First(&meta, "token_id = ?", token.Id).Error)
		assert.True(t, meta.TrackingEnabled)
		assert.Positive(t, meta.TrackingStart)
	}
}

func TestBackfillTokenUsageSourceMetaStartsEnabledAndSkipsDeletedUsers(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const activeUserID = 94022
	const deletedUserID = 94023
	const activeTokenID = 93022
	const deletedUserTokenID = 93023
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id IN ?", []int{activeTokenID, deletedUserTokenID}).Delete(&Token{}).Error
		_ = DB.Unscoped().Where("id IN ?", []int{activeUserID, deletedUserID}).Delete(&User{}).Error
	})
	activeUser := User{
		Id:       activeUserID,
		Username: "usage-source-backfill-active",
		AffCode:  "usage-source-backfill-active-aff",
	}
	activeUser.SetSetting(dto.UserSetting{RecordIpLog: true})
	deletedUser := User{
		Id:       deletedUserID,
		Username: "usage-source-backfill-deleted",
		AffCode:  "usage-source-backfill-deleted-aff",
	}
	deletedUser.SetSetting(dto.UserSetting{RecordIpLog: true})
	require.NoError(t, DB.Create(&[]User{activeUser, deletedUser}).Error)
	require.NoError(t, DB.Create(&[]Token{
		{
			Id:          activeTokenID,
			UserId:      activeUserID,
			Key:         "usage-source-backfill-active-key",
			Name:        "usage-source-backfill-active-token",
			CreatedTime: 100,
		},
		{
			Id:          deletedUserTokenID,
			UserId:      deletedUserID,
			Key:         "usage-source-backfill-deleted-key",
			Name:        "usage-source-backfill-deleted-token",
			CreatedTime: 100,
		},
	}).Error)
	require.NoError(t, DB.Delete(&deletedUser).Error)

	require.NoError(t, BackfillTokenUsageSourceMeta())

	var activeMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&activeMeta, "token_id = ?", activeTokenID).Error)
	assert.True(t, activeMeta.TrackingEnabled)
	assert.GreaterOrEqual(t, activeMeta.TrackingStart, common.GetTimestamp()-1)
	var deletedMeta TokenUsageSourceMeta
	assert.ErrorIs(
		t,
		DB.First(&deletedMeta, "token_id = ?", deletedUserTokenID).Error,
		gorm.ErrRecordNotFound,
	)
}

func TestDisablingRecordIPLogPreservesOrphanedUsageSources(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const tokenID = 93017
	const userID = 94017
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id = ?", userID).Delete(&User{}).Error
	})
	user := User{Id: userID, Username: "usage-source-orphan-owner"}
	user.SetSetting(dto.UserSetting{RecordIpLog: true})
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSource{
		UserID:      userID,
		TokenID:     tokenID,
		SourceKey:   NewTokenUsageSourceKey("192.0.2.47", "client/1.0"),
		IP:          "192.0.2.47",
		UserAgent:   "client/1.0",
		FirstSeenAt: 110,
		LastSeenAt:  120,
	}).Error)

	require.NoError(t, UpdateUserSetting(userID, dto.UserSetting{RecordIpLog: false}))

	var sourceCount int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).Where("user_id = ?", userID).Count(&sourceCount).Error)
	assert.Equal(t, int64(1), sourceCount)
	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.True(t, meta.TrackingEnabled)
	assert.Equal(t, int64(100), meta.TrackingStart)
}

func TestDisabledTokenUsageSourceTrackingDoesNotReportCoverageAsTrackingStart(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const tokenID = 93006
	const userID = 94006
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID: tokenID,
		UserID:  userID,
	}).Error)
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  100,
		Watermark:      200,
		BackfillCursor: 100,
	}))
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})

	page, err := GetTokenUsageSourcePage(context.Background(), userID, tokenID, 0, 50)
	require.NoError(t, err)
	assert.False(t, page.TrackingEnabled)
	assert.Zero(t, page.TrackingStart)
}

func TestLogCleanupPreservesBackfillProgressAboveCleanupBoundary(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  50,
		Watermark:      300,
		BackfillCursor: 200,
	}))
	require.NoError(t, DB.Create(&TokenUsageSourceCountProgress{
		ID:              tokenUsageSourceCountProgressStateID,
		Direction:       TokenUsageSourceCountDirectionBackfill,
		RangeStart:      150,
		RangeEnd:        200,
		MergeStarted:    true,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
		UpdatedAt:       common.GetTimestamp(),
	}).Error)
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReconcileTokenUsageSourceStateAfterLogCleanup(tx, 100)
	}))

	state, err := GetLogStatRollupState(context.Background(), TokenUsageSourceStateName)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, int64(100), state.CoverageStart)
	assert.Equal(t, int64(200), state.BackfillCursor)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration, state.CountGeneration)

	var progress TokenUsageSourceCountProgress
	require.NoError(t, DB.First(&progress, tokenUsageSourceCountProgressStateID).Error)
	assert.Equal(t, TokenUsageSourceCountDirectionBackfill, progress.Direction)
	assert.Equal(t, int64(150), progress.RangeStart)
	assert.Equal(t, int64(200), progress.RangeEnd)
	assert.True(t, progress.MergeStarted)
}

func TestLogCleanupClearsBackfillProgressInsideDeletedRange(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  50,
		Watermark:      300,
		BackfillCursor: 80,
	}))
	require.NoError(t, DB.Create(&TokenUsageSourceCountProgress{
		ID:              tokenUsageSourceCountProgressStateID,
		Direction:       TokenUsageSourceCountDirectionBackfill,
		RangeStart:      60,
		RangeEnd:        80,
		MergeStarted:    true,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
		UpdatedAt:       common.GetTimestamp(),
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         93018,
		UserID:          94018,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, DB.Create(&TokenUsageSource{
		UserID:                94018,
		TokenID:               93018,
		SourceKey:             NewTokenUsageSourceKey("192.0.2.48", "client/1.0"),
		IP:                    "192.0.2.48",
		UserAgent:             "client/1.0",
		FirstSeenAt:           60,
		LastSeenAt:            80,
		SuccessCount:          3,
		ErrorCount:            2,
		BackfillCountedFrom:   60,
		BackfillCounted:       1,
		ForwardCountedThrough: 300,
		CountGeneration:       tokenUsageSourceInitialCountGeneration,
	}).Error)
	t.Cleanup(func() {
		_ = DB.Where("name = ?", TokenUsageSourceStateName).Delete(&LogStatRollupState{}).Error
	})

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReconcileTokenUsageSourceStateAfterLogCleanup(tx, 100)
	}))

	state, err := GetLogStatRollupState(context.Background(), TokenUsageSourceStateName)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, int64(100), state.CoverageStart)
	assert.Equal(t, int64(300), state.BackfillCursor)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration+1, state.CountGeneration)

	var progress TokenUsageSourceCountProgress
	require.NoError(t, DB.First(&progress, tokenUsageSourceCountProgressStateID).Error)
	assert.Empty(t, progress.Direction)
	assert.Zero(t, progress.RangeStart)
	assert.Zero(t, progress.RangeEnd)
	assert.False(t, progress.MergeStarted)

	var source TokenUsageSource
	require.NoError(t, DB.First(&source, "token_id = ?", 93018).Error)
	assert.Equal(t, int64(3), source.SuccessCount)
	assert.Equal(t, int64(2), source.ErrorCount)
	assert.Equal(t, int64(300), source.ForwardCountedThrough)
	assert.Equal(t, int64(60), source.BackfillCountedFrom)
	assert.Equal(t, int64(1), source.BackfillCounted)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration, source.CountGeneration)

	page, err := GetTokenUsageSourcePage(context.Background(), 94018, 93018, 0, 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Zero(t, page.Items[0].SuccessCount)
	assert.Zero(t, page.Items[0].ErrorCount)
	assert.Zero(t, page.Items[0].RequestCount)

	recount := TokenUsageSourceGroup{
		UserID:        94018,
		TokenID:       93018,
		SourceKey:     source.SourceKey,
		IP:            source.IP,
		UserAgent:     source.UserAgent,
		FirstSeenAt:   100,
		LastSeenAt:    300,
		LastSuccessAt: 300,
		LastErrorAt:   300,
		SuccessCount:  4,
		ErrorCount:    1,
	}
	require.NoError(t, MergeTokenUsageSourceCountedGroups(
		context.Background(),
		[]TokenUsageSourceGroup{recount},
		10,
		TokenUsageSourceCountRange{
			Direction:       TokenUsageSourceCountDirectionBackfill,
			Start:           100,
			End:             300,
			CountGeneration: tokenUsageSourceInitialCountGeneration + 1,
		},
	))
	require.NoError(t, DB.First(&source, "token_id = ?", 93018).Error)
	assert.Equal(t, int64(4), source.SuccessCount)
	assert.Equal(t, int64(1), source.ErrorCount)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration+1, source.CountGeneration)
}

func TestLogCleanupWhileDirectCountingPreservesActiveCountGeneration(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	settings := config.GlobalConfig.Get("token_usage_source_setting")
	originalSettings, err := config.ConfigToMap(settings)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settings, map[string]string{
		"enabled": "true",
	}))
	t.Cleanup(func() {
		_ = config.UpdateConfigFromMap(settings, originalSettings)
	})
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:            TokenUsageSourceStateName,
		CoverageStart:   50,
		Watermark:       300,
		BackfillCursor:  80,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
	}))
	require.NoError(t, DB.Create(&TokenUsageSourceCountProgress{
		ID:              tokenUsageSourceCountProgressStateID,
		Direction:       TokenUsageSourceCountDirectionBackfill,
		RangeStart:      60,
		RangeEnd:        80,
		MergeStarted:    true,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
		UpdatedAt:       common.GetTimestamp(),
	}).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReconcileTokenUsageSourceStateAfterLogCleanup(tx, 100)
	}))

	state, err := GetLogStatRollupState(context.Background(), TokenUsageSourceStateName)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, int64(100), state.CoverageStart)
	assert.Equal(t, int64(100), state.BackfillCursor)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration, state.CountGeneration)
	var progress TokenUsageSourceCountProgress
	require.NoError(t, DB.First(&progress, tokenUsageSourceCountProgressStateID).Error)
	assert.Empty(t, progress.Direction)
	assert.False(t, progress.MergeStarted)
}

func TestTokenUsageSourcePageTreatsRollingDeploymentMissingMetaAsUnavailable(t *testing.T) {
	prepareTokenUsageSourceTest(t)

	page, err := GetTokenUsageSourcePage(context.Background(), 94007, 93007, 0, 50)
	require.NoError(t, err)
	assert.False(t, page.Available)
	assert.False(t, page.TrackingEnabled)
	assert.Empty(t, page.Items)
}

func TestMergeDirectTokenUsageSourceGroupsAccumulatesAndPrunes(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const tokenID = 93030
	const userID = 94030
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)
	require.NoError(t, SaveLogStatRollupState(context.Background(), &LogStatRollupState{
		Name:            TokenUsageSourceStateName,
		CoverageStart:   100,
		Watermark:       200,
		BackfillCursor:  100,
		CountGeneration: 9,
	}))

	first := tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.1", 110, 110)
	first.SuccessCount = 2
	first.LastSuccessAt = 110
	second := tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.1", 120, 120)
	second.SuccessCount = 0
	second.LastSuccessAt = 0
	second.ErrorCount = 1
	second.LastErrorAt = 120
	require.NoError(t, MergeDirectTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{first},
		2,
	))
	require.NoError(t, MergeDirectTokenUsageSourceGroups(
		context.Background(),
		[]TokenUsageSourceGroup{second},
		2,
	))
	var accumulated TokenUsageSource
	require.NoError(t, DB.First(&accumulated, "token_id = ?", tokenID).Error)
	assert.Equal(t, int64(2), accumulated.SuccessCount)
	assert.Equal(t, int64(1), accumulated.ErrorCount)
	assert.Equal(t, tokenUsageSourceInitialCountGeneration, accumulated.CountGeneration)

	for index, timestamp := range []int64{130, 140} {
		group := tokenUsageSourceTestGroup(
			tokenID,
			userID,
			"192.0.2."+strconv.Itoa(index+2),
			timestamp,
			timestamp,
		)
		group.SuccessCount = 1
		group.LastSuccessAt = timestamp
		require.NoError(t, MergeDirectTokenUsageSourceGroups(
			context.Background(),
			[]TokenUsageSourceGroup{group},
			2,
		))
	}

	var sources []TokenUsageSource
	require.NoError(t, DB.Where("token_id = ?", tokenID).
		Order("last_seen_at").
		Find(&sources).Error)
	require.Len(t, sources, 2)
	assert.Equal(t, int64(130), sources[0].LastSeenAt)
	assert.Equal(t, int64(140), sources[1].LastSeenAt)
	var meta TokenUsageSourceMeta
	require.NoError(t, DB.First(&meta, "token_id = ?", tokenID).Error)
	assert.True(t, meta.Truncated)
}

func TestMergePendingTokenUsageSourceGroupPreservesBoundsAndCounts(t *testing.T) {
	current := TokenUsageSourceGroup{
		FirstSeenAt:   120,
		LastSeenAt:    130,
		LastSuccessAt: 130,
		SuccessCount:  2,
	}
	incoming := TokenUsageSourceGroup{
		IP:           "192.0.2.8",
		UserAgent:    "client/2.0",
		FirstSeenAt:  110,
		LastSeenAt:   140,
		LastErrorAt:  140,
		SuccessCount: 1,
		ErrorCount:   3,
	}

	merged := mergePendingTokenUsageSourceGroup(current, incoming)

	assert.Equal(t, int64(110), merged.FirstSeenAt)
	assert.Equal(t, int64(140), merged.LastSeenAt)
	assert.Equal(t, int64(130), merged.LastSuccessAt)
	assert.Equal(t, int64(140), merged.LastErrorAt)
	assert.Equal(t, int64(3), merged.SuccessCount)
	assert.Equal(t, int64(3), merged.ErrorCount)
	assert.Equal(t, "192.0.2.8", merged.IP)
	assert.Equal(t, "client/2.0", merged.UserAgent)
}

func TestAddPendingTokenUsageSourceGroupEnforcesLimitAfterMergingExistingIdentity(t *testing.T) {
	first := tokenUsageSourceTestGroup(93031, 94031, "192.0.2.31", 110, 110)
	second := tokenUsageSourceTestGroup(93032, 94032, "192.0.2.32", 120, 120)
	pending := make(map[string]TokenUsageSourceGroup)

	require.True(t, addPendingTokenUsageSourceGroup(pending, first, 2))
	require.True(t, addPendingTokenUsageSourceGroup(pending, second, 2))

	retry := first
	retry.LastSeenAt = 130
	retry.LastSuccessAt = 130
	retry.SuccessCount = 2
	require.True(t, addPendingTokenUsageSourceGroup(pending, retry, 2))
	assert.Len(t, pending, 2)
	assert.Equal(
		t,
		int64(3),
		pending[tokenUsageSourcePendingKey(first)].SuccessCount,
	)

	overflow := tokenUsageSourceTestGroup(93033, 94033, "192.0.2.33", 140, 140)
	assert.False(t, addPendingTokenUsageSourceGroup(pending, overflow, 2))
	assert.Len(t, pending, 2)
}

func TestFlushTokenUsageSourceBatchRequeuesFailedDatabaseBatch(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&LogStatRollupState{}))
	const tokenID = 93031
	const userID = 94031
	require.NoError(t, DB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   100,
	}).Error)

	buffer := &directTokenUsageSourceBuffer
	buffer.mu.Lock()
	require.Empty(t, buffer.pending)
	buffer.mu.Unlock()
	group := tokenUsageSourceTestGroup(tokenID, userID, "192.0.2.31", 110, 110)
	requeueTokenUsageSourceGroups([]TokenUsageSourceGroup{group})

	originalDB := DB
	DB = nil
	err := FlushTokenUsageSourceBatch(context.Background())
	DB = originalDB
	require.Error(t, err)
	buffer.mu.Lock()
	assert.Len(t, buffer.pending, 1)
	buffer.mu.Unlock()

	require.NoError(t, FlushTokenUsageSourceBatch(context.Background()))
	var source TokenUsageSource
	require.NoError(t, DB.First(&source, "token_id = ?", tokenID).Error)
	assert.Equal(t, int64(1), source.SuccessCount)
	buffer.mu.Lock()
	assert.Empty(t, buffer.pending)
	buffer.mu.Unlock()
}

func TestReconcileTokenUsageSourceMetaConvergesMixedVersionChanges(t *testing.T) {
	prepareTokenUsageSourceTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	const enabledUserID = 94008
	const disabledUserID = 94009
	const deletedUserID = 94010
	const missingMetaTokenID = 93008
	const deletedTokenID = 93009
	const disabledUserTokenID = 93010
	const reenabledTokenID = 93011
	const hardDeletedTokenID = 93012
	const deletedUserTokenID = 93013

	enabledUser := User{
		Id:       enabledUserID,
		Username: "usage-source-reconcile-enabled",
		AffCode:  "source-reconcile-enabled",
	}
	enabledUser.SetSetting(dto.UserSetting{RecordIpLog: true})
	disabledUser := User{
		Id:       disabledUserID,
		Username: "usage-source-reconcile-disabled",
		AffCode:  "source-reconcile-disabled",
	}
	disabledUser.SetSetting(dto.UserSetting{RecordIpLog: false})
	deletedUser := User{
		Id:       deletedUserID,
		Username: "usage-source-reconcile-deleted-user",
		AffCode:  "source-reconcile-deleted-user",
	}
	deletedUser.SetSetting(dto.UserSetting{RecordIpLog: true})
	require.NoError(t, DB.Create(&[]User{enabledUser, disabledUser, deletedUser}).Error)

	tokens := []Token{
		{Id: missingMetaTokenID, UserId: enabledUserID, Key: "reconcile-missing-meta", Name: "missing-meta", CreatedTime: 100},
		{Id: deletedTokenID, UserId: enabledUserID, Key: "reconcile-deleted", Name: "deleted", CreatedTime: 100},
		{Id: disabledUserTokenID, UserId: disabledUserID, Key: "reconcile-disabled", Name: "disabled", CreatedTime: 100},
		{Id: reenabledTokenID, UserId: enabledUserID, Key: "reconcile-reenabled", Name: "reenabled", CreatedTime: 100},
		{Id: deletedUserTokenID, UserId: deletedUserID, Key: "reconcile-deleted-user", Name: "deleted-user", CreatedTime: 100},
	}
	require.NoError(t, DB.Create(&tokens).Error)
	require.NoError(t, DB.Delete(&tokens[1]).Error)
	require.NoError(t, DB.Delete(&deletedUser).Error)
	require.NoError(t, DB.Create(&[]TokenUsageSourceMeta{
		{TokenID: deletedTokenID, UserID: enabledUserID, TrackingEnabled: true, TrackingStart: 100},
		{TokenID: disabledUserTokenID, UserID: disabledUserID, TrackingEnabled: true, TrackingStart: 100},
		{TokenID: reenabledTokenID, UserID: enabledUserID, TrackingEnabled: false},
		{TokenID: hardDeletedTokenID, UserID: enabledUserID, TrackingEnabled: true, TrackingStart: 100},
		{TokenID: deletedUserTokenID, UserID: deletedUserID, TrackingEnabled: true, TrackingStart: 100},
	}).Error)
	for _, tokenID := range []int{
		missingMetaTokenID,
		deletedTokenID,
		disabledUserTokenID,
		hardDeletedTokenID,
		deletedUserTokenID,
	} {
		require.NoError(t, DB.Create(&TokenUsageSource{
			UserID: map[int]int{
				missingMetaTokenID:  enabledUserID,
				deletedTokenID:      enabledUserID,
				disabledUserTokenID: disabledUserID,
				hardDeletedTokenID:  enabledUserID,
				deletedUserTokenID:  deletedUserID,
			}[tokenID],
			TokenID:     tokenID,
			SourceKey:   NewTokenUsageSourceKey("192.0.2.1", "client/1.0"),
			IP:          "192.0.2.1",
			UserAgent:   "client/1.0",
			FirstSeenAt: 110,
			LastSeenAt:  120,
		}).Error)
	}
	t.Cleanup(func() {
		_ = DB.Unscoped().Where("id IN ?", []int{
			missingMetaTokenID,
			deletedTokenID,
			disabledUserTokenID,
			reenabledTokenID,
			deletedUserTokenID,
		}).Delete(&Token{}).Error
		_ = DB.Unscoped().Where("id IN ?", []int{
			enabledUserID,
			disabledUserID,
			deletedUserID,
		}).Delete(&User{}).Error
	})

	var completed bool
	for i := 0; i < 10 && !completed; i++ {
		result, err := ReconcileTokenUsageSourceMetaBatch(context.Background(), 2)
		require.NoError(t, err)
		completed = result.CycleCompleted
	}
	require.True(t, completed)

	var missingMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&missingMeta, "token_id = ?", missingMetaTokenID).Error)
	assert.True(t, missingMeta.TrackingEnabled)
	assert.GreaterOrEqual(t, missingMeta.TrackingStart, common.GetTimestamp()-1)

	var deletedMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&deletedMeta, "token_id = ?", deletedTokenID).Error)
	assert.Positive(t, deletedMeta.PurgedAt)
	assert.False(t, deletedMeta.TrackingEnabled)

	var disabledMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&disabledMeta, "token_id = ?", disabledUserTokenID).Error)
	assert.Zero(t, disabledMeta.PurgedAt)
	assert.True(t, disabledMeta.TrackingEnabled)
	assert.Equal(t, int64(100), disabledMeta.TrackingStart)

	var hardDeletedMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&hardDeletedMeta, "token_id = ?", hardDeletedTokenID).Error)
	assert.Positive(t, hardDeletedMeta.PurgedAt)
	assert.False(t, hardDeletedMeta.TrackingEnabled)

	var deletedUserMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&deletedUserMeta, "token_id = ?", deletedUserTokenID).Error)
	assert.Positive(t, deletedUserMeta.PurgedAt)
	assert.False(t, deletedUserMeta.TrackingEnabled)

	var reenabledMeta TokenUsageSourceMeta
	require.NoError(t, DB.First(&reenabledMeta, "token_id = ?", reenabledTokenID).Error)
	assert.True(t, reenabledMeta.TrackingEnabled)
	assert.Positive(t, reenabledMeta.TrackingStart)

	var sourceCount int64
	require.NoError(t, DB.Model(&TokenUsageSource{}).
		Where("token_id IN ?", []int{
			missingMetaTokenID,
			deletedTokenID,
			disabledUserTokenID,
			hardDeletedTokenID,
			deletedUserTokenID,
		}).
		Count(&sourceCount).Error)
	assert.Equal(t, int64(1), sourceCount)
}
