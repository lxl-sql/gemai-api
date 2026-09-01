//go:build integration

package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestBillingDebtMySQLLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("BILLING_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("BILLING_TEST_MYSQL_DSN is not configured")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	require.NoError(t, err)
	require.NotEmpty(t, config.DBName, "BILLING_TEST_MYSQL_DSN must name an existing administrative database")

	adminDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := adminDB.DB()
	require.NoError(t, err)
	require.NoError(t, adminSQL.Ping())
	databaseName := "billing_debt_test_" + strings.ToLower(strings.ReplaceAll(common.GetUUID(), "-", ""))
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4", databaseName)).Error)
	t.Cleanup(func() {
		if dropErr := adminDB.Exec(fmt.Sprintf("DROP DATABASE `%s`", databaseName)).Error; dropErr != nil {
			t.Errorf("drop MySQL billing test database: %v", dropErr)
		}
		_ = adminSQL.Close()
	})

	config.DBName = databaseName
	isolatedDB, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{})
	require.NoError(t, err)
	isolatedSQL, err := isolatedDB.DB()
	require.NoError(t, err)
	require.NoError(t, isolatedSQL.Ping())

	previousDB := DB
	previousLogDB := LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	DB = isolatedDB
	LOG_DB = isolatedDB
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		initCol()
		_ = isolatedSQL.Close()
	})

	require.NoError(t, isolatedDB.AutoMigrate(
		&User{},
		&Token{},
		&QuotaTransaction{},
		&BillingReservation{},
		&BillingSettlementFailure{},
	))

	user := &User{
		Username:  "mysql-debt-user-" + common.GetRandomString(8),
		Password:  "test-password",
		AffCode:   common.GetRandomString(16),
		Quota:     350,
		GiftQuota: 150,
		Status:    1,
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         common.GetRandomString(32),
		Name:        "mysql-debt-token",
		Status:      1,
		RemainQuota: 500,
	}
	require.NoError(t, DB.Create(token).Error)
	requestId := "mysql-debt-" + common.GetRandomString(8)
	_, err = CreateBillingReservation(BillingReservationCreateInput{
		RequestId:     requestId,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		BillingSource: BillingReservationSourceWallet,
		Quota:         400,
		LeaseSeconds:  60,
	})
	require.NoError(t, err)
	_, err = FinalizeBillingReservation(requestId, 700, BillingReservationStatusSettling)
	require.NoError(t, err)
	require.NoError(t, DB.First(user, user.Id).Error)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, -200, user.Quota)
	assert.Zero(t, user.GiftQuota)
	assert.Equal(t, -200, token.RemainQuota)
	assert.Equal(t, 700, token.UsedQuota)

	var ledgerCount int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("idempotency_key = ?", "billing:settle:"+requestId).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)
	_, err = FinalizeBillingReservation(requestId, 700, BillingReservationStatusSettling)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("idempotency_key = ?", "billing:settle:"+requestId).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)

	_, err = CreditRechargeQuota(user.Id, 50, QuotaTransactionRef{IdempotencyKey: "mysql-debt-credit:" + requestId})
	require.NoError(t, err)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Equal(t, -150, user.Quota)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("gift_quota", 500).Error)
	_, err = DebitQuotaPreferGift(user.Id, 1, QuotaTransactionRef{IdempotencyKey: "mysql-debt-blocked:" + requestId})
	require.ErrorIs(t, err, ErrInsufficientUserQuota)
}
