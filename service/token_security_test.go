package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildUserWritableTokenSecurityPolicyPreservesAdministratorFields(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &model.Token{
		UserId:         7006,
		Key:            "user-policy-boundary-key",
		Name:           "user-policy-boundary",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:             token.Id,
		SustainedRps:        5000,
		BurstCapacity:       10000,
		MaxConcurrency:      2000,
		MaxDistinctModels5m: 100,
		RiskMode:            model.TokenRiskModeObserve,
		FailClosed:          true,
	}).Error)

	hourlyQuota := int64(500)
	riskMode := model.TokenRiskModeNotify
	policy, err := BuildUserWritableTokenSecurityPolicy(token.Id, &dto.UserTokenSecurityPolicyRequest{
		HourlyQuota: &hourlyQuota,
		RiskMode:    &riskMode,
	})
	require.NoError(t, err)

	assert.Equal(t, 5000, policy.SustainedRps)
	assert.Equal(t, 10000, policy.BurstCapacity)
	assert.Equal(t, 2000, policy.MaxConcurrency)
	assert.Equal(t, 100, policy.MaxDistinctModels5m)
	assert.True(t, policy.FailClosed)
	assert.Equal(t, hourlyQuota, policy.HourlyQuota)
	assert.Equal(t, riskMode, policy.RiskMode)
}

func TestRequestsPerSecondUsesMinuteRateWhenConfigured(t *testing.T) {
	assert.InDelta(t, 0.25, requestsPerSecond(15, 100), 0.000001)
	assert.InDelta(t, 100, requestsPerSecond(0, 100), 0.000001)
	assert.Zero(t, requestsPerSecond(0, 0))
}

func TestRenewConcurrencyLeasesDropsOnlyLostKeys(t *testing.T) {
	keys := []string{"lost-user", "active-token", "retry-user"}
	transientErr := errors.New("temporary Redis failure")
	renewed := make([]string, 0, len(keys))
	failures := make(map[string]error)

	remaining := renewConcurrencyLeases(
		context.Background(),
		keys,
		"lease-1",
		func(key string, err error) {
			failures[key] = err
		},
		func(_ context.Context, key string, _ string) error {
			renewed = append(renewed, key)
			switch key {
			case "lost-user":
				return errTokenConcurrencyLeaseLost
			case "retry-user":
				return transientErr
			default:
				return nil
			}
		},
	)

	assert.Equal(t, keys, renewed)
	assert.Equal(t, []string{"active-token", "retry-user"}, remaining)
	assert.ErrorIs(t, failures["lost-user"], errTokenConcurrencyLeaseLost)
	assert.ErrorIs(t, failures["retry-user"], transientErr)
}

func TestTokenBudgetWindowIdentifiesSharedUserLimits(t *testing.T) {
	window := &tokenBudgetWindow{
		scope:       "user",
		hourlyQuota: 100,
		dailyQuota:  500,
	}

	var hourlyErr *tokenSecurityBudgetError
	require.ErrorAs(t, window.limitError(-1, 120), &hourlyErr)
	assert.Equal(t, "user_hourly", hourlyErr.kind)
	assert.Equal(t, int64(100), hourlyErr.limit)

	var dailyErr *tokenSecurityBudgetError
	require.ErrorAs(t, window.limitError(-2, 600), &dailyErr)
	assert.Equal(t, "user_daily", dailyErr.kind)
	assert.Equal(t, int64(500), dailyErr.limit)
}

func TestTokenBudgetWindowKeysShareRedisClusterHashTag(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		hashTag string
		window  *tokenBudgetWindow
	}{
		{
			name:    "token",
			hashTag: "{41}",
			window:  newTokenBudgetWindow(41, "request-1", now, 100, 200, 10),
		},
		{
			name:    "user",
			hashTag: "{42}",
			window:  newUserBudgetWindow(42, "request-2", now, 100, 200, 10),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Contains(t, test.window.hourKey, test.hashTag)
			assert.Contains(t, test.window.dayKey, test.hashTag)
			assert.Contains(t, test.window.reservationKey, test.hashTag)
		})
	}
}

func TestPreConsumeBillingRejectsTokenQuotaLimitBeforeDeductionAndRecordsErrorLog(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	user := &model.User{
		Username: "security-budget-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "security-budget-token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 100,
		RiskMode:           model.TokenRiskModeSuspend,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("token_id", token.Id)
	c.Set("token_name", token.Name)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       "security-budget-" + common.GetRandomString(8),
		OriginModelName: "priced-model",
	}
	info.UserSetting.BillingPreference = "wallet_only"

	apiErr := PreConsumeBilling(c, 200, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeTokenSecurityQuotaExceeded, apiErr.GetErrorCode())
	assert.Contains(t, apiErr.Error(), "per-request")
	assert.Contains(t, apiErr.Error(), "rejected before billing")
	assert.Contains(t, apiErr.Error(), "API key was suspended")
	assert.Nil(t, info.Billing)

	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	var reservationCount int64
	require.NoError(t, model.DB.Model(&model.BillingReservation{}).Count(&reservationCount).Error)
	assert.Equal(t, int64(0), reservationCount)

	var rejectionLog model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", token.Id, model.LogTypeError).First(&rejectionLog).Error)
	assert.Equal(t, 0, rejectionLog.Quota)
	assert.Equal(t, "priced-model", rejectionLog.ModelName)
	assert.Contains(t, rejectionLog.Content, "per-request")
	assert.Contains(t, rejectionLog.Other, `"configured_limit":100`)
	assert.Contains(t, rejectionLog.Other, `"estimated_quota":200`)
	assert.Contains(t, rejectionLog.Other, `"error_code":"token_security_quota_exceeded"`)
	assert.Contains(t, rejectionLog.Other, `"token_suspended":true`)
	assert.Contains(t, rejectionLog.Other, `"cache_synchronized":true`)
}

func TestReserveTokenSecurityBudgetDoesNotSuspendInObserveMode(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})

	token := &model.Token{
		UserId:         8002,
		Key:            "observed-budget-rejection-key",
		Name:           "observed-budget-rejection",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 100,
		RiskMode:           model.TokenRiskModeObserve,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := ReserveTokenSecurityBudget(c, token.Id, 101)
	require.ErrorIs(t, err, ErrTokenSecurityBudget)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
}

func TestTokenSecurityBudgetMessagesIdentifyConfiguredScope(t *testing.T) {
	for _, testCase := range []struct {
		kind string
		want string
	}{
		{kind: "per_request", want: "per-request"},
		{kind: "hourly", want: "hourly"},
		{kind: "daily", want: "daily"},
		{kind: "user_hourly", want: "shared user hourly"},
		{kind: "user_daily", want: "shared user daily"},
	} {
		t.Run(testCase.kind, func(t *testing.T) {
			err := &tokenSecurityBudgetError{
				kind:      testCase.kind,
				attempted: 200,
				limit:     100,
			}
			message := TokenSecurityErrorMessage(err)
			assert.Contains(t, message, testCase.want)
			assert.Contains(t, message, "rejected before billing")
			assert.Equal(t, types.ErrorCodeTokenSecurityQuotaExceeded, TokenSecurityErrorCode(err))
		})
	}
}

func TestTokenSecurityTrafficErrorMessageIncludesScopeAndConfiguredLimit(t *testing.T) {
	testCases := []struct {
		name         string
		err          *tokenSecurityTrafficError
		wantMessage  string
		wantSentinel error
	}{
		{
			name: "shared user RPM",
			err: &tokenSecurityTrafficError{
				cause: ErrTokenSecurityRateLimit,
				kind:  "user_rate",
				limit: 4,
				unit:  "RPM",
				burst: 1,
			},
			wantMessage:  "API key shared user request rate limit exceeded: configured limit is 4 RPM with burst capacity 1",
			wantSentinel: ErrTokenSecurityRateLimit,
		},
		{
			name: "API key RPS",
			err: &tokenSecurityTrafficError{
				cause: ErrTokenSecurityRateLimit,
				kind:  "token_rate",
				limit: 10,
				unit:  "RPS",
				burst: 10,
			},
			wantMessage:  "API key request rate limit exceeded: configured limit is 10 RPS with burst capacity 10",
			wantSentinel: ErrTokenSecurityRateLimit,
		},
		{
			name: "shared user concurrency",
			err: &tokenSecurityTrafficError{
				cause: ErrTokenSecurityConcurrency,
				kind:  "user_concurrency",
				limit: 3,
			},
			wantMessage:  "API key shared user concurrency limit exceeded: configured limit is 3",
			wantSentinel: ErrTokenSecurityConcurrency,
		},
		{
			name: "API key concurrency",
			err: &tokenSecurityTrafficError{
				cause: ErrTokenSecurityConcurrency,
				kind:  "token_concurrency",
				limit: 2,
			},
			wantMessage:  "API key concurrency limit exceeded: configured limit is 2",
			wantSentinel: ErrTokenSecurityConcurrency,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.ErrorIs(t, testCase.err, testCase.wantSentinel)
			assert.Contains(t, TokenSecurityErrorMessage(testCase.err), testCase.wantMessage)
			assert.Equal(t, TokenSecurityErrorCode(testCase.wantSentinel), TokenSecurityErrorCode(testCase.err))
		})
	}
}

func TestRecordTokenSecurityRejectionRecordsTrafficLimit(t *testing.T) {
	truncate(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", 8101)
	c.Set("username", "traffic-limit-user")
	c.Set("token_id", 8102)
	c.Set("token_name", "traffic-limit-token")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())

	RecordTokenSecurityRejection(c, &tokenSecurityTrafficError{
		cause: ErrTokenSecurityRateLimit,
		kind:  "user_rate",
		limit: 4,
		unit:  "RPM",
		burst: 1,
	}, "", 0)

	var rejectionLog model.Log
	require.NoError(t, model.LOG_DB.
		Where("token_id = ? AND type = ?", 8102, model.LogTypeError).
		First(&rejectionLog).Error)
	assert.Zero(t, rejectionLog.Quota)
	assert.Contains(t, rejectionLog.Content, "shared user request rate limit exceeded")
	assert.Contains(t, rejectionLog.Other, `"error_code":"token_security_rate_limit_exceeded"`)
	assert.Contains(t, rejectionLog.Other, `"limit_kind":"user_rate"`)
	assert.Contains(t, rejectionLog.Other, `"configured_limit":4`)
	assert.Contains(t, rejectionLog.Other, `"limit_unit":"RPM"`)
	assert.Contains(t, rejectionLog.Other, `"burst_capacity":1`)
}

func TestTokenTrafficLeaseReleaseStopsRenewalWhenRedisIsUnavailable(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	lease := &TokenTrafficLease{
		tokenId:         1,
		leaseId:         "lease-release-test",
		acquired:        true,
		concurrencyKeys: []string{tokenConcurrencyKey(1)},
	}
	lease.startRenewal()
	lease.Release(context.Background())

	require.False(t, lease.acquired)
	select {
	case <-lease.renewDone:
	default:
		t.Fatal("token concurrency renewal did not stop")
	}
}

func TestAcquireTokenTrafficHonorsRedisFailureMode(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8001,
		Key:            "traffic-policy-key",
		Name:           "traffic-policy",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:       token.Id,
		SustainedRps:  10,
		BurstCapacity: 20,
		FailClosed:    true,
	}).Error)
	_, err := AcquireTokenTraffic(c, token.Id)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrTokenSecurityUnavailable))

	require.NoError(t, model.DB.Model(&model.TokenSecurityPolicy{}).
		Where("token_id = ?", token.Id).
		Update("fail_closed", false).Error)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	lease, err := AcquireTokenTraffic(c, token.Id)
	require.NoError(t, err)
	require.NotNil(t, lease)
}

func TestCheckTokenModelRiskHonorsRedisFailureMode(t *testing.T) {
	truncate(t)
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

	token := &model.Token{
		UserId:         8002,
		Key:            "model-risk-policy-key",
		Name:           "model-risk-policy",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:             token.Id,
		MaxDistinctModels5m: 20,
		FailClosed:          false,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	require.NoError(t, CheckTokenModelRisk(c, token.Id, "model-a"))

	require.NoError(t, model.DB.Model(&model.TokenSecurityPolicy{}).
		Where("token_id = ?", token.Id).
		Update("fail_closed", true).Error)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err := CheckTokenModelRisk(c, token.Id, "model-b")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrTokenSecurityUnavailable))
}

func TestFinalizeTokenSecurityBudgetSuspendsActualOverage(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8003,
		Key:            "actual-budget-overage-key",
		Name:           "actual-budget-overage",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 100,
		RiskMode:           model.TokenRiskModeSuspend,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", token.UserId)
	reservation, err := ReserveTokenSecurityBudget(c, token.Id, 50)
	require.NoError(t, err)
	require.NotNil(t, reservation)

	reservation.Finalize(101)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	require.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestFinalizeTokenSecurityBudgetObserveModeDoesNotSuspendActualOverage(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8003,
		Key:            "observed-actual-budget-overage-key",
		Name:           "observed-actual-budget-overage",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 100,
		RiskMode:           model.TokenRiskModeObserve,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", token.UserId)
	reservation, err := ReserveTokenSecurityBudget(c, token.Id, 50)
	require.NoError(t, err)
	require.NotNil(t, reservation)

	reservation.Finalize(101)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)

	var auditLog model.Log
	require.NoError(t, model.LOG_DB.
		Where("user_id = ? AND type = ?", token.UserId, model.LogTypeManage).
		First(&auditLog).Error)
	assert.Contains(t, auditLog.Other, `"signal":"budget_settlement"`)
	assert.Contains(t, auditLog.Other, `"risk_mode":"observe"`)
	assert.Contains(t, auditLog.Other, `"token_suspended":false`)
}

func TestReserveTokenSecurityBudgetKeepsSettlementAfterFailOpenRedisError(t *testing.T) {
	truncate(t)
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

	token := &model.Token{
		UserId:         8004,
		Key:            "reservation-fail-open-key",
		Name:           "reservation-fail-open",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:     token.Id,
		HourlyQuota: 100,
		FailClosed:  false,
	}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reservation, err := ReserveTokenSecurityBudget(c, token.Id, 50)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.Len(t, reservation.windows, 1)
	require.False(t, reservation.windows[0].windowReserved)
}

func TestReserveTokenSecurityBudgetBuildsSharedUserAndTokenWindows(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	user := &model.User{Id: 8005, Username: "shared-budget-user", Group: "default"}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:         user.Id,
		Key:            "shared-budget-key",
		Name:           "shared-budget",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, model.DB.Create(&model.TokenSecurityPolicy{
		TokenId:     token.Id,
		HourlyQuota: 100,
		RiskMode:    model.TokenRiskModeObserve,
	}).Error)
	require.NoError(t, model.UpsertTokenSecurityProfile(&model.TokenSecurityProfile{
		ScopeType:       model.TokenSecurityScopeUser,
		ScopeValue:      strconv.Itoa(user.Id),
		UserHourlyQuota: 500,
		MinimumRiskMode: model.TokenRiskModeObserve,
	}))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", user.Id)
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)

	reservation, err := ReserveTokenSecurityBudget(c, token.Id, 50)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.Len(t, reservation.windows, 2)
	assert.Equal(t, "user", reservation.windows[0].scope)
	assert.Equal(t, int64(500), reservation.windows[0].hourlyQuota)
	assert.Equal(t, "token", reservation.windows[1].scope)
	assert.Equal(t, int64(100), reservation.windows[1].hourlyQuota)
}

func TestFinalizeTokenSecurityBudgetFailsClosedWhenRedisSettlementFails(t *testing.T) {
	truncate(t)
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

	token := &model.Token{
		UserId:         8004,
		Key:            "settlement-fail-closed-key",
		Name:           "settlement-fail-closed",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	reservation := &TokenBudgetReservation{
		tokenId:    token.Id,
		reserved:   50,
		failClosed: true,
		windows: []*tokenBudgetWindow{{
			scope:       "token",
			hourKey:     "token-security:{settlement}:hour",
			dayKey:      "token-security:{settlement}:day",
			reserved:    50,
			hourlyQuota: 100,
		}},
	}
	reservation.Finalize(60)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	require.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestFinalizeTokenSecurityBudgetFailsClosedWhenRedisBecomesUnavailable(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8005,
		Key:            "settlement-redis-unavailable-key",
		Name:           "settlement-redis-unavailable",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	reservation := &TokenBudgetReservation{
		tokenId:    token.Id,
		reserved:   50,
		failClosed: true,
		windows: []*tokenBudgetWindow{{
			scope:       "token",
			hourKey:     "token-security:{unavailable}:hour",
			dayKey:      "token-security:{unavailable}:day",
			reserved:    50,
			hourlyQuota: 100,
		}},
	}
	reservation.Finalize(60)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	require.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestFinalizeTokenSecurityBudgetDoesNotSuspendWhenRefundFails(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8006,
		Key:            "settlement-refund-failure-key",
		Name:           "settlement-refund-failure",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	reservation := &TokenBudgetReservation{
		tokenId:    token.Id,
		reserved:   100,
		failClosed: true,
		windows: []*tokenBudgetWindow{{
			scope:          "token",
			hourKey:        "token-security:{refund}:hour",
			dayKey:         "token-security:{refund}:day",
			reserved:       100,
			hourlyQuota:    200,
			windowReserved: true,
		}},
	}
	reservation.Finalize(60)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, stored.Status)
}

func TestFinalizeTokenSecurityBudgetUsesActualQuotaWhenPreReservationWasSkipped(t *testing.T) {
	truncate(t)
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	token := &model.Token{
		UserId:         8007,
		Key:            "settlement-without-reservation-key",
		Name:           "settlement-without-reservation",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	reservation := &TokenBudgetReservation{
		tokenId:    token.Id,
		reserved:   100,
		failClosed: true,
		windows: []*tokenBudgetWindow{{
			scope:       "token",
			hourKey:     "token-security:{without-reservation}:hour",
			dayKey:      "token-security:{without-reservation}:day",
			reserved:    100,
			hourlyQuota: 200,
		}},
	}
	reservation.Finalize(60)

	var stored model.Token
	require.NoError(t, model.DB.First(&stored, token.Id).Error)
	require.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestTokenSecurityRedisScriptsRemainIdempotent(t *testing.T) {
	redisURL := os.Getenv("TOKEN_SECURITY_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("TOKEN_SECURITY_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	tokenId := int(time.Now().UnixNano()%100000000) + 900000000
	requestId := common.NewRequestId()
	hourKey := fmt.Sprintf("token-security:{%d}:quota:test:hour", tokenId)
	dayKey := fmt.Sprintf("token-security:{%d}:quota:test:day", tokenId)
	reservationKey := fmt.Sprintf(
		"token-security:{%d}:quota:reservation:%s",
		tokenId,
		common.Sha1([]byte(requestId)),
	)
	concurrencyKey := tokenConcurrencyKey(tokenId)
	userId := tokenId + 1
	now := time.Now().UTC()
	idempotencyUserWindow := newUserBudgetWindow(
		userId+1,
		common.NewRequestId(),
		now,
		1000,
		1000,
		50,
	)
	userWindowFirst := newUserBudgetWindow(
		userId,
		common.NewRequestId(),
		now,
		100,
		1000,
		60,
	)
	userWindowSecond := newUserBudgetWindow(
		userId,
		common.NewRequestId(),
		now,
		100,
		1000,
		50,
	)
	userRateLimitKey := userRateKey(userId)
	userConcurrencyLimitKey := userConcurrencyKey(userId)
	t.Cleanup(func() {
		require.NoError(t, client.Del(
			context.Background(),
			hourKey,
			dayKey,
			reservationKey,
			concurrencyKey,
			userWindowFirst.hourKey,
			userWindowFirst.dayKey,
			userWindowFirst.reservationKey,
			userWindowSecond.reservationKey,
			idempotencyUserWindow.hourKey,
			idempotencyUserWindow.dayKey,
			idempotencyUserWindow.reservationKey,
			userRateLimitKey,
			userConcurrencyLimitKey,
		).Err())
	})

	for i := 0; i < 2; i++ {
		result, err := reserveTokenSecurityBudgetScript.Run(
			context.Background(),
			client,
			[]string{hourKey, dayKey, reservationKey},
			50,
			1000,
			1000,
			tokenSecurityBudgetPendingTTL.Milliseconds(),
		).Int()
		require.NoError(t, err)
		require.Equal(t, 1, result)
	}
	hourValue, err := client.Get(context.Background(), hourKey).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(50), hourValue)

	for i := 0; i < 2; i++ {
		result, err := reserveSecurityBudgetWindow(context.Background(), idempotencyUserWindow)
		require.NoError(t, err)
		require.Equal(t, 1, result)
	}
	reservation := &TokenBudgetReservation{
		tokenId:  tokenId,
		userId:   userId + 1,
		reserved: 50,
		windows: []*tokenBudgetWindow{
			idempotencyUserWindow,
			{
				scope:           "token",
				hourKey:         hourKey,
				dayKey:          dayKey,
				reservationKey:  reservationKey,
				reserved:        50,
				hourlyQuota:     1000,
				dailyQuota:      1000,
				windowReserved:  true,
				windowAttempted: true,
			},
		},
	}
	for i := 0; i < 2; i++ {
		reservation.Finalize(60)
	}
	hourValue, err = client.Get(context.Background(), hourKey).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(60), hourValue)
	userHourValue, err := client.Get(context.Background(), idempotencyUserWindow.hourKey).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(60), userHourValue)

	require.ErrorIs(t, renewTokenConcurrencyLease(
		context.Background(),
		tokenId,
		"missing-lease",
	), errTokenConcurrencyLeaseLost)
	cardinality, err := client.ZCard(context.Background(), concurrencyKey).Result()
	require.NoError(t, err)
	require.Zero(t, cardinality)

	result, err := reserveSecurityBudgetWindow(context.Background(), userWindowFirst)
	require.NoError(t, err)
	require.Equal(t, 1, result)
	result, err = reserveSecurityBudgetWindow(context.Background(), userWindowSecond)
	require.NoError(t, err)
	require.Equal(t, -1, result)
	rejectedReservation := &TokenBudgetReservation{
		tokenId: tokenId,
		userId:  userId,
		windows: []*tokenBudgetWindow{userWindowFirst, userWindowSecond},
	}
	rejectedReservation.rollbackBudgetWindows(userWindowSecond)
	rejectedMarkerExists, err := client.Exists(
		context.Background(),
		userWindowSecond.reservationKey,
	).Result()
	require.NoError(t, err)
	require.Zero(t, rejectedMarkerExists)
	require.NoError(t, client.Set(
		context.Background(),
		userWindowSecond.hourKey,
		60,
		2*time.Hour,
	).Err())
	result, err = reserveSecurityBudgetWindow(context.Background(), userWindowSecond)
	require.NoError(t, err)
	require.Equal(t, -1, result)

	allowed, err := allowRequestRate(context.Background(), userRateLimitKey, 5.0/60, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = allowRequestRate(context.Background(), userRateLimitKey, 5.0/60, 1)
	require.NoError(t, err)
	require.False(t, allowed)

	allowed, err = acquireConcurrency(
		context.Background(),
		userConcurrencyLimitKey,
		"shared-user-lease-1",
		1,
	)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = acquireConcurrency(
		context.Background(),
		userConcurrencyLimitKey,
		"shared-user-lease-2",
		1,
	)
	require.NoError(t, err)
	require.False(t, allowed)
}
