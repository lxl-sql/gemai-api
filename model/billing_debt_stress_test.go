//go:build stress

package model

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type billingDebtStressItem struct {
	userId    int
	tokenId   int
	tokenKey  string
	requestId string
}

func TestBillingDebtStress(t *testing.T) {
	if os.Getenv("BILLING_STRESS_RUN") != "1" {
		t.Skip("set BILLING_STRESS_RUN=1 or use scripts/billing-debt-stress.ps1")
	}
	_ = godotenv.Load("../.env")

	users := billingStressEnvInt(t, "BILLING_STRESS_USERS", 5000, 1, 100000)
	workers := billingStressEnvInt(t, "BILLING_STRESS_CONCURRENCY", 64, 1, 512)
	replays := billingStressEnvInt(t, "BILLING_STRESS_REPLAYS", 2, 0, 10)
	redisDB := billingStressEnvInt(t, "BILLING_STRESS_REDIS_DB", 15, 0, 255)
	dsn := strings.TrimSpace(os.Getenv("BILLING_TEST_POSTGRES_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("SQL_DSN"))
	}
	require.NotEmpty(t, dsn, "BILLING_TEST_POSTGRES_DSN or SQL_DSN is required")
	require.NoError(t, requireLocalPostgresDSN(dsn))

	redisURL := strings.TrimSpace(os.Getenv("BILLING_TEST_REDIS_URL"))
	if redisURL == "" {
		redisURL = strings.TrimSpace(os.Getenv("REDIS_CONN_STRING"))
	}
	require.NotEmpty(t, redisURL, "BILLING_TEST_REDIS_URL or REDIS_CONN_STRING is required")
	redisOptions, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	require.NoError(t, requireLoopbackAddress(redisOptions.Addr, "Redis"))
	redisOptions.DB = redisDB
	redisOptions.PoolSize = workers + 16
	redisClient := redis.NewClient(redisOptions)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	t.Cleanup(cancel)
	require.NoError(t, redisClient.Ping(ctx).Err())

	runId := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema := "billing_debt_stress_" + runId
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	adminSQL, err := adminDB.DB()
	require.NoError(t, err)
	require.NoError(t, adminSQL.PingContext(ctx))
	require.NoError(t, adminDB.Exec(fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)).Error)
	t.Cleanup(func() {
		if os.Getenv("BILLING_STRESS_KEEP_SCHEMA") != "1" {
			if dropErr := adminDB.Exec(fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)).Error; dropErr != nil {
				t.Errorf("drop stress schema %s: %v", schema, dropErr)
			}
		} else {
			t.Logf("kept PostgreSQL stress schema %s", schema)
		}
		_ = adminSQL.Close()
	})

	stressDSN := billingStressPostgresSchemaDSN(dsn, schema)
	stressDB, err := gorm.Open(postgres.Open(stressDSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	stressSQL, err := stressDB.DB()
	require.NoError(t, err)
	stressSQL.SetMaxOpenConns(workers + 16)
	stressSQL.SetMaxIdleConns(workers)
	require.NoError(t, stressSQL.PingContext(ctx))

	previousDB := DB
	previousLogDB := LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousRedis := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	DB = stressDB
	LOG_DB = stressDB
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RDB = redisClient
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RDB = previousRedis
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdate
		initCol()
		_ = stressSQL.Close()
		_ = redisClient.Close()
	})

	require.NoError(t, stressDB.AutoMigrate(
		&User{},
		&Token{},
		&QuotaTransaction{},
		&BillingReservation{},
		&BillingSettlementFailure{},
	))

	baseOffset := int(time.Now().UnixNano()%100_000_000) + 1
	items := make([]billingDebtStressItem, users)
	userRows := make([]User, users)
	tokenRows := make([]Token, users)
	redisKeys := make([]string, 0, users*2)
	for i := 0; i < users; i++ {
		userId := 1_000_000_000 + baseOffset + i
		tokenId := 1_300_000_000 + baseOffset + i
		tokenKey := fmt.Sprintf("stress_%s_%08d", runId, i)
		requestId := fmt.Sprintf("stress:%s:%08d", runId, i)
		items[i] = billingDebtStressItem{userId: userId, tokenId: tokenId, tokenKey: tokenKey, requestId: requestId}
		userRows[i] = User{
			Id:        userId,
			Username:  fmt.Sprintf("billing-debt-stress-%s-%d", runId, i),
			Password:  "stress-only",
			AffCode:   fmt.Sprintf("bd%s%08d", runId, i),
			Quota:     350,
			GiftQuota: 150,
			Status:    common.UserStatusEnabled,
		}
		tokenRows[i] = Token{
			Id:          tokenId,
			UserId:      userId,
			Key:         tokenKey,
			Name:        "billing-debt-stress",
			Status:      common.TokenStatusEnabled,
			ExpiredTime: -1,
			RemainQuota: 500,
		}
		redisKeys = append(redisKeys, fmt.Sprintf("user:%d", userId), "token:"+common.GenerateHMAC(tokenKey))
	}

	t.Cleanup(func() {
		billingStressDeleteRedisKeys(context.Background(), redisClient, redisKeys)
	})
	existingRedisKeys, err := billingStressRedisExists(ctx, redisClient, redisKeys)
	require.NoError(t, err)
	require.Zero(t, existingRedisKeys, "generated Redis keys unexpectedly collide with existing data")

	seedStarted := time.Now()
	require.NoError(t, stressDB.CreateInBatches(&userRows, 250).Error)
	require.NoError(t, stressDB.CreateInBatches(&tokenRows, 250).Error)
	require.NoError(t, billingStressParallel(users, workers, func(index int) error {
		item := items[index]
		_, createErr := CreateBillingReservation(BillingReservationCreateInput{
			RequestId:     item.requestId,
			UserId:        item.userId,
			TokenId:       item.tokenId,
			TokenKey:      item.tokenKey,
			BillingSource: BillingReservationSourceWallet,
			Quota:         400,
			LeaseSeconds:  600,
		})
		return createErr
	}))
	seedDuration := time.Since(seedStarted)

	cachePipe := redisClient.Pipeline()
	for _, key := range redisKeys {
		cachePipe.HSet(ctx, key, "stress_run", runId)
		cachePipe.Expire(ctx, key, 30*time.Minute)
	}
	_, err = cachePipe.Exec(ctx)
	require.NoError(t, err)
	common.RedisEnabled = true

	settleStarted := time.Now()
	totalSettlementCalls := users * (replays + 1)
	require.NoError(t, billingStressParallel(totalSettlementCalls, workers, func(index int) error {
		item := items[index%users]
		_, settleErr := FinalizeBillingReservation(item.requestId, 700, BillingReservationStatusSettling)
		return settleErr
	}))
	settleDuration := time.Since(settleStarted)
	remainingRedisKeys, err := billingStressRedisExists(ctx, redisClient, redisKeys)
	require.NoError(t, err)
	require.Zero(t, remainingRedisKeys, "settlement left stale user/token cache keys")

	billingStressAssertSettlementState(t, stressDB, users)
	blockedSamples := users
	if blockedSamples > 100 {
		blockedSamples = 100
	}
	for i := 0; i < blockedSamples; i++ {
		_, debitErr := DebitQuotaPreferGift(items[i].userId, 1, QuotaTransactionRef{
			IdempotencyKey: "stress:blocked:" + items[i].requestId,
		})
		require.ErrorIs(t, debitErr, ErrInsufficientUserQuota)
	}

	creditStarted := time.Now()
	require.NoError(t, billingStressParallel(users, workers, func(index int) error {
		item := items[index]
		ref := QuotaTransactionRef{
			Source:         QuotaTransactionSourceSystem,
			ReferenceType:  "billing_debt_stress",
			ReferenceID:    item.requestId,
			RequestID:      item.requestId,
			IdempotencyKey: "stress:credit:" + item.requestId,
		}
		var first *QuotaBreakdown
		var creditErr error
		if index%2 == 0 {
			first, creditErr = CreditRechargeQuota(item.userId, 50, ref)
		} else {
			first, creditErr = RefundQuotaByBreakdown(item.userId, QuotaDelta{QuotaDelta: 50}, ref)
		}
		if creditErr != nil {
			return creditErr
		}
		if first == nil || first.QuotaAfter != -150 {
			return fmt.Errorf("unexpected debt credit result for user %d: %+v", item.userId, first)
		}
		var replay *QuotaBreakdown
		if index%2 == 0 {
			replay, creditErr = CreditRechargeQuota(item.userId, 50, ref)
		} else {
			replay, creditErr = RefundQuotaByBreakdown(item.userId, QuotaDelta{QuotaDelta: 50}, ref)
		}
		if creditErr != nil {
			return creditErr
		}
		if replay == nil || !replay.IdempotencyReused {
			return fmt.Errorf("credit replay was not idempotently reused for user %d", item.userId)
		}
		return nil
	}))
	creditDuration := time.Since(creditStarted)
	billingStressAssertCreditedState(t, stressDB, users)

	t.Logf("billing debt stress passed: users=%d workers=%d settlement_calls=%d", users, workers, totalSettlementCalls)
	t.Logf("seed=%s (%.1f reservations/s), settle=%s (%.1f calls/s), credit=%s (%.1f users/s)",
		seedDuration.Round(time.Millisecond), float64(users)/seedDuration.Seconds(),
		settleDuration.Round(time.Millisecond), float64(totalSettlementCalls)/settleDuration.Seconds(),
		creditDuration.Round(time.Millisecond), float64(users)/creditDuration.Seconds())
}

func billingStressAssertSettlementState(t *testing.T, db *gorm.DB, users int) {
	t.Helper()
	var userStats struct {
		Count    int64
		Quota    int64
		Gift     int64
		MinGift  int64
		MaxQuota int64
	}
	require.NoError(t, db.Raw(`SELECT COUNT(*) AS count, COALESCE(SUM(quota), 0) AS quota,
		COALESCE(SUM(gift_quota), 0) AS gift, COALESCE(MIN(gift_quota), 0) AS min_gift,
		COALESCE(MAX(quota), 0) AS max_quota FROM users`).Scan(&userStats).Error)
	assert.Equal(t, int64(users), userStats.Count)
	assert.Equal(t, int64(-200*users), userStats.Quota)
	assert.Zero(t, userStats.Gift)
	assert.GreaterOrEqual(t, userStats.MinGift, int64(0))
	assert.Equal(t, int64(-200), userStats.MaxQuota)

	var tokenStats struct {
		Count  int64
		Remain int64
		Used   int64
	}
	require.NoError(t, db.Raw(`SELECT COUNT(*) AS count, COALESCE(SUM(remain_quota), 0) AS remain,
		COALESCE(SUM(used_quota), 0) AS used FROM tokens`).Scan(&tokenStats).Error)
	assert.Equal(t, int64(users), tokenStats.Count)
	assert.Equal(t, int64(-200*users), tokenStats.Remain)
	assert.Equal(t, int64(700*users), tokenStats.Used)

	var completed int64
	require.NoError(t, db.Model(&BillingReservation{}).
		Where("status = ? AND reserved_quota = ? AND desired_quota = ?", BillingReservationStatusCompleted, 700, 700).
		Count(&completed).Error)
	assert.Equal(t, int64(users), completed)
	var manual int64
	require.NoError(t, db.Model(&BillingReservation{}).Where("status = ?", BillingReservationStatusManualRequired).Count(&manual).Error)
	assert.Zero(t, manual)

	var ledgerStats struct {
		Count    int64
		Distinct int64
		Delta    int64
	}
	require.NoError(t, db.Raw(`SELECT COUNT(*) AS count, COUNT(DISTINCT idempotency_key) AS distinct,
		COALESCE(SUM(total_delta), 0) AS delta FROM quota_transactions`).Scan(&ledgerStats).Error)
	assert.Equal(t, int64(users), ledgerStats.Count)
	assert.Equal(t, int64(users), ledgerStats.Distinct)
	assert.Equal(t, int64(-300*users), ledgerStats.Delta)
}

func billingStressAssertCreditedState(t *testing.T, db *gorm.DB, users int) {
	t.Helper()
	var userStats struct {
		Count int64
		Quota int64
		Gift  int64
	}
	require.NoError(t, db.Raw(`SELECT COUNT(*) AS count, COALESCE(SUM(quota), 0) AS quota,
		COALESCE(SUM(gift_quota), 0) AS gift FROM users`).Scan(&userStats).Error)
	assert.Equal(t, int64(users), userStats.Count)
	assert.Equal(t, int64(-150*users), userStats.Quota)
	assert.Zero(t, userStats.Gift)

	var ledgerStats struct {
		Count    int64
		Distinct int64
		Delta    int64
	}
	require.NoError(t, db.Raw(`SELECT COUNT(*) AS count, COUNT(DISTINCT idempotency_key) AS distinct,
		COALESCE(SUM(total_delta), 0) AS delta FROM quota_transactions`).Scan(&ledgerStats).Error)
	assert.Equal(t, int64(2*users), ledgerStats.Count)
	assert.Equal(t, int64(2*users), ledgerStats.Distinct)
	assert.Equal(t, int64(-250*users), ledgerStats.Delta)
}

func billingStressParallel(total int, workers int, fn func(int) error) error {
	if total <= 0 {
		return nil
	}
	if workers > total {
		workers = total
	}
	var next atomic.Int64
	var firstErr error
	var errMu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				errMu.Lock()
				stopped := firstErr != nil
				errMu.Unlock()
				if stopped {
					return
				}
				index := int(next.Add(1) - 1)
				if index >= total {
					return
				}
				if err := fn(index); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("job %d: %w", index, err)
					}
					errMu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func billingStressEnvInt(t *testing.T, key string, fallback int, minimum int, maximum int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "%s must be an integer", key)
	require.GreaterOrEqual(t, value, minimum, "%s is below its safe minimum", key)
	require.LessOrEqual(t, value, maximum, "%s is above its safe maximum", key)
	return value
}

func billingStressPostgresSchemaDSN(dsn string, schema string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema
}

func requireLocalPostgresDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		return requireLoopbackHost(parsed.Hostname(), "PostgreSQL")
	}
	for _, field := range strings.Fields(dsn) {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "host") {
			return requireLoopbackHost(strings.Trim(parts[1], "'\"[]"), "PostgreSQL")
		}
	}
	// No host in a PostgreSQL keyword DSN means a local socket.
	return nil
}

func requireLoopbackAddress(address string, service string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	return requireLoopbackHost(strings.Trim(host, "[]"), service)
}

func requireLoopbackHost(host string, service string) error {
	if strings.EqualFold(host, "localhost") || host == "" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s stress target %q is not local; refusing to run", service, host)
}

func billingStressRedisExists(ctx context.Context, client *redis.Client, keys []string) (int64, error) {
	var total int64
	for start := 0; start < len(keys); start += 1000 {
		end := start + 1000
		if end > len(keys) {
			end = len(keys)
		}
		count, err := client.Exists(ctx, keys[start:end]...).Result()
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func billingStressDeleteRedisKeys(ctx context.Context, client *redis.Client, keys []string) {
	if client == nil {
		return
	}
	for start := 0; start < len(keys); start += 1000 {
		end := start + 1000
		if end > len(keys) {
			end = len(keys)
		}
		_ = client.Del(ctx, keys[start:end]...).Err()
	}
}
