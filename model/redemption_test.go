package model

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupRedemptionTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	oldLogDB := LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	initCol()

	dbPath := filepath.Join(t.TempDir(), "redemption-test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	DB = db
	LOG_DB = db

	if err := db.AutoMigrate(&User{}, &Redemption{}, &QuotaTransaction{}, &Option{}, &Log{}); err != nil {
		t.Fatalf("auto migrate test db: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		DB = oldDB
		LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		initCol()
	})
}

func TestRedeemConcurrentSingleWinner(t *testing.T) {
	setupRedemptionTestDB(t)

	const key = "concurrent-redemption-key"
	const giftQuota = 100
	users := []User{
		{Username: "redeem-user-1", Password: "password123", AffCode: "redeem-aff-1"},
		{Username: "redeem-user-2", Password: "password123", AffCode: "redeem-aff-2"},
		{Username: "redeem-user-3", Password: "password123", AffCode: "redeem-aff-3"},
		{Username: "redeem-user-4", Password: "password123", AffCode: "redeem-aff-4"},
	}
	if err := DB.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	redemption := Redemption{
		UserId:      users[0].Id,
		Name:        "concurrent",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		GiftQuota:   giftQuota,
		CreatedTime: common.GetTimestamp(),
	}
	if err := DB.Create(&redemption).Error; err != nil {
		t.Fatalf("create redemption: %v", err)
	}

	const attempts = 24
	var wg sync.WaitGroup
	successes := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		userID := users[i%len(users)].Id
		go func() {
			defer wg.Done()
			if _, err := Redeem(key, userID); err == nil {
				successes <- userID
			}
		}()
	}
	wg.Wait()
	close(successes)

	winnerCount := 0
	winnerID := 0
	for id := range successes {
		winnerCount++
		winnerID = id
	}
	if winnerCount != 1 {
		t.Fatalf("expected exactly one successful redemption, got %d", winnerCount)
	}

	var updated Redemption
	if err := DB.First(&updated, redemption.Id).Error; err != nil {
		t.Fatalf("load redemption: %v", err)
	}
	if updated.Status != common.RedemptionCodeStatusUsed {
		t.Fatalf("expected redemption status used, got %d", updated.Status)
	}
	if updated.UsedUserId != winnerID {
		t.Fatalf("expected used_user_id %d, got %d", winnerID, updated.UsedUserId)
	}

	var txCount int64
	if err := DB.Model(&QuotaTransaction{}).Where("reference_type = ? AND reference_id = ?", "redemption", strconv.Itoa(redemption.Id)).Count(&txCount).Error; err != nil {
		t.Fatalf("count quota transactions: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("expected one quota transaction, got %d", txCount)
	}

	var totalGiftQuota int
	if err := DB.Model(&User{}).Select("COALESCE(SUM(gift_quota), 0)").Scan(&totalGiftQuota).Error; err != nil {
		t.Fatalf("sum gift quota: %v", err)
	}
	if totalGiftQuota != giftQuota {
		t.Fatalf("expected total gift quota %d, got %d", giftQuota, totalGiftQuota)
	}
}

func TestMigrateRedemptionQuotaSplitOnlyOldFormatAndIdempotent(t *testing.T) {
	setupRedemptionTestDB(t)

	redemptions := []Redemption{
		{Name: "old", Key: "old-format", Status: common.RedemptionCodeStatusEnabled, Quota: 100, GiftQuota: 0},
		{Name: "mixed", Key: "mixed-format", Status: common.RedemptionCodeStatusEnabled, Quota: 200, GiftQuota: 50},
		{Name: "gift", Key: "gift-only", Status: common.RedemptionCodeStatusEnabled, Quota: 0, GiftQuota: 30},
		{Name: "invalid", Key: "invalid-negative", Status: common.RedemptionCodeStatusEnabled, Quota: -10, GiftQuota: 0},
	}
	if err := DB.Create(&redemptions).Error; err != nil {
		t.Fatalf("create redemptions: %v", err)
	}

	if err := migrateRedemptionQuotaSplit(); err != nil {
		t.Fatalf("first migrate redemption quota split: %v", err)
	}
	if err := migrateRedemptionQuotaSplit(); err != nil {
		t.Fatalf("second migrate redemption quota split: %v", err)
	}

	var old Redemption
	if err := DB.First(&old, "key = ?", "old-format").Error; err != nil {
		t.Fatalf("load old redemption: %v", err)
	}
	if old.Quota != 0 || old.GiftQuota != 100 {
		t.Fatalf("expected old format migrated to gift quota, got quota=%d gift_quota=%d", old.Quota, old.GiftQuota)
	}

	var mixed Redemption
	if err := DB.First(&mixed, "key = ?", "mixed-format").Error; err != nil {
		t.Fatalf("load mixed redemption: %v", err)
	}
	if mixed.Quota != 200 || mixed.GiftQuota != 50 {
		t.Fatalf("expected mixed format unchanged, got quota=%d gift_quota=%d", mixed.Quota, mixed.GiftQuota)
	}

	var invalid Redemption
	if err := DB.First(&invalid, "key = ?", "invalid-negative").Error; err != nil {
		t.Fatalf("load invalid redemption: %v", err)
	}
	if invalid.Quota != -10 || invalid.GiftQuota != 0 {
		t.Fatalf("expected negative legacy quota unchanged, got quota=%d gift_quota=%d", invalid.Quota, invalid.GiftQuota)
	}
}
