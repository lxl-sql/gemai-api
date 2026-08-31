package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func buildMaskedTokenResponse(token *model.Token) *model.Token {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Status = token.EffectiveStatus(common.GetTimestamp())
	maskedToken.KeyHint = token.GetMaskedKey()
	maskedToken.Key = ""
	maskedToken.PlainKey = ""
	return &maskedToken
}

func normalizeTokenExpirationForWrite(expiredTime int64) int64 {
	if expiredTime == 0 {
		return -1
	}
	return expiredTime
}

func validTokenExpirationForWrite(expiredTime int64) bool {
	return expiredTime == -1 || expiredTime > common.GetTimestamp()
}

func tokenMutationCachePending(tokenId int, err error) bool {
	if !model.TokenMutationCommitted(err) {
		return false
	}
	common.SysError(fmt.Sprintf("token mutation committed with pending cache synchronization token_id=%d error=%v", tokenId, err))
	return true
}

func tokenSecurityPolicyAuditDetail(policy *model.TokenSecurityPolicy) map[string]interface{} {
	if policy == nil {
		return nil
	}
	detail := map[string]interface{}{
		"max_quota_per_request": policy.MaxQuotaPerRequest,
		"hourly_quota":          policy.HourlyQuota,
		"daily_quota":           policy.DailyQuota,
		"risk_mode":             policy.RiskMode,
	}
	if policy.CacheSynchronized != nil {
		detail["cache_synchronized"] = *policy.CacheSynchronized
	}
	return detail
}

func buildMaskedTokenResponses(tokens []*model.Token) []*model.Token {
	maskedTokens := make([]*model.Token, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func GetAllTokens(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, err := model.CountUserTokens(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	userId := c.GetInt("id")
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(userId, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	// This endpoint displays accounting state, so bypass Redis and return the
	// balance committed in the primary database.
	token, err := model.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), true)
	if err != nil {
		common.SysError("failed to get token by key: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":               "token_usage",
			"name":                 token.Name,
			"total_granted":        token.RemainQuota + token.UsedQuota,
			"total_used":           token.UsedQuota,
			"total_available":      token.RemainQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits":         token.GetModelLimitsMap(),
			"model_limits_enabled": token.ModelLimitsEnabled,
			"expires_at":           expiredAt,
		},
	})
}

func AddToken(c *gin.Context) {
	request := struct {
		model.Token
		SecurityPolicy *dto.UserTokenSecurityPolicyRequest `json:"security_policy"`
	}{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	token.ExpiredTime = normalizeTokenExpirationForWrite(token.ExpiredTime)
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if !validTokenExpirationForWrite(token.ExpiredTime) {
		common.ApiErrorI18n(c, i18n.MsgTokenExpireTimeInvalid)
		return
	}
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := int((1000000000 * common.QuotaPerUnit))
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	maxTokens := operation_setting.GetMaxUserTokens()
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		common.SysLog("failed to generate token key: " + err.Error())
		return
	}
	cleanToken := model.Token{
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
	}
	securityPolicy, err := service.BuildUserWritableTokenSecurityPolicy(0, request.SecurityPolicy)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = cleanToken.InsertWithSecurityPolicyLimit(securityPolicy, maxTokens)
	if err != nil {
		if errors.Is(err, model.ErrTokenLimitExceeded) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": fmt.Sprintf("已达到最大令牌数量限制 (%d)", maxTokens),
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionTokenCreate, "token", strconv.Itoa(cleanToken.Id), true, map[string]interface{}{
		"name":            cleanToken.Name,
		"unlimited_quota": cleanToken.UnlimitedQuota,
		"remain_quota":    cleanToken.RemainQuota,
		"group":           cleanToken.Group,
		"expired_time":    cleanToken.ExpiredTime,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":  cleanToken.Id,
			"key": cleanToken.GetFullKey(),
		},
	})
}

func DeleteToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userId := c.GetInt("id")
	err = model.DeleteTokenById(id, userId)
	cachePending := false
	if err != nil {
		cachePending = tokenMutationCachePending(id, err)
		if !cachePending {
			common.ApiError(c, err)
			return
		}
	}
	model.RecordOperationLog(c, model.OpActionTokenDelete, "token", strconv.Itoa(id), true, map[string]interface{}{
		"cache_invalidation_pending": cachePending,
	})
	message := ""
	if cachePending {
		message = common.TranslateMessage(c, i18n.MsgTokenCacheSyncPending)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": message,
	})
}

func RotateToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		return
	}
	token, err := model.RotateTokenKey(id, c.GetInt("id"), key)
	if err != nil {
		if token != nil && token.GetFullKey() != "" {
			common.SysError(fmt.Sprintf("token %d rotated but cache invalidation needs retry: %v", id, err))
			model.RecordOperationLog(c, model.OpActionTokenRotate, "token", strconv.Itoa(id), true, map[string]interface{}{
				"name":                       token.Name,
				"cache_invalidation_pending": true,
			})
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "API key rotated; cache invalidation is pending and the old key may remain valid briefly",
				"data": gin.H{
					"id":  token.Id,
					"key": token.GetFullKey(),
				},
			})
			return
		}
		switch {
		case errors.Is(err, model.ErrTokenDisabled):
			common.ApiErrorI18n(c, i18n.MsgTokenDisabledCannotRotate)
		case errors.Is(err, model.ErrTokenExpired):
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotRotate)
		case errors.Is(err, model.ErrTokenExhausted):
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotRotate)
		case errors.Is(err, model.ErrTokenMutationRaced):
			common.ApiErrorI18n(c, i18n.MsgTokenRotationConflict)
		default:
			common.ApiError(c, err)
		}
		return
	}
	model.RecordOperationLog(c, model.OpActionTokenRotate, "token", strconv.Itoa(id), true, map[string]interface{}{
		"name": token.Name,
	})
	common.ApiSuccess(c, gin.H{
		"id":  token.Id,
		"key": token.GetFullKey(),
	})
}

func GetTokenSecurityPolicy(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetTokenByIds(tokenId, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	policy, err := model.GetEffectiveTokenSecurityPolicy(
		tokenId,
		c.GetInt("id"),
		common.GetContextKeyString(c, constant.ContextKeyUserGroup),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, policy)
}

func GetDefaultTokenSecurityPolicy(c *gin.Context) {
	requested := model.DefaultTokenSecurityPolicy()
	profile, err := model.GetApplicableTokenSecurityProfile(
		c.GetInt("id"),
		common.GetContextKeyString(c, constant.ContextKeyUserGroup),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &model.TokenSecurityPolicyView{
		TokenSecurityPolicy: requested,
		AdminProfile:        profile,
		EffectivePolicy:     model.MergeTokenSecurityPolicy(requested, profile),
	})
}

func ListTokenSecurityProfiles(c *gin.Context) {
	profiles, err := model.ListTokenSecurityProfiles()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, profiles)
}

func UpsertTokenSecurityProfile(c *gin.Context) {
	profile := &model.TokenSecurityProfile{}
	rawBody, err := c.GetRawData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := common.Unmarshal(rawBody, profile); err != nil {
		common.ApiError(c, err)
		return
	}
	providedFields := make(map[string]interface{})
	if err := common.Unmarshal(rawBody, &providedFields); err != nil {
		common.ApiError(c, err)
		return
	}
	sustainedRpmValue, sustainedRpmProvided := providedFields["sustained_rpm"]
	userSustainedRpmValue, userSustainedRpmProvided := providedFields["user_sustained_rpm"]
	userBurstCapacityValue, userBurstCapacityProvided := providedFields["user_burst_capacity"]
	userMaxConcurrencyValue, userMaxConcurrencyProvided := providedFields["user_max_concurrency"]
	userHourlyQuotaValue, userHourlyQuotaProvided := providedFields["user_hourly_quota"]
	userDailyQuotaValue, userDailyQuotaProvided := providedFields["user_daily_quota"]
	fieldMask := model.TokenSecurityProfileFieldMask{
		SustainedRpm:       sustainedRpmProvided && sustainedRpmValue != nil,
		UserSustainedRpm:   userSustainedRpmProvided && userSustainedRpmValue != nil,
		UserBurstCapacity:  userBurstCapacityProvided && userBurstCapacityValue != nil,
		UserMaxConcurrency: userMaxConcurrencyProvided && userMaxConcurrencyValue != nil,
		UserHourlyQuota:    userHourlyQuotaProvided && userHourlyQuotaValue != nil,
		UserDailyQuota:     userDailyQuotaProvided && userDailyQuotaValue != nil,
	}
	if c.Query("create_only") == "true" {
		err = model.CreateTokenSecurityProfile(profile)
	} else {
		err = model.UpsertTokenSecurityProfileWithFieldMask(profile, fieldMask)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(
		c,
		model.OpActionTokenSecurityProfileUpdate,
		"token_security_profile",
		profile.ScopeType+":"+profile.ScopeValue,
		true,
		map[string]interface{}{
			"scope_type":             profile.ScopeType,
			"scope_value":            profile.ScopeValue,
			"sustained_rps":          profile.SustainedRps,
			"sustained_rpm":          profile.SustainedRpm,
			"burst_capacity":         profile.BurstCapacity,
			"max_concurrency":        profile.MaxConcurrency,
			"max_quota_per_request":  profile.MaxQuotaPerRequest,
			"hourly_quota":           profile.HourlyQuota,
			"daily_quota":            profile.DailyQuota,
			"max_distinct_models_5m": profile.MaxDistinctModels5m,
			"user_sustained_rpm":     profile.UserSustainedRpm,
			"user_burst_capacity":    profile.UserBurstCapacity,
			"user_max_concurrency":   profile.UserMaxConcurrency,
			"user_hourly_quota":      profile.UserHourlyQuota,
			"user_daily_quota":       profile.UserDailyQuota,
			"minimum_risk_mode":      profile.MinimumRiskMode,
			"fail_closed":            profile.FailClosed,
			"cache_synchronized":     profile.CacheSynchronized,
		},
	)
	message := ""
	if !profile.CacheSynchronized {
		message = "security profile saved, but cache synchronization is pending"
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": message,
		"data": struct {
			*model.TokenSecurityProfile
			CacheSynchronized bool `json:"cache_synchronized"`
		}{
			TokenSecurityProfile: profile,
			CacheSynchronized:    profile.CacheSynchronized,
		},
	})
}

func DeleteTokenSecurityProfile(c *gin.Context) {
	scopeType := c.Query("scope_type")
	scopeValue := c.Query("scope_value")
	cacheSynchronized, err := model.DeleteTokenSecurityProfile(scopeType, scopeValue)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(
		c,
		model.OpActionTokenSecurityProfileDelete,
		"token_security_profile",
		scopeType+":"+scopeValue,
		true,
		map[string]interface{}{
			"scope_type":         scopeType,
			"scope_value":        scopeValue,
			"cache_synchronized": cacheSynchronized,
		},
	)
	message := ""
	if !cacheSynchronized {
		message = "security profile deleted, but cache synchronization is pending"
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": message,
		"data": gin.H{
			"cache_synchronized": cacheSynchronized,
		},
	})
}

func UpdateTokenSecurityPolicy(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	request := &dto.UserTokenSecurityPolicyRequest{}
	if err := c.ShouldBindJSON(request); err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetTokenByIds(tokenId, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	previousPolicy, err := model.GetTokenSecurityPolicy(tokenId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	policy, err := service.BuildUserWritableTokenSecurityPolicy(tokenId, request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpsertTokenSecurityPolicy(policy, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionTokenUpdate, "token", strconv.Itoa(tokenId), true, map[string]interface{}{
		"security_policy":        true,
		"security_policy_before": tokenSecurityPolicyAuditDetail(previousPolicy),
		"security_policy_after":  tokenSecurityPolicyAuditDetail(policy),
	})
	common.ApiSuccess(c, policy)
}

func DeleteTokenSecurityPolicy(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetTokenByIds(tokenId, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	previousPolicy, err := model.GetTokenSecurityPolicy(tokenId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	currentPolicy, err := model.ResetUserWritableTokenSecurityPolicy(tokenId, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionTokenUpdate, "token", strconv.Itoa(tokenId), true, map[string]interface{}{
		"security_policy":        "reset",
		"security_policy_before": tokenSecurityPolicyAuditDetail(previousPolicy),
		"security_policy_after":  tokenSecurityPolicyAuditDetail(currentPolicy),
	})
	common.ApiSuccess(c, currentPolicy)
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := struct {
		model.Token
		SecurityPolicy *dto.UserTokenSecurityPolicyRequest `json:"security_policy"`
	}{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if statusOnly != "" && request.SecurityPolicy != nil {
		common.ApiError(c, fmt.Errorf("security_policy cannot be updated in status_only mode"))
		return
	}
	if statusOnly != "" && token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusDisabled {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if statusOnly == "" {
		token.ExpiredTime = normalizeTokenExpirationForWrite(token.ExpiredTime)
		if !validTokenExpirationForWrite(token.ExpiredTime) {
			common.ApiErrorI18n(c, i18n.MsgTokenExpireTimeInvalid)
			return
		}
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := int((1000000000 * common.QuotaPerUnit))
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	cleanToken, err := model.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	previousRemainQuota := cleanToken.RemainQuota
	previousUnlimitedQuota := cleanToken.UnlimitedQuota
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.ExpiredTime != -1 && cleanToken.ExpiredTime <= common.GetTimestamp() {
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotEnable)
			return
		}
		if cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotEable)
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
	}
	var previousSecurityPolicy *model.TokenSecurityPolicy
	var updatedSecurityPolicy *model.TokenSecurityPolicy
	if statusOnly != "" {
		err = cleanToken.Update()
	} else {
		if request.SecurityPolicy != nil {
			previousSecurityPolicy, err = model.GetTokenSecurityPolicy(cleanToken.Id)
			if err != nil {
				common.ApiError(c, err)
				return
			}
		}
		securityPolicy, policyErr := service.BuildUserWritableTokenSecurityPolicy(cleanToken.Id, request.SecurityPolicy)
		if policyErr != nil {
			common.ApiError(c, policyErr)
			return
		}
		updatedSecurityPolicy = securityPolicy
		err = cleanToken.UpdateWithSecurityPolicy(securityPolicy)
	}
	cachePending := false
	if err != nil {
		cachePending = tokenMutationCachePending(cleanToken.Id, err)
		if !cachePending {
			common.ApiError(c, err)
			return
		}
	}
	if updatedSecurityPolicy != nil && updatedSecurityPolicy.CacheSynchronized != nil && !*updatedSecurityPolicy.CacheSynchronized {
		cachePending = true
	}
	operationDetail := map[string]interface{}{
		"name":                       cleanToken.Name,
		"status":                     cleanToken.Status,
		"group":                      cleanToken.Group,
		"status_only":                statusOnly != "",
		"remain_quota_before":        previousRemainQuota,
		"remain_quota_after":         cleanToken.RemainQuota,
		"unlimited_quota_before":     previousUnlimitedQuota,
		"unlimited_quota_after":      cleanToken.UnlimitedQuota,
		"cache_invalidation_pending": cachePending,
	}
	if updatedSecurityPolicy != nil {
		operationDetail["security_policy_before"] = tokenSecurityPolicyAuditDetail(previousSecurityPolicy)
		operationDetail["security_policy_after"] = tokenSecurityPolicyAuditDetail(updatedSecurityPolicy)
	}
	model.RecordOperationLog(c, model.OpActionTokenUpdate, "token", strconv.Itoa(cleanToken.Id), true, operationDetail)
	message := ""
	if cachePending {
		message = common.TranslateMessage(c, i18n.MsgTokenCacheSyncPending)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": message,
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

const maxTokenBatchDelete = 1000

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 || len(tokenBatch.Ids) > maxTokenBatchDelete {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId)
	cachePending := false
	if err != nil {
		cachePending = tokenMutationCachePending(0, err)
		if !cachePending {
			common.ApiError(c, err)
			return
		}
	}
	model.RecordOperationLog(c, model.OpActionTokenDeleteBatch, "token", "", true, map[string]interface{}{
		"requested_count":            len(tokenBatch.Ids),
		"deleted_count":              count,
		"cache_invalidation_pending": cachePending,
	})
	message := ""
	if cachePending {
		message = common.TranslateMessage(c, i18n.MsgTokenCacheSyncPending)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": message,
		"data":    count,
	})
}
