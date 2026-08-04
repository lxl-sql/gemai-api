package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Token struct {
	CacheVersion       int            `json:"-" gorm:"-"`
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"-" gorm:"type:varchar(128);uniqueIndex"`
	KeyHash            *string        `json:"-" gorm:"type:char(64);index"`
	KeyHint            string         `json:"key" gorm:"type:varchar(32)"`
	PlainKey           string         `json:"-" gorm:"-"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (token *Token) Clean() {
	token.Key = ""
	token.PlainKey = ""
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

func (token *Token) GetFullKey() string {
	return token.PlainKey
}

func (token *Token) GetMaskedKey() string {
	if token.KeyHint != "" {
		return token.KeyHint
	}
	return MaskTokenKey(token.Key)
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.Split(cleanIps, "\n")
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}

	// 5. 无 % 时，精确全匹配
	return input, nil
}

func validateLikePattern(input string) error {
	// 1. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return errors.New("搜索模式中不允许包含连续的 % 通配符")
	}

	// 2. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return errors.New("搜索模式中最多允许包含 2 个 % 通配符")
	}

	// 3. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return errors.New("使用模糊搜索时，关键词长度至少为 2 个字符")
		}
	}

	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog("failed to count user tokens: " + err.Error())
			return nil, 0, errors.New("获取令牌数量失败")
		}
		if int(count) > maxTokens {
			return nil, 0, errors.New("令牌数量超过上限，仅允许精确搜索，请勿使用 % 通配符")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		if strings.Contains(token, "%") {
			tokenPattern, err := sanitizeLikePattern(token)
			if err != nil {
				return nil, 0, err
			}
			baseQuery = baseQuery.Where("key_hint LIKE ? ESCAPE '!'", tokenPattern)
		} else {
			keyHash := common.GenerateHMAC(token)
			baseQuery = baseQuery.Where(clause.Or(
				clause.Eq{Column: clause.Column{Name: "key_hash"}, Value: keyHash},
				clause.Eq{Column: clause.Column{Name: "key"}, Value: token},
				clause.Eq{Column: clause.Column{Name: "key_hint"}, Value: token},
			))
		}
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError("failed to count search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError("failed to search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	return ValidateUserTokenContext(context.Background(), key)
}

func ValidateUserTokenContext(ctx context.Context, key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKeyContext(ctx, key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted {
			return token, ErrTokenExhausted
		}
		if token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExpired
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog("failed to update token status" + err.Error())
				}
			}
			return token, ErrTokenInvalid
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExhausted
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog("failed to update token status" + err.Error())
				}
			}
			return token, ErrTokenExhausted
		}
		return token, nil
	}
	common.SysLog("ValidateUserToken: failed to get token: " + err.Error())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	token := Token{Id: id}
	err := DB.First(&token, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	return GetTokenByKeyContext(context.Background(), key, fromDB)
}

func GetTokenByKeyContext(ctx context.Context, key string, fromDB bool) (*Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fromDB || !common.RedisEnabled {
		token, err := loadTokenByKeyFromDB(ctx, key)
		if err != nil {
			return nil, err
		}
		cacheLoadedToken(token)
		return cloneAuthToken(token), nil
	}
	if token, err := cacheGetTokenByKeyContext(ctx, key); err == nil {
		return cloneAuthToken(*token), nil
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	keyHash := common.GenerateHMAC(key)
	token, err := coalesceAuthCacheLoad(ctx, authCacheLoadNamespaceToken, keyHash, func() (Token, error) {
		// Another request may have rebuilt the token while this caller waited to
		// enter the flight. Recheck Redis before querying PostgreSQL.
		if cached, cacheErr := cacheGetTokenByKey(key); cacheErr == nil {
			return *cached, nil
		}
		loaded, loadErr := loadTokenByKeyFromDB(context.Background(), key)
		if loadErr != nil {
			return Token{}, loadErr
		}
		cacheLoadedToken(loaded)
		return loaded, nil
	})
	if err != nil {
		return nil, err
	}
	return cloneAuthToken(token), nil
}

func loadTokenByKeyFromDB(ctx context.Context, key string) (Token, error) {
	var token Token
	db := DB.WithContext(ctx)
	err := db.Where(clause.Eq{
		Column: clause.Column{Name: "key_hash"},
		Value:  common.GenerateHMAC(key),
	}).Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Compatibility fallback for credentials created by pre-key_hash instances.
		// Separate indexed probes avoid PostgreSQL choosing an id-ordered scan for
		// `key_hash = ? OR key = ?` when the credential does not exist.
		err = db.Where(clause.Eq{
			Column: clause.Column{Name: "key"},
			Value:  key,
		}).Take(&token).Error
	}
	if err != nil {
		return Token{}, err
	}
	token.Key = key
	token.PlainKey = key
	return token, nil
}

func cacheLoadedToken(token Token) {
	if common.RedisEnabled {
		if cacheErr := cacheSetToken(token); cacheErr != nil {
			common.SysLog("failed to update token cache: " + cacheErr.Error())
		}
	}
}

func cloneAuthToken(token Token) *Token {
	clone := token
	if token.KeyHash != nil {
		keyHash := *token.KeyHash
		clone.KeyHash = &keyHash
	}
	if token.AllowIps != nil {
		allowIps := *token.AllowIps
		clone.AllowIps = &allowIps
	}
	return &clone
}

func (token *Token) Insert() error {
	rawKey, err := token.prepareNewCredential()
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(token).Error; err != nil {
			return err
		}
		return createTokenUsageSourceMetaTx(tx, token)
	}); err != nil {
		return err
	}
	token.PlainKey = rawKey
	token.Key = rawKey
	return nil
}

func (token *Token) InsertWithSecurityPolicy(policy *TokenSecurityPolicy) error {
	if policy == nil {
		return token.Insert()
	}
	rawKey, err := token.prepareNewCredential()
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(token).Error; err != nil {
			return err
		}
		policy.TokenId = token.Id
		if err := policy.Validate(); err != nil {
			return err
		}
		if err := tx.Save(policy).Error; err != nil {
			return err
		}
		return createTokenUsageSourceMetaTx(tx, token)
	}); err != nil {
		return err
	}
	token.PlainKey = rawKey
	token.Key = rawKey
	return nil
}

func (token *Token) prepareNewCredential() (string, error) {
	rawKey := token.Key
	if rawKey == "" {
		return "", errors.New("token key is empty")
	}
	keyHash := common.GenerateHMAC(rawKey)
	token.KeyHash = &keyHash
	token.KeyHint = MaskTokenKey(rawKey)
	// Keep the plaintext lookup column during the rolling compatibility phase.
	// Older instances only query this column, while upgraded instances can use
	// key_hash. Plaintext removal is a separate post-rollout migration.
	token.Key = rawKey
	return rawKey, nil
}

// BackfillTokenKeyMetadata adds keyed fingerprints and display hints without
// changing the plaintext lookup column required by instances from before the
// rolling compatibility release.
func BackfillTokenKeyMetadata() error {
	const batchSize = 200
	processed := 0
	for {
		count, err := BackfillTokenKeyMetadataBatch(context.Background(), batchSize)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		processed += count
		if processed%1000 == 0 {
			common.SysLog(fmt.Sprintf("token key metadata backfill progress: processed=%d", processed))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func BackfillTokenKeyMetadataBatch(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		return 0, errors.New("token key metadata batch size must be positive")
	}
	var tokens []Token
	if err := DB.WithContext(ctx).Unscoped().
		Where("key_hash IS NULL").
		Where(clause.Neq{Column: clause.Column{Name: "key"}, Value: ""}).
		Order("id").
		Limit(batchSize).
		Find(&tokens).Error; err != nil {
		return 0, err
	}
	if len(tokens) == 0 {
		return 0, nil
	}
	if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, token := range tokens {
			keyHash := common.GenerateHMAC(token.Key)
			if err := tx.Unscoped().
				Model(&Token{}).
				Where("id = ? AND key_hash IS NULL", token.Id).
				Updates(map[string]interface{}{
					"key_hash": keyHash,
					"key_hint": MaskTokenKey(token.Key),
				}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return len(tokens), nil
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	err = updateTokenFields(DB, token)
	if err != nil || !common.RedisEnabled || common.RDB == nil {
		return err
	}
	return cacheDeleteTokenCredential(token)
}

func updateTokenFields(tx *gorm.DB, token *Token) error {
	return tx.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry").Updates(token).Error
}

func (token *Token) UpdateWithSecurityPolicy(policy *TokenSecurityPolicy) (err error) {
	if policy == nil {
		return token.Update()
	}
	policy.TokenId = token.Id
	if err := policy.Validate(); err != nil {
		return err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := updateTokenFields(tx, token); err != nil {
			return err
		}
		return tx.Save(policy).Error
	})
	if err != nil || !common.RedisEnabled || common.RDB == nil {
		return err
	}
	tokenCacheErr := cacheDeleteTokenCredential(token)
	if tokenCacheErr != nil {
		tokenCacheErr = cacheDisableTokenCredential(token)
	}
	policyCacheErr := syncCommittedTokenSecurityPolicyCache(token, policy)
	if policyCacheErr != nil {
		cacheSynchronized := false
		policy.CacheSynchronized = &cacheSynchronized
	}
	if tokenCacheErr == nil && policyCacheErr == nil {
		return nil
	}
	common.SysError(fmt.Sprintf(
		"token security update committed with degraded cache recovery token_id=%d token_cache_error=%v policy_cache_error=%v",
		token.Id, tokenCacheErr, policyCacheErr,
	))
	return nil
}

func (token *Token) SelectUpdate() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheSetToken(*token)
				if err != nil {
					common.SysLog("failed to update token cache: " + err.Error())
				}
			})
		}
	}()
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := PurgeTokenUsageSourcesTx(tx, token.Id, token.UserId); err != nil {
			return err
		}
		return tx.Delete(token).Error
	})
	if err != nil || !common.RedisEnabled {
		return err
	}
	return cacheDeleteTokenCredential(token)
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(tokenId int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	// Token quota is part of the authorization/billing boundary and must be
	// updated synchronously. Batching this in per-process memory is unsafe in
	// multi-instance deployments: other instances can keep accepting requests
	// against a stale DB balance, and decreases bypass the remain_quota guard.
	if err = increaseTokenQuota(tokenId, quota); err != nil {
		return err
	}
	if common.RedisEnabled {
		if cacheErr := cacheIncrTokenQuota(key, int64(quota)); cacheErr != nil {
			common.SysLog("failed to increase token quota cache: " + cacheErr.Error())
		}
	}
	return nil
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

func DecreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	// See IncreaseTokenQuota: token quota changes must remain strongly
	// consistent and keep the DB-side remain_quota >= quota guard.
	if err = decreaseTokenQuota(id, quota); err != nil {
		return err
	}
	if common.RedisEnabled {
		if cacheErr := cacheDecrTokenQuota(key, int64(quota)); cacheErr != nil {
			common.SysLog("failed to decrease token quota cache: " + cacheErr.Error())
		}
	}
	return nil
}

func decreaseTokenQuota(id int, quota int) (err error) {
	result := DB.Model(&Token{}).Where("id = ? AND (unlimited_quota = ? OR remain_quota >= ?)", id, true, quota).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("insufficient token quota")
	}
	return nil
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("ids 不能为空！")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	sort.Slice(tokens, func(i int, j int) bool {
		return tokens[i].Id < tokens[j].Id
	})
	for i := range tokens {
		if err := PurgeTokenUsageSourcesTx(tx, tokens[i].Id, userId); err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	if common.RedisEnabled {
		for _, t := range tokens {
			if err := cacheDeleteTokenCredential(&t); err != nil {
				return len(tokens), err
			}
		}
	}

	return len(tokens), nil
}

// RotateTokenKey atomically replaces a credential while preserving token ID
// and usage history. The old cache entry is synchronously evicted before the
// new credential is returned to the caller.
func RotateTokenKey(id int, userId int, newKey string) (*Token, error) {
	if id <= 0 || userId <= 0 || newKey == "" {
		return nil, errors.New("invalid token rotation request")
	}
	var token Token
	var oldKey string
	var oldKeyHash *string
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ?", id, userId).First(&token).Error; err != nil {
			return err
		}
		oldKey = token.Key
		oldKeyHash = token.KeyHash
		newKeyHash := common.GenerateHMAC(newKey)
		token.KeyHash = &newKeyHash
		token.KeyHint = MaskTokenKey(newKey)
		// Keep rotations readable by pre-compatibility instances until the
		// rolling deployment has fully replaced them.
		token.Key = newKey
		token.PlainKey = newKey
		token.AccessedTime = common.GetTimestamp()
		return tx.Model(&Token{}).
			Where("id = ? AND user_id = ?", id, userId).
			Updates(map[string]interface{}{
				"key":           token.Key,
				"key_hash":      newKeyHash,
				"key_hint":      token.KeyHint,
				"accessed_time": token.AccessedTime,
			}).Error
	}); err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		if err := cacheDeleteStoredTokenCredential(oldKey, oldKeyHash); err != nil {
			return &token, err
		}
		if err := cacheDeleteTokenHash(*token.KeyHash); err != nil {
			return &token, err
		}
	}
	return &token, nil
}

// SuspendTokenForRisk disables only the affected credential. Account access
// and the user's other API tokens remain available for recovery.
func SuspendTokenForRisk(id int, key string) error {
	if id <= 0 || key == "" {
		return errors.New("invalid token risk suspension")
	}
	if err := DB.Model(&Token{}).
		Where("id = ?", id).
		Update("status", common.TokenStatusDisabled).Error; err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		token := &Token{Id: id, PlainKey: key}
		if err := cacheDisableTokenCredential(token); err != nil {
			return &TokenRiskSuspensionCacheError{Err: err}
		}
	}
	return nil
}

type TokenRiskSuspensionCacheError struct {
	Err error
}

func (err *TokenRiskSuspensionCacheError) Error() string {
	return "token was suspended but credential cache synchronization failed: " + err.Err.Error()
}

func (err *TokenRiskSuspensionCacheError) Unwrap() error {
	return err.Err
}

func TokenRiskSuspensionCommitted(err error) bool {
	var cacheErr *TokenRiskSuspensionCacheError
	return errors.As(err, &cacheErr)
}

func SuspendTokenForRiskByID(id int) error {
	if id <= 0 {
		return errors.New("invalid token risk suspension")
	}
	var token Token
	if err := DB.Select("id", "key", "key_hash").First(&token, id).Error; err != nil {
		return err
	}
	if err := DB.Model(&Token{}).Where("id = ?", id).Update("status", common.TokenStatusDisabled).Error; err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		if err := cacheDisableTokenCredential(&token); err != nil {
			return &TokenRiskSuspensionCacheError{Err: err}
		}
	}
	return nil
}

// InvalidateUserTokensCache 清理指定用户所有令牌在 Redis 中的缓存，
// 配合 InvalidateUserCache 使用，可在用户被禁用/删除时立即阻断其令牌的请求。
// 下一次请求将从数据库重新加载令牌及用户状态，从而立即识别出被禁用的用户。
func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("userId 无效")
	}
	var tokens []Token
	if err := DB.Unscoped().
		Select("id", commonKeyCol, "key_hash").
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}
	var firstErr error
	for _, t := range tokens {
		if err := cacheDeleteTokenCredential(&t); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
