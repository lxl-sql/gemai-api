package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareTokenUsageSourceTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&TokenUsageSource{},
		&TokenUsageSourceMeta{},
		&TokenUsageSourceReconcileState{},
	))
	require.NoError(t, DB.Exec("DELETE FROM token_usage_sources").Error)
	require.NoError(t, DB.Exec("DELETE FROM token_usage_source_meta").Error)
	require.NoError(t, DB.Exec("DELETE FROM token_usage_source_reconcile_states").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM token_usage_sources").Error
		_ = DB.Exec("DELETE FROM token_usage_source_meta").Error
		_ = DB.Exec("DELETE FROM token_usage_source_reconcile_states").Error
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
	}
}

func TestMergeTokenUsageSourceGroupsIsIdempotentAndKeepsRecentTopK(t *testing.T) {
	prepareTokenUsageSourceTest(t)
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

func TestRecordIPLogSettingDoesNotAffectUsageSources(t *testing.T) {
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

func TestTokenUsageSourcePageTreatsRollingDeploymentMissingMetaAsUnavailable(t *testing.T) {
	prepareTokenUsageSourceTest(t)

	page, err := GetTokenUsageSourcePage(context.Background(), 94007, 93007, 0, 50)
	require.NoError(t, err)
	assert.False(t, page.Available)
	assert.False(t, page.TrackingEnabled)
	assert.Empty(t, page.Items)
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
	assert.Equal(t, int64(100), missingMeta.TrackingStart)

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
	assert.Equal(t, int64(100), reenabledMeta.TrackingStart)

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
	var remainingSource TokenUsageSource
	require.NoError(t, DB.First(&remainingSource).Error)
	assert.Equal(t, disabledUserTokenID, remainingSource.TokenID)
}
