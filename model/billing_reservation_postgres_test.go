//go:build integration

package model

import (
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

	require.NoError(t, postgresDB.AutoMigrate(
		&User{},
		&Token{},
		&BillingReservation{},
		&BillingSettlementFailure{},
		&BillingAuditMarker{},
		&Log{},
	))
	assert.True(t, postgresDB.Migrator().HasIndex(&BillingReservation{}, "idx_billing_reservation_due"))
	assert.True(t, postgresDB.Migrator().HasIndex(&BillingReservation{}, "idx_billing_reservation_audit"))

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
