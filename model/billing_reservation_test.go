package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var errBillingReservationRedisCommandBlocked = errors.New("billing reservation redis command blocked by test")

type billingReservationRedisDeleteHook struct {
	keys []string
}

func (h *billingReservationRedisDeleteHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "del" && len(cmd.Args()) > 1 {
		h.keys = append(h.keys, fmt.Sprint(cmd.Args()[1]))
	}
	return ctx, errBillingReservationRedisCommandBlocked
}

func (h *billingReservationRedisDeleteHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (h *billingReservationRedisDeleteHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, errBillingReservationRedisCommandBlocked
}

func (h *billingReservationRedisDeleteHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestCreateBillingReservationContextCanceledBeforeWrite(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "canceled-create-" + common.GetRandomString(8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := CreateBillingReservationContext(ctx, BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		BillingSource: BillingReservationSourceWallet,
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.ErrorIs(t, err, context.Canceled)

	var count int64
	require.NoError(t, DB.Model(&BillingReservation{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Zero(t, count)
}

func TestFinalizeBillingReservationContextCanceledBeforeWrite(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "canceled-finalize-" + common.GetRandomString(8)
	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = FinalizeBillingReservationContext(ctx, requestId, 50, BillingReservationStatusSettling)
	require.ErrorIs(t, err, context.Canceled)

	var reservation BillingReservation
	require.NoError(t, DB.First(&reservation, created.Reservation.Id).Error)
	assert.Equal(t, BillingReservationStatusReserved, reservation.Status)
	assert.Equal(t, 100, reservation.DesiredQuota)
}

func TestMarkBillingReservationDispatchedIsIdempotentAndClassifiesMiss(t *testing.T) {
	truncateTables(t)

	err := MarkBillingReservationDispatched("missing-dispatch", 60, 0)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "finalizing-dispatch-" + common.GetRandomString(8)
	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 7))
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 7))

	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("id = ?", created.Reservation.Id).
		Update("status", BillingReservationStatusSettling).Error)

	err = MarkBillingReservationDispatched(requestId, 60, 0)
	require.ErrorContains(t, err, "already finalizing")

	var reservation BillingReservation
	require.NoError(t, DB.First(&reservation, created.Reservation.Id).Error)
	assert.Equal(t, BillingReservationStatusSettling, reservation.Status)
}

func TestRecordBillingSettlementFailureContextCanceledBeforeWrite(t *testing.T) {
	truncateTables(t)
	requestId := "canceled-failure-" + common.GetRandomString(8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RecordBillingSettlementFailureContext(ctx, BillingSettlementFailureInput{
		RequestId:        requestId,
		ActualQuota:      100,
		PreConsumedQuota: 50,
		Delta:            50,
	})
	require.ErrorIs(t, err, context.Canceled)

	var count int64
	require.NoError(t, DB.Model(&BillingSettlementFailure{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingAuditMarkerDeduplicatesLogAndRetainsUntilReceiptAcknowledged(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "audit-marker-" + common.GetRandomString(8)
	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     GetDBTimestamp() + 600,
	})
	require.NoError(t, err)
	_, err = FinalizeBillingReservation(requestId, 300, BillingReservationStatusSettling)
	require.NoError(t, err)
	claim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	auditKey := fmt.Sprintf("request:%s:%d:0:300", requestId, created.Reservation.Id)
	params := RecordTaskBillingLogParams{
		UserId:              user.Id,
		LogType:             LogTypeConsume,
		ModelName:           "test-model",
		Quota:               300,
		TokenId:             token.Id,
		Group:               "default",
		RequestId:           requestId,
		Other:               map[string]interface{}{"billing_audit_key": auditKey},
		AuditClaimExpiresAt: claim.ExpiresAt,
	}
	require.NoError(t, RecordTaskBillingLog(params))
	require.NoError(t, RecordTaskBillingLog(params))

	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", requestId).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
	var markerCount int64
	require.NoError(t, LOG_DB.Model(&BillingAuditMarker{}).Where("audit_key = ?", auditKey).Count(&markerCount).Error)
	assert.Equal(t, int64(1), markerCount)

	deleted, err := DeleteExpiredBillingAuditMarkers(100, GetDBTimestamp()+1)
	require.NoError(t, err)
	assert.Zero(t, deleted)
	require.NoError(t, AcknowledgeBillingReservationAudit(requestId, claim.ExpiresAt))
	deleted, err = DeleteExpiredBillingAuditMarkers(100, GetDBTimestamp()+1)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	require.NoError(t, LOG_DB.Model(&BillingAuditMarker{}).Where("audit_key = ?", auditKey).Count(&markerCount).Error)
	assert.Zero(t, markerCount)
}

func TestBillingAuditMarkerRejectsExpiredClaimBeforeLogWrite(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "expired-audit-marker-" + common.GetRandomString(8)
	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	_, err = FinalizeBillingReservation(requestId, 100, BillingReservationStatusSettling)
	require.NoError(t, err)
	claim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, DB.Model(&BillingReservation{}).Where("request_id = ?", requestId).
		Update("expires_at", GetDBTimestamp()-1).Error)
	auditKey := fmt.Sprintf("request:%s:%d:0:100", requestId, created.Reservation.Id)
	err = RecordTaskBillingLog(RecordTaskBillingLogParams{
		UserId:              user.Id,
		LogType:             LogTypeConsume,
		Quota:               100,
		RequestId:           requestId,
		AuditClaimExpiresAt: claim.ExpiresAt,
		Other:               map[string]interface{}{"billing_audit_key": auditKey},
	})
	require.ErrorIs(t, err, ErrBillingReservationAuditClaimLost)
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, LOG_DB.Model(&BillingAuditMarker{}).Where("audit_key = ?", auditKey).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingReservationRejectsQuotaOutsideDatabaseRange(t *testing.T) {
	tooLarge := int(math.MaxInt32) + 1
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     "oversized-create",
		UserId:        1,
		BillingSource: BillingReservationSourceWallet,
		Quota:         tooLarge,
		ExpiresAt:     common.GetTimestamp() + 60,
	})
	require.ErrorContains(t, err, "exceeds database limit")

	_, err = IncreaseBillingReservation("oversized-increase", tooLarge, 60)
	require.ErrorContains(t, err, "exceeds database limit")
	_, err = FinalizeBillingReservation("oversized-finalize", tooLarge, BillingReservationStatusSettling)
	require.ErrorContains(t, err, "exceeds database limit")
	err = RecordBillingSettlementFailure(BillingSettlementFailureInput{
		RequestId:          "oversized-failure",
		ActualQuota:        tooLarge,
		ReservationManaged: true,
		ReservationStatus:  BillingReservationStatusSettling,
	})
	require.ErrorContains(t, err, "outside database range")
}

func TestFindPendingBillingSettlementFailuresBacksOffRepeatedFailures(t *testing.T) {
	truncateTables(t)
	t.Setenv("BILLING_SETTLEMENT_RETRY_DELAY_SECONDS", "1")
	now := GetDBTimestamp()
	failure := &BillingSettlementFailure{
		RequestId: "retry-backoff-" + common.GetRandomString(8),
		Delta:     1,
		Status:    BillingSettlementStatusPending,
		Attempts:  8,
		UpdatedAt: now - 2,
	}
	require.NoError(t, DB.Create(failure).Error)

	failures, err := FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	assert.Empty(t, failures)
	assert.False(t, HasPendingBillingSettlementFailures())

	require.NoError(t, DB.Model(failure).Update("updated_at", now-129).Error)
	failures, err = FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	assert.Equal(t, failure.RequestId, failures[0].RequestId)
	assert.True(t, HasPendingBillingSettlementFailures())
}

func seedBillingReservationWallet(t *testing.T, quota int, giftQuota int, tokenQuota int) (*User, *Token) {
	t.Helper()
	user := &User{
		Username:  "billing-reservation-user-" + common.GetRandomString(8),
		Password:  "test-password",
		AffCode:   common.GetRandomString(16),
		Quota:     quota,
		GiftQuota: giftQuota,
		Status:    1,
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "billing-reservation-token",
		Status:      1,
		RemainQuota: tokenQuota,
	}
	require.NoError(t, DB.Create(token).Error)
	return user, token
}

func loadBillingReservationBalances(t *testing.T, userId int, tokenId int) (*User, *Token) {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, DB.First(&user, userId).Error)
	require.NoError(t, DB.Unscoped().First(&token, tokenId).Error)
	return &user, &token
}

func assertBillingReservationCacheEffect(t *testing.T, result *BillingReservationSettlementResult, userChanged bool, tokenChanged bool, conservative bool) {
	t.Helper()
	require.NotNil(t, result)
	assert.Equal(t, userChanged, result.cacheEffect.userQuotaChanged)
	assert.Equal(t, tokenChanged, result.cacheEffect.tokenQuotaChanged)
	assert.Equal(t, conservative, result.cacheEffect.conservative)
}

func TestBillingReservationZeroDeltaEvictsDeferredPreConsumeCachesAtSettlement(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "zero-delta-cache-effect-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)

	settled, err := FinalizeBillingReservation(requestId, 400, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, true, false)

	replayed, err := FinalizeBillingReservation(requestId, 400, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, replayed, false, false, true)
}

func TestBillingReservationDefersRedisDeletionUntilSettlement(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	hook := &billingReservationRedisDeleteHook{}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(hook)

	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
		_ = client.Close()
	})

	requestId := "deferred-cache-eviction-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	assert.Empty(t, hook.keys)

	_, err = IncreaseBillingReservation(requestId, 600, 60)
	require.NoError(t, err)
	assert.Empty(t, hook.keys)

	_, err = FinalizeBillingReservation(requestId, 600, BillingReservationStatusSettling)
	require.NoError(t, err)
	assert.Contains(t, hook.keys, getUserCacheKey(user.Id))
	assert.Contains(t, hook.keys, "token:"+common.GenerateHMAC(token.Key))
}

func TestBillingReservationTokenInvalidationPrefersStoredKeyHash(t *testing.T) {
	truncateTables(t)
	_, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	originalKeyHash := common.GenerateHMAC(token.Key)
	newKey := common.GetRandomString(32)
	require.NotEqual(t, originalKeyHash, common.GenerateHMAC(newKey))
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"key":      newKey,
		"key_hash": originalKeyHash,
	}).Error)

	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})
	credential := billingReservationTokenCredentialById(token.Id)
	require.NotNil(t, credential)
	assert.Equal(t, newKey, credential.Key)
	require.NotNil(t, credential.KeyHash)
	assert.Equal(t, originalKeyHash, *credential.KeyHash)
}

func TestBillingReservationWalletLifecycleIsIdempotent(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 300, 2000)
	requestId := "wallet-lifecycle-" + common.GetRandomString(8)
	input := BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() + 600,
	}

	created, err := CreateBillingReservation(input)
	require.NoError(t, err)
	require.NotNil(t, created.Reservation)
	assert.False(t, created.Reused)
	assert.Equal(t, 100, created.Reservation.WalletQuotaReserved)
	assert.Equal(t, 300, created.Reservation.WalletGiftQuotaReserved)

	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 900, actualUser.Quota)
	assert.Zero(t, actualUser.GiftQuota)
	assert.Equal(t, 1600, actualToken.RemainQuota)
	assert.Equal(t, 400, actualToken.UsedQuota)

	reused, err := CreateBillingReservation(input)
	require.NoError(t, err)
	assert.True(t, reused.Reused)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 900, actualUser.Quota)
	assert.Zero(t, actualUser.GiftQuota)
	assert.Equal(t, 1600, actualToken.RemainQuota)

	increased, err := IncreaseBillingReservation(requestId, 600, 900)
	require.NoError(t, err)
	assert.Equal(t, 600, increased.ReservedQuota)
	assert.Equal(t, 300, increased.WalletQuotaReserved)
	assert.Equal(t, 300, increased.WalletGiftQuotaReserved)

	settled, err := FinalizeBillingReservation(requestId, 150, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, true, false)
	assert.Equal(t, 150, settled.ActualQuota)
	assert.Zero(t, settled.WalletQuotaConsumed)
	assert.Equal(t, 150, settled.WalletGiftQuotaConsumed)

	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 150, actualUser.GiftQuota)
	assert.Equal(t, 1850, actualToken.RemainQuota)
	assert.Equal(t, 150, actualToken.UsedQuota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
	replayed, err := FinalizeBillingReservation(requestId, 150, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, replayed, false, false, true)
	require.NoError(t, AcknowledgeBillingReservation(requestId))

	_, err = FinalizeBillingReservation(requestId, 150, BillingReservationStatusSettling)
	require.NoError(t, err)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 150, actualUser.GiftQuota)
	assert.Equal(t, 1850, actualToken.RemainQuota)
}

func TestTaskSubmissionAndTerminalRefundAreAtomic(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "task-atomic-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		ModelName:     "test-task-model",
		Group:         "default",
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 7))

	task := &Task{
		TaskID:    "task_atomic_" + common.GetRandomString(8),
		UserId:    user.Id,
		ChannelId: 7,
		Group:     "default",
		Status:    TaskStatusSubmitted,
		PrivateData: TaskPrivateData{
			BillingSource: BillingReservationSourceWallet,
			TokenId:       token.Id,
		},
	}
	settled, err := InsertTaskWithBillingReservation(task, requestId, 300)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, true, false)
	assert.Equal(t, 7, settled.ChannelId)
	assert.Equal(t, 400, settled.PreConsumedQuota)
	assert.Equal(t, 300, settled.ActualQuota)
	assert.NotZero(t, task.ID)

	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 700, actualUser.Quota)
	assert.Equal(t, 700, actualToken.RemainQuota)
	replayedTask := &Task{
		TaskID: task.TaskID,
		UserId: task.UserId,
		Status: TaskStatusSubmitted,
	}
	replayed, err := InsertTaskWithBillingReservation(replayedTask, requestId, 300)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, replayed, false, false, true)
	_, err = InsertTaskWithBillingReservation(replayedTask, requestId, 301)
	require.ErrorContains(t, err, "quota conflicts with idempotent replay")
	var taskCount int64
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Equal(t, int64(1), taskCount)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 700, actualUser.Quota)
	assert.Equal(t, 700, actualToken.RemainQuota)
	auditClaim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, CompleteBillingReservationAuditClaim(requestId, auditClaim.ExpiresAt, 300))

	fromStatus := task.Status
	task.Status = TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = "upstream failed"
	won, refunded, err := FinalizeTaskBilling(task, fromStatus, 0)
	require.NoError(t, err)
	assert.True(t, won)
	assertBillingReservationCacheEffect(t, refunded, true, true, false)
	assert.Equal(t, 300, refunded.PreConsumedQuota)
	assert.Equal(t, 300, refunded.AuditedQuota)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 1000, actualToken.RemainQuota)

	won, _, err = FinalizeTaskBilling(task, fromStatus, 0)
	require.NoError(t, err)
	assert.False(t, won)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 1000, actualToken.RemainQuota)
}

func TestTaskTerminalBillingWaitsForSubmissionAuditClaim(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "task-audit-serialization-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 0))
	task := &Task{
		TaskID: "task_audit_serialization_" + common.GetRandomString(8),
		UserId: user.Id,
		Status: TaskStatusSubmitted,
		PrivateData: TaskPrivateData{
			BillingSource: BillingReservationSourceWallet,
			TokenId:       token.Id,
		},
	}
	_, err = InsertTaskWithBillingReservation(task, requestId, 300)
	require.NoError(t, err)

	// A generic synchronous acknowledgement must never remove a task receipt.
	require.NoError(t, AcknowledgeBillingReservation(requestId))
	receipt, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, receipt)

	claim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	fromStatus := task.Status
	task.Status = TaskStatusFailure
	task.Progress = "100%"
	won, _, err := FinalizeTaskBilling(task, fromStatus, 0)
	require.ErrorContains(t, err, "active submission audit claim")
	assert.False(t, won)
	var stored Task
	require.NoError(t, DB.First(&stored, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusSubmitted), stored.Status)
	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 700, actualUser.Quota)
	assert.Equal(t, 700, actualToken.RemainQuota)

	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("request_id = ?", requestId).
		Update("expires_at", GetDBTimestamp()-1).Error)
	won, _, err = FinalizeTaskBilling(task, fromStatus, 0)
	require.ErrorContains(t, err, "active submission audit claim")
	assert.False(t, won)
	require.ErrorIs(t, ValidateBillingReservationAuditClaim(requestId, claim.ExpiresAt), ErrBillingReservationAuditClaimLost)
	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("request_id = ?", requestId).
		Update("expires_at", GetDBTimestamp()-int64(BillingAuditLogTimeoutSeconds())-1).Error)
	won, _, err = FinalizeTaskBilling(task, fromStatus, 0)
	require.NoError(t, err)
	assert.True(t, won)
	require.ErrorIs(t, CompleteBillingReservationAuditClaim(requestId, claim.ExpiresAt, 300), ErrBillingReservationAuditClaimLost)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 1000, actualToken.RemainQuota)
}

func TestStaleBillingAuditWorkerCannotReleaseReclaimedLease(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "stale-audit-claim-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	_, err = FinalizeBillingReservation(requestId, 100, BillingReservationStatusSettling)
	require.NoError(t, err)
	staleClaim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("request_id = ?", requestId).
		Update("expires_at", GetDBTimestamp()-int64(BillingAuditLogTimeoutSeconds())-1).Error)
	activeClaim, claimed, err := ClaimBillingReservationAudit(requestId, 120)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEqual(t, staleClaim.ExpiresAt, activeClaim.ExpiresAt)

	ReleaseBillingReservationAudit(requestId, staleClaim.ExpiresAt, errors.New("stale worker error"))
	receipt, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, BillingReservationStatusAuditing, receipt.Status)
	assert.Equal(t, activeClaim.ExpiresAt, receipt.ExpiresAt)
	assert.Empty(t, receipt.LastError)
	require.ErrorIs(t, AcknowledgeBillingReservationAudit(requestId, staleClaim.ExpiresAt), ErrBillingReservationAuditClaimLost)
	require.NoError(t, AcknowledgeBillingReservationAudit(requestId, activeClaim.ExpiresAt))
}

func TestTaskTerminalBillingFailureRollsBackStatus(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 500, 0, 2000)
	requestId := "task-rollback-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 0))
	task := &Task{
		TaskID: "task_rollback_" + common.GetRandomString(8),
		UserId: user.Id,
		Status: TaskStatusInProgress,
		PrivateData: TaskPrivateData{
			BillingSource: BillingReservationSourceWallet,
			TokenId:       token.Id,
		},
	}
	_, err = InsertTaskWithBillingReservation(task, requestId, 400)
	require.NoError(t, err)

	fromStatus := task.Status
	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	won, _, err := FinalizeTaskBilling(task, fromStatus, 1000)
	require.ErrorIs(t, err, ErrInsufficientUserQuota)
	assert.False(t, won)

	var stored Task
	require.NoError(t, DB.First(&stored, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusInProgress), stored.Status)
	assert.Equal(t, 400, stored.Quota)
	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 100, actualUser.Quota)
	assert.Equal(t, 1600, actualToken.RemainQuota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
	assert.Equal(t, 400, reservation.ReservedQuota)
}

func TestLegacySubscriptionTaskFinalizesWithoutOriginalPreConsumeRequestId(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	plan := &SubscriptionPlan{
		Title:         "legacy task plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 1000,
		AmountUsed:  400,
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
	}
	require.NoError(t, DB.Create(subscription).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"remain_quota": 600,
		"used_quota":   400,
	}).Error)
	task := &Task{
		TaskID:   "legacy_subscription_" + common.GetRandomString(8),
		UserId:   user.Id,
		Quota:    400,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource:  BillingReservationSourceSubscription,
			SubscriptionId: subscription.Id,
			TokenId:        token.Id,
		},
	}
	require.NoError(t, DB.Create(task).Error)

	fromStatus := task.Status
	task.Status = TaskStatusFailure
	task.Progress = "100%"
	won, settlement, err := FinalizeTaskBilling(task, fromStatus, 0)
	require.NoError(t, err)
	assert.True(t, won)
	assertBillingReservationCacheEffect(t, settlement, false, true, false)
	assert.Equal(t, 400, settlement.PreConsumedQuota)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Zero(t, subscription.AmountUsed)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	var stored Task
	require.NoError(t, DB.First(&stored, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusFailure), stored.Status)
}

func TestMidjourneyFailureRefundsFundingAndTokenAtomically(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "mj-atomic-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		ModelName:     "mj_test",
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 0))
	task := &Midjourney{
		UserId:    user.Id,
		MjId:      "mj_" + common.GetRandomString(8),
		ChannelId: 9,
		Status:    "IN_PROGRESS",
		Progress:  "50%",
	}
	settled, err := InsertMidjourneyWithBillingReservation(task, requestId, 400)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, true, false)
	replayedTask := &Midjourney{
		UserId: task.UserId,
		MjId:   task.MjId,
		Status: "IN_PROGRESS",
	}
	replayed, err := InsertMidjourneyWithBillingReservation(replayedTask, requestId, 400)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, replayed, false, false, true)
	_, err = InsertMidjourneyWithBillingReservation(replayedTask, requestId, 401)
	require.ErrorContains(t, err, "quota conflicts with idempotent replay")
	var taskCount int64
	require.NoError(t, DB.Model(&Midjourney{}).Where("mj_id = ?", task.MjId).Count(&taskCount).Error)
	assert.Equal(t, int64(1), taskCount)
	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 600, actualUser.Quota)
	assert.Equal(t, 600, actualToken.RemainQuota)
	auditClaim, claimed, err := ClaimBillingReservationAudit(requestId, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, CompleteBillingReservationAuditClaim(requestId, auditClaim.ExpiresAt, 400))

	fromStatus := task.Status
	task.Status = "FAILURE"
	task.Progress = "100%"
	task.FailReason = "upstream failed"
	won, settlement, err := FinalizeMidjourneyBilling(task, fromStatus, 0)
	require.NoError(t, err)
	assert.True(t, won)
	assert.Equal(t, token.Id, settlement.TokenId)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 1000, actualToken.RemainQuota)
	assert.Zero(t, actualToken.UsedQuota)

	var stored Midjourney
	require.NoError(t, DB.First(&stored, task.Id).Error)
	assert.Equal(t, "FAILURE", stored.Status)
	assert.Zero(t, stored.Quota)
}

func TestBillingReservationPersistsSettlementIntentUntilRetrySucceeds(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 500, 0, 1000)
	requestId := "settlement-retry-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() + 600,
	})
	require.NoError(t, err)

	_, err = FinalizeBillingReservation(requestId, 600, BillingReservationStatusSettling)
	require.Error(t, err)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusSettling, reservation.Status)
	assert.Equal(t, 600, reservation.DesiredQuota)
	assert.Equal(t, 1, reservation.Attempts)

	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 100, actualUser.Quota)
	assert.Equal(t, 600, actualToken.RemainQuota)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 300).Error)
	_, err = ApplyBillingReservationIntent(requestId)
	require.NoError(t, err)
	actualUser, actualToken = loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 100, actualUser.Quota)
	assert.Equal(t, 400, actualToken.RemainQuota)
	assert.Equal(t, 600, actualToken.UsedQuota)
	reservation, err = GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
	require.NoError(t, AcknowledgeBillingReservation(requestId))
}

func TestBillingReservationRollbackIsAtomicWhenTokenQuotaIsInsufficient(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 200, 100)
	requestId := "token-insufficient-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         300,
		ExpiresAt:     common.GetTimestamp() + 600,
	})
	require.ErrorContains(t, err, "insufficient token quota")

	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	assert.Equal(t, 200, actualUser.GiftQuota)
	assert.Equal(t, 100, actualToken.RemainQuota)
	assert.Zero(t, actualToken.UsedQuota)
	reservation, findErr := GetBillingReservationByRequestId(requestId)
	require.NoError(t, findErr)
	assert.Nil(t, reservation)
}

func TestBillingReservationRejectsTokenDeletedBeforePreConsume(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	require.NoError(t, DB.Delete(token).Error)
	requestId := "deleted-token-preconsume-" + common.GetRandomString(8)

	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.ErrorIs(t, err, ErrBillingReservationTokenNotFound)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	assert.Nil(t, reservation)
}

func TestBillingReservationRejectsTokenOwnedByAnotherUser(t *testing.T) {
	truncateTables(t)
	user, _ := seedBillingReservationWallet(t, 1000, 0, 1000)
	_, anotherUsersToken := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "foreign-token-" + common.GetRandomString(8)

	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       anotherUsersToken.Id,
		TokenKey:      anotherUsersToken.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.ErrorContains(t, err, "belongs to another user")

	var storedUser User
	var storedToken Token
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.NoError(t, DB.First(&storedToken, anotherUsersToken.Id).Error)
	assert.Equal(t, 1000, storedUser.Quota)
	assert.Equal(t, 1000, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
	var count int64
	require.NoError(t, DB.Model(&BillingReservation{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingReservationRejectsMissingTokenForZeroQuota(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	require.NoError(t, DB.Delete(token).Error)

	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     "missing-zero-quota-token-" + common.GetRandomString(8),
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         0,
		LeaseSeconds:  60,
	})
	require.ErrorIs(t, err, ErrBillingReservationTokenNotFound)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Equal(t, 1000, user.Quota)
}

func TestBillingReservationRefundRejectsInvalidTokenBalance(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "invalid-token-refund-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Update("used_quota", 0).Error)

	_, err = FinalizeBillingReservation(requestId, 0, BillingReservationStatusRefunding)
	require.ErrorContains(t, err, "token quota refund would exceed database bounds")
	actualUser, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 600, actualUser.Quota)
	assert.Equal(t, 600, actualToken.RemainQuota)
	assert.Zero(t, actualToken.UsedQuota)
	reservation, findErr := GetBillingReservationByRequestId(requestId)
	require.NoError(t, findErr)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusRefunding, reservation.Status)

	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Update("used_quota", 400).Error)
	_, err = ApplyBillingReservationIntent(requestId)
	require.NoError(t, err)
}

func TestBillingReservationPlaygroundNeverAdjustsTokenQuota(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "playground-token-policy-" + common.GetRandomString(8)

	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		IsPlayground:  true,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NotNil(t, created.Reservation.TokenQuotaEnabled)
	assert.False(t, *created.Reservation.TokenQuotaEnabled)
	assert.Zero(t, created.Reservation.TokenQuotaReserved)

	_, err = IncreaseBillingReservation(requestId, 600, 60)
	require.NoError(t, err)
	settled, err := FinalizeBillingReservation(requestId, 250, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, false, false)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 750, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestBillingReservationRefundCompletesAfterTokenDeletion(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "deleted-token-refund-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() + 600,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&Token{}, token.Id).Error)

	settled, err := FinalizeBillingReservation(requestId, 0, BillingReservationStatusRefunding)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, true, true, false)
	actualUser, _ := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1000, actualUser.Quota)
	reservation, findErr := GetBillingReservationByRequestId(requestId)
	require.NoError(t, findErr)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
	require.NoError(t, AcknowledgeBillingReservation(requestId))
}

func TestBillingReservationRefundCompletesAfterUserSoftDeletion(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "deleted-user-refund-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() + 60,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Delete(user).Error)

	_, err = FinalizeBillingReservation(requestId, 0, BillingReservationStatusRefunding)
	require.NoError(t, err)
	require.NoError(t, DB.Unscoped().First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
	require.NoError(t, AcknowledgeBillingReservation(requestId))
}

func TestRepairExpiredBillingReservationRechecksRenewedLease(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "renewed-before-repair-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     GetDBTimestamp() - 1,
	})
	require.NoError(t, err)
	require.NoError(t, TouchBillingReservation(requestId, 600))

	repaired, err := RepairExpiredBillingReservation(requestId)
	require.NoError(t, err)
	assert.False(t, repaired)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 600, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Greater(t, reservation.ExpiresAt, GetDBTimestamp())

	_, err = FinalizeBillingReservation(requestId, 0, BillingReservationStatusRefunding)
	require.NoError(t, err)
}

func TestRepairExpiredDispatchedReservationKeepsReservedCharge(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	requestId := "expired-dispatched-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, MarkBillingReservationDispatched(requestId, 60, 0))
	require.NoError(t, DB.Model(&BillingReservation{}).Where("request_id = ?", requestId).
		Update("expires_at", GetDBTimestamp()-1).Error)

	repaired, err := RepairExpiredBillingReservation(requestId)
	require.NoError(t, err)
	assert.True(t, repaired)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 600, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)
	assert.Equal(t, 400, token.UsedQuota)
	reservation, err := GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, BillingReservationStatusCompleted, reservation.Status)
}

func TestBillingReservationSubscriptionLifecycle(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 2000)
	plan := &SubscriptionPlan{
		Title:         "reservation plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 1000,
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
	}
	require.NoError(t, DB.Create(subscription).Error)
	requestId := "subscription-reservation-" + common.GetRandomString(8)

	created, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceSubscription,
		ModelName:     "test-model",
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() + 600,
	})
	require.NoError(t, err)
	assert.Equal(t, subscription.Id, created.Reservation.SubscriptionId)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Equal(t, int64(400), subscription.AmountUsed)

	_, err = IncreaseBillingReservation(requestId, 600, 900)
	require.NoError(t, err)
	var preConsume SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", requestId).First(&preConsume).Error)
	assert.Equal(t, int64(600), preConsume.PreConsumed)
	settled, err := FinalizeBillingReservation(requestId, 250, BillingReservationStatusSettling)
	require.NoError(t, err)
	assertBillingReservationCacheEffect(t, settled, false, true, false)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Equal(t, int64(250), subscription.AmountUsed)
	_, actualToken := loadBillingReservationBalances(t, user.Id, token.Id)
	assert.Equal(t, 1750, actualToken.RemainQuota)

	require.NoError(t, DB.Where("request_id = ?", requestId).First(&preConsume).Error)
	assert.Equal(t, "consumed", preConsume.Status)
	assert.Equal(t, int64(250), preConsume.PreConsumed)
}

func TestSubscriptionDeltaRejectsAmountUsedOverflow(t *testing.T) {
	truncateTables(t)
	user, _ := seedBillingReservationWallet(t, 1000, 0, 1000)
	plan := &SubscriptionPlan{
		Title:         "overflow plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   0,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 0,
		AmountUsed:  math.MaxInt64,
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
	}
	require.NoError(t, DB.Create(subscription).Error)

	err := PostConsumeUserSubscriptionDelta(subscription.Id, 1)
	require.ErrorContains(t, err, "would overflow")
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Equal(t, int64(math.MaxInt64), subscription.AmountUsed)
}

func TestSubscriptionPreConsumeCleanupRetainsDurableBillingDependency(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	plan := &SubscriptionPlan{
		Title:         "cleanup protection plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 1000,
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
	}
	require.NoError(t, DB.Create(subscription).Error)
	requestId := "cleanup-protected-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceSubscription,
		ModelName:     "test-model",
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestId).
		Update("updated_at", GetDBTimestamp()-3600).Error)

	deleted, err := CleanupSubscriptionPreConsumeRecords(60)
	require.NoError(t, err)
	assert.Zero(t, deleted)
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	_, err = FinalizeBillingReservation(requestId, 100, BillingReservationStatusSettling)
	require.NoError(t, err)
	require.NoError(t, AcknowledgeBillingReservation(requestId))
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestId).
		Update("updated_at", GetDBTimestamp()-3600).Error)
	deleted, err = CleanupSubscriptionPreConsumeRecords(60)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestSubscriptionSettlementRejectsInconsistentPreConsumeReceipt(t *testing.T) {
	truncateTables(t)
	user, token := seedBillingReservationWallet(t, 1000, 0, 1000)
	plan := &SubscriptionPlan{
		Title:         "receipt consistency plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 1000,
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
	}
	require.NoError(t, DB.Create(subscription).Error)
	requestId := "inconsistent-preconsume-" + common.GetRandomString(8)
	_, err := CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceSubscription,
		ModelName:     "test-model",
		Quota:         100,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestId).
		Update("status", "refunded").Error)

	_, err = FinalizeBillingReservation(requestId, 50, BillingReservationStatusSettling)
	require.ErrorContains(t, err, "pre-consume record is inconsistent")
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Equal(t, int64(100), subscription.AmountUsed)
}
