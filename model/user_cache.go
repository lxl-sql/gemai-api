package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
)

// UserBase is an internal auth/billing cache shape, not the public User API.
// Important: UserBase.Quota stores total remaining quota for fast checks.
// Public REST user.quota stores recharge quota only; use total_quota for total.
type UserBase struct {
	CacheVersion    int    `json:"-"`
	Id              int    `json:"id"`
	Group           string `json:"group"`
	Email           string `json:"email"`
	Quota           int    `json:"quota"` // total remaining quota for auth and billing checks
	GiftQuota       int    `json:"gift_quota"`
	Status          int    `json:"status"`
	Role            int    `json:"role"`
	SecurityVersion int64  `json:"security_version"`
	Username        string `json:"username"`
	Setting         string `json:"setting"`
}

const authCacheSchemaVersion = 1

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache is the exported version of invalidateUserCache.
// 供 controller 等上层包在用户状态变更（如禁用、删除、角色变更）后主动清理缓存。
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

func populateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}

	return common.RedisHSetObj(
		getUserCacheKey(user.Id),
		user.ToBaseUser(),
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
}

// updateUserCache refreshes non-quota user cache fields.
// Quota is maintained by atomic quota delta paths and must not be overwritten
// by stale user snapshots from profile/settings updates.
func updateUserCache(user User) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	if err := updateUserGroupCache(user.Id, user.Group); err != nil {
		return err
	}
	if err := updateUserEmailCache(user.Id, user.Email); err != nil {
		return err
	}
	if err := updateUserStatusCache(user.Id, user.Status == common.UserStatusEnabled); err != nil {
		return err
	}
	if err := common.RedisHSetField(getUserCacheKey(user.Id), "Role", fmt.Sprintf("%d", user.Role)); err != nil {
		return err
	}
	if err := common.RedisHSetField(getUserCacheKey(user.Id), "SecurityVersion", fmt.Sprintf("%d", user.SecurityVersion)); err != nil {
		return err
	}
	if err := updateUserNameCache(user.Id, user.Username); err != nil {
		return err
	}
	return updateUserSettingCache(user.Id, user.Setting)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (userCache *UserBase, err error) {
	return GetUserCacheContext(context.Background(), userId)
}

func GetUserCacheContext(ctx context.Context, userId int) (*UserBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cached, ok := getValidCachedUserBase(ctx, userId); ok {
		return &cached, nil
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !common.RedisEnabled {
		loaded, err := loadUserBaseFromDB(ctx, userId)
		if err != nil {
			return nil, err
		}
		return &loaded, nil
	}

	userCache, err := coalesceAuthCacheLoad(
		ctx,
		authCacheLoadNamespaceUser,
		strconv.Itoa(userId),
		func() (UserBase, error) {
			// Another request may have rebuilt the user while this caller waited to
			// enter the flight. Recheck Redis before querying PostgreSQL.
			if cached, ok := getValidCachedUserBase(context.Background(), userId); ok {
				return cached, nil
			}
			return loadUserBaseFromDB(context.Background(), userId)
		},
	)
	if err != nil {
		return nil, err
	}
	return &userCache, nil
}

func getValidCachedUserBase(ctx context.Context, userId int) (UserBase, bool) {
	userCache, err := cacheGetUserBaseContext(ctx, userId)
	if err == nil && validCachedUserBase(userId, userCache) {
		return *userCache, true
	}
	if err == nil && common.RedisEnabled && common.RDB != nil {
		if deleteErr := invalidateUserCache(userId); deleteErr != nil {
			common.SysLog("failed to delete incomplete user cache: " + deleteErr.Error())
		}
	}
	return UserBase{}, false
}

func loadUserBaseFromDB(ctx context.Context, userId int) (UserBase, error) {
	user, err := GetUserByIdContext(ctx, userId, false)
	if err != nil {
		return UserBase{}, err
	}
	userCache := *user.ToBaseUser()

	// Synchronously rebuild cache from DB (safe: full object write, no partial field overwrite race).
	if common.RedisEnabled && common.RDB != nil {
		if cacheErr := populateUserCache(*user); cacheErr != nil {
			common.SysLog("failed to rebuild user cache: " + cacheErr.Error())
		}
	}
	return userCache, nil
}

func validCachedUserBase(userId int, user *UserBase) bool {
	if user == nil || user.CacheVersion != authCacheSchemaVersion ||
		userId <= 0 || user.Id != userId || user.Username == "" || user.Group == "" {
		return false
	}
	if !common.IsValidateRole(user.Role) {
		return false
	}
	return user.Status == common.UserStatusEnabled || user.Status == common.UserStatusDisabled
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	return cacheGetUserBaseContext(context.Background(), userId)
}

func cacheGetUserBaseContext(ctx context.Context, userId int) (*UserBase, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObjContext(ctx, getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	return &userCache, nil
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserGiftQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.GiftQuota, nil
}

func getUserStatusCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Status, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// New functions for individual field updates
func updateUserStatusCache(userId int, status bool) error {
	if !common.RedisEnabled {
		return nil
	}
	statusInt := common.UserStatusEnabled
	if !status {
		statusInt = common.UserStatusDisabled
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Status", fmt.Sprintf("%d", statusInt))
}

func updateUserGroupCache(userId int, group string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Group", group)
}

func UpdateUserGroupCache(userId int, group string) error {
	return updateUserGroupCache(userId, group)
}

func updateUserEmailCache(userId int, email string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Email", email)
}

func updateUserNameCache(userId int, username string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Setting", setting)
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}
