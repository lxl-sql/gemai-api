package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/oauthqueue"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOAuthQueueEncryptionRejectsTampering(t *testing.T) {
	encoded, err := encryptOAuthQueueValue(queuedOAuthExchangeResult{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	})
	require.NoError(t, err)
	var decoded queuedOAuthExchangeResult
	require.NoError(t, decryptOAuthQueueValue(encoded, &decoded))
	assert.Equal(t, "access-token", decoded.AccessToken)
	assert.Equal(t, "refresh-token", decoded.RefreshToken)

	replacement := "A"
	if encoded[len(encoded)-1:] == replacement {
		replacement = "B"
	}
	tampered := encoded[:len(encoded)-1] + replacement
	require.Error(t, decryptOAuthQueueValue(tampered, &decoded))
}

func TestOAuthExchangeQueueKeepsNoRedisStartupCompatible(t *testing.T) {
	previousRedis := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previousRedis })
	for _, key := range []string{
		"OAUTH_QUEUE_ENABLE",
		"OAUTH_QUEUE_REDIS_URLS",
		"OAUTH_QUEUE_REDIS_CLUSTER_ADDRS",
		"OAUTH_QUEUE_REDIS_SENTINEL_ADDRS",
		"OAUTH_QUEUE_REDIS_SENTINEL_MASTER",
		"OAUTH_QUEUE_COORDINATOR_REDIS_URL",
	} {
		t.Setenv(key, "")
	}

	require.NoError(t, StartOAuthExchangeQueue())
	assert.False(t, OAuthExchangeQueueEnabled())
}

func TestOAuthExchangeQueueExplicitEnableRequiresRedis(t *testing.T) {
	previousRedis := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previousRedis })
	t.Setenv("OAUTH_QUEUE_ENABLE", "true")
	for _, key := range []string{
		"OAUTH_QUEUE_REDIS_URLS",
		"OAUTH_QUEUE_REDIS_CLUSTER_ADDRS",
		"OAUTH_QUEUE_REDIS_SENTINEL_ADDRS",
		"OAUTH_QUEUE_REDIS_SENTINEL_MASTER",
		"OAUTH_QUEUE_COORDINATOR_REDIS_URL",
	} {
		t.Setenv(key, "")
	}

	require.Error(t, StartOAuthExchangeQueue())
	assert.False(t, OAuthExchangeQueueEnabled())
}

func TestOAuthExchangeQueueProcessesAuthorizationCodeOnce(t *testing.T) {
	redisURL := os.Getenv("RATE_LIMIT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("RATE_LIMIT_REDIS_TEST_URL is not configured")
	}
	namespace := "oauth_service_test_" + common.GetRandomString(8)
	for key, value := range map[string]string{
		"OAUTH_QUEUE_ENABLE":                "true",
		"OAUTH_QUEUE_NAMESPACE":             namespace,
		"OAUTH_QUEUE_REDIS_URLS":            redisURL,
		"OAUTH_QUEUE_REDIS_CLUSTER_ADDRS":   "",
		"OAUTH_QUEUE_REDIS_SENTINEL_ADDRS":  "",
		"OAUTH_QUEUE_REDIS_SENTINEL_MASTER": "",
		"OAUTH_QUEUE_COORDINATOR_REDIS_URL": "",
	} {
		t.Setenv(key, value)
	}
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.OAuthApp{},
		&model.OAuthAuthorizationCode{},
		&model.OAuthGrant{},
		&model.OAuthRefreshTokenHistory{},
		&model.OperationLog{},
	))
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
	})
	user := &model.User{
		Username: "queued-oauth-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)
	app := &model.OAuthApp{
		Name:         "Queued OAuth App",
		ClientId:     "gai_queued_oauth",
		ClientType:   model.OAuthClientTypePublic,
		RedirectUris: `["https://tool.example.com/callback"]`,
		UserId:       user.Id,
		Status:       common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(app).Error)
	code := &model.OAuthAuthorizationCode{
		Code:        "queued-authorization-code",
		ClientId:    app.ClientId,
		UserId:      user.Id,
		RedirectUri: "https://tool.example.com/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, db.Create(code).Error)

	require.NoError(t, StartOAuthExchangeQueue())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = StopOAuthExchangeQueue(ctx)
		options, err := redis.ParseURL(redisURL)
		if err != nil {
			return
		}
		client := redis.NewClient(options)
		defer client.Close()
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(ctx, cursor, namespace+":*", 100).Result()
			if scanErr != nil {
				return
			}
			if len(keys) > 0 {
				_ = client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	config, enabled := OAuthExchangeQueueConfig()
	require.True(t, enabled)
	validations := make([]*OAuthValidationAdmission, 0, config.InitialConcurrency)
	for index := 0; index < config.InitialConcurrency; index++ {
		validation, err := AcquireOAuthValidationAdmission(context.Background())
		require.NoError(t, err)
		validations = append(validations, validation)
	}
	noPermitContext, cancelNoPermit := context.WithCancel(context.Background())
	cancelNoPermit()
	_, err = AcquireOAuthExchangeAdmission(noPermitContext)
	require.ErrorIs(t, err, context.Canceled)
	validations[0].Finish()
	replacementValidation, err := AcquireOAuthValidationAdmission(context.Background())
	require.NoError(t, err)
	replacementValidation.Finish()
	for _, validation := range validations {
		validation.Finish()
	}
	auditStarted := make(chan struct{})
	releaseAudit := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseAudit:
		default:
			close(releaseAudit)
		}
	})
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("oauth_test_block_audit", func(tx *gorm.DB) {
		if tx.Statement.Table == "operation_logs" {
			close(auditStarted)
			<-releaseAudit
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("oauth_test_block_audit") })
	ticket, err := EnqueueOAuthAuthorizationCode(
		context.Background(),
		app,
		code,
		"request-id",
		"127.0.0.1",
		"test-agent",
	)
	require.NoError(t, err)
	result, err := WaitOAuthExchangeResult(
		context.Background(),
		ticket.ExchangeID,
		ticket.PollToken,
		5*time.Second,
	)
	require.NoError(t, err)
	assert.Equal(t, oauthqueue.StatusSucceeded, result.Status)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	storedCode, err := model.GetOAuthAuthorizationCode(code.Code)
	require.NoError(t, err)
	assert.True(t, storedCode.Used)
	var grants int64
	require.NoError(t, db.Model(&model.OAuthGrant{}).Count(&grants).Error)
	assert.Equal(t, int64(1), grants)
	select {
	case <-auditStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "queued OAuth audit log did not start")
	}
	blockers := make([]*OAuthExchangeAdmission, 0, config.InitialConcurrency)
	for index := 0; index < config.InitialConcurrency; index++ {
		blocker, err := AcquireOAuthExchangeAdmission(context.Background())
		require.NoError(t, err)
		blockers = append(blockers, blocker)
	}
	close(releaseAudit)
	var operationLogs int64
	require.Eventually(t, func() bool {
		if err := db.Model(&model.OperationLog{}).
			Where("action = ?", model.OpActionOAuthTokenIssue).
			Count(&operationLogs).Error; err != nil {
			return false
		}
		return operationLogs == 1
	}, 2*time.Second, 25*time.Millisecond)
	assert.Equal(t, int64(1), operationLogs)

	t.Cleanup(func() {
		for _, blocker := range blockers {
			blocker.Finish(false)
		}
	})
	disabledCode := &model.OAuthAuthorizationCode{
		Code:        "queued-disabled-app-code",
		ClientId:    app.ClientId,
		UserId:      user.Id,
		RedirectUri: code.RedirectUri,
		Scope:       code.Scope,
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, db.Create(disabledCode).Error)
	disabledTicket, err := EnqueueOAuthAuthorizationCode(
		context.Background(),
		app,
		disabledCode,
		"disabled-request-id",
		"127.0.0.1",
		"test-agent",
	)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.OAuthApp{}).
		Where("id = ?", app.Id).
		Update("status", common.UserStatusDisabled).Error)
	for _, blocker := range blockers {
		blocker.Finish(false)
	}
	disabledResult, err := WaitOAuthExchangeResult(
		context.Background(),
		disabledTicket.ExchangeID,
		disabledTicket.PollToken,
		5*time.Second,
	)
	require.NoError(t, err)
	assert.Equal(t, oauthqueue.StatusFailed, disabledResult.Status)
	assert.Equal(t, "invalid_client", disabledResult.Error)
	assert.False(t, disabledResult.Reauthorize)
	storedDisabledCode, err := model.GetOAuthAuthorizationCode(disabledCode.Code)
	require.NoError(t, err)
	assert.False(t, storedDisabledCode.Used)
}
