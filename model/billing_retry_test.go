package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRetryDelayUsesErrorSpecificBackoff(t *testing.T) {
	insufficient := fmt.Errorf("settlement failed: %w", ErrInsufficientUserQuota)
	assert.Equal(t, int64(60), billingRetryDelaySeconds("request-a", 1, insufficient))
	assert.Equal(t, int64(300), billingRetryDelaySeconds("request-a", 2, insufficient))
	assert.Equal(t, int64(900), billingRetryDelaySeconds("request-a", 3, insufficient))
	assert.Equal(t, int64(900), billingRetryDelaySeconds("request-a", 8, insufficient))

	transient := context.DeadlineExceeded
	assertRetryDelayRange(t, "request-b", 1, transient, 15)
	assertRetryDelayRange(t, "request-b", 2, transient, 30)
	assertRetryDelayRange(t, "request-b", 5, transient, 300)
	assertRetryDelayRange(t, "request-b", 20, transient, 300)

	generic := errors.New("unexpected settlement state")
	assertRetryDelayRange(t, "request-c", 1, generic, 60)
	assertRetryDelayRange(t, "request-c", 5, generic, 900)
}

func TestBillingRetryDelayClassifiesDatabaseTransientErrors(t *testing.T) {
	for _, message := range []string{
		"lock not available (SQLSTATE 55P03)",
		"deadlock detected (SQLSTATE 40P01)",
		"connection refused",
		"canceling statement due to statement timeout (SQLSTATE 57014)",
	} {
		assert.Equal(t, billingRetryClassTransient, classifyBillingRetryError(errors.New(message)))
	}
}

func TestBillingRetryJitterIsDeterministicAndBounded(t *testing.T) {
	first := billingRetryDelaySeconds("stable-request", 4, context.DeadlineExceeded)
	second := billingRetryDelaySeconds("stable-request", 4, context.DeadlineExceeded)
	assert.Equal(t, first, second)
	assert.GreaterOrEqual(t, first, int64(120))
	assert.LessOrEqual(t, first, int64(144))
}

func TestBillingSettlementFailureAttemptPersistsNextRetryTime(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	failure := &BillingSettlementFailure{
		RequestId:   "adaptive-failure-" + common.GetRandomString(8),
		Delta:       1,
		Status:      BillingSettlementStatusPending,
		NextRetryAt: now,
	}
	require.NoError(t, DB.Create(failure).Error)
	require.NoError(t, MarkBillingSettlementFailureAttempt(failure.Id, context.DeadlineExceeded))
	require.NoError(t, DB.First(failure, failure.Id).Error)
	assert.Equal(t, 1, failure.Attempts)
	assert.GreaterOrEqual(t, failure.NextRetryAt, now+15)
	assert.LessOrEqual(t, failure.NextRetryAt, now+18)

	pending, err := FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	assert.Empty(t, pending)
	require.NoError(t, DB.Model(failure).Update("next_retry_at", GetDBTimestamp()-1).Error)
	pending, err = FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
}

func TestBillingReservationAttemptUsesAdaptiveBackoffAndIgnoresManualRows(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	reservation := &BillingReservation{
		RequestId:     "adaptive-reservation-" + common.GetRandomString(8),
		UserId:        1,
		BillingSource: BillingReservationSourceWallet,
		ReservedQuota: 400,
		DesiredQuota:  600,
		Status:        BillingReservationStatusSettling,
	}
	require.NoError(t, DB.Create(reservation).Error)
	RecordBillingReservationAttempt(reservation.RequestId, ErrInsufficientUserQuota)
	require.NoError(t, DB.First(reservation, reservation.Id).Error)
	assert.Equal(t, 1, reservation.Attempts)
	assert.GreaterOrEqual(t, reservation.ExpiresAt, now+60)
	assert.LessOrEqual(t, reservation.ExpiresAt, GetDBTimestamp()+60)

	RecordBillingReservationAttempt(reservation.RequestId, ErrInsufficientUserQuota)
	require.NoError(t, DB.First(reservation, reservation.Id).Error)
	assert.Equal(t, 2, reservation.Attempts)
	assert.GreaterOrEqual(t, reservation.ExpiresAt, GetDBTimestamp()+299)
	manualExpiry := reservation.ExpiresAt
	require.NoError(t, DB.Model(reservation).Update("status", BillingReservationStatusManualRequired).Error)
	RecordBillingReservationAttempt(reservation.RequestId, errors.New("late retry"))
	require.NoError(t, DB.First(reservation, reservation.Id).Error)
	assert.Equal(t, 2, reservation.Attempts)
	assert.Equal(t, manualExpiry, reservation.ExpiresAt)
}

func TestRequeueManualBillingReservationRequiresExactQuotaAndIsSingleUse(t *testing.T) {
	truncateTables(t)
	requestId := "manual-requeue-" + common.GetRandomString(8)
	reservation := &BillingReservation{
		RequestId:     requestId,
		UserId:        1,
		BillingSource: BillingReservationSourceWallet,
		ReservedQuota: 400,
		DesiredQuota:  600,
		Status:        BillingReservationStatusManualRequired,
		Attempts:      8,
		LastError:     ErrInsufficientUserQuota.Error(),
		ExpiresAt:     billingReservationNoExpiry,
	}
	require.NoError(t, DB.Create(reservation).Error)
	failure := &BillingSettlementFailure{
		RequestId:          requestId,
		UserId:             1,
		ActualQuota:        600,
		PreConsumedQuota:   400,
		Delta:              200,
		ReservationManaged: true,
		ReservationStatus:  BillingReservationStatusManualRequired,
		Status:             BillingSettlementStatusManualRequired,
		Attempts:           8,
		LastError:          ErrInsufficientUserQuota.Error(),
	}
	require.NoError(t, DB.Create(failure).Error)

	_, err := RequeueManualBillingReservation(requestId, 599)
	require.ErrorContains(t, err, "confirmation mismatch")
	require.NoError(t, DB.First(reservation, reservation.Id).Error)
	assert.Equal(t, BillingReservationStatusManualRequired, reservation.Status)
	require.NoError(t, DB.Model(reservation).Update("last_error", "gift quota is already negative").Error)
	_, err = RequeueManualBillingReservation(requestId, 600)
	require.ErrorContains(t, err, "not parked for legacy insufficient quota")
	require.NoError(t, DB.Model(reservation).Update("last_error", ErrInsufficientUserQuota.Error()).Error)

	startedAt := GetDBTimestamp()
	result, err := RequeueManualBillingReservation(requestId, 600)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 8, result.PreviousAttempts)
	assert.True(t, result.FailureRequeued)
	assert.Equal(t, BillingReservationStatusSettling, result.Reservation.Status)
	assert.Zero(t, result.Reservation.Attempts)
	assert.GreaterOrEqual(t, result.Reservation.ExpiresAt, startedAt)

	require.NoError(t, DB.First(failure, failure.Id).Error)
	assert.Equal(t, BillingSettlementStatusPending, failure.Status)
	assert.Equal(t, BillingReservationStatusSettling, failure.ReservationStatus)
	assert.Zero(t, failure.Attempts)
	assert.Empty(t, failure.LastError)
	assert.GreaterOrEqual(t, failure.NextRetryAt, startedAt)

	_, err = RequeueManualBillingReservation(requestId, 600)
	require.ErrorContains(t, err, "not awaiting manual reconciliation")
}

func TestGetNextBillingFinancialRepairAtUsesEarliestEffectiveDueTime(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&BillingSettlementFailure{
		RequestId:   "next-failure-" + common.GetRandomString(8),
		Delta:       1,
		Status:      BillingSettlementStatusPending,
		NextRetryAt: now + 120,
	}).Error)
	require.NoError(t, DB.Create(&BillingReservation{
		RequestId: "next-lease-" + common.GetRandomString(8),
		UserId:    1,
		Status:    BillingReservationStatusDispatched,
		ExpiresAt: now + 90,
	}).Error)
	require.NoError(t, DB.Create(&BillingReservation{
		RequestId: "next-settlement-" + common.GetRandomString(8),
		UserId:    1,
		Status:    BillingReservationStatusSettling,
		ExpiresAt: now + 30,
	}).Error)

	next, found, err := GetNextBillingFinancialRepairAt()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, now+30+billingReservationSettleGraceSeconds(), next)

	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("status = ?", BillingReservationStatusSettling).
		Update("status", BillingReservationStatusManualRequired).Error)
	next, found, err = GetNextBillingFinancialRepairAt()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, now+90, next)
}

func assertRetryDelayRange(t *testing.T, requestId string, attempt int, retryErr error, base int64) {
	t.Helper()
	delay := billingRetryDelaySeconds(requestId, attempt, retryErr)
	assert.GreaterOrEqual(t, delay, base)
	assert.LessOrEqual(t, delay, base+base/5)
}
