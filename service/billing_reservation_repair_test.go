package service

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNewBillingSessionRejectsQuotaOutsideDatabaseBounds(t *testing.T) {
	for _, quota := range []int{-1, int(math.MaxInt32) + 1} {
		_, apiErr := NewBillingSession(nil, &relaycommon.RelayInfo{}, quota)
		require.NotNil(t, apiErr)
		assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	}
}

func TestBillingSessionPreConsumesBeforeChannelMetaInitializationDispatchesAndRefundsDurably(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "billing-session-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "billing-session-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelId, 12)
	requestId := "billing-session-" + common.GetRandomString(8)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       requestId,
		OriginModelName: "test-model",
	}
	info.UserSetting.BillingPreference = "wallet_only"

	apiErr := PreConsumeBilling(c, 400, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.True(t, info.Billing.NeedsRefund())
	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, 12, reservation.ChannelId)
	require.Nil(t, info.ChannelMeta)
	info.InitChannelMeta(c)
	require.NoError(t, info.Billing.MarkDispatched())
	reservation, err = model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusDispatched, reservation.Status)
	assert.Equal(t, 12, reservation.ChannelId)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 600, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)

	require.NoError(t, info.Billing.Refund(c))
	assert.False(t, info.Billing.NeedsRefund())
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	reservation, err = model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	assert.Nil(t, reservation)
}

func TestBillingSessionAlwaysPreConsumesWhenTrustThresholdIsEnabled(t *testing.T) {
	truncate(t)
	t.Setenv("TRUST_QUOTA", "10")
	user := &model.User{
		Username: "billing-no-trust-bypass-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "billing-no-trust-bypass-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("token_quota", 1000)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       "billing-no-trust-bypass-" + common.GetRandomString(8),
		OriginModelName: "test-model",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	info.UserSetting.BillingPreference = "wallet_only"

	apiErr := PreConsumeBilling(c, 400, info)
	require.Nil(t, apiErr)
	assert.Equal(t, 400, info.FinalPreConsumedQuota)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 600, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)
	require.NoError(t, info.Billing.Refund(c))
}

func TestBillingSessionDoesNotRefundAfterPreciseSettlementStarts(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "billing-finalization-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    500,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "billing-finalization-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	requestId := "billing-finalization-" + common.GetRandomString(8)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       requestId,
		OriginModelName: "test-model",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	require.Nil(t, PreConsumeBilling(c, 400, info))
	require.NoError(t, info.Billing.MarkDispatched())
	require.ErrorContains(t, info.Billing.Settle(-1), "cannot be negative")
	assert.False(t, info.Billing.NeedsRefund())
	require.NoError(t, info.Billing.Refund(c))
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)

	// The provider has succeeded, but the exact 600-quota settlement cannot be
	// completed until another 200 quota becomes available. Automatic error
	// cleanup must not overwrite that durable settlement intent with a refund.
	require.Error(t, SettleBilling(c, info, 600))
	assert.False(t, info.Billing.NeedsRefund())
	require.NoError(t, info.Billing.Refund(c))
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)

	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusSettling, reservation.Status)
	assert.Equal(t, 600, reservation.DesiredQuota)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", 200)).Error)
	require.NoError(t, info.Billing.Settle(600))
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 600, token.UsedQuota)
}

func TestTaskCommitFailurePersistsExactSettlementIntent(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "task-commit-intent-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    500,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "task-commit-intent-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	requestId := "task-commit-intent-" + common.GetRandomString(8)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       requestId,
		OriginModelName: "test-task-model",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 7},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	require.Nil(t, PreConsumeBilling(c, 400, info))
	require.NoError(t, info.Billing.MarkDispatched())

	task := &model.Task{
		TaskID: "task_commit_intent_" + common.GetRandomString(8),
		UserId: user.Id,
		Status: model.TaskStatusSubmitted,
	}
	require.Error(t, CommitTaskSubmission(task, info, 600))
	assert.False(t, info.Billing.NeedsRefund())
	require.NoError(t, info.Billing.Refund(c))

	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusSettling, reservation.Status)
	assert.Equal(t, 600, reservation.DesiredQuota)
	var failure model.BillingSettlementFailure
	require.NoError(t, model.DB.Where("request_id = ?", requestId).First(&failure).Error)
	assert.True(t, failure.ReservationManaged)
	assert.Equal(t, 600, failure.ActualQuota)
	assert.Equal(t, model.BillingSettlementStatusPending, failure.Status)
	var taskCount int64
	require.NoError(t, model.DB.Model(&model.Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestBillingSettlementRepairRefundsExpiredReservation(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "expired-reservation-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "expired-reservation-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	requestId := "expired-reservation-" + common.GetRandomString(8)
	_, err := model.CreateBillingReservation(model.BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: model.BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() - 1,
	})
	require.NoError(t, err)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Equal(t, 1, summary.ReservationsScanned)
	assert.Equal(t, 1, summary.ReservationsRepaired)
	assert.Zero(t, summary.ReservationsFailed)

	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	assert.Nil(t, reservation)
}

func TestBillingSettlementFailureUsesReservationWithoutDoubleSettlement(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "managed-settlement-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "managed-settlement-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	requestId := "managed-settlement-" + common.GetRandomString(8)
	_, err := model.CreateBillingReservation(model.BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: model.BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() - 1,
	})
	require.NoError(t, err)
	require.NoError(t, model.RecordBillingSettlementFailure(model.BillingSettlementFailureInput{
		RequestId:          requestId,
		UserId:             user.Id,
		TokenId:            token.Id,
		BillingSource:      model.BillingReservationSourceWallet,
		ActualQuota:        200,
		PreConsumedQuota:   400,
		Delta:              -200,
		ReservationManaged: true,
		ReservationStatus:  model.BillingReservationStatusSettling,
		LastError:          "simulated transient failure",
	}))
	reservation, findErr := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, findErr)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusSettling, reservation.Status)
	assert.Equal(t, 200, reservation.DesiredQuota)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Zero(t, summary.ReservationsRepaired)
	assert.Equal(t, 1, summary.Settled)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 800, user.Quota)
	assert.Equal(t, 800, token.RemainQuota)
	assert.Equal(t, 200, token.UsedQuota)

	var failure model.BillingSettlementFailure
	require.NoError(t, model.DB.Where("request_id = ?", requestId).First(&failure).Error)
	assert.Equal(t, model.BillingSettlementStatusSettled, failure.Status)
}

func TestBillingSettlementRepairDoesNotRefundExactManagedCharge(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "exact-managed-settlement-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "exact-managed-settlement-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	requestId := "exact-managed-settlement-" + common.GetRandomString(8)
	_, err := model.CreateBillingReservation(model.BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: model.BillingReservationSourceWallet,
		Quota:         400,
		ExpiresAt:     common.GetTimestamp() - 1,
	})
	require.NoError(t, err)
	require.NoError(t, model.RecordBillingSettlementFailure(model.BillingSettlementFailureInput{
		RequestId:          requestId,
		UserId:             user.Id,
		TokenId:            token.Id,
		BillingSource:      model.BillingReservationSourceWallet,
		ActualQuota:        400,
		PreConsumedQuota:   400,
		Delta:              0,
		ReservationManaged: true,
		ReservationStatus:  model.BillingReservationStatusSettling,
		LastError:          "simulated intent persistence failure",
	}))
	reservation, findErr := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, findErr)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusSettling, reservation.Status)
	assert.Equal(t, 400, reservation.DesiredQuota)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Equal(t, 1, summary.Scanned)
	assert.Equal(t, 1, summary.Settled)
	assert.Zero(t, summary.ReservationsRepaired)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 600, user.Quota)
	assert.Equal(t, 600, token.RemainQuota)
	assert.Equal(t, 400, token.UsedQuota)

	reservation, err = model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusCompleted, reservation.Status)
	require.NoError(t, model.AcknowledgeBillingReservation(requestId))
}

func TestBillingSettlementRepairReconstructsMissingConsumeAudit(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "audit-repair-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "audit-repair-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	requestId := "audit-repair-" + common.GetRandomString(8)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       requestId,
		OriginModelName: "test-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 8},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	apiErr := PreConsumeBilling(c, 400, info)
	require.Nil(t, apiErr)
	require.NoError(t, SettleBilling(c, info, 300))
	require.NoError(t, model.DB.Model(&model.BillingReservation{}).
		Where("request_id = ?", requestId).
		Update("updated_at", model.GetDBTimestamp()-120).Error)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Equal(t, 1, summary.AuditsRepaired)
	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	assert.Nil(t, reservation)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestId).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, model.LogTypeConsume, logs[0].Type)
	assert.Equal(t, 300, logs[0].Quota)
}

func TestBillingSettlementRepairReconstructsRunningTaskSubmissionAudit(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "task-submit-audit-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "task-submit-audit-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	requestId := "task-submit-audit-" + common.GetRandomString(8)
	_, err := model.CreateBillingReservation(model.BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		ChannelId:     9,
		BillingSource: model.BillingReservationSourceWallet,
		ModelName:     "test-model",
		Group:         "default",
		Quota:         300,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	task := &model.Task{
		CreatedAt: common.GetTimestamp(),
		UpdatedAt: common.GetTimestamp(),
		TaskID:    "task_" + common.GetRandomString(16),
		UserId:    user.Id,
		Group:     "default",
		ChannelId: 9,
		Status:    model.TaskStatusSubmitted,
		Progress:  "10%",
		PrivateData: model.TaskPrivateData{
			TokenId:          token.Id,
			BillingRequestId: requestId,
			BillingContext: &model.TaskBillingContext{
				OriginModelName: "test-model",
				SubmittedQuota:  300,
			},
		},
	}
	_, err = model.InsertTaskWithBillingReservation(task, requestId, 300)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.BillingReservation{}).Where("request_id = ?", requestId).
		Update("updated_at", model.GetDBTimestamp()-120).Error)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Equal(t, 1, summary.AuditsRepaired)
	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	assert.Equal(t, model.BillingReservationStatusCompleted, reservation.Status)
	assert.Equal(t, 300, reservation.AuditedQuota)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestId).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, model.LogTypeConsume, logs[0].Type)
	assert.Equal(t, 300, logs[0].Quota)

	summary = RunBillingSettlementRepairOnce(context.Background())
	assert.Zero(t, summary.AuditsRepaired)
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestId).Find(&logs).Error)
	require.Len(t, logs, 1)
}

func TestBillingSettlementRepairDoesNotDuplicateCommittedAudit(t *testing.T) {
	truncate(t)
	user := &model.User{
		Username: "audit-dedupe-user-" + common.GetRandomString(8),
		Password: "test-password",
		Quota:    1000,
		Status:   1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "audit-dedupe-token",
		Status:      1,
		RemainQuota: 1000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	requestId := "audit-dedupe-" + common.GetRandomString(8)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		RequestId:       requestId,
		OriginModelName: "test-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 8},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	apiErr := PreConsumeBilling(c, 400, info)
	require.Nil(t, apiErr)
	require.NoError(t, SettleBilling(c, info, 300))
	reservation, err := model.GetBillingReservationByRequestId(requestId)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	auditKey := billingAuditKey("request", requestId, reservation.Id, 0, 300)
	require.NoError(t, model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    user.Id,
		LogType:   model.LogTypeConsume,
		ModelName: "test-model",
		Quota:     300,
		TokenId:   token.Id,
		Group:     "default",
		RequestId: requestId,
		Other:     map[string]interface{}{"billing_audit_key": auditKey},
	}))
	require.NoError(t, model.DB.Model(&model.BillingReservation{}).
		Where("request_id = ?", requestId).
		Update("updated_at", model.GetDBTimestamp()-120).Error)

	summary := RunBillingSettlementRepairOnce(context.Background())
	assert.Equal(t, 1, summary.AuditsRepaired)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("request_id = ?", requestId).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
