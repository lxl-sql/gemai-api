package model

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	tokenUsageSourceFlushInterval   = 5 * time.Second
	tokenUsageSourceFlushTokenBatch = 25
	tokenUsageSourcePendingLimit    = 100000
)

type TokenUsageSourceOutcome struct {
	UserID     int
	TokenID    int
	IP         string
	UserAgent  string
	OccurredAt int64
	Success    bool
}

type tokenUsageSourceDirectBuffer struct {
	mu                sync.Mutex
	flushMu           sync.Mutex
	pending           map[string]TokenUsageSourceGroup
	stop              chan struct{}
	done              chan struct{}
	started           bool
	lastOverflowLogAt int64
}

var directTokenUsageSourceBuffer = tokenUsageSourceDirectBuffer{
	pending: make(map[string]TokenUsageSourceGroup),
}

func TokenUsageSourceFlushIntervalSeconds() int64 {
	return int64(tokenUsageSourceFlushInterval / time.Second)
}

func EnqueueTokenUsageSourceOutcome(outcome TokenUsageSourceOutcome) bool {
	if outcome.UserID <= 0 ||
		outcome.TokenID <= 0 ||
		outcome.OccurredAt <= 0 ||
		!system_setting.TokenUsageSourceDirectCountingEnabledAt(outcome.OccurredAt) {
		return false
	}
	outcome.IP = normalizeTokenUsageSourceIP(outcome.IP)
	outcome.UserAgent = normalizeLogUserAgent(outcome.UserAgent)
	if outcome.IP == "" && outcome.UserAgent == "" {
		return false
	}

	group := TokenUsageSourceGroup{
		UserID:      outcome.UserID,
		TokenID:     outcome.TokenID,
		SourceKey:   NewTokenUsageSourceKey(outcome.IP, outcome.UserAgent),
		IP:          outcome.IP,
		UserAgent:   outcome.UserAgent,
		FirstSeenAt: outcome.OccurredAt,
		LastSeenAt:  outcome.OccurredAt,
	}
	if outcome.Success {
		group.LastSuccessAt = outcome.OccurredAt
		group.SuccessCount = 1
	} else {
		group.LastErrorAt = outcome.OccurredAt
		group.ErrorCount = 1
	}

	StartTokenUsageSourceBatchUpdater()
	buffer := &directTokenUsageSourceBuffer
	buffer.mu.Lock()
	accepted := addPendingTokenUsageSourceGroup(
		buffer.pending,
		group,
		tokenUsageSourcePendingLimit,
	)
	shouldLog := false
	if !accepted {
		shouldLog = markTokenUsageSourceOverflowLocked(buffer)
	}
	buffer.mu.Unlock()
	if shouldLog {
		common.SysError("token usage source direct buffer is full; dropping new source identities")
	}
	return accepted
}

func StartTokenUsageSourceBatchUpdater() {
	buffer := &directTokenUsageSourceBuffer
	buffer.mu.Lock()
	if buffer.started {
		buffer.mu.Unlock()
		return
	}
	buffer.stop = make(chan struct{})
	buffer.done = make(chan struct{})
	buffer.started = true
	stop := buffer.stop
	done := buffer.done
	buffer.mu.Unlock()

	go func() {
		ticker := time.NewTicker(tokenUsageSourceFlushInterval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), tokenUsageSourceFlushInterval)
				if err := FlushTokenUsageSourceBatch(ctx); err != nil {
					common.SysError("flush token usage source batch failed: " + err.Error())
				}
				cancel()
			case <-stop:
				return
			}
		}
	}()
}

func StopTokenUsageSourceBatchUpdater(ctx context.Context) error {
	buffer := &directTokenUsageSourceBuffer
	buffer.mu.Lock()
	if !buffer.started {
		buffer.mu.Unlock()
		return FlushTokenUsageSourceBatch(ctx)
	}
	stop := buffer.stop
	done := buffer.done
	buffer.started = false
	buffer.stop = nil
	buffer.done = nil
	close(stop)
	buffer.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return FlushTokenUsageSourceBatch(ctx)
}

func FlushTokenUsageSourceBatch(ctx context.Context) error {
	buffer := &directTokenUsageSourceBuffer
	buffer.flushMu.Lock()
	defer buffer.flushMu.Unlock()

	buffer.mu.Lock()
	if len(buffer.pending) == 0 {
		buffer.mu.Unlock()
		return nil
	}
	pending := buffer.pending
	buffer.pending = make(map[string]TokenUsageSourceGroup)
	buffer.mu.Unlock()

	groupsByToken := make(map[int][]TokenUsageSourceGroup)
	tokenIDs := make([]int, 0)
	for _, group := range pending {
		if _, exists := groupsByToken[group.TokenID]; !exists {
			tokenIDs = append(tokenIDs, group.TokenID)
		}
		groupsByToken[group.TokenID] = append(groupsByToken[group.TokenID], group)
	}
	sort.Ints(tokenIDs)

	for start := 0; start < len(tokenIDs); start += tokenUsageSourceFlushTokenBatch {
		end := start + tokenUsageSourceFlushTokenBatch
		if end > len(tokenIDs) {
			end = len(tokenIDs)
		}
		batch := make([]TokenUsageSourceGroup, 0)
		for _, tokenID := range tokenIDs[start:end] {
			batch = append(batch, groupsByToken[tokenID]...)
		}
		if err := MergeDirectTokenUsageSourceGroups(
			ctx,
			batch,
			system_setting.GetTokenUsageSourceSettings().MaxSourcesPerToken,
		); err != nil {
			requeue := make([]TokenUsageSourceGroup, 0)
			for _, tokenID := range tokenIDs[start:] {
				requeue = append(requeue, groupsByToken[tokenID]...)
			}
			requeueTokenUsageSourceGroups(requeue)
			return err
		}
	}
	return nil
}

func requeueTokenUsageSourceGroups(groups []TokenUsageSourceGroup) {
	buffer := &directTokenUsageSourceBuffer
	buffer.mu.Lock()
	dropped := false
	for _, group := range groups {
		if !addPendingTokenUsageSourceGroup(
			buffer.pending,
			group,
			tokenUsageSourcePendingLimit,
		) {
			dropped = true
		}
	}
	shouldLog := dropped && markTokenUsageSourceOverflowLocked(buffer)
	buffer.mu.Unlock()
	if shouldLog {
		common.SysError("token usage source direct buffer is full; dropping failed source identities")
	}
}

func addPendingTokenUsageSourceGroup(
	pending map[string]TokenUsageSourceGroup,
	group TokenUsageSourceGroup,
	limit int,
) bool {
	mapKey := tokenUsageSourcePendingKey(group)
	if existing, exists := pending[mapKey]; exists {
		pending[mapKey] = mergePendingTokenUsageSourceGroup(existing, group)
		return true
	}
	if len(pending) >= limit {
		return false
	}
	pending[mapKey] = group
	return true
}

// markTokenUsageSourceOverflowLocked rate-limits overflow logs. The caller
// must hold directTokenUsageSourceBuffer.mu.
func markTokenUsageSourceOverflowLocked(buffer *tokenUsageSourceDirectBuffer) bool {
	now := common.GetTimestamp()
	if now-buffer.lastOverflowLogAt < 60 {
		return false
	}
	buffer.lastOverflowLogAt = now
	return true
}

func tokenUsageSourcePendingKey(group TokenUsageSourceGroup) string {
	return strings.Join([]string{
		strconv.Itoa(group.TokenID),
		group.SourceKey,
	}, "\x00")
}

func mergePendingTokenUsageSourceGroup(
	current TokenUsageSourceGroup,
	incoming TokenUsageSourceGroup,
) TokenUsageSourceGroup {
	if current.FirstSeenAt == 0 ||
		(incoming.FirstSeenAt > 0 && incoming.FirstSeenAt < current.FirstSeenAt) {
		current.FirstSeenAt = incoming.FirstSeenAt
	}
	if incoming.LastSeenAt > current.LastSeenAt {
		current.LastSeenAt = incoming.LastSeenAt
		current.IP = incoming.IP
		current.UserAgent = incoming.UserAgent
	}
	if incoming.LastSuccessAt > current.LastSuccessAt {
		current.LastSuccessAt = incoming.LastSuccessAt
	}
	if incoming.LastErrorAt > current.LastErrorAt {
		current.LastErrorAt = incoming.LastErrorAt
	}
	current.SuccessCount += incoming.SuccessCount
	current.ErrorCount += incoming.ErrorCount
	return current
}

func tokenUsageSourceDirectCountingEnabled(timestamp int64) bool {
	return system_setting.TokenUsageSourceDirectCountingEnabledAt(timestamp)
}
