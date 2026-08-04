package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const UserNameMaxLength = 20

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
type User struct {
	Id               int     `json:"id"`
	Username         string  `json:"username" gorm:"unique;index" validate:"max=20"`
	Password         string  `json:"password" gorm:"not null;" validate:"min=8,max=20"`
	OriginalPassword string  `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	DisplayName      string  `json:"display_name" gorm:"index" validate:"max=20"`
	Role             int     `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status           int     `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email            string  `json:"email" gorm:"index" validate:"max=50"`
	GitHubId         string  `json:"github_id" gorm:"column:github_id;index"`
	DiscordId        string  `json:"discord_id" gorm:"column:discord_id;index"`
	OidcId           string  `json:"oidc_id" gorm:"column:oidc_id;index"`
	WeChatId         string  `json:"wechat_id" gorm:"column:wechat_id;index"`
	TelegramId       string  `json:"telegram_id" gorm:"column:telegram_id;index"`
	VerificationCode string  `json:"verification_code" gorm:"-:all"`                         // this field is only for Email verification, don't save it to database!
	AccessToken      *string `json:"-" gorm:"type:char(32);column:access_token;uniqueIndex"` // this token is for system management
	SecurityVersion  int64   `json:"-" gorm:"column:security_version"`                       // increment to revoke existing dashboard sessions
	// API contract after wallet split:
	// quota is recharge quota only, not total remaining quota.
	// Use gift_quota for gifted balance and total_quota for quota + gift_quota.
	Quota            int                        `json:"quota" gorm:"type:int;default:0"`
	GiftQuota        int                        `json:"gift_quota" gorm:"type:int;default:0;column:gift_quota"` // gifted quota, consumed before recharge quota
	TotalQuotaValue  int                        `json:"total_quota" gorm:"-:all"`                               // response-only remaining total
	UsedQuota        int                        `json:"used_quota" gorm:"type:int;default:0;column:used_quota"` // used quota
	RequestCount     int                        `json:"request_count" gorm:"type:int;default:0;"`               // request number
	Group            string                     `json:"group" gorm:"type:varchar(64);default:'default'"`
	AffCode          string                     `json:"aff_code" gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
	AffCount         int                        `json:"aff_count" gorm:"type:int;default:0;column:aff_count"`
	AffQuota         int                        `json:"aff_quota" gorm:"type:int;default:0;column:aff_quota"`           // 邀请剩余额度
	AffHistoryQuota  int                        `json:"aff_history_quota" gorm:"type:int;default:0;column:aff_history"` // 邀请历史额度
	InviterId        int                        `json:"inviter_id" gorm:"type:int;column:inviter_id;index"`
	DeletedAt        gorm.DeletedAt             `gorm:"index"`
	LinuxDOId        string                     `json:"linux_do_id" gorm:"column:linux_do_id;index"`
	Setting          string                     `json:"setting" gorm:"type:text;column:setting"`
	Remark           string                     `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	StripeCustomer   string                     `json:"stripe_customer" gorm:"type:varchar(64);column:stripe_customer;index"`
	CreatedAt        int64                      `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	LastLoginAt      int64                      `json:"last_login_at" gorm:"default:0;column:last_login_at"`
	AdminPermissions map[string]map[string]bool `json:"admin_permissions,omitempty" gorm:"-:all"`
}

func (user *User) ToBaseUser() *UserBase {
	cache := &UserBase{
		CacheVersion:    authCacheSchemaVersion,
		Id:              user.Id,
		Group:           user.Group,
		Quota:           user.TotalQuota(),
		GiftQuota:       user.GiftQuota,
		Status:          user.Status,
		Role:            user.Role,
		SecurityVersion: user.SecurityVersion,
		Username:        user.Username,
		Setting:         user.Setting,
		Email:           user.Email,
	}
	return cache
}

func (user *User) TotalQuota() int {
	if user == nil {
		return 0
	}
	return user.Quota + user.GiftQuota
}

// WithQuotaResponseFields preserves the public API contract:
// quota remains recharge quota, while total_quota carries recharge + gift quota.
func (user *User) WithQuotaResponseFields() *User {
	if user == nil {
		return nil
	}
	response := *user
	response.Quota = user.Quota
	response.TotalQuotaValue = user.TotalQuota()
	return &response
}

func UsersWithQuotaResponseFields(users []*User) []*User {
	result := make([]*User, 0, len(users))
	for _, user := range users {
		result = append(result, user.WithQuotaResponseFields())
	}
	return result
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

func (user *User) SetSetting(setting dto.UserSetting) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog("failed to marshal setting: " + err.Error())
		return
	}
	user.Setting = string(settingBytes)
}

func UpdateUserSetting(userId int, setting dto.UserSetting) error {
	if userId == 0 {
		return errors.New("id 为空！")
	}
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		return err
	}
	settingValue := string(settingBytes)
	err = DB.Model(&User{}).Where("id = ?", userId).Update("setting", settingValue).Error
	if err != nil {
		return err
	}
	return updateUserSettingCache(userId, settingValue)
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{}

	// 聊天区域 - 所有用户都可以访问
	defaultConfig["chat"] = map[string]interface{}{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	// 控制台区域 - 所有用户都可以访问
	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	// 个人中心区域 - 所有用户都可以访问
	defaultConfig["personal"] = map[string]interface{}{
		"enabled":            true,
		"topup":              true,
		"quota_transactions": true,
		"personal":           true,
	}

	// 管理员区域 - 根据角色决定
	if userRole == common.RoleAdminUser {
		// 管理员可以访问管理员区域，但不能访问系统设置
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":     true,
			"channel":     true,
			"models":      true,
			"redemption":  true,
			"user":        true,
			"system_info": false, // 管理员不能访问系统信息
			"setting":     false, // 管理员不能访问系统设置
		}
	} else if userRole == common.RoleRootUser {
		// 超级管理员可以访问所有功能
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":     true,
			"channel":     true,
			"models":      true,
			"redemption":  true,
			"user":        true,
			"system_info": true,
			"setting":     true,
		}
	}
	// 普通用户不包含admin区域

	// 转换为JSON字符串
	configBytes, err := common.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("生成默认边栏配置失败: " + err.Error())
		return ""
	}

	return string(configBytes)
}

// CheckUserExistOrDeleted check if user exist or deleted, if not exist, return false, nil, if deleted or exist, return true, nil
func CheckUserExistOrDeleted(username string, email string) (bool, error) {
	var user User

	// err := DB.Unscoped().First(&user, "username = ? or email = ?", username, email).Error
	// check email if empty
	var err error
	email = NormalizeEmail(email)
	if email == "" {
		err = DB.Unscoped().First(&user, "username = ?", username).Error
	} else {
		err = DB.Unscoped().First(&user, "username = ? or LOWER(email) = ?", username, email).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// not exist, return false, nil
			return false, nil
		}
		// other error, return false, err
		return false, err
	}
	// exist, return true, nil
	return true, nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func emailQuery(tx *gorm.DB, email string) *gorm.DB {
	if tx == nil {
		tx = DB
	}
	return tx.Unscoped().Model(&User{}).Where("LOWER(email) = ?", NormalizeEmail(email))
}

func CountUsersByEmail(email string) (int64, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return 0, nil
	}
	var count int64
	err := emailQuery(DB, email).Count(&count).Error
	return count, err
}

func IsEmailAvailable(email string, excludeUserID int) (bool, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return true, nil
	}
	query := emailQuery(DB, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func EnsureEmailAvailable(email string, excludeUserID int) error {
	available, err := IsEmailAvailable(email, excludeUserID)
	if err != nil {
		return err
	}
	if !available {
		return ErrEmailAlreadyTaken
	}
	return nil
}

// withNormalizedEmailLock serializes concurrent writers that target the same
// normalized email inside tx, so a "check then write" sequence cannot be raced
// by two transactions. It must be called inside an active transaction; the lock
// is scoped to that transaction and released on commit/rollback.
//
//   - PostgreSQL: transaction-level advisory lock keyed by the normalized email.
//   - MySQL (default REPEATABLE READ): a locking read that takes a next-key/gap
//     lock on the email index, blocking concurrent inserts of the same value.
//   - SQLite: no explicit lock; the single-writer model already serializes the
//     write, so a racing second write fails instead of duplicating.
//
// An empty email is allowed to repeat and needs no serialization.
func withNormalizedEmailLock(tx *gorm.DB, email string, fn func(tx *gorm.DB) error) error {
	email = NormalizeEmail(email)
	if email == "" {
		return fn(tx)
	}
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", email).Error; err != nil {
			return err
		}
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		var ids []int
		if err := tx.Raw("SELECT id FROM users WHERE email = ? FOR UPDATE", email).Scan(&ids).Error; err != nil {
			return err
		}
	}
	return fn(tx)
}

func GetMaxUserId() int {
	var user User
	DB.Unscoped().Last(&user)
	return user.Id
}

func GetAllUsers(pageInfo *common.PageInfo) (users []*User, total int64, err error) {
	// 只读列表无需事务：事务会跨 COUNT+SELECT 长时间占用一条连接，
	// 高并发下加剧连接堆积与 "idle in transaction"。直接用连接池查询即可。
	if err = DB.Unscoped().Model(&User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = DB.Unscoped().Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Omit("password", "access_token").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func SearchUsers(keyword string, group string, role *int, status *int, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64
	var err error

	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 构建基础查询
	query := tx.Unscoped().Model(&User{})

	// 构建搜索条件
	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"
	likeArgs := []interface{}{"%" + keyword + "%", "%" + keyword + "%", "%" + keyword + "%"}

	// 尝试将关键字转换为整数ID
	keywordInt, err := strconv.Atoi(keyword)
	if err == nil {
		// 如果是数字，同时搜索ID和其他字段
		likeCondition = "id = ? OR " + likeCondition
		likeArgs = append([]interface{}{keywordInt}, likeArgs...)
	}

	query = query.Where("("+likeCondition+")", likeArgs...)
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	if role != nil {
		query = query.Where("role = ?", *role)
	}
	if status != nil {
		if *status == -1 {
			query = query.Where("deleted_at IS NOT NULL")
		} else {
			query = query.Where("deleted_at IS NULL").Where("status = ?", *status)
		}
	}

	// 获取总数
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = query.Omit("password", "access_token").Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func GetUserById(id int, selectAll bool) (*User, error) {
	return GetUserByIdContext(context.Background(), id, selectAll)
}

func GetUserByIdContext(ctx context.Context, id int, selectAll bool) (*User, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	user := User{Id: id}
	var err error = nil
	if selectAll {
		err = DB.WithContext(ctx).First(&user, "id = ?", id).Error
	} else {
		err = DB.WithContext(ctx).Omit("password", "access_token").First(&user, "id = ?", id).Error
	}
	return &user, err
}

func GetUserIdByAffCode(affCode string) (int, error) {
	if affCode == "" {
		return 0, errors.New("affCode 为空！")
	}
	var user User
	err := DB.Select("id").First(&user, "aff_code = ?", affCode).Error
	return user.Id, err
}

func DeleteUserById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	user := User{Id: id}
	return user.Delete()
}

func HardDeleteUserById(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := PurgeUserTokenUsageSourcesTx(tx, id); err != nil {
			return err
		}
		if err := deleteUserOAuthBindingsByUserId(tx, id); err != nil {
			return err
		}
		return tx.Unscoped().Delete(&User{}, "id = ?", id).Error
	})
}

func inviteUser(inviterId int) (err error) {
	// 原子增量更新邀请统计，避免 Save 整行写回旧的绝对余额（quota/gift_quota），
	// 与邀请人并发消费产生丢更新。
	result := DB.Model(&User{}).Where("id = ?", inviterId).Updates(map[string]interface{}{
		"aff_count":   gorm.Expr("aff_count + ?", 1),
		"aff_quota":   gorm.Expr("aff_quota + ?", common.QuotaForInviter),
		"aff_history": gorm.Expr("aff_history + ?", common.QuotaForInviter),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("inviter not found")
	}
	return nil
}

func (user *User) TransferAffQuotaToQuota(quota int) error {
	// 检查quota是否小于最小额度
	if float64(quota) < common.QuotaPerUnit {
		return fmt.Errorf("转移额度最小为%s！", logger.LogQuota(int(common.QuotaPerUnit)))
	}

	err := withBoundedQuotaUserTransaction(context.Background(), user.Id, func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&user, user.Id).Error; err != nil {
			return err
		}
		if user.AffQuota < quota {
			return errors.New("邀请额度不足！")
		}

		res := tx.Model(&User{}).
			Where("id = ? AND aff_quota >= ?", user.Id, quota).
			Update("aff_quota", gorm.Expr("aff_quota - ?", quota))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("邀请额度不足！")
		}
		user.AffQuota -= quota

		_, err := createQuotaTransactionTx(tx, user, 0, quota, normalizeQuotaRef(QuotaTransactionRef{
			Type:           QuotaTransactionTypeAffTransfer,
			Source:         "invite",
			ReferenceType:  "user",
			ReferenceID:    strconv.Itoa(user.Id),
			IdempotencyKey: "aff_transfer:" + strconv.Itoa(user.Id) + ":" + common.GetUUID(),
		}, QuotaTransactionTypeAffTransfer))
		return err
	})
	if err != nil {
		return err
	}
	return invalidateUserCache(user.Id)
}

// GenerateAffCode 生成不与现有用户冲突的邀请码。
// 8 位字母数字约有 2.18e14 种组合，正常情况下一次即可命中；
// 查询需带 Unscoped，因为唯一索引同样覆盖软删除的用户。
func GenerateAffCode() string {
	return generateAffCodeTx(DB)
}

// generateAffCodeTx 在指定事务/连接上生成邀请码。
// 事务内必须传 tx：SQLite 写事务持锁期间用全局 DB 查询会自死锁。
func generateAffCodeTx(tx *gorm.DB) string {
	for i := 0; i < 5; i++ {
		code := common.GetRandomString(8)
		var count int64
		err := tx.Unscoped().Model(&User{}).Where("aff_code = ?", code).Count(&count).Error
		if err == nil && count == 0 {
			return code
		}
	}
	// 兜底：多次冲突或查询失败时，用更长的码使碰撞概率可忽略
	return common.GetRandomString(16)
}

func (user *User) prepareForInsert(tx *gorm.DB) error {
	user.Email = NormalizeEmail(user.Email)
	if err := ensureEmailAvailableWithTx(tx, user.Email, 0); err != nil {
		return err
	}
	if user.Password == "" {
		return nil
	}
	var err error
	user.Password, err = common.Password2Hash(user.Password)
	return err
}

// BindEmailToUser atomically checks email availability and assigns it to the
// user, serializing concurrent binds of the same email so two accounts cannot
// end up sharing one address. The email is normalized before check and store.
func BindEmailToUser(user *User, email string) error {
	email = NormalizeEmail(email)
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, email, func(tx *gorm.DB) error {
			if err := ensureEmailAvailableWithTx(tx, email, user.Id); err != nil {
				return err
			}
			user.Email = email
			return user.UpdateWithTx(tx, false)
		})
	}); err != nil {
		return err
	}
	return updateUserCache(*user)
}

func ensureEmailAvailableWithTx(tx *gorm.DB, email string, excludeUserID int) error {
	email = NormalizeEmail(email)
	if email == "" {
		return nil
	}
	query := emailQuery(tx, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrEmailAlreadyTaken
	}
	return nil
}

func (user *User) Insert(inviterId int) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
			if err := user.prepareForInsert(tx); err != nil {
				return err
			}
			// 钱包拆分后：注册赠送额度走赠送额度账本，不直接写入充值额度。
			user.Quota = 0
			user.GiftQuota = 0
			user.AffCode = generateAffCodeTx(tx)

			// 初始化用户设置，包括默认的边栏配置
			if user.Setting == "" {
				defaultSetting := dto.UserSetting{}
				// 这里暂时不设置SidebarModules，因为需要在用户创建后根据角色设置
				user.SetSetting(defaultSetting)
			}

			if err := tx.Create(user).Error; err != nil {
				return err
			}
			if common.QuotaForNewUser > 0 {
				if _, err := CreditGiftQuotaTx(tx, user.Id, common.QuotaForNewUser, QuotaTransactionRef{
					Type:           QuotaTransactionTypeGift,
					Source:         "register",
					ReferenceType:  "user",
					ReferenceID:    strconv.Itoa(user.Id),
					IdempotencyKey: "register_gift:" + strconv.Itoa(user.Id),
				}); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		return err
	}

	user.finishInsert(inviterId)
	return nil
}

func (user *User) finishInsert(inviterId int) {
	// 用户创建成功后，根据角色初始化边栏配置
	// 需要重新获取用户以确保有正确的ID和Role
	var createdUser User
	if err := DB.Where("username = ?", user.Username).First(&createdUser).Error; err == nil {
		// 生成基于角色的默认边栏配置
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 && operation_setting.IsPaymentComplianceConfirmed() {
		if common.QuotaForInvitee > 0 {
			_ = IncreaseUserGiftQuota(user.Id, common.QuotaForInvitee, true)
			RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(common.QuotaForInvitee)))
		}
		if common.QuotaForInviter > 0 {
			if err := inviteUser(inviterId); err != nil {
				common.SysError(fmt.Sprintf("邀请奖励发放失败, inviter=%d, err=%s", inviterId, err.Error()))
			} else {
				RecordLog(inviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(common.QuotaForInviter)))
			}
		}
	}
}

func (user *User) FinishInsert(inviterId int) {
	user.finishInsert(inviterId)
}

// InsertWithTx inserts a new user within an existing transaction.
// This is used for OAuth registration where user creation and binding need to be atomic.
// Post-creation tasks (sidebar config, logs, inviter rewards) are handled after the transaction commits.
func (user *User) InsertWithTx(tx *gorm.DB, inviterId int) error {
	return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
		if err := user.prepareForInsert(tx); err != nil {
			return err
		}
		// 钱包拆分后：注册赠送额度走赠送额度账本，不直接写入充值额度。
		user.Quota = 0
		user.GiftQuota = 0
		user.AffCode = generateAffCodeTx(tx)

		// 初始化用户设置
		if user.Setting == "" {
			defaultSetting := dto.UserSetting{}
			user.SetSetting(defaultSetting)
		}

		if err := tx.Create(user).Error; err != nil {
			return err
		}
		if common.QuotaForNewUser > 0 {
			if _, err := CreditGiftQuotaTx(tx, user.Id, common.QuotaForNewUser, QuotaTransactionRef{
				Type:           QuotaTransactionTypeGift,
				Source:         "register",
				ReferenceType:  "user",
				ReferenceID:    strconv.Itoa(user.Id),
				IdempotencyKey: "register_gift:" + strconv.Itoa(user.Id),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// FinalizeOAuthUserCreation performs post-transaction tasks for OAuth user creation.
// This should be called after the transaction commits successfully.
func (user *User) FinalizeOAuthUserCreation(inviterId int) {
	// 用户创建成功后，根据角色初始化边栏配置
	var createdUser User
	if err := DB.Where("id = ?", user.Id).First(&createdUser).Error; err == nil {
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 && operation_setting.IsPaymentComplianceConfirmed() {
		if common.QuotaForInvitee > 0 {
			_ = IncreaseUserGiftQuota(user.Id, common.QuotaForInvitee, true)
			RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(common.QuotaForInvitee)))
		}
		if common.QuotaForInviter > 0 {
			if err := inviteUser(inviterId); err != nil {
				common.SysError(fmt.Sprintf("邀请奖励发放失败, inviter=%d, err=%s", inviterId, err.Error()))
			} else {
				RecordLog(inviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(common.QuotaForInviter)))
			}
		}
	}
}

func (user *User) Update(updatePassword bool) error {
	var err error
	if updatePassword {
		err = DB.Transaction(func(tx *gorm.DB) error {
			return user.UpdateWithTx(tx, true)
		})
	} else {
		err = user.UpdateWithTx(DB, false)
	}
	if err != nil {
		return err
	}
	return updateUserCache(*user)
}

func (user *User) UpdateWithTx(tx *gorm.DB, updatePassword bool) error {
	var err error
	plainPassword := user.Password
	newUser := *user
	current := User{}
	if err = tx.First(&current, user.Id).Error; err != nil {
		return err
	}
	// 余额与统计列禁止经由 Update 写回：newUser 是请求开始时的快照，
	// 直接 Updates 会把旧的绝对余额覆盖回去，与并发扣费/入账产生丢更新。
	// 余额变更必须走统一钱包服务（quota_transaction.go）。
	if updatePassword {
		if err := applyPasswordSecurityTx(tx, user.Id, plainPassword); err != nil {
			return err
		}
	}
	if err = tx.Model(&current).
		Omit("password", "access_token", "security_version", "quota", "gift_quota", "used_quota", "request_count", "aff_quota", "aff_history", "aff_count").
		Updates(newUser).Error; err != nil {
		return err
	}
	return tx.First(user, user.Id).Error
}

func (user *User) Edit(updatePassword bool) error {
	var err error
	if updatePassword {
		err = DB.Transaction(func(tx *gorm.DB) error {
			return user.EditWithTx(tx, true)
		})
	} else {
		err = user.EditWithTx(DB, false)
	}
	if err != nil {
		return err
	}
	return updateUserCache(*user)
}

func (user *User) EditWithTx(tx *gorm.DB, updatePassword bool) error {
	var err error
	plainPassword := user.Password
	newUser := *user
	updates := map[string]interface{}{
		"username":     newUser.Username,
		"display_name": newUser.DisplayName,
		"group":        newUser.Group,
		"remark":       newUser.Remark,
	}
	if updatePassword {
		if err := applyPasswordSecurityTx(tx, user.Id, plainPassword); err != nil {
			return err
		}
	}

	current := User{}
	if err = tx.First(&current, user.Id).Error; err != nil {
		return err
	}
	if err = tx.Model(&current).Updates(updates).Error; err != nil {
		return err
	}
	return tx.First(user, user.Id).Error
}

func (user *User) UpdateEmail(email string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Update("email", email).Error; err != nil {
		return err
	}
	if err := DB.Where("id = ?", user.Id).First(user).Error; err != nil {
		return err
	}
	return invalidateUserCache(user.Id)
}

func (user *User) ClearBinding(bindingType string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}

	bindingColumnMap := map[string]string{
		"email":    "email",
		"github":   "github_id",
		"discord":  "discord_id",
		"oidc":     "oidc_id",
		"wechat":   "wechat_id",
		"telegram": "telegram_id",
		"linuxdo":  "linux_do_id",
	}

	column, ok := bindingColumnMap[bindingType]
	if !ok {
		return errors.New("invalid binding type")
	}

	if err := DB.Model(&User{}).Where("id = ?", user.Id).Update(column, "").Error; err != nil {
		return err
	}

	if err := DB.Where("id = ?", user.Id).First(user).Error; err != nil {
		return err
	}

	return invalidateUserCache(user.Id)
}

func (user *User) Delete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := PurgeUserTokenUsageSourcesTx(tx, user.Id); err != nil {
			return err
		}
		return tx.Delete(user).Error
	}); err != nil {
		return err
	}

	// 清除缓存
	return invalidateUserCache(user.Id)
}

func (user *User) HardDelete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := PurgeUserTokenUsageSourcesTx(tx, user.Id); err != nil {
			return err
		}
		if err := deleteUserOAuthBindingsByUserId(tx, user.Id); err != nil {
			return err
		}
		return tx.Unscoped().Delete(user).Error
	})
}

// ValidateAndFill check password & user status
func (user *User) ValidateAndFill() (err error) {
	// When querying with struct, GORM will only query with non-zero fields,
	// that means if your field's value is 0, '', false or other zero values,
	// it won't be used to build query conditions
	password := user.Password
	username := strings.TrimSpace(user.Username)
	if username == "" || password == "" {
		return ErrUserEmptyCredentials
	}
	// find by username or email
	err = DB.Where("username = ? OR email = ?", username, username).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if user.Password == "" {
		return ErrInvalidCredentials
	}
	okay := common.ValidatePasswordAndHash(password, user.Password)
	if !okay || user.Status != common.UserStatusEnabled {
		return ErrInvalidCredentials
	}
	return nil
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	DB.Where(User{Id: user.Id}).First(user)
	return nil
}

func (user *User) FillUserByEmail() error {
	if user.Email == "" {
		return errors.New("email 为空！")
	}
	DB.Where(User{Email: user.Email}).First(user)
	return nil
}

func (user *User) FillUserByGitHubId() error {
	if user.GitHubId == "" {
		return errors.New("GitHub id 为空！")
	}
	DB.Where(User{GitHubId: user.GitHubId}).First(user)
	return nil
}

// UpdateGitHubId updates the user's GitHub ID (used for migration from login to numeric ID)
func (user *User) UpdateGitHubId(newGitHubId string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}
	return DB.Model(user).Update("github_id", newGitHubId).Error
}

func (user *User) FillUserByDiscordId() error {
	if user.DiscordId == "" {
		return errors.New("discord id 为空！")
	}
	DB.Where(User{DiscordId: user.DiscordId}).First(user)
	return nil
}

func (user *User) FillUserByOidcId() error {
	if user.OidcId == "" {
		return errors.New("oidc id 为空！")
	}
	DB.Where(User{OidcId: user.OidcId}).First(user)
	return nil
}

func (user *User) FillUserByWeChatId() error {
	if user.WeChatId == "" {
		return errors.New("WeChat id 为空！")
	}
	DB.Where(User{WeChatId: user.WeChatId}).First(user)
	return nil
}

func (user *User) FillUserByTelegramId() error {
	if user.TelegramId == "" {
		return errors.New("Telegram id 为空！")
	}
	err := DB.Where(User{TelegramId: user.TelegramId}).First(user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("该 Telegram 账户未绑定")
	}
	return nil
}

func IsEmailAlreadyTaken(email string) bool {
	count, err := CountUsersByEmail(email)
	return err == nil && count > 0
}

func GetUniqueUserByEmail(email string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, ErrEmailNotFound
	}
	var users []User
	if err := DB.Where("LOWER(email) = ?", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	switch len(users) {
	case 0:
		return nil, ErrEmailNotFound
	case 1:
		return &users[0], nil
	default:
		return nil, ErrEmailAmbiguous
	}
}

func IsWeChatIdAlreadyTaken(wechatId string) bool {
	return DB.Unscoped().Where("wechat_id = ?", wechatId).Find(&User{}).RowsAffected == 1
}

func IsGitHubIdAlreadyTaken(githubId string) bool {
	return DB.Unscoped().Where("github_id = ?", githubId).Find(&User{}).RowsAffected == 1
}

func IsDiscordIdAlreadyTaken(discordId string) bool {
	return DB.Unscoped().Where("discord_id = ?", discordId).Find(&User{}).RowsAffected == 1
}

func IsOidcIdAlreadyTaken(oidcId string) bool {
	return DB.Where("oidc_id = ?", oidcId).Find(&User{}).RowsAffected == 1
}

func IsTelegramIdAlreadyTaken(telegramId string) bool {
	return DB.Unscoped().Where("telegram_id = ?", telegramId).Find(&User{}).RowsAffected == 1
}

func ResetUserPasswordByEmail(email string, password string) error {
	if email == "" || password == "" {
		return errors.New("邮箱地址或密码为空！")
	}
	user, err := GetUniqueUserByEmail(email)
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return applyPasswordSecurityTx(tx, user.Id, password)
	}); err != nil {
		return err
	}
	return InvalidateUserCache(user.Id)
}

func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleAdminUser
}

func ValidateAccessToken(token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	token = strings.Replace(token, "Bearer ", "", 1)
	user := &User{}
	err := DB.Where("access_token = ?", token).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	return user, nil
}

// GetUserQuota gets quota from Redis first, falls back to DB if needed
func GetUserQuota(id int, fromDB bool) (quota int, err error) {
	return GetUserQuotaContext(context.Background(), id, fromDB)
}

func GetUserQuotaContext(ctx context.Context, id int, fromDB bool) (quota int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !fromDB && common.RedisEnabled {
		userCache, cacheErr := GetUserCacheContext(ctx, id)
		if cacheErr == nil {
			return userCache.Quota, nil
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	var balances struct {
		Quota     int
		GiftQuota int
	}
	err = DB.WithContext(ctx).Model(&User{}).
		Where("id = ?", id).
		Select("quota", "gift_quota").
		Find(&balances).Error
	if err != nil {
		return 0, err
	}

	// PostgreSQL evaluates int4 + int4 as int4, so adding the columns in SQL
	// can overflow even though both stored balances are individually valid.
	// Keep the addition in Go for all three dialects and reject architectures
	// that cannot represent the combined value instead of wrapping it.
	total := int64(balances.Quota) + int64(balances.GiftQuota)
	maxInt := int64(^uint(0) >> 1)
	if total > maxInt || total < -maxInt-1 {
		return 0, errors.New("combined user quota exceeds platform integer range")
	}
	return int(total), nil
}

func GetUserRechargeQuota(id int, fromDB bool) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("quota").Find(&quota).Error
	return quota, err
}

func GetUserGiftQuota(id int, fromDB bool) (quota int, err error) {
	if !fromDB && common.RedisEnabled {
		quota, err := getUserGiftQuotaCache(id)
		if err == nil {
			return quota, nil
		}
	}
	err = DB.Model(&User{}).Where("id = ?", id).Select("gift_quota").Find(&quota).Error
	return quota, err
}

func GetUserUsedQuota(id int) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("used_quota").Find(&quota).Error
	return quota, err
}

func GetUserEmail(id int) (email string, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("email").Find(&email).Error
	return email, err
}

// GetUserGroup gets group from Redis first, falls back to DB if needed
func GetUserGroup(id int, fromDB bool) (group string, err error) {
	if !fromDB && common.RedisEnabled {
		group, err := getUserGroupCache(id)
		if err == nil {
			return group, nil
		}
	}
	groupCol := commonGroupCol
	if groupCol == "" {
		groupCol = "`group`"
	}
	err = DB.Model(&User{}).Where("id = ?", id).Select(groupCol).Find(&group).Error
	if err != nil {
		return "", err
	}

	return group, nil
}

// GetUserSetting gets setting from Redis first, falls back to DB if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
	}
	var safeSetting sql.NullString
	err = DB.Model(&User{}).Where("id = ?", id).Select("setting").Find(&safeSetting).Error
	if err != nil {
		return settingMap, err
	}
	var setting string
	if safeSetting.Valid {
		setting = safeSetting.String
	}
	userBase := &UserBase{
		Setting: setting,
	}
	return userBase.GetSetting(), nil
}

func IncreaseUserQuota(id int, quota int, db bool) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	_, err = CreditRechargeQuota(id, quota, QuotaTransactionRef{
		Type:           QuotaTransactionTypeTopup,
		Source:         QuotaTransactionSourceLegacy,
		ReferenceType:  "legacy_user_quota",
		IdempotencyKey: "legacy:increase:recharge:" + common.GetUUID(),
	})
	return err
}

func IncreaseUserGiftQuota(id int, quota int, db bool) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	_, err = CreditGiftQuota(id, quota, QuotaTransactionRef{
		Type:           QuotaTransactionTypeGift,
		Source:         QuotaTransactionSourceLegacy,
		ReferenceType:  "legacy_user_gift_quota",
		IdempotencyKey: "legacy:increase:gift:" + common.GetUUID(),
	})
	return err
}

func increaseUserQuota(id int, quota int) (err error) {
	return IncreaseUserQuota(id, quota, true)
}

func DecreaseUserQuota(id int, quota int, db bool) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	_, err = DebitQuotaPreferGift(id, quota, QuotaTransactionRef{
		Type:           QuotaTransactionTypeConsumePre,
		Source:         QuotaTransactionSourceLegacy,
		ReferenceType:  "legacy_user_quota",
		IdempotencyKey: "legacy:decrease:" + common.GetUUID(),
	})
	return err
}

func decreaseUserQuota(id int, quota int) (err error) {
	return DecreaseUserQuota(id, quota, true)
}

func DeltaUpdateUserQuota(id int, delta int) (err error) {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return IncreaseUserQuota(id, delta, false)
	} else {
		return DecreaseUserQuota(id, -delta, false)
	}
}

//func GetRootUserEmail() (email string) {
//	DB.Model(&User{}).Where("role = ?", common.RoleRootUser).Select("email").Find(&email)
//	return email
//}

func GetRootUser() (user *User) {
	DB.Where("role = ?", common.RoleRootUser).First(&user)
	return user
}

func UpdateUserLastLoginAt(id int) {
	if err := DB.Model(&User{}).Where("id = ?", id).Update("last_login_at", common.GetTimestamp()).Error; err != nil {
		common.SysLog("failed to update user last_login_at: " + err.Error())
	}
}

func UpdateUserUsedQuotaAndRequestCount(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUsedQuota, id, quota)
		addNewRecord(BatchUpdateTypeRequestCount, id, 1)
		return
	}
	updateUserUsedQuotaAndRequestCount(id, quota, 1)
}

func updateUserUsedQuotaAndRequestCount(id int, quota int, count int) {
	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"request_count": gorm.Expr("request_count + ?", count),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota and request count: " + err.Error())
		return
	}

	//// 更新缓存
	//if err := invalidateUserCache(id); err != nil {
	//	common.SysError("failed to invalidate user cache: " + err.Error())
	//}
}

func updateUserQuotaUsedQuotaAndRequestCount(id int, quota int, usedQuota int, requestCount int) {
	if quota == 0 && usedQuota == 0 && requestCount == 0 {
		return
	}
	if quota != 0 {
		if err := DeltaUpdateUserQuota(id, quota); err != nil {
			common.SysLog("failed to batch update user wallet quota: " + err.Error())
		}
	}
	if usedQuota == 0 && requestCount == 0 {
		return
	}

	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", usedQuota),
			"request_count": gorm.Expr("request_count + ?", requestCount),
		},
	).Error
	if err != nil {
		common.SysLog("failed to batch update user quota, used quota and request count: " + err.Error())
	}
}

func updateUserUsedQuota(id int, quota int) {
	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota": gorm.Expr("used_quota + ?", quota),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota: " + err.Error())
	}
}

func updateUserRequestCount(id int, count int) {
	err := DB.Model(&User{}).Where("id = ?", id).Update("request_count", gorm.Expr("request_count + ?", count)).Error
	if err != nil {
		common.SysLog("failed to update user request count: " + err.Error())
	}
}

// GetUsernameById gets username from Redis first, falls back to DB if needed
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		username, err := getUserNameCache(id)
		if err == nil {
			return username, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error
	if err != nil {
		return "", err
	}

	return username, nil
}

func IsLinuxDOIdAlreadyTaken(linuxDOId string) bool {
	var user User
	err := DB.Unscoped().Where("linux_do_id = ?", linuxDOId).First(&user).Error
	return !errors.Is(err, gorm.ErrRecordNotFound)
}

func (user *User) FillUserByLinuxDOId() error {
	if user.LinuxDOId == "" {
		return errors.New("linux do id is empty")
	}
	err := DB.Where("linux_do_id = ?", user.LinuxDOId).First(user).Error
	return err
}

func RootUserExists() bool {
	var user User
	err := DB.Where("role = ?", common.RoleRootUser).First(&user).Error
	if err != nil {
		return false
	}
	return true
}
