package model

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
)

type billingRetryClass int

const (
	billingRetryClassGeneric billingRetryClass = iota
	billingRetryClassTransient
	billingRetryClassInsufficientQuota
)

func billingRetryDelaySeconds(requestId string, attempt int, retryErr error) int64 {
	if attempt < 1 {
		attempt = 1
	}
	retryClass := classifyBillingRetryError(retryErr)
	var delays []int64
	switch retryClass {
	case billingRetryClassTransient:
		delays = []int64{15, 30, 60, 120, 300}
	case billingRetryClassInsufficientQuota:
		// Balance shortfalls need time for a recharge, not rapid database churn.
		delays = []int64{60, 300, 900}
	default:
		delays = []int64{60, 120, 300, 600, 900}
	}
	index := attempt - 1
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := delays[index]
	if retryClass == billingRetryClassInsufficientQuota {
		return delay
	}
	return delay + billingRetryJitterSeconds(requestId, attempt, delay)
}

func classifyBillingRetryError(retryErr error) billingRetryClass {
	if errors.Is(retryErr, ErrInsufficientUserQuota) {
		return billingRetryClassInsufficientQuota
	}
	if errors.Is(retryErr, context.DeadlineExceeded) || errors.Is(retryErr, context.Canceled) {
		return billingRetryClassTransient
	}
	if retryErr == nil {
		return billingRetryClassGeneric
	}
	message := strings.ToLower(retryErr.Error())
	for _, marker := range []string{
		"timeout",
		"timed out",
		"deadline exceeded",
		"connection refused",
		"connection reset",
		"broken pipe",
		"too many clients",
		"temporarily unavailable",
		"deadlock",
		"lock not available",
		"sqlstate 40001",
		"sqlstate 40p01",
		"sqlstate 55p03",
		"sqlstate 57014",
	} {
		if strings.Contains(message, marker) {
			return billingRetryClassTransient
		}
	}
	return billingRetryClassGeneric
}

func billingRetryJitterSeconds(requestId string, attempt int, delay int64) int64 {
	maximum := delay / 5
	if maximum <= 0 {
		return 0
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(requestId))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.Itoa(attempt)))
	return int64(hash.Sum32() % uint32(maximum+1))
}

// GetNextBillingFinancialRepairAt returns the earliest persisted time at which
// a settlement failure or financial reservation becomes eligible for repair.
// Audit-only receipts remain on the low-frequency fallback path.
func GetNextBillingFinancialRepairAt() (int64, bool, error) {
	next := int64(0)
	consider := func(candidate sql.NullInt64, offset int64) {
		if !candidate.Valid {
			return
		}
		value := candidate.Int64 + offset
		if next == 0 || value < next {
			next = value
		}
	}

	var failureNext sql.NullInt64
	err := DB.Model(&BillingSettlementFailure{}).
		Select("MIN(next_retry_at)").
		Where("status = ? AND (delta != 0 OR reservation_managed = ?)", BillingSettlementStatusPending, true).
		Row().
		Scan(&failureNext)
	if err != nil {
		return 0, false, err
	}
	consider(failureNext, 0)

	var leaseNext sql.NullInt64
	err = DB.Model(&BillingReservation{}).
		Select("MIN(expires_at)").
		Where("status IN ?", []string{BillingReservationStatusReserved, BillingReservationStatusDispatched}).
		Row().
		Scan(&leaseNext)
	if err != nil {
		return 0, false, err
	}
	consider(leaseNext, 0)

	var settlementNext sql.NullInt64
	err = DB.Model(&BillingReservation{}).
		Select("MIN(expires_at)").
		Where("status IN ?", []string{BillingReservationStatusSettling, BillingReservationStatusRefunding}).
		Row().
		Scan(&settlementNext)
	if err != nil {
		return 0, false, err
	}
	consider(settlementNext, billingReservationSettleGraceSeconds())
	return next, next != 0, nil
}
