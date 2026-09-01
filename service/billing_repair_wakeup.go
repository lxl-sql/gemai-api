package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	billingRepairWakeupChannel    = "system_task:billing_settlement_repair:wakeup"
	billingRepairFallbackInterval = 5 * time.Minute
)

var (
	billingRepairRequestInFlight   atomic.Bool
	billingRepairLastProbe         atomic.Int64
	billingRepairWakeTimerEnabled  atomic.Bool
	billingRepairWakeTimerMu       sync.Mutex
	billingRepairWakeTimer         *time.Timer
	billingRepairWakeTimerUnixTime int64
)

// requestBillingSettlementRepair persists a deduplicated task first, then uses
// Redis only as a best-effort cross-instance wakeup. Lost notifications never
// lose financial work because the task and settlement intent remain in the DB.
func requestBillingSettlementRepair() {
	if !billingRepairRequestInFlight.CompareAndSwap(false, true) {
		return
	}
	gopool.Go(func() {
		defer billingRepairRequestInFlight.Store(false)
		task, created, err := EnqueueSystemTask(model.SystemTaskTypeBillingSettlementRepair, nil)
		if err != nil {
			common.SysLog("failed to enqueue billing settlement repair wakeup: " + err.Error())
			scheduleBillingRepairWakeupAt(time.Now().Add(systemTaskSchedulerInterval).Unix())
			return
		}
		if !created && task != nil && task.Status == model.SystemTaskStatusRunning {
			scheduleBillingRepairWakeupAt(time.Now().Add(systemTaskSchedulerInterval).Unix())
		}
		publishBillingRepairWakeup()
	})
}

func publishBillingRepairWakeup() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := common.RDB.Publish(ctx, billingRepairWakeupChannel, "1").Err(); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("billing repair wakeup publish failed: %v", err))
	}
}

func startBillingRepairWakeSubscriber() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	gopool.Go(func() {
		retryDelay := time.Second
		for {
			pubsub := common.RDB.Subscribe(context.Background(), billingRepairWakeupChannel)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := pubsub.Receive(ctx)
			cancel()
			if err != nil {
				_ = pubsub.Close()
				logger.LogWarn(context.Background(), fmt.Sprintf("billing repair wakeup subscribe failed: %v", err))
				time.Sleep(retryDelay)
				if retryDelay < 30*time.Second {
					retryDelay *= 2
					if retryDelay > 30*time.Second {
						retryDelay = 30 * time.Second
					}
				}
				continue
			}
			retryDelay = time.Second
			for range pubsub.Channel() {
				notifySystemTaskRunner()
			}
			_ = pubsub.Close()
			time.Sleep(retryDelay)
		}
	})
}

func billingRepairFallbackProbeDue(now time.Time) bool {
	nowUnixNano := now.UnixNano()
	for {
		previous := billingRepairLastProbe.Load()
		if previous != 0 && nowUnixNano-previous < billingRepairFallbackInterval.Nanoseconds() {
			return false
		}
		if billingRepairLastProbe.CompareAndSwap(previous, nowUnixNano) {
			return true
		}
	}
}

func refreshBillingRepairWakeup() {
	if !billingRepairWakeTimerEnabled.Load() {
		return
	}
	nextRetryAt, found, err := model.GetNextBillingFinancialRepairAt()
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("billing repair next retry query failed: %v", err))
		return
	}
	if !found {
		cancelBillingRepairWakeupTimer()
		return
	}
	scheduleBillingRepairWakeupAt(nextRetryAt)
}

func scheduleBillingRepairWakeupAt(unixTime int64) {
	if !billingRepairWakeTimerEnabled.Load() || unixTime <= 0 {
		return
	}
	billingRepairWakeTimerMu.Lock()
	defer billingRepairWakeTimerMu.Unlock()
	if billingRepairWakeTimer != nil && billingRepairWakeTimerUnixTime <= unixTime {
		return
	}
	if billingRepairWakeTimer != nil {
		billingRepairWakeTimer.Stop()
	}
	billingRepairWakeTimerUnixTime = unixTime
	delay := time.Until(time.Unix(unixTime, 0))
	if delay < 0 {
		delay = 0
	}
	billingRepairWakeTimer = time.AfterFunc(delay, func() {
		billingRepairWakeTimerMu.Lock()
		if billingRepairWakeTimerUnixTime != unixTime {
			billingRepairWakeTimerMu.Unlock()
			return
		}
		billingRepairWakeTimer = nil
		billingRepairWakeTimerUnixTime = 0
		billingRepairWakeTimerMu.Unlock()
		requestBillingSettlementRepair()
	})
}

func cancelBillingRepairWakeupTimer() {
	billingRepairWakeTimerMu.Lock()
	defer billingRepairWakeTimerMu.Unlock()
	if billingRepairWakeTimer != nil {
		billingRepairWakeTimer.Stop()
	}
	billingRepairWakeTimer = nil
	billingRepairWakeTimerUnixTime = 0
}
