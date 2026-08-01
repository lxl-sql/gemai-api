package model

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupQuotaTestUser(t *testing.T, quota int, giftQuota int) *User {
	t.Helper()
	user := &User{
		Username:  fmt.Sprintf("quota-tx-user-%s", t.Name()),
		Password:  "password123",
		AffCode:   fmt.Sprintf("qt-%s", t.Name()),
		Quota:     quota,
		GiftQuota: giftQuota,
	}
	require.NoError(t, DB.Create(user).Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM quota_transactions")
		DB.Exec("DELETE FROM users")
	})
	return user
}

func reloadQuotaTestUser(t *testing.T, id int) *User {
	t.Helper()
	user := &User{}
	require.NoError(t, DB.Where("id = ?", id).First(user).Error)
	return user
}

func TestDebitQuotaPreferGiftSplit(t *testing.T) {
	// 赠送额度足够：全部从赠送扣
	recharge, gift := debitQuotaPreferGiftSplit(500, 200)
	assert.Equal(t, 0, recharge)
	assert.Equal(t, 200, gift)

	// 赠送额度不足：先扣完赠送，剩余从充值扣
	recharge, gift = debitQuotaPreferGiftSplit(150, 200)
	assert.Equal(t, 50, recharge)
	assert.Equal(t, 150, gift)

	// 无赠送额度
	recharge, gift = debitQuotaPreferGiftSplit(0, 200)
	assert.Equal(t, 200, recharge)
	assert.Equal(t, 0, gift)

	// 赠送额度为负（历史脏数据）：与悲观锁路径保持一致的语义
	recharge, gift = debitQuotaPreferGiftSplit(-30, 200)
	assert.Equal(t, 230, recharge)
	assert.Equal(t, -30, gift)
}

func TestDebitQuotaPreferGiftConcurrent(t *testing.T) {
	truncateTables(t)
	const goroutines = 30
	const perDebit = 50
	// 总额 1500 = 30 × 50，恰好扣光
	user := setupQuotaTestUser(t, 1000, 500)

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = DebitQuotaPreferGift(user.Id, perDebit, QuotaTransactionRef{
				IdempotencyKey: fmt.Sprintf("test:concurrent:%d", idx),
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "debit %d should succeed", i)
	}

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, 0, reloaded.Quota, "recharge quota should be fully consumed")
	assert.Equal(t, 0, reloaded.GiftQuota, "gift quota should be fully consumed")

	var ledgerCount int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("user_id = ?", user.Id).Count(&ledgerCount).Error)
	assert.Equal(t, int64(goroutines), ledgerCount)

	// 流水链校验：所有 delta 之和等于总扣减
	var totalDelta int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("user_id = ?", user.Id).
		Select("COALESCE(SUM(total_delta), 0)").Scan(&totalDelta).Error)
	assert.Equal(t, int64(-goroutines*perDebit), totalDelta)
}

func TestDebitQuotaInsufficient(t *testing.T) {
	truncateTables(t)
	user := setupQuotaTestUser(t, 100, 50)

	_, err := DebitQuotaPreferGift(user.Id, 200, QuotaTransactionRef{
		IdempotencyKey: "test:insufficient:1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInsufficientUserQuota))

	// 余额不应被修改，且不应留下流水
	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, 100, reloaded.Quota)
	assert.Equal(t, 50, reloaded.GiftQuota)
	var ledgerCount int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("user_id = ?", user.Id).Count(&ledgerCount).Error)
	assert.Equal(t, int64(0), ledgerCount)
}

func TestCreditQuotaAllowsBalanceBeyondInt32(t *testing.T) {
	truncateTables(t)
	user := setupQuotaTestUser(t, math.MaxInt32, 0)

	_, err := CreditRechargeQuota(user.Id, 1, QuotaTransactionRef{
		IdempotencyKey: "test:credit:beyond-int32",
	})
	require.NoError(t, err)

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, int64(math.MaxInt32)+1, int64(reloaded.Quota))
	var ledgerCount int64
	require.NoError(t, DB.Model(&QuotaTransaction{}).Where("user_id = ?", user.Id).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)
}

func TestCreditQuotaBreakdownAllowsTransactionDeltaBeyondInt32(t *testing.T) {
	truncateTables(t)
	user := setupQuotaTestUser(t, 0, 0)

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, creditErr := CreditQuotaBreakdownTx(
			tx,
			user.Id,
			math.MaxInt32,
			1,
			QuotaTransactionRef{IdempotencyKey: "test:credit:total-delta-beyond-int32"},
		)
		return creditErr
	})
	require.NoError(t, err)

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, math.MaxInt32, reloaded.Quota)
	assert.Equal(t, 1, reloaded.GiftQuota)
}

func TestDebitQuotaAllowsBalanceBeyondInt32(t *testing.T) {
	truncateTables(t)
	// 模拟生产高余额账户：余额远超 int32 时扣费不得被守卫误拒
	user := setupQuotaTestUser(t, int(math.MaxInt32)*2, 0)

	_, err := DebitQuotaPreferGift(user.Id, 100, QuotaTransactionRef{
		IdempotencyKey: "test:debit:beyond-int32",
	})
	require.NoError(t, err)

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, int64(math.MaxInt32)*2-100, int64(reloaded.Quota))
}

func TestQuotaTransactionDeltaOverflows(t *testing.T) {
	assert.False(t, quotaTransactionDeltaOverflows(math.MaxInt64, 0))
	assert.False(t, quotaTransactionDeltaOverflows(math.MaxInt64, -1))
	assert.False(t, quotaTransactionDeltaOverflows(math.MinInt64, 1))
	assert.True(t, quotaTransactionDeltaOverflows(math.MaxInt64, 1))
	assert.True(t, quotaTransactionDeltaOverflows(math.MinInt64, -1))
}

func TestDebitQuotaIdempotentReplay(t *testing.T) {
	truncateTables(t)
	user := setupQuotaTestUser(t, 1000, 0)
	ref := QuotaTransactionRef{IdempotencyKey: "test:idempotent:1"}

	first, err := DebitQuotaPreferGift(user.Id, 300, ref)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.False(t, first.IdempotencyReused)
	assert.Equal(t, 700, first.QuotaAfter)

	// 相同幂等键重放：返回原流水，不重复扣费
	second, err := DebitQuotaPreferGift(user.Id, 300, ref)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.True(t, second.IdempotencyReused)
	assert.Equal(t, first.TransactionID, second.TransactionID)

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, 700, reloaded.Quota, "balance should be debited exactly once")
}

func TestDebitThenRefundRestoresBalance(t *testing.T) {
	truncateTables(t)
	user := setupQuotaTestUser(t, 800, 200)

	breakdown, err := DebitQuotaPreferGift(user.Id, 500, QuotaTransactionRef{
		IdempotencyKey: "test:refund:debit",
	})
	require.NoError(t, err)
	// 赠送优先：扣 200 赠送 + 300 充值
	assert.Equal(t, -300, breakdown.QuotaDelta)
	assert.Equal(t, -200, breakdown.GiftQuotaDelta)

	_, err = RefundQuotaByBreakdown(user.Id, QuotaDelta{
		QuotaDelta:     -breakdown.QuotaDelta,
		GiftQuotaDelta: -breakdown.GiftQuotaDelta,
	}, QuotaTransactionRef{IdempotencyKey: "test:refund:refund"})
	require.NoError(t, err)

	reloaded := reloadQuotaTestUser(t, user.Id)
	assert.Equal(t, 800, reloaded.Quota)
	assert.Equal(t, 200, reloaded.GiftQuota)
}

func TestLockQuotaUserRecycled(t *testing.T) {
	// 闸门锁在无等待者后应从锁池中回收，避免随用户数无界增长
	unlock := lockQuotaUser(424242)
	quotaUserLocksMu.Lock()
	_, exists := quotaUserLocks[424242]
	quotaUserLocksMu.Unlock()
	assert.True(t, exists)

	unlock()
	quotaUserLocksMu.Lock()
	_, exists = quotaUserLocks[424242]
	quotaUserLocksMu.Unlock()
	assert.False(t, exists, "lock entry should be recycled after release")

	// 并发获取同一用户锁：串行执行且最终回收
	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := lockQuotaUser(424242)
			counter++ // 受锁保护，无需原子操作
			release()
		}()
	}
	wg.Wait()
	assert.Equal(t, 20, counter)
	quotaUserLocksMu.Lock()
	_, exists = quotaUserLocks[424242]
	quotaUserLocksMu.Unlock()
	assert.False(t, exists)
}
