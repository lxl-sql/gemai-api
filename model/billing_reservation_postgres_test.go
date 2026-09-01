//go:build integration

package model

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBillingReservationPostgreSQLLifecycle(t *testing.T) {
	dsn := os.Getenv("BILLING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BILLING_TEST_POSTGRES_DSN is not configured")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQLDB, err := adminDB.DB()
	require.NoError(t, err)
	require.NoError(t, adminSQLDB.Ping())
	schema := "billing_test_" + strings.ToLower(strings.ReplaceAll(common.GetUUID(), "-", ""))
	require.NoError(t, adminDB.Exec(fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)).Error)
	t.Cleanup(func() {
		require.NoError(t, adminDB.Exec(fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)).Error)
		require.NoError(t, adminSQLDB.Close())
	})
	isolatedDSN := dsn
	if parsed, parseErr := url.Parse(dsn); parseErr == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		isolatedDSN = parsed.String()
	} else {
		isolatedDSN += " search_path=" + schema
	}
	postgresDB, err := gorm.Open(postgres.Open(isolatedDSN), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := postgresDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping())

	previousDB := DB
	previousLogDB := LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	DB = postgresDB
	LOG_DB = postgresDB
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, postgresDB.AutoMigrate(&billingSettlementFailureLegacy{}))
	legacyFailure := &billingSettlementFailureLegacy{
		RequestId: "postgres-legacy-retry-" + common.GetRandomString(8),
		Delta:     1,
		Status:    BillingSettlementStatusPending,
		CreatedAt: GetDBTimestamp() - 60,
		UpdatedAt: GetDBTimestamp() - 60,
	}
	require.NoError(t, postgresDB.Create(legacyFailure).Error)
	migrationModels := []interface{}{
		&User{},
		&Token{},
		&QuotaTransaction{},
		&BillingReservation{},
		&BillingSettlementFailure{},
		&BillingAuditMarker{},
		&Log{},
	}
	require.NoError(t, postgresDB.AutoMigrate(migrationModels...))
	require.NoError(t, postgresDB.AutoMigrate(migrationModels...))
	assert.True(t, postgresDB.Migrator().HasColumn(&BillingSettlementFailure{}, "next_retry_at"))
	assert.True(t, postgresDB.Migrator().HasIndex(&BillingReservation{}, "idx_billing_reservation_due"))
	assert.True(t, postgresDB.Migrator().HasIndex(&BillingReservation{}, "idx_billing_reservation_audit"))
	assert.True(t, postgresDB.Migrator().HasIndex(&BillingSettlementFailure{}, "idx_billing_settle_status_retry"))
	var upgradedLegacy BillingSettlementFailure
	require.NoError(t, DB.Where("request_id = ?", legacyFailure.RequestId).First(&upgradedLegacy).Error)
	assert.Zero(t, upgradedLegacy.NextRetryAt)
	legacyPending, err := FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	require.Len(t, legacyPending, 1)
	assert.Equal(t, legacyFailure.RequestId, legacyPending[0].RequestId)
	require.NoError(t, MarkBillingSettlementFailureSettled(upgradedLegacy.Id))

	manualReservation := &BillingReservation{
		RequestId:     "postgres-manual-required-" + common.GetRandomString(8),
		UserId:        1,
		BillingSource: BillingReservationSourceWallet,
		ReservedQuota: 400,
		DesiredQuota:  600,
		Status:        BillingReservationStatusSettling,
		Attempts:      7,
		ExpiresAt:     1,
	}
	require.NoError(t, DB.Create(manualReservation).Error)
	manualFailure := &BillingSettlementFailure{
		RequestId:          manualReservation.RequestId,
		UserId:             manualReservation.UserId,
		ActualQuota:        manualReservation.DesiredQuota,
		PreConsumedQuota:   manualReservation.ReservedQuota,
		Delta:              manualReservation.DesiredQuota - manualReservation.ReservedQuota,
		ReservationManaged: true,
		ReservationStatus:  BillingReservationStatusSettling,
		Status:             BillingSettlementStatusPending,
		Attempts:           7,
		UpdatedAt:          GetDBTimestamp(),
	}
	require.NoError(t, DB.Create(manualFailure).Error)
	marked, err := MarkBillingReservationManualRequired(manualReservation.Id, ErrInsufficientUserQuota)
	require.NoError(t, err)
	require.True(t, marked)
	require.NoError(t, DB.First(manualReservation, manualReservation.Id).Error)
	assert.Equal(t, BillingReservationStatusManualRequired, manualReservation.Status)
	assert.Equal(t, 8, manualReservation.Attempts)
	assert.Equal(t, 400, manualReservation.ReservedQuota)
	assert.Equal(t, 600, manualReservation.DesiredQuota)
	assert.Contains(t, manualReservation.LastError, ErrInsufficientUserQuota.Error())
	require.NoError(t, DB.First(manualFailure, manualFailure.Id).Error)
	assert.Equal(t, BillingSettlementStatusManualRequired, manualFailure.Status)
	assert.Equal(t, 8, manualFailure.Attempts)
	assert.Zero(t, manualFailure.NextRetryAt)
	assert.Contains(t, manualFailure.LastError, ErrInsufficientUserQuota.Error())
	_, err = RequeueManualBillingReservation(manualReservation.RequestId, 599)
	require.ErrorContains(t, err, "confirmation mismatch")
	requeued, err := RequeueManualBillingReservation(manualReservation.RequestId, 600)
	require.NoError(t, err)
	require.True(t, requeued.FailureRequeued)
	assert.Equal(t, BillingReservationStatusSettling, requeued.Reservation.Status)
	require.NoError(t, DB.First(manualFailure, manualFailure.Id).Error)
	assert.Equal(t, BillingSettlementStatusPending, manualFailure.Status)
	require.NoError(t, DB.Delete(manualReservation).Error)
	require.NoError(t, DB.Delete(manualFailure).Error)

	adaptiveFailure := &BillingSettlementFailure{
		RequestId: "postgres-adaptive-retry-" + common.GetRandomString(8),
		Delta:     1,
		Status:    BillingSettlementStatusPending,
	}
	require.NoError(t, DB.Create(adaptiveFailure).Error)
	adaptiveStart := GetDBTimestamp()
	require.NoError(t, MarkBillingSettlementFailureAttempt(adaptiveFailure.Id, context.DeadlineExceeded))
	require.NoError(t, DB.First(adaptiveFailure, adaptiveFailure.Id).Error)
	assert.Equal(t, 1, adaptiveFailure.Attempts)
	assert.GreaterOrEqual(t, adaptiveFailure.NextRetryAt, adaptiveStart+15)
	assert.LessOrEqual(t, adaptiveFailure.NextRetryAt, GetDBTimestamp()+18)
	require.NoError(t, MarkBillingSettlementFailureSettled(adaptiveFailure.Id))

	retryFailure := &BillingSettlementFailure{
		RequestId:   "postgres-retry-backoff-" + common.GetRandomString(8),
		Delta:       1,
		Status:      BillingSettlementStatusPending,
		Attempts:    8,
		NextRetryAt: GetDBTimestamp() - 1,
	}
	require.NoError(t, DB.Create(retryFailure).Error)
	pendingFailures, err := FindPendingBillingSettlementFailures(10)
	require.NoError(t, err)
	require.Len(t, pendingFailures, 1)
	assert.Equal(t, retryFailure.RequestId, pendingFailures[0].RequestId)

	largeBalanceUser := &User{
		Username:  "postgres-large-balance-" + common.GetRandomString(8),
		Password:  "test-password",
		AffCode:   common.GetRandomString(16),
		Quota:     math.MaxInt32,
		GiftQuota: math.MaxInt32,
		Status:    1,
	}
	require.NoError(t, DB.Create(largeBalanceUser).Error)
	totalQuota, err := GetUserQuota(largeBalanceUser.Id, true)
	require.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt32)*2, int64(totalQuota))

	debtUser := &User{
		Username:  "postgres-debt-user-" + common.GetRandomString(8),
		Password:  "test-password",
		AffCode:   common.GetRandomString(16),
		Quota:     350,
		GiftQuota: 150,
		Status:    1,
	}
	require.NoError(t, DB.Create(debtUser).Error)
	debtToken := &Token{
		UserId:      debtUser.Id,
		Key:         common.GetRandomString(32),
		Name:        "postgres-debt-token",
		Status:      1,
		RemainQuota: 500,
	}
	require.NoError(t, DB.Create(debtToken).Error)
	debtRequestId := "postgres-debt-" + common.GetRandomString(8)
	_, err = CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     debtRequestId,
		UserId:        debtUser.Id,
		TokenId:       debtToken.Id,
		TokenKey:      debtToken.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	_, err = FinalizeBillingReservation(debtRequestId, 700, BillingReservationStatusSettling)
	require.NoError(t, err)
	require.NoError(t, DB.First(debtUser, debtUser.Id).Error)
	require.NoError(t, DB.First(debtToken, debtToken.Id).Error)
	assert.Equal(t, -200, debtUser.Quota)
	assert.Zero(t, debtUser.GiftQuota)
	assert.Equal(t, -200, debtToken.RemainQuota)
	assert.Equal(t, 700, debtToken.UsedQuota)
	var settleLedgerCount int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).
		Where("idempotency_key = ?", "billing:settle:"+debtRequestId).
		Count(&settleLedgerCount).Error)
	assert.Equal(t, int64(1), settleLedgerCount)
	_, err = CreditRechargeQuota(debtUser.Id, 50, QuotaTransactionRef{IdempotencyKey: "postgres-debt-credit:" + debtRequestId})
	require.NoError(t, err)
	require.NoError(t, DB.First(debtUser, debtUser.Id).Error)
	assert.Equal(t, -150, debtUser.Quota)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", debtUser.Id).Update("gift_quota", 500).Error)
	_, err = DebitQuotaPreferGift(debtUser.Id, 1, QuotaTransactionRef{IdempotencyKey: "postgres-debt-blocked:" + debtRequestId})
	require.ErrorIs(t, err, ErrInsufficientUserQuota)
	require.NoError(t, AcknowledgeBillingReservation(debtRequestId))

	user := &User{
		Username: "postgres-billing-user-" + common.GetRandomString(8),
		Password: "test-password",
		AffCode:  common.GetRandomString(16),
		Quota:    2000,
		Status:   1,
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "postgres-billing-token",
		Status:      1,
		RemainQuota: 2000,
	}
	require.NoError(t, DB.Create(token).Error)

	dispatchedRequestId := "postgres-dispatched-" + common.GetRandomString(8)
	input := BillingReservationCreateInput{
		RequestId:     dispatchedRequestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         500,
		LeaseSeconds:  60,
	}
	created, err := CreateBillingReservation(input)
	require.NoError(t, err)
	assert.False(t, created.Reused)
	reused, err := CreateBillingReservation(input)
	require.NoError(t, err)
	assert.True(t, reused.Reused)

	require.NoError(t, MarkBillingReservationDispatched(dispatchedRequestId, 60, 7))
	require.NoError(t, DB.Model(&BillingReservation{}).
		Where("request_id = ?", dispatchedRequestId).
		Update("expires_at", GetDBTimestamp()-1).Error)
	repaired, err := RepairExpiredBillingReservation(dispatchedRequestId)
	require.NoError(t, err)
	assert.True(t, repaired)
	require.NoError(t, AcknowledgeBillingReservation(dispatchedRequestId))

	reservedRequestId := "postgres-reserved-" + common.GetRandomString(8)
	_, err = CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     reservedRequestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         300,
		ExpiresAt:     GetDBTimestamp() - 1,
	})
	require.NoError(t, err)
	repaired, err = RepairExpiredBillingReservation(reservedRequestId)
	require.NoError(t, err)
	assert.True(t, repaired)

	require.NoError(t, DB.First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 1500, user.Quota)
	assert.Equal(t, 1500, token.RemainQuota)
	assert.Equal(t, 500, token.UsedQuota)

	softDeletedRequestId := "postgres-soft-deleted-" + common.GetRandomString(8)
	_, err = CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     softDeletedRequestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         200,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Delete(user).Error)
	_, err = FinalizeBillingReservation(softDeletedRequestId, 0, BillingReservationStatusRefunding)
	require.NoError(t, err)
	require.NoError(t, AcknowledgeBillingReservation(softDeletedRequestId))
	require.NoError(t, DB.Unscoped().First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 1500, user.Quota)
	assert.Equal(t, 1500, token.RemainQuota)
	assert.Equal(t, 500, token.UsedQuota)

	var activeReservations int64
	require.NoError(t, DB.Model(&BillingReservation{}).Count(&activeReservations).Error)
	assert.Zero(t, activeReservations)
}

type billingSettlementFailureLegacy struct {
	Id                 int64  `gorm:"primaryKey"`
	RequestId          string `gorm:"type:varchar(64);uniqueIndex"`
	Delta              int
	ReservationManaged bool
	Status             string `gorm:"type:varchar(32);index"`
	Attempts           int
	LastError          string
	CreatedAt          int64
	UpdatedAt          int64
}

func (billingSettlementFailureLegacy) TableName() string {
	return "billing_settlement_failures"
}
