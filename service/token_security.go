package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

var (
	ErrTokenSecurityRateLimit    = errors.New("token request rate limit exceeded")
	ErrTokenSecurityConcurrency  = errors.New("token concurrency limit exceeded")
	ErrTokenSecurityBudget       = errors.New("token security budget exceeded")
	ErrTokenSecurityModelRisk    = fmt.Errorf("%w: model risk limit exceeded", ErrTokenSecurityBudget)
	ErrTokenSecurityUnavailable  = errors.New("token security enforcement is unavailable")
	errTokenConcurrencyLeaseLost = errors.New("token concurrency lease no longer exists")
)

const (
	tokenSecurityConcurrencyLease         = 30 * time.Minute
	tokenSecurityConcurrencyRenewInterval = tokenSecurityConcurrencyLease / 3
	tokenSecurityPolicyContextKey         = "token_security_effective_policy"
	tokenSecurityBudgetPendingTTL         = 24 * time.Hour
	tokenSecurityBudgetFinalizedTTL       = 5 * time.Minute
)

var (
	reserveTokenSecurityBudgetScript = redis.NewScript(`
local existing_status = redis.call('HGET', KEYS[3], 'status')
if existing_status == 'reserved' or existing_status == 'finalized' then return 1 end
local amount = tonumber(ARGV[1])
local hour_limit = tonumber(ARGV[2])
local day_limit = tonumber(ARGV[3])
local hour_value = tonumber(redis.call('GET', KEYS[1]) or '0')
local day_value = tonumber(redis.call('GET', KEYS[2]) or '0')
if hour_limit > 0 and hour_value + amount > hour_limit then return -1 end
if day_limit > 0 and day_value + amount > day_limit then return -2 end
redis.call('INCRBY', KEYS[1], amount)
redis.call('EXPIRE', KEYS[1], 7200)
redis.call('INCRBY', KEYS[2], amount)
redis.call('EXPIRE', KEYS[2], 172800)
redis.call('HSET', KEYS[3], 'status', 'reserved', 'amount', amount)
redis.call('PEXPIRE', KEYS[3], ARGV[4])
return 1
`)
	finalizeTokenSecurityBudgetScript = redis.NewScript(`
local existing_status = redis.call('HGET', KEYS[3], 'status')
if existing_status == 'finalized' then
  return tonumber(redis.call('HGET', KEYS[3], 'result') or '0')
end
local actual = tonumber(ARGV[1])
local reserved = 0
if existing_status == 'reserved' then
  reserved = tonumber(redis.call('HGET', KEYS[3], 'amount') or '0')
elseif tonumber(ARGV[3]) == 1 then
  reserved = tonumber(ARGV[2])
end
local amount = actual - reserved
local hour_value = tonumber(redis.call('GET', KEYS[1]) or '0')
local day_value = tonumber(redis.call('GET', KEYS[2]) or '0')
if amount ~= 0 then
  hour_value = redis.call('INCRBY', KEYS[1], amount)
  day_value = redis.call('INCRBY', KEYS[2], amount)
  if hour_value < 0 then
    hour_value = 0
    redis.call('SET', KEYS[1], 0)
  end
  if day_value < 0 then
    day_value = 0
    redis.call('SET', KEYS[2], 0)
  end
  redis.call('EXPIRE', KEYS[1], 7200)
  redis.call('EXPIRE', KEYS[2], 172800)
end
local result = 0
if tonumber(ARGV[4]) > 0 and hour_value > tonumber(ARGV[4]) then
  result = -1
elseif tonumber(ARGV[5]) > 0 and day_value > tonumber(ARGV[5]) then
  result = -2
end
redis.call('HSET', KEYS[3], 'status', 'finalized', 'result', result)
redis.call('PEXPIRE', KEYS[3], ARGV[6])
return result
`)
	releaseTokenConcurrencyScript = redis.NewScript(`
local removed = redis.call('ZREM', KEYS[1], ARGV[1])
if redis.call('ZCARD', KEYS[1]) == 0 then
  redis.call('DEL', KEYS[1])
end
return removed
`)
	renewTokenConcurrencyScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local lease_ms = tonumber(ARGV[2])
local renewed = redis.call('ZADD', KEYS[1], 'XX', now_ms + lease_ms, ARGV[1])
if renewed ~= 1 then return 0 end
redis.call('PEXPIRE', KEYS[1], lease_ms)
return 1
`)
	allowTokenBucketScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local values = redis.call('HMGET', KEYS[1], 'tokens', 'updated_ms')
local tokens = tonumber(values[1])
local updated = tonumber(values[2])
if tokens == nil then tokens = capacity end
if updated == nil then updated = now_ms end
tokens = math.min(capacity, tokens + ((now_ms - updated) / 1000.0) * rate)
local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], math.ceil((capacity / rate) * 2000))
return allowed
`)
	acquireTokenConcurrencyScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local lease_ms = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[2]) then return 0 end
local added = redis.call('ZADD', KEYS[1], 'NX', now_ms + lease_ms, ARGV[1])
if added ~= 1 then return 0 end
redis.call('PEXPIRE', KEYS[1], lease_ms)
return 1
`)
	tokenModelRiskScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local cutoff_ms = now_ms - tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff_ms)
redis.call('ZADD', KEYS[1], now_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return redis.call('ZCARD', KEYS[1])
`)
)

type TokenTrafficLease struct {
	tokenId       int
	leaseId       string
	acquired      bool
	renewStop     chan struct{}
	renewDone     chan struct{}
	renewStopOnce sync.Once
}

type TokenBudgetReservation struct {
	tokenId            int
	userId             int
	clientIP           string
	hourKey            string
	dayKey             string
	reservationKey     string
	reserved           int64
	maxQuotaPerRequest int64
	hourlyQuota        int64
	dailyQuota         int64
	riskMode           string
	failClosed         bool
	windowReserved     bool
	windowAttempted    bool
}

type tokenSecurityBudgetError struct {
	kind      string
	attempted int64
	limit     int64
	suspended bool
	cacheSync *bool
}

func (err *tokenSecurityBudgetError) Error() string {
	return ErrTokenSecurityBudget.Error()
}

func (err *tokenSecurityBudgetError) Unwrap() error {
	return ErrTokenSecurityBudget
}

func notifyTokenSecurityUser(userId int, tokenId int, content string) {
	if userId <= 0 {
		return
	}
	permit, allowed, err := acquireNotificationPermit(userId, dto.NotifyTypeTokenSecurity)
	if err != nil {
		common.SysLog(fmt.Sprintf(
			"failed to reserve token security notification user_id=%d token_id=%d: %v",
			userId,
			tokenId,
			err,
		))
		return
	}
	if !allowed {
		return
	}
	gopool.Go(func() {
		attempted := false
		defer func() {
			if !attempted {
				permit.Release()
			}
		}()
		user, err := model.GetUserById(userId, true)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to load user for token security notification user_id=%d: %v", userId, err))
			return
		}
		userSetting := user.GetSetting()
		if !notificationChannelConfigured(
			user.Id,
			user.Email,
			userSetting,
			effectiveNotificationType(userSetting),
		) {
			return
		}
		if err := notifyUser(
			user.Id,
			user.Email,
			userSetting,
			dto.NewNotify(
				dto.NotifyTypeTokenSecurity,
				"API token security warning",
				content,
				nil,
			),
			false,
		); err != nil {
			common.SysLog(fmt.Sprintf("failed to send token security notification user_id=%d token_id=%d: %v", userId, tokenId, err))
			return
		}
		attempted = true
	})
}

func suspendTokenForSecurityRisk(tokenId int, key string) (bool, bool, error) {
	var err error
	if key == "" {
		err = model.SuspendTokenForRiskByID(tokenId)
	} else {
		err = model.SuspendTokenForRisk(tokenId, key)
	}
	if err == nil {
		return true, true, nil
	}
	if model.TokenRiskSuspensionCommitted(err) {
		return true, false, err
	}
	return false, false, err
}

func applyTokenBudgetRiskResponse(c *gin.Context, tokenId int, riskMode string, rejection error) error {
	var budgetErr *tokenSecurityBudgetError
	errors.As(rejection, &budgetErr)
	if riskMode == model.TokenRiskModeNotify || riskMode == model.TokenRiskModeSuspend {
		content := fmt.Sprintf("Token %d exceeded its configured quota limit.", tokenId)
		if budgetErr != nil {
			content = fmt.Sprintf(
				"Token %d exceeded its %s quota limit (attempted: %s, limit: %s).",
				tokenId,
				budgetErr.kind,
				logger.FormatQuota(int(budgetErr.attempted)),
				logger.FormatQuota(int(budgetErr.limit)),
			)
		}
		notifyTokenSecurityUser(c.GetInt("id"), tokenId, content)
	}
	if riskMode != model.TokenRiskModeSuspend {
		return rejection
	}
	suspended, cacheSynchronized, err := suspendTokenForSecurityRisk(tokenId, "")
	if budgetErr != nil && suspended {
		budgetErr.suspended = true
		budgetErr.cacheSync = &cacheSynchronized
	}
	if err != nil {
		common.SysError(fmt.Sprintf(
			"token budget suspension degraded token_id=%d committed=%t cache_synchronized=%t error=%v",
			tokenId,
			suspended,
			cacheSynchronized,
			err,
		))
	}
	return rejection
}

func BuildUserWritableTokenSecurityPolicy(
	tokenId int,
	request *dto.UserTokenSecurityPolicyRequest,
) (*model.TokenSecurityPolicy, error) {
	if request == nil {
		return nil, nil
	}
	if err := request.ValidateUserWritable(); err != nil {
		return nil, err
	}

	var policy *model.TokenSecurityPolicy
	if tokenId > 0 {
		stored, err := model.GetTokenSecurityPolicy(tokenId)
		if err != nil {
			return nil, err
		}
		policy = stored
	} else {
		policy = model.DefaultTokenSecurityPolicy()
	}
	if request.MaxQuotaPerRequest != nil {
		policy.MaxQuotaPerRequest = *request.MaxQuotaPerRequest
	}
	if request.HourlyQuota != nil {
		policy.HourlyQuota = *request.HourlyQuota
	}
	if request.DailyQuota != nil {
		policy.DailyQuota = *request.DailyQuota
	}
	if request.RiskMode != nil {
		policy.RiskMode = *request.RiskMode
	}
	policy.TokenId = tokenId
	return policy, nil
}

func tokenSecurityActive(policy *model.TokenSecurityPolicy) bool {
	return policy != nil && (policy.SustainedRps > 0 ||
		policy.MaxConcurrency > 0 ||
		policy.MaxQuotaPerRequest > 0 ||
		policy.HourlyQuota > 0 ||
		policy.DailyQuota > 0 ||
		policy.MaxDistinctModels5m > 0)
}

func getEffectiveTokenSecurityPolicy(c *gin.Context, tokenId int) (*model.TokenSecurityPolicy, error) {
	if cached, ok := c.Get(tokenSecurityPolicyContextKey); ok {
		if policy, ok := cached.(*model.TokenSecurityPolicy); ok && policy.TokenId == tokenId {
			return policy, nil
		}
	}
	view, err := model.GetEffectiveTokenSecurityPolicy(
		tokenId,
		c.GetInt("id"),
		common.GetContextKeyString(c, constant.ContextKeyUserGroup),
	)
	if err != nil {
		return nil, err
	}
	c.Set(tokenSecurityPolicyContextKey, view.EffectivePolicy)
	return view.EffectivePolicy, nil
}

func AcquireTokenTraffic(c *gin.Context, tokenId int) (*TokenTrafficLease, error) {
	policy, err := getEffectiveTokenSecurityPolicy(c, tokenId)
	if err != nil {
		return nil, err
	}
	lease := &TokenTrafficLease{tokenId: tokenId}
	if !tokenSecurityActive(policy) {
		return lease, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		if policy.FailClosed {
			return nil, ErrTokenSecurityUnavailable
		}
		logger.LogWarn(c, fmt.Sprintf("token security bypassed because Redis is unavailable token_id=%d", tokenId))
		return lease, nil
	}

	ctx := c.Request.Context()
	if policy.SustainedRps > 0 {
		allowed, err := allowTokenBucket(ctx, tokenId, policy.SustainedRps, policy.BurstCapacity)
		if err != nil {
			if policy.FailClosed {
				return nil, fmt.Errorf("%w: %v", ErrTokenSecurityUnavailable, err)
			}
			logger.LogWarn(c, fmt.Sprintf("token rate enforcement degraded token_id=%d error=%v", tokenId, err))
		} else if !allowed {
			return nil, ErrTokenSecurityRateLimit
		}
	}
	if policy.MaxConcurrency > 0 {
		lease.leaseId = c.GetString(common.RequestIdKey)
		if lease.leaseId == "" {
			lease.leaseId = common.NewRequestId()
		}
		allowed, err := acquireTokenConcurrency(ctx, tokenId, lease.leaseId, policy.MaxConcurrency)
		if err != nil {
			if policy.FailClosed {
				return nil, fmt.Errorf("%w: %v", ErrTokenSecurityUnavailable, err)
			}
			logger.LogWarn(c, fmt.Sprintf("token concurrency enforcement degraded token_id=%d error=%v", tokenId, err))
		} else if !allowed {
			return nil, ErrTokenSecurityConcurrency
		} else {
			lease.acquired = true
			lease.startRenewal()
		}
	}
	return lease, nil
}

func tokenConcurrencyKey(tokenId int) string {
	return fmt.Sprintf("token-security:{%d}:concurrency:v2", tokenId)
}

func (lease *TokenTrafficLease) startRenewal() {
	lease.renewStop = make(chan struct{})
	lease.renewDone = make(chan struct{})
	gopool.Go(func() {
		defer close(lease.renewDone)
		ticker := time.NewTicker(tokenSecurityConcurrencyRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !common.RedisEnabled || common.RDB == nil {
					continue
				}
				if err := renewTokenConcurrencyLease(context.Background(), lease.tokenId, lease.leaseId); err != nil {
					common.SysLog(fmt.Sprintf("failed to renew token concurrency lease token_id=%d: %v", lease.tokenId, err))
					if errors.Is(err, errTokenConcurrencyLeaseLost) {
						return
					}
				}
			case <-lease.renewStop:
				return
			}
		}
	})
}

func (lease *TokenTrafficLease) stopRenewal() {
	if lease == nil || lease.renewStop == nil {
		return
	}
	lease.renewStopOnce.Do(func() {
		close(lease.renewStop)
	})
	<-lease.renewDone
}

func (lease *TokenTrafficLease) Release(ctx context.Context) {
	if lease == nil || !lease.acquired || lease.leaseId == "" {
		return
	}
	lease.stopRenewal()
	if !common.RedisEnabled || common.RDB == nil {
		lease.acquired = false
		return
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if _, err := releaseTokenConcurrencyScript.Run(ctx, common.RDB, []string{
		tokenConcurrencyKey(lease.tokenId),
	}, lease.leaseId).Result(); err != nil {
		common.SysLog(fmt.Sprintf("failed to release token concurrency token_id=%d: %v", lease.tokenId, err))
	}
	lease.acquired = false
}

func renewTokenConcurrencyLease(ctx context.Context, tokenId int, leaseId string) error {
	renewed, err := renewTokenConcurrencyScript.Run(ctx, common.RDB, []string{
		tokenConcurrencyKey(tokenId),
	}, leaseId, tokenSecurityConcurrencyLease.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if renewed != 1 {
		return errTokenConcurrencyLeaseLost
	}
	return nil
}

func allowTokenBucket(ctx context.Context, tokenId int, rate int, capacity int) (bool, error) {
	if capacity < rate {
		capacity = rate
	}
	result, err := allowTokenBucketScript.Run(ctx, common.RDB, []string{
		fmt.Sprintf("token-security:{%d}:rate", tokenId),
	}, rate, capacity).Int()
	return result == 1, err
}

func acquireTokenConcurrency(ctx context.Context, tokenId int, leaseId string, limit int) (bool, error) {
	result, err := acquireTokenConcurrencyScript.Run(ctx, common.RDB, []string{
		tokenConcurrencyKey(tokenId),
	}, leaseId, limit, tokenSecurityConcurrencyLease.Milliseconds()).Int()
	return result == 1, err
}

func ReserveTokenSecurityBudget(c *gin.Context, tokenId int, quota int) (*TokenBudgetReservation, error) {
	if tokenId <= 0 || quota < 0 {
		return nil, errors.New("invalid token security budget reservation")
	}
	policy, err := getEffectiveTokenSecurityPolicy(c, tokenId)
	if err != nil {
		return nil, err
	}
	if policy.MaxQuotaPerRequest > 0 && int64(quota) > policy.MaxQuotaPerRequest {
		rejection := &tokenSecurityBudgetError{
			kind:      "per_request",
			attempted: int64(quota),
			limit:     policy.MaxQuotaPerRequest,
		}
		return nil, applyTokenBudgetRiskResponse(c, tokenId, policy.RiskMode, rejection)
	}
	now := time.Now().UTC()
	requestId := c.GetString(common.RequestIdKey)
	if requestId == "" {
		requestId = common.NewRequestId()
	}
	reservation := &TokenBudgetReservation{
		tokenId:            tokenId,
		userId:             c.GetInt("id"),
		clientIP:           c.ClientIP(),
		hourKey:            fmt.Sprintf("token-security:{%d}:quota:hour:%s", tokenId, now.Format("2006010215")),
		dayKey:             fmt.Sprintf("token-security:{%d}:quota:day:%s", tokenId, now.Format("20060102")),
		reservationKey:     fmt.Sprintf("token-security:{%d}:quota:reservation:%s", tokenId, common.Sha1([]byte(requestId))),
		reserved:           int64(quota),
		maxQuotaPerRequest: policy.MaxQuotaPerRequest,
		hourlyQuota:        policy.HourlyQuota,
		dailyQuota:         policy.DailyQuota,
		riskMode:           policy.RiskMode,
		failClosed:         policy.FailClosed,
	}
	if quota == 0 || (policy.HourlyQuota == 0 && policy.DailyQuota == 0) {
		return reservation, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		if policy.FailClosed {
			return nil, ErrTokenSecurityUnavailable
		}
		logger.LogWarn(c, fmt.Sprintf("token budget enforcement bypassed because Redis is unavailable token_id=%d", tokenId))
		return reservation, nil
	}
	reservation.windowAttempted = true
	allowed, err := reserveTokenSecurityBudgetScript.Run(
		c.Request.Context(),
		common.RDB,
		[]string{reservation.hourKey, reservation.dayKey, reservation.reservationKey},
		quota,
		policy.HourlyQuota,
		policy.DailyQuota,
		tokenSecurityBudgetPendingTTL.Milliseconds(),
	).Int()
	if err != nil {
		if policy.FailClosed {
			if _, rollbackErr := reservation.finalizeWindow(0); rollbackErr != nil {
				logger.LogWarn(c, fmt.Sprintf(
					"failed to roll back ambiguous token budget reservation token_id=%d error=%v",
					tokenId,
					rollbackErr,
				))
			}
			return nil, fmt.Errorf("%w: %v", ErrTokenSecurityUnavailable, err)
		}
		logger.LogWarn(c, fmt.Sprintf("token budget enforcement degraded token_id=%d error=%v", tokenId, err))
		return reservation, nil
	}
	if allowed == -1 {
		rejection := &tokenSecurityBudgetError{
			kind:      "hourly",
			attempted: int64(quota),
			limit:     policy.HourlyQuota,
		}
		return nil, applyTokenBudgetRiskResponse(c, tokenId, policy.RiskMode, rejection)
	}
	if allowed == -2 {
		rejection := &tokenSecurityBudgetError{
			kind:      "daily",
			attempted: int64(quota),
			limit:     policy.DailyQuota,
		}
		return nil, applyTokenBudgetRiskResponse(c, tokenId, policy.RiskMode, rejection)
	}
	if allowed != 1 {
		return nil, applyTokenBudgetRiskResponse(c, tokenId, policy.RiskMode, ErrTokenSecurityBudget)
	}
	reservation.windowReserved = true
	return reservation, nil
}

func (reservation *TokenBudgetReservation) finalizeWindow(actualQuota int64) (int, error) {
	if reservation.reservationKey == "" {
		reservation.reservationKey = fmt.Sprintf(
			"token-security:{%d}:quota:reservation:%s",
			reservation.tokenId,
			common.Sha1([]byte(common.NewRequestId())),
		)
	}
	windowReserved := 0
	if reservation.windowReserved {
		windowReserved = 1
	}
	return finalizeTokenSecurityBudgetScript.Run(
		context.Background(),
		common.RDB,
		[]string{reservation.hourKey, reservation.dayKey, reservation.reservationKey},
		actualQuota,
		reservation.reserved,
		windowReserved,
		reservation.hourlyQuota,
		reservation.dailyQuota,
		tokenSecurityBudgetFinalizedTTL.Milliseconds(),
	).Int()
}

func (reservation *TokenBudgetReservation) Finalize(actualQuota int64) {
	if reservation == nil {
		return
	}
	budgetExceeded := reservation.maxQuotaPerRequest > 0 && actualQuota > reservation.maxQuotaPerRequest
	limitKind := ""
	if budgetExceeded {
		limitKind = "per_request"
	}
	enforcementFailure := false
	settlementQuota := actualQuota
	if reservation.windowReserved {
		settlementQuota -= reservation.reserved
	}
	hasWindowBudget := reservation.hourlyQuota > 0 || reservation.dailyQuota > 0
	if settlementQuota != 0 && hasWindowBudget && (!common.RedisEnabled || common.RDB == nil) {
		enforcementFailure = settlementQuota > 0 && reservation.failClosed
	}
	shouldFinalizeWindow := hasWindowBudget && (settlementQuota != 0 || reservation.windowAttempted)
	if shouldFinalizeWindow && common.RedisEnabled && common.RDB != nil {
		windowResult, err := reservation.finalizeWindow(actualQuota)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to finalize token security budget token_id=%d: %v", reservation.tokenId, err))
			enforcementFailure = settlementQuota > 0 && reservation.failClosed
		} else if windowResult == -1 || windowResult == -2 {
			budgetExceeded = true
			if limitKind == "" {
				if windowResult == -1 {
					limitKind = "hourly"
				} else {
					limitKind = "daily"
				}
			}
		}
	}
	if !budgetExceeded && !enforcementFailure {
		return
	}

	suspendRequired := enforcementFailure ||
		(budgetExceeded && reservation.riskMode == model.TokenRiskModeSuspend)
	suspended := false
	cacheSynchronized := true
	var suspensionErr error
	if suspendRequired {
		suspended, cacheSynchronized, suspensionErr = suspendTokenForSecurityRisk(reservation.tokenId, "")
		if suspensionErr != nil {
			common.SysError(fmt.Sprintf(
				"token settlement suspension degraded token_id=%d committed=%t cache_synchronized=%t error=%v",
				reservation.tokenId,
				suspended,
				cacheSynchronized,
				suspensionErr,
			))
		}
	}
	common.SysError(fmt.Sprintf(
		"token security budget violation during settlement token_id=%d actual_quota=%d limit_kind=%s risk_mode=%s enforcement_failure=%t suspended=%t",
		reservation.tokenId,
		actualQuota,
		limitKind,
		reservation.riskMode,
		enforcementFailure,
		suspended,
	))

	if reservation.userId > 0 {
		detail := map[string]interface{}{
			"token_id":          reservation.tokenId,
			"signal":            "budget_settlement",
			"limit_kind":        limitKind,
			"reserved_quota":    reservation.reserved,
			"actual_quota":      actualQuota,
			"risk_mode":         reservation.riskMode,
			"enforcement_error": enforcementFailure,
			"token_suspended":   suspended,
		}
		if suspendRequired {
			detail["cache_synchronized"] = cacheSynchronized
		}
		model.RecordOperationAuditLog(
			reservation.userId,
			"Token budget violation detected during settlement",
			reservation.clientIP,
			model.OpActionTokenRisk,
			detail,
			nil,
			nil,
		)
	}

	if enforcementFailure ||
		(budgetExceeded &&
			(reservation.riskMode == model.TokenRiskModeNotify ||
				reservation.riskMode == model.TokenRiskModeSuspend)) {
		content := fmt.Sprintf(
			"Token %d exceeded its %s quota during settlement (reserved: %s, actual: %s).",
			reservation.tokenId,
			limitKind,
			logger.FormatQuota(int(reservation.reserved)),
			logger.FormatQuota(int(actualQuota)),
		)
		if enforcementFailure {
			content = fmt.Sprintf(
				"Token %d was suspended because quota enforcement became unavailable during settlement.",
				reservation.tokenId,
			)
		}
		notifyTokenSecurityUser(reservation.userId, reservation.tokenId, content)
	}
}

func CheckTokenModelRisk(c *gin.Context, tokenId int, modelName string) error {
	if tokenId <= 0 || modelName == "" {
		return nil
	}
	policy, err := getEffectiveTokenSecurityPolicy(c, tokenId)
	if err != nil || policy.MaxDistinctModels5m == 0 {
		return err
	}
	if !common.RedisEnabled || common.RDB == nil {
		if policy.FailClosed {
			return ErrTokenSecurityUnavailable
		}
		return nil
	}
	key := fmt.Sprintf("token-security:{%d}:models:5m", tokenId)
	ctx := c.Request.Context()
	count, err := tokenModelRiskScript.Run(
		ctx,
		common.RDB,
		[]string{key},
		modelName,
		(5 * time.Minute).Milliseconds(),
	).Int64()
	if err != nil {
		if policy.FailClosed {
			return fmt.Errorf("%w: %v", ErrTokenSecurityUnavailable, err)
		}
		logger.LogWarn(c, fmt.Sprintf("token model risk enforcement degraded token_id=%d error=%v", tokenId, err))
		return nil
	}
	if count <= int64(policy.MaxDistinctModels5m) {
		return nil
	}
	suspended := false
	cacheSynchronized := true
	if policy.RiskMode == model.TokenRiskModeSuspend {
		var suspensionErr error
		suspended, cacheSynchronized, suspensionErr = suspendTokenForSecurityRisk(
			tokenId,
			c.GetString("token_key"),
		)
		if suspensionErr != nil {
			common.SysError(fmt.Sprintf(
				"token model-risk suspension degraded token_id=%d committed=%t cache_synchronized=%t error=%v",
				tokenId,
				suspended,
				cacheSynchronized,
				suspensionErr,
			))
		}
		if !suspended {
			return fmt.Errorf("%w: failed to suspend risky token", ErrTokenSecurityUnavailable)
		}
	}

	alerted, err := common.RDB.SetNX(
		ctx,
		fmt.Sprintf("token-security:{%d}:models:5m:alert", tokenId),
		1,
		5*time.Minute,
	).Result()
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("token model-risk alert deduplication degraded token_id=%d error=%v", tokenId, err))
	}
	if err == nil && alerted {
		logger.LogWarn(c, fmt.Sprintf(
			"token model fanout risk token_id=%d models=%d limit=%d mode=%s",
			tokenId, count, policy.MaxDistinctModels5m, policy.RiskMode,
		))
		model.RecordOperationAuditLog(
			c.GetInt("id"),
			"Token model fanout risk detected",
			c.ClientIP(),
			model.OpActionTokenRisk,
			map[string]interface{}{
				"token_id":           tokenId,
				"signal":             "distinct_models_5m",
				"value":              count,
				"limit":              policy.MaxDistinctModels5m,
				"mode":               policy.RiskMode,
				"token_suspended":    suspended,
				"cache_synchronized": cacheSynchronized,
			},
			nil,
			nil,
		)
		if policy.RiskMode == model.TokenRiskModeNotify || policy.RiskMode == model.TokenRiskModeSuspend {
			notifyTokenSecurityUser(
				c.GetInt("id"),
				tokenId,
				fmt.Sprintf(
					"Token %d accessed %d distinct models within 5 minutes (limit: %d).",
					tokenId,
					count,
					policy.MaxDistinctModels5m,
				),
			)
		}
	}
	if suspended {
		return ErrTokenSecurityModelRisk
	}
	return nil
}

func TokenSecurityErrorMessage(err error) string {
	var budgetErr *tokenSecurityBudgetError
	if errors.As(err, &budgetErr) {
		scope := "spending"
		switch budgetErr.kind {
		case "per_request":
			scope = "per-request"
		case "hourly":
			scope = "hourly"
		case "daily":
			scope = "daily"
		}
		message := fmt.Sprintf(
			"API key %s quota limit exceeded: this request needs %s, configured limit is %s; the request was rejected before billing",
			scope,
			logger.FormatQuota(int(budgetErr.attempted)),
			logger.FormatQuota(int(budgetErr.limit)),
		)
		if budgetErr.suspended {
			message += " and the API key was suspended"
		}
		return message
	}
	switch {
	case errors.Is(err, ErrTokenSecurityRateLimit):
		return "API key request rate limit exceeded; the request was rejected before billing"
	case errors.Is(err, ErrTokenSecurityConcurrency):
		return "API key concurrency limit exceeded; the request was rejected before billing"
	case errors.Is(err, ErrTokenSecurityModelRisk):
		return "API key model-risk limit exceeded and this key has been suspended; the request was rejected before billing"
	case errors.Is(err, ErrTokenSecurityBudget):
		return "API key quota limit exceeded; the request was rejected before billing"
	case errors.Is(err, ErrTokenSecurityUnavailable):
		return "API key security enforcement is temporarily unavailable; the request was rejected before billing"
	default:
		return "API key security check failed; the request was rejected before billing"
	}
}

func TokenSecurityErrorCode(err error) types.ErrorCode {
	switch {
	case errors.Is(err, ErrTokenSecurityRateLimit):
		return types.ErrorCodeTokenSecurityRateLimitExceeded
	case errors.Is(err, ErrTokenSecurityConcurrency):
		return types.ErrorCodeTokenSecurityConcurrencyExceeded
	case errors.Is(err, ErrTokenSecurityModelRisk):
		return types.ErrorCodeTokenSecurityModelRiskExceeded
	case errors.Is(err, ErrTokenSecurityBudget):
		return types.ErrorCodeTokenSecurityQuotaExceeded
	case errors.Is(err, ErrTokenSecurityUnavailable):
		return types.ErrorCodeTokenSecurityTemporarilyUnavailable
	default:
		return types.ErrorCodeAccessDenied
	}
}

func RecordTokenSecurityRejection(c *gin.Context, err error, modelName string, estimatedQuota int) {
	if c == nil || err == nil {
		return
	}
	if modelName == "" {
		modelName = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	}
	adminInfo := map[string]interface{}{
		"token_security_error_code": TokenSecurityErrorCode(err),
	}
	var budgetErr *tokenSecurityBudgetError
	if errors.As(err, &budgetErr) {
		adminInfo["limit_kind"] = budgetErr.kind
		adminInfo["estimated_quota"] = budgetErr.attempted
		adminInfo["configured_limit"] = budgetErr.limit
		adminInfo["token_suspended"] = budgetErr.suspended
		if budgetErr.cacheSync != nil {
			adminInfo["cache_synchronized"] = *budgetErr.cacheSync
		}
	} else if estimatedQuota > 0 {
		adminInfo["estimated_quota"] = estimatedQuota
	}
	other := map[string]interface{}{
		"error_type":  "token_security",
		"error_code":  TokenSecurityErrorCode(err),
		"status_code": TokenSecurityHTTPStatus(err),
		"admin_info":  adminInfo,
	}
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	model.RecordErrorLog(
		c,
		c.GetInt("id"),
		common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		modelName,
		c.GetString("token_name"),
		TokenSecurityErrorMessage(err),
		c.GetInt("token_id"),
		int(time.Since(startTime).Seconds()),
		common.GetContextKeyBool(c, constant.ContextKeyIsStream),
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		other,
	)
}

func TokenSecurityHTTPStatus(err error) int {
	if errors.Is(err, ErrTokenSecurityUnavailable) {
		return http.StatusServiceUnavailable
	}
	return http.StatusTooManyRequests
}
