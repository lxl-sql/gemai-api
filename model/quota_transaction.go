package model

import (
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	QuotaBucketRecharge = "recharge"
	QuotaBucketGift     = "gift"
)

const (
	QuotaTransactionTypeTopup           = "topup"
	QuotaTransactionTypeGift            = "gift"
	QuotaTransactionTypeRedemption      = "redemption"
	QuotaTransactionTypeConsumePre      = "consume_pre"
	QuotaTransactionTypeConsumeSettle   = "consume_settle"
	QuotaTransactionTypeRefund          = "refund"
	QuotaTransactionTypeAdminAdjust     = "admin_adjust"
	QuotaTransactionTypeAffTransfer     = "aff_transfer"
	QuotaTransactionTypeSubscriptionBuy = "subscription_buy"
)

const (
	QuotaTransactionSourceSystem = "system"
	QuotaTransactionSourceAdmin  = "admin"
	QuotaTransactionSourceLegacy = "legacy"
)

var (
	ErrInsufficientUserQuota = errors.New("user quota is not enough")
	ErrInvalidQuotaBucket    = errors.New("invalid quota bucket")
	ErrInvalidQuotaMode      = errors.New("invalid quota mode")
)

type quotaTransactionCreateError struct {
	idempotencyKey string
	err            error
}

func (e quotaTransactionCreateError) Error() string {
	return e.err.Error()
}

func (e quotaTransactionCreateError) Unwrap() error {
	return e.err
}

var sqliteQuotaUserLocks sync.Map

func lockSQLiteQuotaUser(userId int) func() {
	if !common.UsingSQLite {
		return func() {}
	}
	lockValue, _ := sqliteQuotaUserLocks.LoadOrStore(userId, &sync.Mutex{})
	mu := lockValue.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

type QuotaTransaction struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id" gorm:"index:idx_quota_tx_user_created,priority:1;index"`
	Type              string `json:"type" gorm:"type:varchar(32);index:idx_quota_tx_type_created,priority:1;default:''"`
	QuotaDelta        int    `json:"quota_delta" gorm:"type:int;default:0"`
	GiftQuotaDelta    int    `json:"gift_quota_delta" gorm:"type:int;default:0"`
	BalanceBefore     int    `json:"balance_before" gorm:"type:int;default:0"`
	GiftBalanceBefore int    `json:"gift_balance_before" gorm:"type:int;default:0"`
	BalanceAfter      int    `json:"balance_after" gorm:"type:int;default:0"`
	GiftBalanceAfter  int    `json:"gift_balance_after" gorm:"type:int;default:0"`
	TotalDelta        int    `json:"total_delta" gorm:"type:int;default:0"`
	Source            string `json:"source" gorm:"type:varchar(64);index:idx_quota_tx_source_created,priority:1;default:''"`
	ReferenceType     string `json:"reference_type" gorm:"type:varchar(64);index:idx_quota_tx_reference,priority:1;default:''"`
	ReferenceId       string `json:"reference_id" gorm:"type:varchar(191);index:idx_quota_tx_reference,priority:2;default:''"`
	RequestId         string `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	IdempotencyKey    string `json:"idempotency_key" gorm:"type:varchar(191);uniqueIndex;default:''"`
	OperatorId        int    `json:"operator_id" gorm:"type:int;default:0"`
	Metadata          string `json:"metadata" gorm:"type:text"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_quota_tx_user_created,priority:2;index:idx_quota_tx_type_created,priority:2;index:idx_quota_tx_source_created,priority:2"`
}

type QuotaTransactionRef struct {
	Type           string
	Source         string
	ReferenceType  string
	ReferenceID    string
	RequestID      string
	IdempotencyKey string
	OperatorID     int
	Metadata       map[string]interface{}
}

type QuotaDelta struct {
	QuotaDelta     int
	GiftQuotaDelta int
}

type QuotaBreakdown struct {
	QuotaDelta        int
	GiftQuotaDelta    int
	QuotaBefore       int
	GiftQuotaBefore   int
	QuotaAfter        int
	GiftQuotaAfter    int
	TransactionID     int
	IdempotencyReused bool
}

func (b QuotaBreakdown) TotalDelta() int {
	return b.QuotaDelta + b.GiftQuotaDelta
}

func normalizeQuotaRef(ref QuotaTransactionRef, defaultType string) QuotaTransactionRef {
	if ref.Type == "" {
		ref.Type = defaultType
	}
	if ref.Source == "" {
		ref.Source = QuotaTransactionSourceLegacy
	}
	if ref.ReferenceType == "" {
		ref.ReferenceType = "manual"
	}
	if ref.IdempotencyKey == "" {
		ref.IdempotencyKey = "quota:" + common.GetUUID()
	}
	return ref
}

func metadataToString(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return ""
	}
	bytes, err := common.Marshal(metadata)
	if err != nil {
		common.SysLog("failed to marshal quota transaction metadata: " + err.Error())
		return ""
	}
	return string(bytes)
}

func quotaBreakdownFromTransaction(txn *QuotaTransaction, reused bool) *QuotaBreakdown {
	if txn == nil {
		return nil
	}
	return &QuotaBreakdown{
		QuotaDelta:        txn.QuotaDelta,
		GiftQuotaDelta:    txn.GiftQuotaDelta,
		QuotaBefore:       txn.BalanceBefore,
		GiftQuotaBefore:   txn.GiftBalanceBefore,
		QuotaAfter:        txn.BalanceAfter,
		GiftQuotaAfter:    txn.GiftBalanceAfter,
		TransactionID:     txn.Id,
		IdempotencyReused: reused,
	}
}

func lockUserForQuotaTx(tx *gorm.DB, userId int) (*User, error) {
	user := &User{}
	if err := LockForUpdate(tx).Where("id = ?", userId).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func getQuotaTransactionByIdempotencyKeyTx(tx *gorm.DB, key string) (*QuotaTransaction, error) {
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	transaction := &QuotaTransaction{}
	if err := tx.Where("idempotency_key = ?", key).First(transaction).Error; err != nil {
		return nil, err
	}
	return transaction, nil
}

func createQuotaTransactionTx(tx *gorm.DB, user *User, quotaDelta int, giftQuotaDelta int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}
	if quotaDelta == 0 && giftQuotaDelta == 0 {
		return &QuotaBreakdown{
			QuotaBefore:     user.Quota,
			GiftQuotaBefore: user.GiftQuota,
			QuotaAfter:      user.Quota,
			GiftQuotaAfter:  user.GiftQuota,
		}, nil
	}
	if existing, err := getQuotaTransactionByIdempotencyKeyTx(tx, ref.IdempotencyKey); err == nil {
		return quotaBreakdownFromTransaction(existing, true), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	quotaBefore := user.Quota
	giftBefore := user.GiftQuota
	quotaAfter := quotaBefore + quotaDelta
	giftAfter := giftBefore + giftQuotaDelta
	if quotaAfter < 0 || giftAfter < 0 {
		return nil, ErrInsufficientUserQuota
	}

	// Use atomic column expressions (defense in depth): even if the row lock is
	// somehow bypassed, concurrent credits/debits won't lose updates. The
	// before/after recorded below reflect the locked-read snapshot.
	if err := tx.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":      gorm.Expr("quota + ?", quotaDelta),
		"gift_quota": gorm.Expr("gift_quota + ?", giftQuotaDelta),
	}).Error; err != nil {
		return nil, err
	}

	transaction := &QuotaTransaction{
		UserId:            user.Id,
		Type:              ref.Type,
		QuotaDelta:        quotaDelta,
		GiftQuotaDelta:    giftQuotaDelta,
		BalanceBefore:     quotaBefore,
		GiftBalanceBefore: giftBefore,
		BalanceAfter:      quotaAfter,
		GiftBalanceAfter:  giftAfter,
		TotalDelta:        quotaDelta + giftQuotaDelta,
		Source:            ref.Source,
		ReferenceType:     ref.ReferenceType,
		ReferenceId:       ref.ReferenceID,
		RequestId:         ref.RequestID,
		IdempotencyKey:    ref.IdempotencyKey,
		OperatorId:        ref.OperatorID,
		Metadata:          metadataToString(ref.Metadata),
		CreatedAt:         common.GetTimestamp(),
	}
	if err := tx.Create(transaction).Error; err != nil {
		// 此时本事务内的余额原子更新已经执行，绝不能在事务内"恢复成功"返回 nil，
		// 否则提交事务会把余额变更重复应用一次（幂等键冲突说明已有并发流水入账）。
		// 返回错误让整个事务回滚，由 withQuotaTransactionForUser 在事务外按幂等键恢复。
		return nil, quotaTransactionCreateError{idempotencyKey: ref.IdempotencyKey, err: err}
	}

	user.Quota = quotaAfter
	user.GiftQuota = giftAfter
	return quotaBreakdownFromTransaction(transaction, false), nil
}

func applyQuotaDeltaNoLedgerTx(tx *gorm.DB, user *User, quotaDelta int, giftQuotaDelta int) (*QuotaBreakdown, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}
	quotaBefore := user.Quota
	giftBefore := user.GiftQuota
	quotaAfter := quotaBefore + quotaDelta
	giftAfter := giftBefore + giftQuotaDelta
	if quotaAfter < 0 || giftAfter < 0 {
		return nil, ErrInsufficientUserQuota
	}
	if quotaDelta != 0 || giftQuotaDelta != 0 {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
			"quota":      gorm.Expr("quota + ?", quotaDelta),
			"gift_quota": gorm.Expr("gift_quota + ?", giftQuotaDelta),
		}).Error; err != nil {
			return nil, err
		}
	}
	user.Quota = quotaAfter
	user.GiftQuota = giftAfter
	return &QuotaBreakdown{
		QuotaDelta:      quotaDelta,
		GiftQuotaDelta:  giftQuotaDelta,
		QuotaBefore:     quotaBefore,
		GiftQuotaBefore: giftBefore,
		QuotaAfter:      quotaAfter,
		GiftQuotaAfter:  giftAfter,
	}, nil
}

func applyQuotaDeltaTx(tx *gorm.DB, userId int, quotaDelta int, giftQuotaDelta int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	return createQuotaTransactionTx(tx, user, quotaDelta, giftQuotaDelta, ref)
}

func withQuotaTransactionForUser(userId int, fn func(tx *gorm.DB) (*QuotaBreakdown, error)) (*QuotaBreakdown, error) {
	var breakdown *QuotaBreakdown
	unlock := lockSQLiteQuotaUser(userId)
	defer unlock()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		breakdown, err = fn(tx)
		return err
	})
	if err != nil {
		var createErr quotaTransactionCreateError
		if errors.As(err, &createErr) && createErr.idempotencyKey != "" {
			if existing, findErr := getQuotaTransactionByIdempotencyKeyTx(DB, createErr.idempotencyKey); findErr == nil {
				return quotaBreakdownFromTransaction(existing, true), nil
			}
		}
		return nil, err
	}
	return breakdown, nil
}

func withQuotaTransaction(fn func(tx *gorm.DB) (*QuotaBreakdown, error)) (*QuotaBreakdown, error) {
	return withQuotaTransactionForUser(0, fn)
}

func withQuotaBalanceForUser(userId int, fn func(tx *gorm.DB) (*QuotaBreakdown, error)) (*QuotaBreakdown, error) {
	var breakdown *QuotaBreakdown
	unlock := lockSQLiteQuotaUser(userId)
	defer unlock()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		breakdown, err = fn(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return breakdown, nil
}

func CreditRechargeQuota(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeTopup)
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		return applyQuotaDeltaTx(tx, userId, amount, 0, ref)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func CreditRechargeQuotaTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeTopup)
	return applyQuotaDeltaTx(tx, userId, amount, 0, ref)
}

func CreditGiftQuota(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeGift)
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		return applyQuotaDeltaTx(tx, userId, 0, amount, ref)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func CreditGiftQuotaTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeGift)
	return applyQuotaDeltaTx(tx, userId, 0, amount, ref)
}

func CreditQuotaBreakdownTx(tx *gorm.DB, userId int, quotaAmount int, giftQuotaAmount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if quotaAmount < 0 || giftQuotaAmount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if quotaAmount == 0 && giftQuotaAmount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeRedemption)
	return applyQuotaDeltaTx(tx, userId, quotaAmount, giftQuotaAmount, ref)
}

func DebitQuotaPreferGift(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeConsumePre)
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		return DebitQuotaPreferGiftTx(tx, userId, amount, ref)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func DebitQuotaPreferGiftTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeConsumePre)
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	if user.TotalQuota() < amount {
		return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, user.TotalQuota(), amount)
	}
	giftDebit := amount
	if user.GiftQuota < giftDebit {
		giftDebit = user.GiftQuota
	}
	rechargeDebit := amount - giftDebit
	return createQuotaTransactionTx(tx, user, -rechargeDebit, -giftDebit, ref)
}

func DebitQuotaPreferGiftNoLedger(userId int, amount int) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	breakdown, err := withQuotaBalanceForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		return DebitQuotaPreferGiftNoLedgerTx(tx, userId, amount)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func DebitQuotaPreferGiftNoLedgerTx(tx *gorm.DB, userId int, amount int) (*QuotaBreakdown, error) {
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	if user.TotalQuota() < amount {
		return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, user.TotalQuota(), amount)
	}
	giftDebit := amount
	if user.GiftQuota < giftDebit {
		giftDebit = user.GiftQuota
	}
	rechargeDebit := amount - giftDebit
	return applyQuotaDeltaNoLedgerTx(tx, user, -rechargeDebit, -giftDebit)
}

func RefundQuotaByBreakdown(userId int, delta QuotaDelta, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if delta.QuotaDelta < 0 || delta.GiftQuotaDelta < 0 {
		return nil, errors.New("refund quota delta cannot be negative")
	}
	if delta.QuotaDelta == 0 && delta.GiftQuotaDelta == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeRefund)
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		return applyQuotaDeltaTx(tx, userId, delta.QuotaDelta, delta.GiftQuotaDelta, ref)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func RefundQuotaByBreakdownNoLedger(userId int, delta QuotaDelta) (*QuotaBreakdown, error) {
	if delta.QuotaDelta < 0 || delta.GiftQuotaDelta < 0 {
		return nil, errors.New("refund quota delta cannot be negative")
	}
	if delta.QuotaDelta == 0 && delta.GiftQuotaDelta == 0 {
		return nil, nil
	}
	breakdown, err := withQuotaBalanceForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		user, err := lockUserForQuotaTx(tx, userId)
		if err != nil {
			return nil, err
		}
		return applyQuotaDeltaNoLedgerTx(tx, user, delta.QuotaDelta, delta.GiftQuotaDelta)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func RefundQuotaByTransaction(originalTransactionID int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if originalTransactionID == 0 {
		return nil, errors.New("original transaction id is empty")
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeRefund)
	// 先在事务外定位原流水，拿到归属用户，确保 SQLite 串行锁锁到正确的用户。
	original := &QuotaTransaction{}
	if err := DB.Where("id = ?", originalTransactionID).First(original).Error; err != nil {
		return nil, err
	}
	userId := original.UserId
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		if ref.Metadata == nil {
			ref.Metadata = map[string]interface{}{}
		}
		ref.Metadata["original_transaction_id"] = originalTransactionID
		return applyQuotaDeltaTx(tx, userId, -original.QuotaDelta, -original.GiftQuotaDelta, ref)
	})
	if err == nil && userId != 0 {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}

func AdjustQuota(userId int, bucket string, mode string, value int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if bucket == "" {
		bucket = QuotaBucketRecharge
	}
	if bucket != QuotaBucketRecharge && bucket != QuotaBucketGift {
		return nil, ErrInvalidQuotaBucket
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeAdminAdjust)
	var quotaDelta int
	var giftDelta int
	breakdown, err := withQuotaTransactionForUser(userId, func(tx *gorm.DB) (*QuotaBreakdown, error) {
		user, err := lockUserForQuotaTx(tx, userId)
		if err != nil {
			return nil, err
		}
		switch mode {
		case "add":
			if value <= 0 {
				return nil, errors.New("quota change value must be positive")
			}
			if bucket == QuotaBucketGift {
				giftDelta = value
			} else {
				quotaDelta = value
			}
		case "subtract":
			if value <= 0 {
				return nil, errors.New("quota change value must be positive")
			}
			if bucket == QuotaBucketGift {
				giftDelta = -value
			} else {
				quotaDelta = -value
			}
		case "override":
			if bucket == QuotaBucketGift {
				giftDelta = value - user.GiftQuota
			} else {
				quotaDelta = value - user.Quota
			}
		default:
			return nil, ErrInvalidQuotaMode
		}
		return createQuotaTransactionTx(tx, user, quotaDelta, giftDelta, ref)
	})
	if err == nil {
		_ = invalidateUserCache(userId)
	}
	return breakdown, err
}
