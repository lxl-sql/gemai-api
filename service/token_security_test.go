package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

	RecordTokenSecurityRejection(c, ErrTokenSecurityRateLimit, "", 0)

	var rejectionLog model.Log
	require.NoError(t, model.LOG_DB.
		Where("token_id = ? AND type = ?", 8102, model.LogTypeError).
		First(&rejectionLog).Error)
	assert.Zero(t, rejectionLog.Quota)
	assert.Contains(t, rejectionLog.Content, "request rate limit exceeded")
	assert.Contains(t, rejectionLog.Other, `"error_code":"token_security_rate_limit_exceeded"`)
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
		tokenId:  1,
		leaseId:  "lease-release-test",
		acquired: true,
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
	require.False(t, reservation.windowReserved)
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
		tokenId:     token.Id,
		hourKey:     "token-security:{settlement}:hour",
		dayKey:      "token-security:{settlement}:day",
		reserved:    50,
		hourlyQuota: 100,
		failClosed:  true,
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
		tokenId:     token.Id,
		hourKey:     "token-security:{unavailable}:hour",
		dayKey:      "token-security:{unavailable}:day",
		reserved:    50,
		hourlyQuota: 100,
		failClosed:  true,
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
		tokenId:        token.Id,
		hourKey:        "token-security:{refund}:hour",
		dayKey:         "token-security:{refund}:day",
		reserved:       100,
		hourlyQuota:    200,
		failClosed:     true,
		windowReserved: true,
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
		tokenId:     token.Id,
		hourKey:     "token-security:{without-reservation}:hour",
		dayKey:      "token-security:{without-reservation}:day",
		reserved:    100,
		hourlyQuota: 200,
		failClosed:  true,
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
	t.Cleanup(func() {
		require.NoError(t, client.Del(
			context.Background(),
			hourKey,
			dayKey,
			reservationKey,
			concurrencyKey,
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

	reservation := &TokenBudgetReservation{
		tokenId:         tokenId,
		hourKey:         hourKey,
		dayKey:          dayKey,
		reservationKey:  reservationKey,
		reserved:        50,
		hourlyQuota:     1000,
		dailyQuota:      1000,
		windowReserved:  true,
		windowAttempted: true,
	}
	for i := 0; i < 2; i++ {
		result, err := reservation.finalizeWindow(60)
		require.NoError(t, err)
		require.Zero(t, result)
	}
	hourValue, err = client.Get(context.Background(), hourKey).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(60), hourValue)

	require.ErrorIs(t, renewTokenConcurrencyLease(
		context.Background(),
		tokenId,
		"missing-lease",
	), errTokenConcurrencyLeaseLost)
	cardinality, err := client.ZCard(context.Background(), concurrencyKey).Result()
	require.NoError(t, err)
	require.Zero(t, cardinality)
}
