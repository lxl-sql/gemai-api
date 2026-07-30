package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const TokenUsageSourceStateName = "token_usage_source_v1"

const tokenUsageSourceReconcileStateID = 1

// TokenUsageSource is the bounded, persistent materialized view used by the
// API-key usage-source dialog. It intentionally stores no request counters:
// min/max timestamps make replaying a log range naturally idempotent.
type TokenUsageSource struct {
	ID            int64  `json:"-" gorm:"primaryKey"`
	UserID        int    `json:"-" gorm:"not null;index:idx_token_usage_source_user_token,priority:1"`
	TokenID       int    `json:"-" gorm:"not null;uniqueIndex:idx_token_usage_source_identity,priority:1;index:idx_token_usage_source_user_token,priority:2;index:idx_token_usage_source_recent,priority:1"`
	SourceKey     string `json:"-" gorm:"type:char(64);not null;uniqueIndex:idx_token_usage_source_identity,priority:2"`
	IP            string `json:"ip" gorm:"type:varchar(64);not null"`
	UserAgent     string `json:"user_agent" gorm:"type:varchar(512);not null"`
	FirstSeenAt   int64  `json:"first_seen_at" gorm:"bigint;not null"`
	LastSeenAt    int64  `json:"last_seen_at" gorm:"bigint;not null;index:idx_token_usage_source_recent,priority:2"`
	LastSuccessAt int64  `json:"last_success_at" gorm:"bigint;not null"`
	LastErrorAt   int64  `json:"last_error_at" gorm:"bigint;not null"`
	UpdatedAt     int64  `json:"-" gorm:"bigint;not null"`
}

// TokenUsageSourceMeta is both the per-token tracking state and the
// coordination row for aggregation/deletion. Aggregators and deletion paths
// lock this row, never the hot tokens row. PurgedAt is a permanent tombstone so
// historical log backfill cannot recreate sources for a deleted token.
type TokenUsageSourceMeta struct {
	TokenID         int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	UserID          int   `json:"-" gorm:"not null;index"`
	TrackingEnabled bool  `json:"tracking_enabled"`
	TrackingStart   int64 `json:"tracking_start" gorm:"bigint;not null"`
	PurgedAt        int64 `json:"-" gorm:"bigint;not null"`
	Truncated       bool  `json:"truncated"`
	UpdatedAt       int64 `json:"-" gorm:"bigint;not null"`
}

// TokenUsageSourceReconcileState keeps the bounded rolling-deployment
// reconciliation cursor. A complete pass after all old instances have drained
// converges keys created/deleted by old binaries and privacy-setting changes.
type TokenUsageSourceReconcileState struct {
	ID           int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	TokenCursor  int   `json:"token_cursor" gorm:"not null"`
	ReconciledAt int64 `json:"reconciled_at" gorm:"bigint;not null"`
	UpdatedAt    int64 `json:"updated_at" gorm:"bigint;not null"`
}

type TokenUsageSourceReconcileResult struct {
	Processed      int   `json:"processed"`
	Created        int   `json:"created"`
	Enabled        int   `json:"enabled"`
	Disabled       int   `json:"disabled"`
	Purged         int   `json:"purged"`
	DeletedSources int64 `json:"deleted_sources"`
	NextCursor     int   `json:"next_cursor"`
	CycleCompleted bool  `json:"cycle_completed"`
	ReconciledAt   int64 `json:"reconciled_at"`
}

type TokenUsageSourceGroup struct {
	UserID        int
	TokenID       int
	SourceKey     string
	IP            string
	UserAgent     string
	FirstSeenAt   int64
	LastSeenAt    int64
	LastSuccessAt int64
	LastErrorAt   int64
}

type TokenUsageSourcePage struct {
	Items           []TokenUsageSource `json:"items"`
	Total           int64              `json:"total"`
	TrackingEnabled bool               `json:"tracking_enabled"`
	TrackingStart   int64              `json:"tracking_start"`
	CoverageStart   int64              `json:"coverage_start"`
	Watermark       int64              `json:"watermark"`
	Backfilling     bool               `json:"backfilling"`
	Truncated       bool               `json:"truncated"`
	Available       bool               `json:"available"`
}

func NewTokenUsageSourceKey(ip string, userAgent string) string {
	hash := sha256.New()
	var size [8]byte
	for _, field := range []string{ip, userAgent} {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeTokenUsageSourceIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if parsed := net.ParseIP(ip); parsed != nil {
		return parsed.String()
	}
	if len(ip) > 64 {
		return ip[:64]
	}
	return ip
}

func AggregateTokenUsageSourceGroups(
	ctx context.Context,
	startTimestamp int64,
	endTimestamp int64,
	limit int,
) ([]TokenUsageSourceGroup, bool, error) {
	if LOG_DB == nil {
		return nil, false, errors.New("log database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp {
		return nil, false, errors.New("invalid token usage source time range")
	}

	type aggregateRow struct {
		UserID        int
		TokenID       int
		IP            string
		UserAgent     string
		FirstSeenAt   int64
		LastSeenAt    int64
		LastSuccessAt int64
		LastErrorAt   int64
	}
	var rows []aggregateRow
	query := `SELECT user_id, token_id,
			COALESCE(ip, '') AS ip,
			COALESCE(user_agent, '') AS user_agent,
			MIN(created_at) AS first_seen_at,
			MAX(created_at) AS last_seen_at,
			MAX(CASE WHEN type = ? THEN created_at ELSE 0 END) AS last_success_at,
			MAX(CASE WHEN type = ? THEN created_at ELSE 0 END) AS last_error_at
		FROM logs
		WHERE created_at >= ? AND created_at < ?
			AND token_id > 0
			AND type IN (?, ?)
			AND (ip <> '' OR user_agent <> '')
		GROUP BY user_id, token_id, ip, user_agent`
	args := []interface{}{
		LogTypeConsume,
		LogTypeError,
		startTimestamp,
		endTimestamp,
		LogTypeConsume,
		LogTypeError,
	}
	if limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, limit+1)
	}
	if err := LOG_DB.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	truncated := limit > 0 && len(rows) > limit
	if truncated {
		return nil, true, nil
	}

	// Historical rows may predate the current IP/UA normalization. Merge again
	// in Go so equivalent canonical sources still collapse to one identity.
	groupByIdentity := make(map[string]TokenUsageSourceGroup, len(rows))
	for _, row := range rows {
		ip := normalizeTokenUsageSourceIP(row.IP)
		userAgent := normalizeLogUserAgent(row.UserAgent)
		if ip == "" && userAgent == "" {
			continue
		}
		sourceKey := NewTokenUsageSourceKey(ip, userAgent)
		mapKey := strings.Join([]string{
			strconv.Itoa(row.TokenID),
			sourceKey,
		}, "\x00")
		group, exists := groupByIdentity[mapKey]
		if !exists {
			groupByIdentity[mapKey] = TokenUsageSourceGroup{
				UserID:        row.UserID,
				TokenID:       row.TokenID,
				SourceKey:     sourceKey,
				IP:            ip,
				UserAgent:     userAgent,
				FirstSeenAt:   row.FirstSeenAt,
				LastSeenAt:    row.LastSeenAt,
				LastSuccessAt: row.LastSuccessAt,
				LastErrorAt:   row.LastErrorAt,
			}
			continue
		}
		if row.FirstSeenAt < group.FirstSeenAt {
			group.FirstSeenAt = row.FirstSeenAt
		}
		if row.LastSeenAt > group.LastSeenAt {
			group.LastSeenAt = row.LastSeenAt
		}
		if row.LastSuccessAt > group.LastSuccessAt {
			group.LastSuccessAt = row.LastSuccessAt
		}
		if row.LastErrorAt > group.LastErrorAt {
			group.LastErrorAt = row.LastErrorAt
		}
		groupByIdentity[mapKey] = group
	}

	groups := make([]TokenUsageSourceGroup, 0, len(groupByIdentity))
	for _, group := range groupByIdentity {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i int, j int) bool {
		if groups[i].TokenID != groups[j].TokenID {
			return groups[i].TokenID < groups[j].TokenID
		}
		return groups[i].SourceKey < groups[j].SourceKey
	})
	return groups, false, nil
}

func GetEarliestTokenUsageSourceLogTimestamp(ctx context.Context) (int64, error) {
	if LOG_DB == nil {
		return 0, errors.New("log database is not initialized")
	}
	var row struct {
		CreatedAt int64
	}
	if err := LOG_DB.WithContext(ctx).
		Raw("SELECT created_at FROM logs ORDER BY created_at ASC LIMIT 1").
		Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.CreatedAt, nil
}

func MergeTokenUsageSourceGroups(ctx context.Context, groups []TokenUsageSourceGroup, sourceLimit int) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if len(groups) == 0 {
		return nil
	}
	if sourceLimit < 1 {
		return errors.New("token usage source limit must be positive")
	}

	groupsByToken := make(map[int][]TokenUsageSourceGroup)
	tokenIDs := make([]int, 0)
	for _, group := range groups {
		if group.TokenID <= 0 || group.SourceKey == "" {
			continue
		}
		if _, exists := groupsByToken[group.TokenID]; !exists {
			tokenIDs = append(tokenIDs, group.TokenID)
		}
		groupsByToken[group.TokenID] = append(groupsByToken[group.TokenID], group)
	}
	sort.Ints(tokenIDs)

	const tokenBatchSize = 25
	for start := 0; start < len(tokenIDs); start += tokenBatchSize {
		end := start + tokenBatchSize
		if end > len(tokenIDs) {
			end = len(tokenIDs)
		}
		batchIDs := tokenIDs[start:end]
		if err := mergeTokenUsageSourceBatch(ctx, batchIDs, groupsByToken, sourceLimit); err != nil {
			return err
		}
	}
	return nil
}

func mergeTokenUsageSourceBatch(
	ctx context.Context,
	tokenIDs []int,
	groupsByToken map[int][]TokenUsageSourceGroup,
	sourceLimit int,
) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var metas []TokenUsageSourceMeta
		if err := lockForUpdate(tx).
			Where("token_id IN ?", tokenIDs).
			Order("token_id").
			Find(&metas).Error; err != nil {
			return err
		}
		metaByToken := make(map[int]*TokenUsageSourceMeta, len(metas))
		activeTokenIDs := make([]int, 0, len(metas))
		for i := range metas {
			meta := &metas[i]
			metaByToken[meta.TokenID] = meta
			if meta.PurgedAt == 0 && meta.TrackingEnabled {
				activeTokenIDs = append(activeTokenIDs, meta.TokenID)
			}
		}
		if len(activeTokenIDs) == 0 {
			return nil
		}

		var existing []TokenUsageSource
		if err := tx.Where("token_id IN ?", activeTokenIDs).Find(&existing).Error; err != nil {
			return err
		}
		existingByToken := make(map[int][]TokenUsageSource, len(activeTokenIDs))
		for _, source := range existing {
			existingByToken[source.TokenID] = append(existingByToken[source.TokenID], source)
		}

		now := common.GetTimestamp()
		for _, tokenID := range activeTokenIDs {
			meta := metaByToken[tokenID]
			current := existingByToken[tokenID]
			byKey := make(map[string]TokenUsageSource, len(current)+len(groupsByToken[tokenID]))
			originalKeys := make(map[string]struct{}, len(current))
			touchedKeys := make(map[string]struct{}, len(groupsByToken[tokenID]))
			for _, source := range current {
				byKey[source.SourceKey] = source
				originalKeys[source.SourceKey] = struct{}{}
			}

			for _, group := range groupsByToken[tokenID] {
				if group.UserID != meta.UserID || group.LastSeenAt < meta.TrackingStart {
					continue
				}
				// Never approximate a first-seen timestamp across the tracking
				// boundary. The single boundary chunk is skipped and the next
				// complete chunk is still processed normally.
				if group.FirstSeenAt < meta.TrackingStart {
					continue
				}
				source, exists := byKey[group.SourceKey]
				if !exists {
					source = TokenUsageSource{
						UserID:      meta.UserID,
						TokenID:     tokenID,
						SourceKey:   group.SourceKey,
						IP:          group.IP,
						UserAgent:   group.UserAgent,
						FirstSeenAt: group.FirstSeenAt,
					}
				}
				if group.FirstSeenAt < source.FirstSeenAt {
					source.FirstSeenAt = group.FirstSeenAt
				}
				if group.LastSeenAt > source.LastSeenAt {
					source.LastSeenAt = group.LastSeenAt
					source.IP = group.IP
					source.UserAgent = group.UserAgent
				}
				if group.LastSuccessAt > source.LastSuccessAt {
					source.LastSuccessAt = group.LastSuccessAt
				}
				if group.LastErrorAt > source.LastErrorAt {
					source.LastErrorAt = group.LastErrorAt
				}
				source.UpdatedAt = now
				byKey[group.SourceKey] = source
				touchedKeys[group.SourceKey] = struct{}{}
			}

			merged := make([]TokenUsageSource, 0, len(byKey))
			for _, source := range byKey {
				merged = append(merged, source)
			}
			sort.Slice(merged, func(i int, j int) bool {
				if merged[i].LastSeenAt != merged[j].LastSeenAt {
					return merged[i].LastSeenAt > merged[j].LastSeenAt
				}
				return merged[i].SourceKey < merged[j].SourceKey
			})
			wasTruncated := len(merged) > sourceLimit
			if wasTruncated {
				merged = merged[:sourceLimit]
				if !meta.Truncated {
					if err := tx.Model(&TokenUsageSourceMeta{}).
						Where("token_id = ? AND truncated = ?", tokenID, false).
						Updates(map[string]interface{}{
							"truncated":  true,
							"updated_at": now,
						}).Error; err != nil {
						return err
					}
				}
			}

			keptKeys := make(map[string]struct{}, len(merged))
			upserts := make([]TokenUsageSource, 0, len(touchedKeys))
			for _, source := range merged {
				keptKeys[source.SourceKey] = struct{}{}
				if _, touched := touchedKeys[source.SourceKey]; touched {
					upserts = append(upserts, source)
				}
			}
			if len(upserts) > 0 {
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "token_id"}, {Name: "source_key"}},
					DoUpdates: clause.AssignmentColumns([]string{
						"ip",
						"user_agent",
						"first_seen_at",
						"last_seen_at",
						"last_success_at",
						"last_error_at",
						"updated_at",
					}),
				}).CreateInBatches(upserts, 200).Error; err != nil {
					return err
				}
			}

			evictedKeys := make([]string, 0)
			for key := range originalKeys {
				if _, kept := keptKeys[key]; !kept {
					evictedKeys = append(evictedKeys, key)
				}
			}
			if len(evictedKeys) > 0 {
				if err := tx.Where("token_id = ? AND source_key IN ?", tokenID, evictedKeys).
					Delete(&TokenUsageSource{}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func GetTokenUsageSourcePage(
	ctx context.Context,
	userID int,
	tokenID int,
	offset int,
	limit int,
) (*TokenUsageSourcePage, error) {
	if DB == nil {
		return nil, errors.New("main database is not initialized")
	}
	if userID <= 0 || tokenID <= 0 || offset < 0 || limit <= 0 {
		return nil, errors.New("invalid token usage source query")
	}
	var meta TokenUsageSourceMeta
	if err := DB.WithContext(ctx).
		Where("token_id = ? AND user_id = ? AND purged_at = 0", tokenID, userID).
		First(&meta).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return &TokenUsageSourcePage{
			Items: make([]TokenUsageSource, 0),
		}, nil
	} else if err != nil {
		return nil, err
	}

	page := &TokenUsageSourcePage{
		Items:           make([]TokenUsageSource, 0),
		TrackingEnabled: meta.TrackingEnabled,
		TrackingStart:   meta.TrackingStart,
		Truncated:       meta.Truncated,
	}
	state, err := GetLogStatRollupState(ctx, TokenUsageSourceStateName)
	if err != nil {
		return nil, err
	}
	if state != nil {
		page.Available = true
		page.CoverageStart = state.CoverageStart
		page.Watermark = state.Watermark
		page.Backfilling = state.BackfillCursor > state.CoverageStart
		if meta.TrackingEnabled && page.TrackingStart < state.CoverageStart {
			page.TrackingStart = state.CoverageStart
		}
	}
	if !meta.TrackingEnabled {
		return page, nil
	}
	if err := DB.WithContext(ctx).Model(&TokenUsageSource{}).
		Where("token_id = ? AND user_id = ?", tokenID, userID).
		Count(&page.Total).Error; err != nil {
		return nil, err
	}
	if err := DB.WithContext(ctx).
		Where("token_id = ? AND user_id = ?", tokenID, userID).
		Order("last_seen_at DESC").
		Order("source_key ASC").
		Offset(offset).
		Limit(limit).
		Find(&page.Items).Error; err != nil {
		return nil, err
	}
	return page, nil
}

func createTokenUsageSourceMetaTx(tx *gorm.DB, token *Token) error {
	if tx == nil || token == nil || token.Id <= 0 || token.UserId <= 0 {
		return errors.New("invalid token usage source meta")
	}
	if !tx.Migrator().HasTable(&TokenUsageSourceMeta{}) {
		return nil
	}
	trackingStart := token.CreatedTime
	if trackingStart <= 0 {
		trackingStart = common.GetTimestamp()
	}
	meta := TokenUsageSourceMeta{
		TokenID:         token.Id,
		UserID:          token.UserId,
		TrackingEnabled: true,
		TrackingStart:   trackingStart,
		UpdatedAt:       common.GetTimestamp(),
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&meta).Error
}

func BackfillTokenUsageSourceMeta() error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	const batchSize = 200
	var cursor int
	for {
		var tokens []Token
		if err := DB.
			Where("tokens.id > ?", cursor).
			Where("NOT EXISTS (SELECT 1 FROM token_usage_source_meta WHERE token_usage_source_meta.token_id = tokens.id)").
			Order("tokens.id").
			Limit(batchSize).
			Find(&tokens).Error; err != nil {
			return err
		}
		if len(tokens) == 0 {
			return nil
		}
		if err := DB.Transaction(func(tx *gorm.DB) error {
			now := common.GetTimestamp()
			metas := make([]TokenUsageSourceMeta, 0, len(tokens))
			for _, token := range tokens {
				trackingStart := token.CreatedTime
				if trackingStart <= 0 {
					trackingStart = now
				}
				metas = append(metas, TokenUsageSourceMeta{
					TokenID:         token.Id,
					UserID:          token.UserId,
					TrackingEnabled: true,
					TrackingStart:   trackingStart,
					UpdatedAt:       now,
				})
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(metas, batchSize).Error
		}); err != nil {
			return err
		}
		cursor = tokens[len(tokens)-1].Id
	}
}

func ReconcileTokenUsageSourceMetaBatch(
	ctx context.Context,
	batchSize int,
) (*TokenUsageSourceReconcileResult, error) {
	if DB == nil {
		return nil, errors.New("main database is not initialized")
	}
	if batchSize < 1 {
		return nil, errors.New("token usage source reconcile batch size must be positive")
	}

	result := &TokenUsageSourceReconcileResult{}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		stateSeed := TokenUsageSourceReconcileState{
			ID:        tokenUsageSourceReconcileStateID,
			UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stateSeed).Error; err != nil {
			return err
		}

		var state TokenUsageSourceReconcileState
		if err := lockForUpdate(tx).
			Where("id = ?", tokenUsageSourceReconcileStateID).
			First(&state).Error; err != nil {
			return err
		}

		var tokenIDs []int
		if err := tx.Unscoped().
			Model(&Token{}).
			Where("id > ?", state.TokenCursor).
			Order("id").
			Limit(batchSize).
			Pluck("id", &tokenIDs).Error; err != nil {
			return err
		}
		var metaIDs []int
		if err := tx.Model(&TokenUsageSourceMeta{}).
			Where("token_id > ?", state.TokenCursor).
			Order("token_id").
			Limit(batchSize).
			Pluck("token_id", &metaIDs).Error; err != nil {
			return err
		}

		candidateSet := make(map[int]struct{}, len(tokenIDs)+len(metaIDs))
		for _, tokenID := range tokenIDs {
			candidateSet[tokenID] = struct{}{}
		}
		for _, tokenID := range metaIDs {
			candidateSet[tokenID] = struct{}{}
		}
		candidateIDs := make([]int, 0, len(candidateSet))
		for tokenID := range candidateSet {
			candidateIDs = append(candidateIDs, tokenID)
		}
		sort.Ints(candidateIDs)
		if len(candidateIDs) > batchSize {
			candidateIDs = candidateIDs[:batchSize]
		}
		if len(candidateIDs) == 0 {
			result.CycleCompleted = true
			result.ReconciledAt = now
			return tx.Model(&TokenUsageSourceReconcileState{}).
				Where("id = ?", tokenUsageSourceReconcileStateID).
				Updates(map[string]interface{}{
					"token_cursor":  0,
					"reconciled_at": now,
					"updated_at":    now,
				}).Error
		}

		var tokens []Token
		if err := tx.Unscoped().
			Select("id", "user_id", "created_time", "deleted_at").
			Where("id IN ?", candidateIDs).
			Find(&tokens).Error; err != nil {
			return err
		}
		tokenByID := make(map[int]*Token, len(tokens))
		userIDSet := make(map[int]struct{})
		for i := range tokens {
			token := &tokens[i]
			tokenByID[token.Id] = token
			if !token.DeletedAt.Valid {
				userIDSet[token.UserId] = struct{}{}
			}
		}

		userIDs := make([]int, 0, len(userIDSet))
		for userID := range userIDSet {
			userIDs = append(userIDs, userID)
		}
		var users []User
		if len(userIDs) > 0 {
			if err := tx.Select("id").
				Where("id IN ?", userIDs).
				Find(&users).Error; err != nil {
				return err
			}
		}
		userByID := make(map[int]*User, len(users))
		for i := range users {
			userByID[users[i].Id] = &users[i]
		}

		var metas []TokenUsageSourceMeta
		if err := lockForUpdate(tx).
			Where("token_id IN ?", candidateIDs).
			Order("token_id").
			Find(&metas).Error; err != nil {
			return err
		}
		metaByTokenID := make(map[int]*TokenUsageSourceMeta, len(metas))
		for i := range metas {
			metaByTokenID[metas[i].TokenID] = &metas[i]
		}

		deleteSources := func(tokenID int) error {
			deleteResult := tx.Where("token_id = ?", tokenID).Delete(&TokenUsageSource{})
			if deleteResult.Error != nil {
				return deleteResult.Error
			}
			result.DeletedSources += deleteResult.RowsAffected
			return nil
		}

		for _, tokenID := range candidateIDs {
			result.Processed++
			token := tokenByID[tokenID]
			meta := metaByTokenID[tokenID]
			user := (*User)(nil)
			if token != nil && !token.DeletedAt.Valid {
				user = userByID[token.UserId]
			}

			if token == nil || token.DeletedAt.Valid || user == nil {
				if meta == nil {
					continue
				}
				purgedAt := meta.PurgedAt
				if purgedAt == 0 {
					purgedAt = now
					result.Purged++
				}
				if err := tx.Model(&TokenUsageSourceMeta{}).
					Where("token_id = ?", tokenID).
					Updates(map[string]interface{}{
						"tracking_enabled": false,
						"tracking_start":   int64(0),
						"purged_at":        purgedAt,
						"truncated":        false,
						"updated_at":       now,
					}).Error; err != nil {
					return err
				}
				if err := deleteSources(tokenID); err != nil {
					return err
				}
				continue
			}

			if meta == nil {
				trackingStart := token.CreatedTime
				if trackingStart <= 0 {
					trackingStart = now
				}
				newMeta := TokenUsageSourceMeta{
					TokenID:         tokenID,
					UserID:          token.UserId,
					TrackingEnabled: true,
					TrackingStart:   trackingStart,
					UpdatedAt:       now,
				}
				createResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newMeta)
				if createResult.Error != nil {
					return createResult.Error
				}
				if createResult.RowsAffected > 0 {
					result.Created++
				}
				if err := deleteSources(tokenID); err != nil {
					return err
				}
				continue
			}

			if meta.PurgedAt > 0 || meta.UserID != token.UserId {
				purgedAt := meta.PurgedAt
				if purgedAt == 0 {
					purgedAt = now
					result.Purged++
				}
				if err := tx.Model(&TokenUsageSourceMeta{}).
					Where("token_id = ?", tokenID).
					Updates(map[string]interface{}{
						"tracking_enabled": false,
						"tracking_start":   int64(0),
						"purged_at":        purgedAt,
						"truncated":        false,
						"updated_at":       now,
					}).Error; err != nil {
					return err
				}
				if err := deleteSources(tokenID); err != nil {
					return err
				}
				continue
			}

			if !meta.TrackingEnabled || meta.TrackingStart == 0 {
				trackingStart := token.CreatedTime
				if trackingStart <= 0 {
					trackingStart = now
				}
				if err := tx.Model(&TokenUsageSourceMeta{}).
					Where("token_id = ?", tokenID).
					Updates(map[string]interface{}{
						"tracking_enabled": true,
						"tracking_start":   trackingStart,
						"truncated":        false,
						"updated_at":       now,
					}).Error; err != nil {
					return err
				}
				result.Enabled++
			}
		}

		result.NextCursor = candidateIDs[len(candidateIDs)-1]
		return tx.Model(&TokenUsageSourceReconcileState{}).
			Where("id = ?", tokenUsageSourceReconcileStateID).
			Updates(map[string]interface{}{
				"token_cursor": result.NextCursor,
				"updated_at":   now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func PurgeTokenUsageSourcesTx(tx *gorm.DB, tokenID int, userID int) error {
	if tx == nil || tokenID <= 0 || userID <= 0 {
		return errors.New("invalid token usage source purge")
	}
	if !tx.Migrator().HasTable(&TokenUsageSourceMeta{}) {
		return nil
	}
	var meta TokenUsageSourceMeta
	err := lockForUpdate(tx).
		Where("token_id = ?", tokenID).
		First(&meta).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		meta = TokenUsageSourceMeta{
			TokenID:   tokenID,
			UserID:    userID,
			PurgedAt:  common.GetTimestamp(),
			UpdatedAt: common.GetTimestamp(),
		}
		if err := tx.Create(&meta).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if meta.UserID != userID {
			return errors.New("token usage source owner mismatch")
		}
		if err := tx.Model(&TokenUsageSourceMeta{}).
			Where("token_id = ?", tokenID).
			Updates(map[string]interface{}{
				"tracking_enabled": false,
				"purged_at":        common.GetTimestamp(),
				"truncated":        false,
				"updated_at":       common.GetTimestamp(),
			}).Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasTable(&TokenUsageSource{}) {
		return nil
	}
	return tx.Where("token_id = ? AND user_id = ?", tokenID, userID).Delete(&TokenUsageSource{}).Error
}

func PurgeUserTokenUsageSourcesTx(tx *gorm.DB, userID int) error {
	if tx == nil || userID <= 0 {
		return errors.New("invalid user token usage source purge")
	}
	if !tx.Migrator().HasTable(&TokenUsageSourceMeta{}) {
		return nil
	}
	var tokens []Token
	if err := tx.Unscoped().
		Select("id", "user_id").
		Where("user_id = ?", userID).
		Order("id").
		Find(&tokens).Error; err != nil {
		return err
	}
	for i := range tokens {
		if err := PurgeTokenUsageSourcesTx(tx, tokens[i].Id, userID); err != nil {
			return err
		}
	}
	if !tx.Migrator().HasTable(&TokenUsageSource{}) {
		return nil
	}
	return tx.Where("user_id = ?", userID).Delete(&TokenUsageSource{}).Error
}

func InitializeTokenUsageSourceState(
	ctx context.Context,
	coverageStart int64,
	cutover int64,
) (*LogStatRollupState, error) {
	if DB == nil {
		return nil, errors.New("main database is not initialized")
	}
	if coverageStart < 0 || cutover <= coverageStart {
		return nil, errors.New("invalid token usage source state range")
	}
	state := LogStatRollupState{
		Name:           TokenUsageSourceStateName,
		CoverageStart:  coverageStart,
		Watermark:      cutover,
		BackfillCursor: cutover,
		UpdatedAt:      common.GetTimestamp(),
	}
	if err := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return nil, err
	}
	return GetLogStatRollupState(ctx, TokenUsageSourceStateName)
}

func AdvanceTokenUsageSourceWatermark(ctx context.Context, expected int64, next int64) error {
	if next <= expected {
		return nil
	}
	result := DB.WithContext(ctx).Model(&LogStatRollupState{}).
		Where("name = ? AND watermark = ?", TokenUsageSourceStateName, expected).
		Updates(map[string]interface{}{
			"watermark":  next,
			"updated_at": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLogStatRollupStateChanged
	}
	return nil
}

func LowerTokenUsageSourceBackfillCursor(ctx context.Context, expected int64, next int64) error {
	if next >= expected {
		return nil
	}
	result := DB.WithContext(ctx).Model(&LogStatRollupState{}).
		Where("name = ? AND backfill_cursor = ? AND coverage_start <= ?", TokenUsageSourceStateName, expected, next).
		Updates(map[string]interface{}{
			"backfill_cursor": next,
			"updated_at":      common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLogStatRollupStateChanged
	}
	return nil
}

func ReconcileTokenUsageSourceStateAfterLogCleanup(tx *gorm.DB, targetTimestamp int64) error {
	if tx == nil || targetTimestamp <= 0 {
		return nil
	}
	var state LogStatRollupState
	err := tx.Where("name = ?", TokenUsageSourceStateName).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Sources already aggregated before cleanup remain valid. Only an
	// unprocessed historical gap that the cleanup just removed loses coverage.
	if state.BackfillCursor <= targetTimestamp {
		return nil
	}
	return tx.Model(&LogStatRollupState{}).
		Where("name = ?", TokenUsageSourceStateName).
		Updates(map[string]interface{}{
			"coverage_start":  targetTimestamp,
			"backfill_cursor": targetTimestamp,
			"updated_at":      common.GetTimestamp(),
		}).Error
}
