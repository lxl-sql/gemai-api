package model

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	TokenRiskModeObserve = "observe"
	TokenRiskModeNotify  = "notify"
	TokenRiskModeSuspend = "suspend"
)

type TokenSecurityPolicy struct {
	TokenId             int    `json:"token_id" gorm:"primaryKey;column:token_id"`
	SustainedRps        int    `json:"sustained_rps"`
	BurstCapacity       int    `json:"burst_capacity"`
	MaxConcurrency      int    `json:"max_concurrency"`
	MaxQuotaPerRequest  int64  `json:"max_quota_per_request" gorm:"type:bigint"`
	HourlyQuota         int64  `json:"hourly_quota" gorm:"type:bigint"`
	DailyQuota          int64  `json:"daily_quota" gorm:"type:bigint"`
	MaxDistinctModels5m int    `json:"max_distinct_models_5m"`
	RiskMode            string `json:"risk_mode" gorm:"type:varchar(16)"`
	FailClosed          bool   `json:"fail_closed"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	UpdatedAt           int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
	CacheSynchronized   *bool  `json:"cache_synchronized,omitempty" gorm:"-"`
}

type TokenSecurityPolicyView struct {
	*TokenSecurityPolicy
	AdminProfile    *TokenSecurityProfile `json:"admin_profile"`
	EffectivePolicy *TokenSecurityPolicy  `json:"effective_policy"`
}

func DefaultTokenSecurityPolicy() *TokenSecurityPolicy {
	return &TokenSecurityPolicy{
		RiskMode:   TokenRiskModeObserve,
		FailClosed: false,
	}
}

func (policy *TokenSecurityPolicy) Normalize() {
	if policy.RiskMode != TokenRiskModeNotify && policy.RiskMode != TokenRiskModeSuspend {
		policy.RiskMode = TokenRiskModeObserve
	}
	if policy.SustainedRps == 0 {
		policy.BurstCapacity = 0
	} else if policy.BurstCapacity < policy.SustainedRps {
		policy.BurstCapacity = policy.SustainedRps
	}
}

func (policy *TokenSecurityPolicy) Validate() error {
	if policy == nil || policy.TokenId <= 0 {
		return errors.New("invalid token security policy")
	}
	if policy.SustainedRps < 0 || policy.SustainedRps > 100000 {
		return errors.New("sustained_rps is outside the supported range")
	}
	if policy.BurstCapacity < 0 || policy.BurstCapacity > 1000000 {
		return errors.New("burst_capacity is outside the supported range")
	}
	if policy.MaxConcurrency < 0 || policy.MaxConcurrency > 1000000 {
		return errors.New("max_concurrency is outside the supported range")
	}
	if policy.MaxQuotaPerRequest < 0 || policy.HourlyQuota < 0 || policy.DailyQuota < 0 {
		return errors.New("quota security limits cannot be negative")
	}
	if policy.MaxQuotaPerRequest > math.MaxInt32 {
		return errors.New("max_quota_per_request is outside the supported range")
	}
	const maxExactRedisInteger = int64(1<<53 - 1)
	if policy.HourlyQuota > maxExactRedisInteger || policy.DailyQuota > maxExactRedisInteger {
		return errors.New("window quota limit is outside the supported range")
	}
	if policy.MaxDistinctModels5m < 0 || policy.MaxDistinctModels5m > 10000 {
		return errors.New("max_distinct_models_5m is outside the supported range")
	}
	policy.Normalize()
	return nil
}

func tokenSecurityRiskRank(mode string) int {
	switch mode {
	case TokenRiskModeSuspend:
		return 2
	case TokenRiskModeNotify:
		return 1
	default:
		return 0
	}
}

func stricterTokenSecurityLimit(requested int64, administratorMaximum int64) int64 {
	if requested <= 0 {
		return administratorMaximum
	}
	if administratorMaximum <= 0 || requested < administratorMaximum {
		return requested
	}
	return administratorMaximum
}

func applyAdministratorManagedTokenSecurity(
	effective *TokenSecurityPolicy,
	requested *TokenSecurityPolicy,
	profile *TokenSecurityProfile,
) {
	if profile.BuiltIn {
		effective.SustainedRps = requested.SustainedRps
		effective.BurstCapacity = requested.BurstCapacity
		effective.MaxConcurrency = requested.MaxConcurrency
		effective.MaxDistinctModels5m = requested.MaxDistinctModels5m
		effective.FailClosed = requested.FailClosed
		return
	}
	effective.SustainedRps = profile.SustainedRps
	effective.BurstCapacity = profile.BurstCapacity
	effective.MaxConcurrency = profile.MaxConcurrency
	effective.MaxDistinctModels5m = profile.MaxDistinctModels5m
	effective.FailClosed = profile.FailClosed
}

// MergeTokenSecurityPolicy applies an administrator profile as a hard upper
// boundary. Traffic capacity, model breadth, and Redis outage behavior are
// administrator-managed when a stored profile applies. The built-in fallback
// preserves legacy token values until an administrator creates a profile.
// Token owners may only tighten spending budgets and risk response.
func MergeTokenSecurityPolicy(
	requested *TokenSecurityPolicy,
	profile *TokenSecurityProfile,
) *TokenSecurityPolicy {
	if requested == nil {
		requested = &TokenSecurityPolicy{RiskMode: TokenRiskModeObserve}
	}
	if profile == nil {
		profile = BuiltInTokenSecurityProfile()
	}
	effective := &TokenSecurityPolicy{
		TokenId: requested.TokenId,
		MaxQuotaPerRequest: stricterTokenSecurityLimit(
			requested.MaxQuotaPerRequest,
			profile.MaxQuotaPerRequest,
		),
		HourlyQuota: stricterTokenSecurityLimit(
			requested.HourlyQuota,
			profile.HourlyQuota,
		),
		DailyQuota: stricterTokenSecurityLimit(
			requested.DailyQuota,
			profile.DailyQuota,
		),
		RiskMode: requested.RiskMode,
	}
	applyAdministratorManagedTokenSecurity(effective, requested, profile)
	if tokenSecurityRiskRank(effective.RiskMode) < tokenSecurityRiskRank(profile.MinimumRiskMode) {
		effective.RiskMode = profile.MinimumRiskMode
	}
	effective.Normalize()
	return effective
}

func tokenSecurityPolicyCacheKey(tokenId int) string {
	return fmt.Sprintf("token-security-policy:%d", tokenId)
}

func cacheSetTokenSecurityPolicy(policy *TokenSecurityPolicy) error {
	return common.RedisHSetObj(
		tokenSecurityPolicyCacheKey(policy.TokenId),
		policy,
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
}

func syncCommittedTokenSecurityPolicyCache(token *Token, policy *TokenSecurityPolicy) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	var cacheErr error
	if policy == nil {
		cacheErr = common.RedisDelKey(tokenSecurityPolicyCacheKey(token.Id))
	} else {
		cacheErr = cacheSetTokenSecurityPolicy(policy)
		if cacheErr != nil {
			if deleteErr := common.RedisDelKey(tokenSecurityPolicyCacheKey(token.Id)); deleteErr == nil {
				cacheErr = nil
			}
		}
	}
	if cacheErr == nil {
		return nil
	}
	if disableErr := cacheMarkTokenDisabled(token); disableErr != nil {
		return errors.Join(cacheErr, disableErr)
	}
	return fmt.Errorf("credential cache was disabled after policy cache synchronization failed: %w", cacheErr)
}

func GetTokenSecurityPolicy(tokenId int) (*TokenSecurityPolicy, error) {
	if tokenId <= 0 {
		return nil, errors.New("invalid token id")
	}
	if common.RedisEnabled && common.RDB != nil {
		var cached TokenSecurityPolicy
		if err := common.RedisHGetObj(tokenSecurityPolicyCacheKey(tokenId), &cached); err == nil {
			cached.Normalize()
			return &cached, nil
		}
	}

	policy := &TokenSecurityPolicy{TokenId: tokenId, RiskMode: TokenRiskModeObserve}
	err := DB.Where("token_id = ?", tokenId).First(policy).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	policy.Normalize()
	if common.RedisEnabled && common.RDB != nil {
		if err := cacheSetTokenSecurityPolicy(policy); err != nil {
			common.SysLog("failed to cache token security policy: " + err.Error())
		}
	}
	return policy, nil
}

func GetEffectiveTokenSecurityPolicy(
	tokenId int,
	userId int,
	userGroup string,
) (*TokenSecurityPolicyView, error) {
	requested, err := GetTokenSecurityPolicy(tokenId)
	if err != nil {
		return nil, err
	}
	profile, err := GetApplicableTokenSecurityProfile(userId, userGroup)
	if err != nil {
		return nil, err
	}
	return &TokenSecurityPolicyView{
		TokenSecurityPolicy: requested,
		AdminProfile:        profile,
		EffectivePolicy:     MergeTokenSecurityPolicy(requested, profile),
	}, nil
}

func UpsertTokenSecurityPolicy(policy *TokenSecurityPolicy, userId int) error {
	if policy == nil || userId <= 0 {
		return errors.New("invalid token security policy update")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	var token Token
	if err := DB.Select([]string{"id", "key", "key_hash"}).
		Where("id = ? AND user_id = ?", policy.TokenId, userId).
		First(&token).Error; err != nil {
		return err
	}
	if err := DB.Save(policy).Error; err != nil {
		return err
	}
	if err := syncCommittedTokenSecurityPolicyCache(&token, policy); err != nil {
		cacheSynchronized := false
		policy.CacheSynchronized = &cacheSynchronized
		common.SysError(fmt.Sprintf(
			"token security policy committed with degraded cache recovery token_id=%d error=%v",
			policy.TokenId, err,
		))
	}
	return nil
}

func ResetUserWritableTokenSecurityPolicy(tokenId int, userId int) (*TokenSecurityPolicy, error) {
	if _, err := GetTokenByIds(tokenId, userId); err != nil {
		return nil, err
	}
	policy, err := GetTokenSecurityPolicy(tokenId)
	if err != nil {
		return nil, err
	}
	policy.MaxQuotaPerRequest = 0
	policy.HourlyQuota = 0
	policy.DailyQuota = 0
	policy.RiskMode = TokenRiskModeObserve
	if err := UpsertTokenSecurityPolicy(policy, userId); err != nil {
		return nil, err
	}
	return policy, nil
}
