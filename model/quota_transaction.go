package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

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

// quotaTransactionDeltaOverflows 仅拦截会导致 int64 溢出的变更量。
// 余额/流水列在数据库中为 bigint，合法的余额与单次调整可以超过 int32
// （例如管理员大额调整、历史高余额账户），不应被 int32 边界误拒。
func quotaTransactionDeltaOverflows(quotaDelta int, giftQuotaDelta int) bool {
	if quotaDelta > 0 && giftQuotaDelta > 0 {
		return quotaDelta > math.MaxInt64-giftQuotaDelta
	}
	if quotaDelta < 0 && giftQuotaDelta < 0 {
		return quotaDelta < math.MinInt64-giftQuotaDelta
	}
	return false
}

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

// quotaUserLockEntry 带引用计数的用户级互斥锁；无等待者时从 map 中删除，
// 避免锁池随历史用户数无界增长。
type quotaUserLockEntry struct {
	semaphore chan struct{}
	refs      int
}

var (
	quotaUserLocksMu sync.Mutex
	quotaUserLocks   = make(map[int]*quotaUserLockEntry)
)

// acquireQuotaUser 将同一实例内同一用户的额度事务串行化（对所有数据库生效）。
//
// 目的：热点用户的高并发扣费如果直接打到数据库，每个等待者都会占用一条数据库
// 连接在行锁上排队（历史上曾把 users 表行锁队列打爆并阻塞 autovacuum）。
// 在进程内先串行化后，数据库端每个用户行的锁等待者最多为实例数个。
//
// userId 为 0 表示无用户上下文（如部分兑换流程），SQLite 之外无需串行化。
func acquireQuotaUser(ctx context.Context, userId int) (func(), error) {
	if userId == 0 && !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return func() {}, nil
	}
	quotaUserLocksMu.Lock()
	entry, ok := quotaUserLocks[userId]
	if !ok {
		entry = &quotaUserLockEntry{semaphore: make(chan struct{}, 1)}
		entry.semaphore <- struct{}{}
		quotaUserLocks[userId] = entry
	}
	entry.refs++
	quotaUserLocksMu.Unlock()

	select {
	case <-ctx.Done():
		quotaUserLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(quotaUserLocks, userId)
		}
		quotaUserLocksMu.Unlock()
		return nil, ctx.Err()
	case <-entry.semaphore:
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			entry.semaphore <- struct{}{}
			quotaUserLocksMu.Lock()
			entry.refs--
			if entry.refs == 0 {
				delete(quotaUserLocks, userId)
			}
			quotaUserLocksMu.Unlock()
		})
	}, nil
}

type QuotaTransaction struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id" gorm:"index:idx_quota_tx_user_created,priority:1;index"`
	Type              string `json:"type" gorm:"type:varchar(32);index:idx_quota_tx_type_created,priority:1;default:''"`
	QuotaDelta        int    `json:"quota_delta" gorm:"type:bigint;default:0"`
	GiftQuotaDelta    int    `json:"gift_quota_delta" gorm:"type:bigint;default:0"`
	BalanceBefore     int    `json:"balance_before" gorm:"type:bigint;default:0"`
	GiftBalanceBefore int    `json:"gift_balance_before" gorm:"type:bigint;default:0"`
	BalanceAfter      int    `json:"balance_after" gorm:"type:bigint;default:0"`
	GiftBalanceAfter  int    `json:"gift_balance_after" gorm:"type:bigint;default:0"`
	TotalDelta        int    `json:"total_delta" gorm:"type:bigint;default:0"`
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

// applyQuotaTxLockTimeout 为 PostgreSQL 额度事务设置锁等待上限。
// 即使出现异常持锁（如僵尸事务），等待者也会在超时后报错返回，
// 而不是无限排队占满连接池。SET LOCAL 仅对当前事务生效。
func applyQuotaTxLockTimeout(tx *gorm.DB) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return
	}
	if err := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; err != nil {
		common.SysLog("failed to set quota tx lock_timeout: " + err.Error())
	}
}

// quotaTransactionTimeout bounds one quota/billing transaction end to end.
//
// The budget has to cover the round trips the transaction makes, not just the
// work the database does: one settlement is ~18 statements, so an instance that
// is far from the database spends most of this budget on the wire. Deployments
// whose application and database share a datacenter should leave the default
// alone; a high-latency topology can raise it, at the cost of holding a pooled
// connection for longer per request.
func quotaTransactionTimeoutDuration() time.Duration {
	seconds := common.GetEnvOrDefault("SQL_QUOTA_TX_TIMEOUT_SECONDS", 15)
	if seconds < 5 {
		seconds = 5
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// newQuotaOperationContext gives every quota state transition the same bounded
// end-to-end budget, including non-transactional conditional updates.
func newQuotaOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, quotaTransactionTimeoutDuration())
}

func withBoundedQuotaTransaction(parent context.Context, fn func(tx *gorm.DB) error) error {
	ctx, cancel := newQuotaOperationContext(parent)
	defer cancel()

	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		applyQuotaTxLockTimeout(tx)
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			if err := tx.Exec("SET LOCAL idle_in_transaction_session_timeout = '15s'").Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func withBoundedQuotaUserTransaction(parent context.Context, userId int, fn func(tx *gorm.DB) error) error {
	ctx, cancel := newQuotaOperationContext(parent)
	defer cancel()

	unlock, err := acquireQuotaUser(ctx, userId)
	if err != nil {
		return fmt.Errorf("acquire quota user lock: %w", err)
	}
	defer unlock()

	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		applyQuotaTxLockTimeout(tx)
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			if err := tx.Exec("SET LOCAL idle_in_transaction_session_timeout = '15s'").Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func lockUserForQuotaTx(tx *gorm.DB, userId int) (*User, error) {
	user := &User{}
	if err := LockForUpdate(tx).Where("id = ?", userId).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

// readQuotaSnapshotTx 无锁读取用户当前余额快照（PostgreSQL 快路径用）。
func readQuotaSnapshotTx(tx *gorm.DB, userId int) (*User, error) {
	user := &User{}
	if err := tx.Select("id", "quota", "gift_quota").Where("id = ?", userId).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func getQuotaTransactionByIdempotencyKeyTx(tx *gorm.DB, key string) (*QuotaTransaction, error) {
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	transaction := &QuotaTransaction{}
	// Isolate the expected not-found result from the caller's statement. This is
	// especially important for callers that pass a reusable Unscoped handle:
	// GORM otherwise keeps ErrRecordNotFound on that handle and short-circuits
	// the following locked user query in the same database transaction.
	query := tx.Session(&gorm.Session{}).Where("idempotency_key = ?", key).Limit(1).Find(transaction)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
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
	// 余额与流水列在 PostgreSQL 中为 bigint；生产存在合法超过 int32 的
	// 余额与调整量（如充值比例较高的站点），因此边界按 int64 校验，仅防
	// 御真正的整型溢出。
	if quotaTransactionDeltaOverflows(quotaDelta, giftQuotaDelta) {
		return nil, errors.New("quota change exceeds database limit")
	}

	quotaBefore := user.Quota
	giftBefore := user.GiftQuota
	if (quotaDelta > 0 && quotaBefore > math.MaxInt64-quotaDelta) ||
		(giftQuotaDelta > 0 && giftBefore > math.MaxInt64-giftQuotaDelta) {
		return nil, errors.New("quota balance exceeds database limit")
	}
	quotaAfter := quotaBefore + quotaDelta
	giftAfter := giftBefore + giftQuotaDelta
	// Positive recharge credits and refunds must be able to reduce an existing
	// debt even when they do not clear it in one operation. Ordinary debits still
	// may not create debt, and gift quota is never allowed below zero.
	if (quotaDelta < 0 && quotaAfter < 0) || giftAfter < 0 {
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

// ---------------------------------------------------------------------------
// PostgreSQL 快路径
//
// 悲观锁路径（SELECT ... FOR UPDATE → 读余额 → UPDATE → INSERT 流水 → COMMIT）
// 行锁跨 4~5 次网络往返持有，热点用户高并发时行锁队列会拖垮数据库。
// 快路径改用"带余额守卫的单条原子 UPDATE ... RETURNING"：
//   - 余额校验、扣减、取新值在一条语句内完成，锁持有缩短到 UPDATE→COMMIT；
//   - 流水的 before 值由 after 反推（before = after - delta），与实际应用的
//     变更严格一致；
//   - 幂等键冲突沿用 quotaTransactionCreateError 回滚 + 事务外恢复机制。
// MySQL/SQLite 仍走悲观锁路径（Rule 2 三库兼容；MySQL 的 UPDATE 右值取已更新
// 值、且无 RETURNING，不能套用同一 SQL）。
// ---------------------------------------------------------------------------

// tryApplyQuotaDeltaAtomicPG 以单条带守卫的条件 UPDATE 原子应用余额变更。
// ok=false 表示守卫不满足（余额不足或用户不存在），未做任何修改。
func tryApplyQuotaDeltaAtomicPG(tx *gorm.DB, userId int, quotaDelta int, giftQuotaDelta int, requireNonnegativeRecharge bool) (quotaAfter int, giftAfter int, ok bool, err error) {
	var res struct {
		Quota     int
		GiftQuota int
	}
	// Do not compare quotaDelta itself with a bare integer literal in SQL. pgx
	// then infers that standalone parameter as int4 and rejects valid bigint
	// credits (up to the configured 5,000,000,000,000 top-up maximum) before
	// PostgreSQL executes the statement. Pass the sign decision as a boolean.
	allowRechargeDebt := quotaDelta >= 0
	softDeletePredicate := " AND deleted_at IS NULL"
	if tx.Statement != nil && tx.Statement.Unscoped {
		softDeletePredicate = ""
	}
	result := tx.Raw(
		`UPDATE users SET quota = quota + ?, gift_quota = gift_quota + ? `+
			`WHERE id = ?`+softDeletePredicate+
			` AND (? = false OR quota >= 0)`+
			` AND (? = true OR quota::bigint + ? >= 0) AND gift_quota::bigint + ? >= 0 `+
			`RETURNING quota, gift_quota`,
		quotaDelta, giftQuotaDelta, userId,
		requireNonnegativeRecharge, allowRechargeDebt, quotaDelta, giftQuotaDelta,
	).Scan(&res)
	if result.Error != nil {
		return 0, 0, false, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, 0, false, nil
	}
	return res.Quota, res.GiftQuota, true, nil
}

// insertQuotaTransactionRecordTx 在余额变更已原子应用后补插流水。
// before 由 after 反推，保证与本次实际应用的变更一致。
func insertQuotaTransactionRecordTx(tx *gorm.DB, userId int, quotaDelta int, giftQuotaDelta int, quotaAfter int, giftAfter int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	transaction := &QuotaTransaction{
		UserId:            userId,
		Type:              ref.Type,
		QuotaDelta:        quotaDelta,
		GiftQuotaDelta:    giftQuotaDelta,
		BalanceBefore:     quotaAfter - quotaDelta,
		GiftBalanceBefore: giftAfter - giftQuotaDelta,
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
		// 余额原子更新已在本事务内执行，必须整体回滚，
		// 由 withQuotaTransactionForUser 在事务外按幂等键恢复（见 createQuotaTransactionTx 同款注释）。
		return nil, quotaTransactionCreateError{idempotencyKey: ref.IdempotencyKey, err: err}
	}
	return quotaBreakdownFromTransaction(transaction, false), nil
}

// applyQuotaDeltaPGTx 是 applyQuotaDeltaTx 的 PostgreSQL 快路径：
// delta 已知（贷记/退款/指定拆分的借记），无需先读余额。
func applyQuotaDeltaPGTx(tx *gorm.DB, userId int, quotaDelta int, giftQuotaDelta int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if quotaDelta == 0 && giftQuotaDelta == 0 {
		snap, err := readQuotaSnapshotTx(tx, userId)
		if err != nil {
			return nil, err
		}
		return &QuotaBreakdown{
			QuotaBefore:     snap.Quota,
			GiftQuotaBefore: snap.GiftQuota,
			QuotaAfter:      snap.Quota,
			GiftQuotaAfter:  snap.GiftQuota,
		}, nil
	}
	// 幂等检查提前到加锁之前；并发重复写入由流水表唯一索引兜底
	if existing, err := getQuotaTransactionByIdempotencyKeyTx(tx, ref.IdempotencyKey); err == nil {
		return quotaBreakdownFromTransaction(existing, true), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if quotaTransactionDeltaOverflows(quotaDelta, giftQuotaDelta) {
		return nil, errors.New("quota change exceeds database limit")
	}
	quotaAfter, giftAfter, ok, err := tryApplyQuotaDeltaAtomicPG(tx, userId, quotaDelta, giftQuotaDelta, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		// 区分用户不存在与余额不足
		if _, err := readQuotaSnapshotTx(tx, userId); err != nil {
			return nil, err
		}
		return nil, ErrInsufficientUserQuota
	}
	return insertQuotaTransactionRecordTx(tx, userId, quotaDelta, giftQuotaDelta, quotaAfter, giftAfter, ref)
}

// debitQuotaPreferGiftSplit 计算"赠送额度优先"的扣费拆分，与悲观锁路径逻辑一致。
func debitQuotaPreferGiftSplit(giftQuota int, amount int) (rechargeDebit int, giftDebit int) {
	giftDebit = amount
	if giftQuota < giftDebit {
		giftDebit = giftQuota
	}
	return amount - giftDebit, giftDebit
}

// debitQuotaPreferGiftPGTx 是 DebitQuotaPreferGiftTx 的 PostgreSQL 快路径。
// 拆分依赖当前赠送余额，采用"无锁快照 + 原子条件更新 + 失败重读"的乐观策略；
// 同实例并发已被 acquireQuotaUser 串行化，跨实例竞争极少，重试基本不会发生。
func debitQuotaPreferGiftPGTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef, withLedger bool) (*QuotaBreakdown, error) {
	if withLedger {
		if existing, err := getQuotaTransactionByIdempotencyKeyTx(tx, ref.IdempotencyKey); err == nil {
			return quotaBreakdownFromTransaction(existing, true), nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		snap, err := readQuotaSnapshotTx(tx, userId)
		if err != nil {
			return nil, err
		}
		if snap.Quota < 0 || snap.TotalQuota() < amount {
			return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, snap.TotalQuota(), amount)
		}
		rechargeDebit, giftDebit := debitQuotaPreferGiftSplit(snap.GiftQuota, amount)
		quotaAfter, giftAfter, ok, err := tryApplyQuotaDeltaAtomicPG(tx, userId, -rechargeDebit, -giftDebit, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			// 快照过期（其他实例并发修改了余额），重读后重试
			continue
		}
		if !withLedger {
			return &QuotaBreakdown{
				QuotaDelta:      -rechargeDebit,
				GiftQuotaDelta:  -giftDebit,
				QuotaBefore:     quotaAfter + rechargeDebit,
				GiftQuotaBefore: giftAfter + giftDebit,
				QuotaAfter:      quotaAfter,
				GiftQuotaAfter:  giftAfter,
			}, nil
		}
		return insertQuotaTransactionRecordTx(tx, userId, -rechargeDebit, -giftDebit, quotaAfter, giftAfter, ref)
	}
	// 极端并发下重试耗尽，回退悲观锁路径保证正确性
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	if user.Quota < 0 || user.TotalQuota() < amount {
		return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, user.TotalQuota(), amount)
	}
	rechargeDebit, giftDebit := debitQuotaPreferGiftSplit(user.GiftQuota, amount)
	if !withLedger {
		return applyQuotaDeltaNoLedgerTx(tx, user, -rechargeDebit, -giftDebit)
	}
	return createQuotaTransactionTx(tx, user, -rechargeDebit, -giftDebit, ref)
}

func applyQuotaDeltaNoLedgerTx(tx *gorm.DB, user *User, quotaDelta int, giftQuotaDelta int) (*QuotaBreakdown, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}
	quotaBefore := user.Quota
	giftBefore := user.GiftQuota
	if (quotaDelta > 0 && quotaBefore > math.MaxInt64-quotaDelta) ||
		(giftQuotaDelta > 0 && giftBefore > math.MaxInt64-giftQuotaDelta) {
		return nil, errors.New("quota balance exceeds database limit")
	}
	quotaAfter := quotaBefore + quotaDelta
	giftAfter := giftBefore + giftQuotaDelta
	if (quotaDelta < 0 && quotaAfter < 0) || giftAfter < 0 {
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
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return applyQuotaDeltaPGTx(tx, userId, quotaDelta, giftQuotaDelta, ref)
	}
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	return createQuotaTransactionTx(tx, user, quotaDelta, giftQuotaDelta, ref)
}

func withQuotaTransactionForUser(userId int, fn func(tx *gorm.DB) (*QuotaBreakdown, error)) (*QuotaBreakdown, error) {
	var breakdown *QuotaBreakdown
	err := withBoundedQuotaUserTransaction(context.Background(), userId, func(tx *gorm.DB) error {
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
	err := withBoundedQuotaUserTransaction(context.Background(), userId, func(tx *gorm.DB) error {
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
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return debitQuotaPreferGiftPGTx(tx, userId, amount, ref, true)
	}
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	if user.Quota < 0 || user.TotalQuota() < amount {
		return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, user.TotalQuota(), amount)
	}
	rechargeDebit, giftDebit := debitQuotaPreferGiftSplit(user.GiftQuota, amount)
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
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return debitQuotaPreferGiftPGTx(tx, userId, amount, QuotaTransactionRef{}, false)
	}
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	if user.Quota < 0 || user.TotalQuota() < amount {
		return nil, fmt.Errorf("%w, user quota: %d, need quota: %d", ErrInsufficientUserQuota, user.TotalQuota(), amount)
	}
	rechargeDebit, giftDebit := debitQuotaPreferGiftSplit(user.GiftQuota, amount)
	return applyQuotaDeltaNoLedgerTx(tx, user, -rechargeDebit, -giftDebit)
}

// DebitConfirmedSettlementAllowRechargeDebtTx applies a confirmed delivered
// service settlement. Gift quota is consumed first and never becomes negative;
// only recharge quota may cross below zero. This function must never be used by
// pre-consume or reserve paths.
func DebitConfirmedSettlementAllowRechargeDebtTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error) {
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}
	if userId <= 0 {
		return nil, errors.New("invalid user_id")
	}
	if amount < 0 {
		return nil, errors.New("quota 不能为负数！")
	}
	if amount == 0 {
		return nil, nil
	}
	ref = normalizeQuotaRef(ref, QuotaTransactionTypeConsumeSettle)
	if existing, err := getQuotaTransactionByIdempotencyKeyTx(tx, ref.IdempotencyKey); err == nil {
		if existing.UserId != userId || existing.Type != QuotaTransactionTypeConsumeSettle ||
			existing.RequestId != ref.RequestID || existing.TotalDelta != -amount {
			return nil, fmt.Errorf("%w: confirmed settlement idempotency key conflicts with another quota transaction", ErrBillingSettlementRequiresManual)
		}
		return quotaBreakdownFromTransaction(existing, true), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	user, err := lockUserForQuotaTx(tx.Session(&gorm.Session{NewDB: true}).Unscoped(), userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: confirmed settlement user %d does not exist", ErrBillingSettlementRequiresManual, userId)
		}
		return nil, fmt.Errorf("lock confirmed settlement user %d: %w", userId, err)
	}
	if user.GiftQuota < 0 {
		return nil, fmt.Errorf("%w: gift quota is already negative", ErrBillingSettlementRequiresManual)
	}
	rechargeDebit, giftDebit := debitQuotaPreferGiftSplit(user.GiftQuota, amount)
	if int64(user.Quota) < math.MinInt64+int64(rechargeDebit) {
		return nil, fmt.Errorf("%w: recharge quota debt exceeds database limit", ErrBillingSettlementRequiresManual)
	}
	quotaAfter := user.Quota - rechargeDebit
	giftAfter := user.GiftQuota - giftDebit
	result := tx.Session(&gorm.Session{NewDB: true}).Unscoped().Model(&User{}).
		Where("id = ? AND gift_quota >= ? AND quota >= ?", user.Id, giftDebit, math.MinInt64+int64(rechargeDebit)).
		Updates(map[string]interface{}{
			"quota":      gorm.Expr("quota - ?", rechargeDebit),
			"gift_quota": gorm.Expr("gift_quota - ?", giftDebit),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, fmt.Errorf("%w: confirmed settlement debt update matched no user", ErrBillingSettlementRequiresManual)
	}
	breakdown, err := insertQuotaTransactionRecordTx(tx.Session(&gorm.Session{NewDB: true}), user.Id, -rechargeDebit, -giftDebit, quotaAfter, giftAfter, ref)
	if err != nil {
		return nil, fmt.Errorf("record confirmed settlement transaction: %w", err)
	}
	user.Quota = quotaAfter
	user.GiftQuota = giftAfter
	return breakdown, nil
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
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			return applyQuotaDeltaNoLedgerPGTx(tx, userId, delta.QuotaDelta, delta.GiftQuotaDelta)
		}
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

// RefundQuotaByBreakdownNoLedgerTx returns a known wallet quota split inside
// the caller's transaction. It is used when a larger billing state transition
// must atomically update both the user balance and its durable reservation.
func RefundQuotaByBreakdownNoLedgerTx(tx *gorm.DB, userId int, delta QuotaDelta) (*QuotaBreakdown, error) {
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}
	if delta.QuotaDelta < 0 || delta.GiftQuotaDelta < 0 {
		return nil, errors.New("refund quota delta cannot be negative")
	}
	if delta.QuotaDelta == 0 && delta.GiftQuotaDelta == 0 {
		return nil, nil
	}
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return applyQuotaDeltaNoLedgerPGTx(tx, userId, delta.QuotaDelta, delta.GiftQuotaDelta)
	}
	user, err := lockUserForQuotaTx(tx, userId)
	if err != nil {
		return nil, err
	}
	return applyQuotaDeltaNoLedgerTx(tx, user, delta.QuotaDelta, delta.GiftQuotaDelta)
}

// applyQuotaDeltaNoLedgerPGTx 是 applyQuotaDeltaNoLedgerTx 的 PostgreSQL 快路径。
func applyQuotaDeltaNoLedgerPGTx(tx *gorm.DB, userId int, quotaDelta int, giftQuotaDelta int) (*QuotaBreakdown, error) {
	if quotaDelta == 0 && giftQuotaDelta == 0 {
		snap, err := readQuotaSnapshotTx(tx, userId)
		if err != nil {
			return nil, err
		}
		return &QuotaBreakdown{
			QuotaBefore:     snap.Quota,
			GiftQuotaBefore: snap.GiftQuota,
			QuotaAfter:      snap.Quota,
			GiftQuotaAfter:  snap.GiftQuota,
		}, nil
	}
	quotaAfter, giftAfter, ok, err := tryApplyQuotaDeltaAtomicPG(tx, userId, quotaDelta, giftQuotaDelta, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		if _, err := readQuotaSnapshotTx(tx, userId); err != nil {
			return nil, err
		}
		return nil, ErrInsufficientUserQuota
	}
	return &QuotaBreakdown{
		QuotaDelta:      quotaDelta,
		GiftQuotaDelta:  giftQuotaDelta,
		QuotaBefore:     quotaAfter - quotaDelta,
		GiftQuotaBefore: giftAfter - giftQuotaDelta,
		QuotaAfter:      quotaAfter,
		GiftQuotaAfter:  giftAfter,
	}, nil
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
