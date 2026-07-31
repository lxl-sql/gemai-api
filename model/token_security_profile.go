package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

const (
	TokenSecurityScopePlatform = "platform"
	TokenSecurityScopeGroup    = "group"
	TokenSecurityScopeUser     = "user"

	tokenSecurityProfileCachePrefix = "token-security-profile:v2"
	tokenSecurityProfileCacheTag    = "{profiles}"
	tokenSecurityProfileCacheEmpty  = "-"
)

// TokenSecurityProfile is an administrator-owned upper-bound profile.
// More specific scopes replace less specific scopes as a complete profile:
// user > group > platform > built-in unrestricted profile.
type TokenSecurityProfile struct {
	Id                  int    `json:"id"`
	ScopeType           string `json:"scope_type" gorm:"type:varchar(16);uniqueIndex:idx_token_security_profile_scope"`
	ScopeValue          string `json:"scope_value" gorm:"type:varchar(128);uniqueIndex:idx_token_security_profile_scope"`
	SustainedRps        int    `json:"sustained_rps"`
	SustainedRpm        int    `json:"sustained_rpm" gorm:"default:0"`
	BurstCapacity       int    `json:"burst_capacity"`
	MaxConcurrency      int    `json:"max_concurrency"`
	MaxQuotaPerRequest  int64  `json:"max_quota_per_request" gorm:"type:bigint"`
	HourlyQuota         int64  `json:"hourly_quota" gorm:"type:bigint"`
	DailyQuota          int64  `json:"daily_quota" gorm:"type:bigint"`
	MaxDistinctModels5m int    `json:"max_distinct_models_5m"`
	UserSustainedRpm    int    `json:"user_sustained_rpm" gorm:"default:0"`
	UserBurstCapacity   int    `json:"user_burst_capacity" gorm:"default:0"`
	UserMaxConcurrency  int    `json:"user_max_concurrency" gorm:"default:0"`
	UserHourlyQuota     int64  `json:"user_hourly_quota" gorm:"type:bigint;default:0"`
	UserDailyQuota      int64  `json:"user_daily_quota" gorm:"type:bigint;default:0"`
	MinimumRiskMode     string `json:"minimum_risk_mode" gorm:"type:varchar(16)"`
	FailClosed          bool   `json:"fail_closed"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	UpdatedAt           int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
	BuiltIn             bool   `json:"built_in" gorm:"-"`
	CacheSynchronized   bool   `json:"-" gorm:"-"`
}

type TokenSecurityProfileFieldMask struct {
	SustainedRpm       bool
	UserSustainedRpm   bool
	UserBurstCapacity  bool
	UserMaxConcurrency bool
	UserHourlyQuota    bool
	UserDailyQuota     bool
}

func BuiltInTokenSecurityProfile() *TokenSecurityProfile {
	return &TokenSecurityProfile{
		ScopeType:       TokenSecurityScopePlatform,
		MinimumRiskMode: TokenRiskModeObserve,
		BuiltIn:         true,
	}
}

func (profile *TokenSecurityProfile) normalizeScope() {
	profile.ScopeType = strings.ToLower(strings.TrimSpace(profile.ScopeType))
	profile.ScopeValue = strings.TrimSpace(profile.ScopeValue)
	if profile.ScopeType == TokenSecurityScopePlatform {
		profile.ScopeValue = ""
	}
}

func (profile *TokenSecurityProfile) Normalize() {
	profile.normalizeScope()
	profile.SustainedRps, profile.BurstCapacity = normalizeTokenSecurityRate(
		profile.SustainedRps,
		profile.SustainedRpm,
		profile.BurstCapacity,
	)
	_, profile.UserBurstCapacity = normalizeTokenSecurityRate(
		0,
		profile.UserSustainedRpm,
		profile.UserBurstCapacity,
	)
}

func (profile *TokenSecurityProfile) Validate() error {
	return profile.validateWithDB(DB)
}

func (profile *TokenSecurityProfile) validateWithDB(db *gorm.DB) error {
	if profile == nil {
		return errors.New("invalid token security profile")
	}
	if db == nil {
		return errors.New("main database is not initialized")
	}
	profile.Normalize()
	switch profile.ScopeType {
	case TokenSecurityScopePlatform:
		if profile.ScopeValue != "" {
			return errors.New("platform token security profile cannot have a scope value")
		}
	case TokenSecurityScopeGroup:
		if profile.ScopeValue == "" || len(profile.ScopeValue) > 64 {
			return errors.New("group token security profile requires a valid group name")
		}
		if !ratio_setting.ContainsGroupRatio(profile.ScopeValue) {
			return errors.New("group token security profile requires an existing group")
		}
	case TokenSecurityScopeUser:
		userId, err := strconv.Atoi(profile.ScopeValue)
		if err != nil || userId <= 0 {
			return errors.New("user token security profile requires a valid user id")
		}
		var count int64
		if err := db.Model(&User{}).Where("id = ?", userId).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return gorm.ErrRecordNotFound
		}
	default:
		return errors.New("unsupported token security profile scope")
	}
	if profile.MinimumRiskMode != TokenRiskModeObserve &&
		profile.MinimumRiskMode != TokenRiskModeNotify &&
		profile.MinimumRiskMode != TokenRiskModeSuspend {
		return errors.New("invalid minimum token risk mode")
	}
	policy := &TokenSecurityPolicy{
		TokenId:             1,
		SustainedRps:        profile.SustainedRps,
		SustainedRpm:        profile.SustainedRpm,
		BurstCapacity:       profile.BurstCapacity,
		MaxConcurrency:      profile.MaxConcurrency,
		MaxQuotaPerRequest:  profile.MaxQuotaPerRequest,
		HourlyQuota:         profile.HourlyQuota,
		DailyQuota:          profile.DailyQuota,
		MaxDistinctModels5m: profile.MaxDistinctModels5m,
		UserSustainedRpm:    profile.UserSustainedRpm,
		UserBurstCapacity:   profile.UserBurstCapacity,
		UserMaxConcurrency:  profile.UserMaxConcurrency,
		UserHourlyQuota:     profile.UserHourlyQuota,
		UserDailyQuota:      profile.UserDailyQuota,
		RiskMode:            profile.MinimumRiskMode,
		FailClosed:          profile.FailClosed,
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	profile.BurstCapacity = policy.BurstCapacity
	return nil
}

func tokenSecurityProfileCacheKey(scopeType string, scopeValue string) string {
	return fmt.Sprintf(
		"%s:%s:%s:%s",
		tokenSecurityProfileCachePrefix,
		tokenSecurityProfileCacheTag,
		scopeType,
		scopeValue,
	)
}

func cacheTokenSecurityProfile(profile *TokenSecurityProfile) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	value := tokenSecurityProfileCacheEmpty
	scopeType := profile.ScopeType
	scopeValue := profile.ScopeValue
	if !profile.BuiltIn && profile.Id > 0 {
		data, err := common.Marshal(profile)
		if err != nil {
			return err
		}
		value = string(data)
	}
	ttl := time.Duration(common.RedisKeyCacheSeconds()) * time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	return common.RedisSet(tokenSecurityProfileCacheKey(scopeType, scopeValue), value, ttl)
}

func invalidateCommittedTokenSecurityProfileCache(scopeType string, scopeValue string) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	cacheKey := tokenSecurityProfileCacheKey(scopeType, scopeValue)
	var invalidationErr error
	for attempt := 0; attempt < 3; attempt++ {
		invalidationErr = common.RedisDelKey(cacheKey)
		if invalidationErr == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
		}
	}
	return invalidationErr
}

func loadTokenSecurityProfilesFromDB(userId int, userGroup string) ([]TokenSecurityProfile, error) {
	query := DB.Where("scope_type = ? AND scope_value = ?", TokenSecurityScopePlatform, "")
	if userGroup != "" {
		query = query.Or("scope_type = ? AND scope_value = ?", TokenSecurityScopeGroup, userGroup)
	}
	if userId > 0 {
		query = query.Or("scope_type = ? AND scope_value = ?", TokenSecurityScopeUser, strconv.Itoa(userId))
	}
	var profiles []TokenSecurityProfile
	return profiles, query.Find(&profiles).Error
}

func selectTokenSecurityProfile(profiles []TokenSecurityProfile, userId int, userGroup string) *TokenSecurityProfile {
	selected := BuiltInTokenSecurityProfile()
	for i := range profiles {
		profile := profiles[i]
		switch {
		case profile.ScopeType == TokenSecurityScopeUser && profile.ScopeValue == strconv.Itoa(userId):
			return &profile
		case profile.ScopeType == TokenSecurityScopeGroup && profile.ScopeValue == userGroup:
			selected = &profile
		case profile.ScopeType == TokenSecurityScopePlatform && selected.BuiltIn:
			selected = &profile
		}
	}
	return selected
}

func getCachedTokenSecurityProfile(userId int, userGroup string) (*TokenSecurityProfile, bool) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, false
	}
	scopes := [][2]string{
		{TokenSecurityScopePlatform, ""},
		{TokenSecurityScopeGroup, userGroup},
		{TokenSecurityScopeUser, strconv.Itoa(userId)},
	}
	keys := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope[0] == TokenSecurityScopeGroup && scope[1] == "" {
			continue
		}
		if scope[0] == TokenSecurityScopeUser && userId <= 0 {
			continue
		}
		keys = append(keys, tokenSecurityProfileCacheKey(scope[0], scope[1]))
	}
	values, err := common.RDB.MGet(context.Background(), keys...).Result()
	if err != nil || len(values) != len(keys) {
		return nil, false
	}
	profiles := make([]TokenSecurityProfile, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, false
		}
		raw, ok := value.(string)
		if !ok {
			return nil, false
		}
		if raw == tokenSecurityProfileCacheEmpty {
			continue
		}
		var profile TokenSecurityProfile
		if err := common.UnmarshalJsonStr(raw, &profile); err != nil {
			return nil, false
		}
		profiles = append(profiles, profile)
	}
	return selectTokenSecurityProfile(profiles, userId, userGroup), true
}

func GetApplicableTokenSecurityProfile(userId int, userGroup string) (*TokenSecurityProfile, error) {
	if profile, ok := getCachedTokenSecurityProfile(userId, userGroup); ok {
		return profile, nil
	}
	profiles, err := loadTokenSecurityProfilesFromDB(userId, userGroup)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled && common.RDB != nil {
		found := make(map[string]*TokenSecurityProfile, len(profiles))
		for i := range profiles {
			profile := &profiles[i]
			found[tokenSecurityProfileCacheKey(profile.ScopeType, profile.ScopeValue)] = profile
		}
		scopes := [][2]string{
			{TokenSecurityScopePlatform, ""},
			{TokenSecurityScopeGroup, userGroup},
			{TokenSecurityScopeUser, strconv.Itoa(userId)},
		}
		for _, scope := range scopes {
			if scope[0] == TokenSecurityScopeGroup && scope[1] == "" {
				continue
			}
			if scope[0] == TokenSecurityScopeUser && userId <= 0 {
				continue
			}
			profile := found[tokenSecurityProfileCacheKey(scope[0], scope[1])]
			if profile == nil {
				profile = &TokenSecurityProfile{
					ScopeType:  scope[0],
					ScopeValue: scope[1],
					BuiltIn:    true,
				}
			}
			if err := cacheTokenSecurityProfile(profile); err != nil {
				common.SysLog("failed to cache token security profile: " + err.Error())
			}
		}
	}
	return selectTokenSecurityProfile(profiles, userId, userGroup), nil
}

func ListTokenSecurityProfiles() ([]TokenSecurityProfile, error) {
	var profiles []TokenSecurityProfile
	if err := DB.Order("scope_type, scope_value").Find(&profiles).Error; err != nil {
		return nil, err
	}
	hasPlatform := false
	for i := range profiles {
		hasPlatform = hasPlatform || profiles[i].ScopeType == TokenSecurityScopePlatform
	}
	if !hasPlatform {
		profiles = append([]TokenSecurityProfile{*BuiltInTokenSecurityProfile()}, profiles...)
	}
	return profiles, nil
}

func (profile *TokenSecurityProfile) preserveOmittedRollingFields(
	stored *TokenSecurityProfile,
	fieldMask *TokenSecurityProfileFieldMask,
) {
	if profile == nil || stored == nil || fieldMask == nil {
		return
	}
	if !fieldMask.SustainedRpm {
		profile.SustainedRpm = stored.SustainedRpm
	}
	if !fieldMask.UserSustainedRpm {
		profile.UserSustainedRpm = stored.UserSustainedRpm
	}
	if !fieldMask.UserBurstCapacity {
		profile.UserBurstCapacity = stored.UserBurstCapacity
	}
	if !fieldMask.UserMaxConcurrency {
		profile.UserMaxConcurrency = stored.UserMaxConcurrency
	}
	if !fieldMask.UserHourlyQuota {
		profile.UserHourlyQuota = stored.UserHourlyQuota
	}
	if !fieldMask.UserDailyQuota {
		profile.UserDailyQuota = stored.UserDailyQuota
	}
}

func saveTokenSecurityProfile(
	profile *TokenSecurityProfile,
	createOnly bool,
	fieldMask *TokenSecurityProfileFieldMask,
) error {
	if profile == nil {
		return errors.New("invalid token security profile")
	}
	profile.normalizeScope()
	profile.Id = 0
	profile.CreatedAt = 0
	profile.UpdatedAt = 0
	profile.BuiltIn = false
	profile.CacheSynchronized = true
	var committed TokenSecurityProfile
	err := DB.Transaction(func(tx *gorm.DB) error {
		if createOnly {
			if err := profile.validateWithDB(tx); err != nil {
				return err
			}
			if err := tx.Create(profile).Error; err != nil {
				return err
			}
			committed = *profile
			return nil
		}
		var stored TokenSecurityProfile
		err := lockForUpdate(tx).
			Where("scope_type = ? AND scope_value = ?", profile.ScopeType, profile.ScopeValue).
			First(&stored).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			profile.preserveOmittedRollingFields(&stored, fieldMask)
			profile.Id = stored.Id
			profile.CreatedAt = stored.CreatedAt
		}
		if err := profile.validateWithDB(tx); err != nil {
			return err
		}
		if err := tx.Save(profile).Error; err != nil {
			return err
		}
		committed = *profile
		return nil
	})
	if err != nil {
		return err
	}
	*profile = committed
	profile.CacheSynchronized = true
	if err := invalidateCommittedTokenSecurityProfileCache(profile.ScopeType, profile.ScopeValue); err != nil {
		profile.CacheSynchronized = false
		common.SysError(fmt.Sprintf(
			"token security profile committed but cache invalidation failed scope=%s:%s error=%v",
			profile.ScopeType, profile.ScopeValue, err,
		))
	}
	return nil
}

func CreateTokenSecurityProfile(profile *TokenSecurityProfile) error {
	return saveTokenSecurityProfile(profile, true, nil)
}

func UpsertTokenSecurityProfile(profile *TokenSecurityProfile) error {
	return saveTokenSecurityProfile(profile, false, nil)
}

func UpsertTokenSecurityProfileWithFieldMask(
	profile *TokenSecurityProfile,
	fieldMask TokenSecurityProfileFieldMask,
) error {
	return saveTokenSecurityProfile(profile, false, &fieldMask)
}

func DeleteTokenSecurityProfile(scopeType string, scopeValue string) (bool, error) {
	profile := &TokenSecurityProfile{ScopeType: scopeType, ScopeValue: scopeValue}
	profile.Normalize()
	if profile.ScopeType != TokenSecurityScopePlatform &&
		profile.ScopeType != TokenSecurityScopeGroup &&
		profile.ScopeType != TokenSecurityScopeUser {
		return false, errors.New("unsupported token security profile scope")
	}
	if profile.ScopeType != TokenSecurityScopePlatform && profile.ScopeValue == "" {
		return false, errors.New("token security profile scope value is required")
	}
	if err := DB.Where("scope_type = ? AND scope_value = ?", profile.ScopeType, profile.ScopeValue).
		Delete(&TokenSecurityProfile{}).Error; err != nil {
		return false, err
	}
	if err := invalidateCommittedTokenSecurityProfileCache(profile.ScopeType, profile.ScopeValue); err != nil {
		common.SysError(fmt.Sprintf(
			"token security profile deleted but cache invalidation failed scope=%s:%s error=%v",
			profile.ScopeType, profile.ScopeValue, err,
		))
		return false, nil
	}
	return true, nil
}
