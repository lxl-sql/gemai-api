package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenSecurityPolicyNormalizesBurstAndRiskMode(t *testing.T) {
	policy := &TokenSecurityPolicy{
		TokenId:       1,
		SustainedRps:  100,
		BurstCapacity: 20,
		RiskMode:      "unknown",
	}

	require.NoError(t, policy.Validate())
	assert.Equal(t, 100, policy.BurstCapacity)
	assert.Equal(t, TokenRiskModeObserve, policy.RiskMode)
}

func TestDefaultTokenSecurityPolicyDoesNotImplicitlyLimitNewTokens(t *testing.T) {
	policy := DefaultTokenSecurityPolicy()

	assert.Zero(t, policy.SustainedRps)
	assert.Zero(t, policy.BurstCapacity)
	assert.Zero(t, policy.MaxConcurrency)
	assert.Zero(t, policy.MaxQuotaPerRequest)
	assert.Zero(t, policy.HourlyQuota)
	assert.Zero(t, policy.DailyQuota)
	assert.Zero(t, policy.MaxDistinctModels5m)
	assert.Equal(t, TokenRiskModeObserve, policy.RiskMode)
	assert.False(t, policy.FailClosed)
}

func TestInsertWithNilSecurityPolicyDoesNotCreateImplicitLimits(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &Token{
		UserId:         7201,
		Key:            "new-token-without-security-policy",
		Name:           "new-token-without-security-policy",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.InsertWithSecurityPolicy(nil))

	var policyCount int64
	require.NoError(t, DB.Model(&TokenSecurityPolicy{}).
		Where("token_id = ?", token.Id).
		Count(&policyCount).Error)
	assert.Zero(t, policyCount)
}

func TestSuspendTokenForRiskReportsCommittedCacheFailure(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	token := &Token{
		UserId:         7202,
		Key:            "risk-suspension-cache-failure-key",
		Name:           "risk-suspension-cache-failure",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	err := SuspendTokenForRiskByID(token.Id)
	require.Error(t, err)
	assert.True(t, TokenRiskSuspensionCommitted(err))

	var stored Token
	require.NoError(t, DB.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestTokenSecurityPolicyUsesAdministratorBoundaryAndInheritance(t *testing.T) {
	requested := &TokenSecurityPolicy{
		TokenId:             9,
		SustainedRps:        0,
		BurstCapacity:       0,
		MaxConcurrency:      80,
		MaxQuotaPerRequest:  0,
		HourlyQuota:         200,
		DailyQuota:          0,
		MaxDistinctModels5m: 50,
		RiskMode:            TokenRiskModeObserve,
		FailClosed:          true,
	}
	profile := &TokenSecurityProfile{
		SustainedRps:        100,
		BurstCapacity:       500,
		MaxConcurrency:      40,
		MaxQuotaPerRequest:  1000,
		HourlyQuota:         5000,
		DailyQuota:          20000,
		MaxDistinctModels5m: 20,
		MinimumRiskMode:     TokenRiskModeNotify,
	}

	effective := MergeTokenSecurityPolicy(requested, profile)

	assert.Equal(t, 100, effective.SustainedRps)
	assert.Equal(t, 500, effective.BurstCapacity)
	assert.Equal(t, 40, effective.MaxConcurrency)
	assert.Equal(t, int64(1000), effective.MaxQuotaPerRequest)
	assert.Equal(t, int64(200), effective.HourlyQuota)
	assert.Equal(t, int64(20000), effective.DailyQuota)
	assert.Equal(t, 20, effective.MaxDistinctModels5m)
	assert.Equal(t, TokenRiskModeNotify, effective.RiskMode)
	assert.False(t, effective.FailClosed)
}

func TestTokenSecurityPolicyUsesAdministratorEnterpriseCapacityExactly(t *testing.T) {
	requested := DefaultTokenSecurityPolicy()
	profile := &TokenSecurityProfile{
		SustainedRps:    5000,
		BurstCapacity:   10000,
		MaxConcurrency:  2000,
		MinimumRiskMode: TokenRiskModeObserve,
	}

	effective := MergeTokenSecurityPolicy(requested, profile)

	assert.Equal(t, 5000, effective.SustainedRps)
	assert.Equal(t, 10000, effective.BurstCapacity)
	assert.Equal(t, 2000, effective.MaxConcurrency)
}

func TestTokenSecurityPolicyBuiltInProfilePreservesLegacyCapacity(t *testing.T) {
	requested := &TokenSecurityPolicy{
		SustainedRps:        120,
		BurstCapacity:       600,
		MaxConcurrency:      80,
		MaxDistinctModels5m: 40,
		RiskMode:            TokenRiskModeNotify,
		FailClosed:          true,
	}

	effective := MergeTokenSecurityPolicy(requested, BuiltInTokenSecurityProfile())

	assert.Equal(t, requested.SustainedRps, effective.SustainedRps)
	assert.Equal(t, requested.BurstCapacity, effective.BurstCapacity)
	assert.Equal(t, requested.MaxConcurrency, effective.MaxConcurrency)
	assert.Equal(t, requested.MaxDistinctModels5m, effective.MaxDistinctModels5m)
	assert.True(t, effective.FailClosed)
}

func TestApplicableTokenSecurityProfileUsesMostSpecificScope(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	user := &User{Id: 7101, Username: "profile-user", Group: "vip"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, UpsertTokenSecurityProfile(&TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		SustainedRps:    5,
		BurstCapacity:   25,
		MinimumRiskMode: TokenRiskModeSuspend,
	}))
	require.NoError(t, UpsertTokenSecurityProfile(&TokenSecurityProfile{
		ScopeType:       TokenSecurityScopeGroup,
		ScopeValue:      "vip",
		SustainedRps:    100,
		BurstCapacity:   500,
		MinimumRiskMode: TokenRiskModeNotify,
	}))

	groupProfile, err := GetApplicableTokenSecurityProfile(user.Id, user.Group)
	require.NoError(t, err)
	assert.Equal(t, TokenSecurityScopeGroup, groupProfile.ScopeType)
	assert.Equal(t, 100, groupProfile.SustainedRps)

	require.NoError(t, UpsertTokenSecurityProfile(&TokenSecurityProfile{
		ScopeType:       TokenSecurityScopeUser,
		ScopeValue:      "7101",
		SustainedRps:    200,
		BurstCapacity:   600,
		MinimumRiskMode: TokenRiskModeObserve,
	}))
	userProfile, err := GetApplicableTokenSecurityProfile(user.Id, user.Group)
	require.NoError(t, err)
	assert.Equal(t, TokenSecurityScopeUser, userProfile.ScopeType)
	assert.Equal(t, 200, userProfile.SustainedRps)
}

func TestTokenSecurityProfileRejectsUnknownGroup(t *testing.T) {
	profile := &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopeGroup,
		ScopeValue:      "group-that-does-not-exist",
		MinimumRiskMode: TokenRiskModeObserve,
	}

	require.Error(t, profile.Validate())
}

func TestTokenSecurityProfileUpsertIgnoresClientManagedFields(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	platform := &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		SustainedRps:    5,
		BurstCapacity:   25,
		MinimumRiskMode: TokenRiskModeObserve,
	}
	require.NoError(t, UpsertTokenSecurityProfile(platform))
	require.Positive(t, platform.Id)

	group := &TokenSecurityProfile{
		Id:              platform.Id,
		ScopeType:       TokenSecurityScopeGroup,
		ScopeValue:      "vip",
		SustainedRps:    100,
		BurstCapacity:   500,
		MinimumRiskMode: TokenRiskModeNotify,
		BuiltIn:         true,
	}
	require.NoError(t, UpsertTokenSecurityProfile(group))
	require.NotEqual(t, platform.Id, group.Id)

	var storedPlatform TokenSecurityProfile
	require.NoError(t, DB.First(&storedPlatform, platform.Id).Error)
	assert.Equal(t, TokenSecurityScopePlatform, storedPlatform.ScopeType)
	assert.Equal(t, 5, storedPlatform.SustainedRps)
}

func TestCreateTokenSecurityProfileDoesNotOverwriteExistingTarget(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	original := &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		SustainedRps:    5,
		BurstCapacity:   25,
		MinimumRiskMode: TokenRiskModeObserve,
	}
	require.NoError(t, CreateTokenSecurityProfile(original))

	duplicate := &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		SustainedRps:    100,
		BurstCapacity:   500,
		MinimumRiskMode: TokenRiskModeSuspend,
	}
	require.Error(t, CreateTokenSecurityProfile(duplicate))

	var stored TokenSecurityProfile
	require.NoError(t, DB.First(&stored, original.Id).Error)
	assert.Equal(t, 5, stored.SustainedRps)
	assert.Equal(t, 25, stored.BurstCapacity)
	assert.Equal(t, TokenRiskModeObserve, stored.MinimumRiskMode)
}

func TestTokenSecurityProfileReportsCacheSynchronizationFailureAfterCommit(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	profile := &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		SustainedRps:    10,
		BurstCapacity:   20,
		MinimumRiskMode: TokenRiskModeObserve,
	}
	require.NoError(t, UpsertTokenSecurityProfile(profile))
	assert.False(t, profile.CacheSynchronized)

	var stored TokenSecurityProfile
	require.NoError(t, DB.First(&stored, profile.Id).Error)
	assert.Equal(t, 10, stored.SustainedRps)

	cacheSynchronized, err := DeleteTokenSecurityProfile(
		TokenSecurityScopePlatform,
		"",
	)
	require.NoError(t, err)
	assert.False(t, cacheSynchronized)
	require.ErrorIs(t, DB.First(&stored, profile.Id).Error, gorm.ErrRecordNotFound)
}

func TestUpsertTokenSecurityPolicyRequiresTokenOwnership(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &Token{
		UserId:         7001,
		Key:            "security-policy-owner-key",
		Name:           "security-policy-owner",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	policy := &TokenSecurityPolicy{
		TokenId:             token.Id,
		SustainedRps:        100,
		BurstCapacity:       500,
		MaxConcurrency:      800,
		MaxDistinctModels5m: 40,
		RiskMode:            TokenRiskModeSuspend,
		FailClosed:          true,
	}

	require.Error(t, UpsertTokenSecurityPolicy(policy, 7002))
	require.NoError(t, UpsertTokenSecurityPolicy(policy, 7001))

	stored, err := GetTokenSecurityPolicy(token.Id)
	require.NoError(t, err)
	assert.Equal(t, 100, stored.SustainedRps)
	assert.Equal(t, 500, stored.BurstCapacity)
	assert.Equal(t, 800, stored.MaxConcurrency)
	assert.Equal(t, TokenRiskModeSuspend, stored.RiskMode)
	assert.True(t, stored.FailClosed)
}

func TestResetUserWritableTokenSecurityPolicyPreservesAdministratorFields(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &Token{
		UserId:         7007,
		Key:            "user-policy-reset-key",
		Name:           "user-policy-reset",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, DB.Create(&TokenSecurityPolicy{
		TokenId:             token.Id,
		SustainedRps:        5000,
		BurstCapacity:       10000,
		MaxConcurrency:      2000,
		MaxQuotaPerRequest:  100,
		HourlyQuota:         500,
		DailyQuota:          1000,
		MaxDistinctModels5m: 100,
		RiskMode:            TokenRiskModeSuspend,
		FailClosed:          true,
	}).Error)

	_, err := ResetUserWritableTokenSecurityPolicy(token.Id, token.UserId+1)
	require.Error(t, err)
	resetPolicy, err := ResetUserWritableTokenSecurityPolicy(token.Id, token.UserId)
	require.NoError(t, err)
	require.NotNil(t, resetPolicy)
	assert.Zero(t, resetPolicy.MaxQuotaPerRequest)
	assert.Zero(t, resetPolicy.HourlyQuota)
	assert.Zero(t, resetPolicy.DailyQuota)
	assert.Equal(t, TokenRiskModeObserve, resetPolicy.RiskMode)

	stored, err := GetTokenSecurityPolicy(token.Id)
	require.NoError(t, err)
	assert.Equal(t, 5000, stored.SustainedRps)
	assert.Equal(t, 10000, stored.BurstCapacity)
	assert.Equal(t, 2000, stored.MaxConcurrency)
	assert.Equal(t, 100, stored.MaxDistinctModels5m)
	assert.True(t, stored.FailClosed)
	assert.Zero(t, stored.MaxQuotaPerRequest)
	assert.Zero(t, stored.HourlyQuota)
	assert.Zero(t, stored.DailyQuota)
	assert.Equal(t, TokenRiskModeObserve, stored.RiskMode)
}

func TestTokenUpdateWithSecurityPolicyCommitsTogether(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &Token{
		UserId:         7003,
		Key:            "combined-token-policy-key",
		Name:           "before",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	token.Name = "after"

	require.NoError(t, token.UpdateWithSecurityPolicy(&TokenSecurityPolicy{
		SustainedRps:  50,
		BurstCapacity: 100,
		RiskMode:      TokenRiskModeNotify,
	}))

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, "after", storedToken.Name)

	var storedPolicy TokenSecurityPolicy
	require.NoError(t, DB.First(&storedPolicy, "token_id = ?", token.Id).Error)
	assert.Equal(t, 50, storedPolicy.SustainedRps)
	assert.Equal(t, TokenRiskModeNotify, storedPolicy.RiskMode)
}

func TestTokenUpdateWithSecurityPolicyDoesNotReportFailureAfterCommit(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &Token{
		UserId:         7004,
		Key:            "committed-cache-failure-key",
		Name:           "before",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	token.Name = "after"

	require.NoError(t, token.UpdateWithSecurityPolicy(&TokenSecurityPolicy{
		SustainedRps:  25,
		BurstCapacity: 50,
		RiskMode:      TokenRiskModeObserve,
	}))

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, "after", storedToken.Name)

	var storedPolicy TokenSecurityPolicy
	require.NoError(t, DB.First(&storedPolicy, "token_id = ?", token.Id).Error)
	assert.Equal(t, 25, storedPolicy.SustainedRps)
}

func TestStandaloneTokenSecurityPolicyChangesDoNotReportCacheFailureAfterCommit(t *testing.T) {
	truncateTables(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &Token{
		UserId:         7005,
		Key:            "standalone-policy-cache-failure-key",
		Name:           "standalone-policy-cache-failure",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	policy := &TokenSecurityPolicy{
		TokenId:            token.Id,
		SustainedRps:       20,
		BurstCapacity:      40,
		HourlyQuota:        100,
		DailyQuota:         200,
		MaxQuotaPerRequest: 50,
		RiskMode:           TokenRiskModeSuspend,
	}
	require.NoError(t, UpsertTokenSecurityPolicy(policy, token.UserId))
	require.NotNil(t, policy.CacheSynchronized)
	assert.False(t, *policy.CacheSynchronized)

	var storedPolicy TokenSecurityPolicy
	require.NoError(t, DB.First(&storedPolicy, "token_id = ?", token.Id).Error)
	assert.Equal(t, 20, storedPolicy.SustainedRps)

	resetPolicy, err := ResetUserWritableTokenSecurityPolicy(token.Id, token.UserId)
	require.NoError(t, err)
	require.NotNil(t, resetPolicy.CacheSynchronized)
	assert.False(t, *resetPolicy.CacheSynchronized)

	require.NoError(t, DB.First(&storedPolicy, "token_id = ?", token.Id).Error)
	assert.Equal(t, 20, storedPolicy.SustainedRps)
	assert.Equal(t, int64(0), storedPolicy.MaxQuotaPerRequest)
	assert.Equal(t, int64(0), storedPolicy.HourlyQuota)
	assert.Equal(t, int64(0), storedPolicy.DailyQuota)
	assert.Equal(t, TokenRiskModeObserve, storedPolicy.RiskMode)
}
