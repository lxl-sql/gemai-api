package model

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 计费租约需要各实例对"现在"取得一致口径，原实现为此在每个计费事务里单独发一条
// SELECT clock_timestamp()。这条查询不碰任何表，但它是在事务内执行的：数据库变慢时
// 每次几百毫秒都直接计入连接被事务占用的时长，而一次中继请求要经过 reserve /
// dispatch / audit / settle 多个事务，等于凭空延长数倍的连接占用，成倍放大连接池排队。
//
// 改为按固定间隔向数据库采样一次时钟偏移量，间隔内用 应用时钟 + 偏移量 得到同一口径。
// 各实例都以同一个数据库时钟为基准校正自己的本地时钟，跨实例一致性仍在毫秒级，
// 远小于租约（900 秒）的粒度；而 GetDBTimestamp 原本在出错时就直接回退到未经校正的
// 应用时钟，因此校正后的时钟对它只会更准。
const dbClockResampleIntervalSeconds = 5

var (
	dbClockOffsetSeconds atomic.Int64
	// dbClockSampledAtUnix 为 0 表示尚未成功采样过，此时必须真实查库。
	dbClockSampledAtUnix atomic.Int64
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
func GetDBTimestamp() int64 {
	ts, err := queryDBTimestampTx(DB)
	if err != nil {
		return common.GetTimestamp()
	}
	return ts
}

// queryDBTimestampTx is the strict form used by billing state transitions.
// Falling back to an application clock there would reintroduce cross-instance
// lease races, so callers must abort their transaction when this query fails.
func queryDBTimestampTx(tx *gorm.DB) (int64, error) {
	if tx == nil {
		return 0, errors.New("database connection is nil")
	}
	appNow := common.GetTimestamp()
	if lastSample := dbClockSampledAtUnix.Load(); lastSample > 0 {
		if appNow-lastSample < dbClockResampleIntervalSeconds {
			return appNow + dbClockOffsetSeconds.Load(), nil
		}
		// 采样窗口到期时只让抢到 CAS 的那一个事务真正查库，其余继续使用当前偏移量，
		// 否则所有在途事务会在同一时刻一起发起采样。
		if !dbClockSampledAtUnix.CompareAndSwap(lastSample, appNow) {
			return appNow + dbClockOffsetSeconds.Load(), nil
		}
	}

	var ts int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = tx.Raw("SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint").Scan(&ts).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = tx.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = tx.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil {
		return 0, fmt.Errorf("query database timestamp: %w", err)
	}
	if ts <= 0 {
		return 0, errors.New("database returned an invalid timestamp")
	}
	dbClockOffsetSeconds.Store(ts - appNow)
	dbClockSampledAtUnix.Store(appNow)
	return ts, nil
}
