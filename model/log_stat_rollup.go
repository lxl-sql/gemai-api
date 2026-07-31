package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	LogStatRollupStateName      = "minute"
	LogStatMinuteTotalStateName = "minute_total_v1"
)

var ErrLogStatRollupStateChanged = errors.New("log stat rollup state changed")

// LogStatRollup 仅保留两个维度组合索引（username/model_name 为最常用筛选）。
// token/channel/group 筛选依赖 bucket_start 前缀扫描即可；六索引方案在高基数
// 场景下 DELETE+INSERT 写放大过大。
type LogStatRollup struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	BucketStart      int64  `json:"bucket_start" gorm:"bigint;not null;uniqueIndex:idx_log_stat_rollup_bucket_dimension,priority:1;index:idx_log_stat_username_bucket,priority:2;index:idx_log_stat_model_bucket,priority:2"`
	DimensionKey     string `json:"dimension_key" gorm:"type:char(64);not null;uniqueIndex:idx_log_stat_rollup_bucket_dimension,priority:2"`
	Username         string `json:"username" gorm:"size:255;not null;index:idx_log_stat_username_bucket,priority:1"`
	TokenName        string `json:"token_name" gorm:"size:255;not null"`
	ModelName        string `json:"model_name" gorm:"size:255;not null;index:idx_log_stat_model_bucket,priority:1"`
	ChannelID        int    `json:"channel_id" gorm:"not null"`
	Group            string `json:"group" gorm:"size:255;not null"`
	RequestCount     int64  `json:"request_count" gorm:"bigint;not null"`
	Quota            int64  `json:"quota" gorm:"bigint;not null"`
	PromptTokens     int64  `json:"prompt_tokens" gorm:"bigint;not null"`
	CompletionTokens int64  `json:"completion_tokens" gorm:"bigint;not null"`
}

// LogStatMinuteTotal stores one all-site aggregate row for every complete
// minute, including zero-usage minutes. Unfiltered admin statistics can
// therefore scan at most one row per minute instead of every dimension row.
type LogStatMinuteTotal struct {
	BucketStart      int64 `json:"bucket_start" gorm:"primaryKey;autoIncrement:false;bigint;not null"`
	RequestCount     int64 `json:"request_count" gorm:"bigint;not null"`
	Quota            int64 `json:"quota" gorm:"bigint;not null"`
	PromptTokens     int64 `json:"prompt_tokens" gorm:"bigint;not null"`
	CompletionTokens int64 `json:"completion_tokens" gorm:"bigint;not null"`
}

// LogStatRollupState 记录聚合覆盖状态：
//   - Watermark：连续完整覆盖区间的上界（实时任务每分钟推进）。
//   - BackfillCursor：连续完整覆盖区间的下界。回填由近及远执行，cursor 逐步
//     下降；日志清理后 cursor 会被抬升到清理点，保证门禁与真实数据一致。
//   - CoverageStart：回填目标下界（7 天窗口 + 1 小时缓冲），每整点由 prune
//     收缩推进；即使回填未完成也照常推进，防止聚合表无限增长。
//
// 查询可用区间 = [max(BackfillCursor, CoverageStart), Watermark)，
// 再加水位之后的短原始日志尾部。
type LogStatRollupState struct {
	Name            string `json:"name" gorm:"type:varchar(64);primaryKey"`
	CoverageStart   int64  `json:"coverage_start" gorm:"bigint;not null"`
	Watermark       int64  `json:"watermark" gorm:"bigint;not null"`
	BackfillCursor  int64  `json:"backfill_cursor" gorm:"bigint;not null"`
	CountGeneration int64  `json:"-" gorm:"bigint;not null;default:1"`
	CleanupPending  bool   `json:"cleanup_pending"`
	// CleanupTarget 记录进行中清理的目标时间戳，供中断后的自愈对账使用：
	// 清理进程崩溃或对账失败时，实时聚合任务据此每分钟重试对账并清除
	// cleanup_pending，避免统计永久停在“滞后”。
	CleanupTarget int64 `json:"cleanup_target" gorm:"bigint"`
	UpdatedAt     int64 `json:"updated_at" gorm:"bigint;not null"`
}

type LogStatRollupFilter struct {
	StartTimestamp int64
	EndTimestamp   int64
	Username       string
	UsernameMatch  LogStatTextMatchMode
	TokenName      string
	ModelName      string
	ChannelID      int
	Group          string
}

type LogStatRollupAggregate struct {
	Quota            int64 `json:"quota"`
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

func (aggregate LogStatRollupAggregate) TotalTokens() int64 {
	return aggregate.PromptTokens + aggregate.CompletionTokens
}

func NewLogStatRollupDimensionKey(username string, tokenName string, modelName string, channelID int, group string) string {
	hash := sha256.New()
	fields := []string{username, tokenName, modelName, fmt.Sprintf("%d", channelID), group}
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func AggregateLogStatRollups(ctx context.Context, startTimestamp int64, endTimestamp int64) ([]LogStatRollup, error) {
	rollups, _, err := AggregateLogStatRollupsLimited(ctx, startTimestamp, endTimestamp, 0)
	return rollups, err
}

// AggregateLogStatRollupsLimited caps the number of grouped rows materialized
// in Go memory. SQL requests limit+1 rows so the caller can distinguish an
// exact result from truncation without first loading an unbounded result set.
// limit <= 0 keeps the unrestricted behavior for known-small ranges such as a
// single cleanup boundary minute.
func AggregateLogStatRollupsLimited(ctx context.Context, startTimestamp int64, endTimestamp int64, limit int) ([]LogStatRollup, bool, error) {
	if LOG_DB == nil {
		return nil, false, errors.New("log database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp {
		return nil, false, errors.New("invalid log stat rollup time range")
	}

	bucketExpression, err := logStatRollupBucketExpression()
	if err != nil {
		return nil, false, err
	}
	groupColumn := logGroupCol
	if groupColumn == "" {
		groupColumn = "`group`"
		if common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
			groupColumn = `"group"`
		}
	}

	type aggregateRow struct {
		BucketStart      int64
		Username         string
		TokenName        string
		ModelName        string
		ChannelID        int
		GroupName        string
		RequestCount     int64
		Quota            int64
		PromptTokens     int64
		CompletionTokens int64
	}
	var rows []aggregateRow
	query := fmt.Sprintf(
		`SELECT %s AS bucket_start,
			COALESCE(username, '') AS username,
			COALESCE(token_name, '') AS token_name,
			COALESCE(model_name, '') AS model_name,
			channel_id,
			COALESCE(%s, '') AS group_name,
			COUNT(*) AS request_count,
			COALESCE(SUM(quota), 0) AS quota,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens
		FROM logs
		WHERE type = ? AND created_at >= ? AND created_at < ?
		GROUP BY %s, username, token_name, model_name, channel_id, %s`,
		bucketExpression, groupColumn, bucketExpression, groupColumn,
	)
	args := []interface{}{LogTypeConsume, startTimestamp, endTimestamp}
	if limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, limit+1)
	}
	if err := LOG_DB.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	truncated := limit > 0 && len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}

	rollups := make([]LogStatRollup, 0, len(rows))
	for _, row := range rows {
		rollups = append(rollups, LogStatRollup{
			BucketStart:      row.BucketStart,
			DimensionKey:     NewLogStatRollupDimensionKey(row.Username, row.TokenName, row.ModelName, row.ChannelID, row.GroupName),
			Username:         row.Username,
			TokenName:        row.TokenName,
			ModelName:        row.ModelName,
			ChannelID:        row.ChannelID,
			Group:            row.GroupName,
			RequestCount:     row.RequestCount,
			Quota:            row.Quota,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
		})
	}
	return rollups, truncated, nil
}

func fillLogStatRollupDimensionKeys(rollups []LogStatRollup) {
	for i := range rollups {
		if rollups[i].DimensionKey == "" {
			rollups[i].DimensionKey = NewLogStatRollupDimensionKey(
				rollups[i].Username,
				rollups[i].TokenName,
				rollups[i].ModelName,
				rollups[i].ChannelID,
				rollups[i].Group,
			)
		}
	}
}

func buildLogStatMinuteTotals(startTimestamp int64, endTimestamp int64, rollups []LogStatRollup) ([]LogStatMinuteTotal, error) {
	if startTimestamp < 0 || endTimestamp <= startTimestamp ||
		startTimestamp%60 != 0 || endTimestamp%60 != 0 {
		return nil, errors.New("invalid log stat minute total time range")
	}
	minuteCount := (endTimestamp - startTimestamp) / 60
	totals := make([]LogStatMinuteTotal, minuteCount)
	for i := range totals {
		totals[i].BucketStart = startTimestamp + int64(i)*60
	}
	for i := range rollups {
		bucketStart := rollups[i].BucketStart
		if bucketStart < startTimestamp || bucketStart >= endTimestamp || bucketStart%60 != 0 {
			return nil, errors.New("log stat rollup bucket is outside minute total range")
		}
		total := &totals[(bucketStart-startTimestamp)/60]
		total.RequestCount += rollups[i].RequestCount
		total.Quota += rollups[i].Quota
		total.PromptTokens += rollups[i].PromptTokens
		total.CompletionTokens += rollups[i].CompletionTokens
	}
	return totals, nil
}

// AggregateLogStatMinuteTotalsFromRollups builds upgrade/backfill data from the
// already materialized dimension rollups. It deliberately avoids rescanning
// the much larger raw logs table.
func AggregateLogStatMinuteTotalsFromRollups(ctx context.Context, startTimestamp int64, endTimestamp int64) ([]LogStatMinuteTotal, error) {
	if DB == nil {
		return nil, errors.New("main database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp ||
		startTimestamp%60 != 0 || endTimestamp%60 != 0 {
		return nil, errors.New("invalid log stat minute total time range")
	}
	var rollups []LogStatRollup
	err := DB.WithContext(ctx).Model(&LogStatRollup{}).
		Select(`bucket_start,
			COALESCE(SUM(request_count), 0) AS request_count,
			COALESCE(SUM(quota), 0) AS quota,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens`).
		Where("bucket_start >= ? AND bucket_start < ?", startTimestamp, endTimestamp).
		Group("bucket_start").
		Scan(&rollups).Error
	if err != nil {
		return nil, err
	}
	return buildLogStatMinuteTotals(startTimestamp, endTimestamp, rollups)
}

func ReplaceLogStatRollups(ctx context.Context, startTimestamp int64, endTimestamp int64, rollups []LogStatRollup) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp {
		return errors.New("invalid log stat rollup time range")
	}
	fillLogStatRollupDimensionKeys(rollups)
	for i := range rollups {
		if rollups[i].BucketStart < startTimestamp || rollups[i].BucketStart >= endTimestamp {
			return errors.New("log stat rollup bucket is outside replacement range")
		}
	}
	totals, err := buildLogStatMinuteTotals(startTimestamp, endTimestamp, rollups)
	if err != nil {
		return err
	}

	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_start >= ? AND bucket_start < ?", startTimestamp, endTimestamp).
			Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := upsertLogStatRollups(tx, rollups); err != nil {
			return err
		}
		return replaceLogStatMinuteTotals(tx, startTimestamp, endTimestamp, totals)
	})
}

func UpsertLogStatRollups(ctx context.Context, rollups []LogStatRollup) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	fillLogStatRollupDimensionKeys(rollups)
	return upsertLogStatRollups(DB.WithContext(ctx), rollups)
}

func UpsertLogStatMinuteTotals(ctx context.Context, totals []LogStatMinuteTotal) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	return upsertLogStatMinuteTotals(DB.WithContext(ctx), totals)
}

func upsertLogStatRollups(tx *gorm.DB, rollups []LogStatRollup) error {
	if len(rollups) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "bucket_start"}, {Name: "dimension_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"username",
			"token_name",
			"model_name",
			"channel_id",
			"group",
			"request_count",
			"quota",
			"prompt_tokens",
			"completion_tokens",
		}),
	}).CreateInBatches(&rollups, 500).Error
}

func upsertLogStatMinuteTotals(tx *gorm.DB, totals []LogStatMinuteTotal) error {
	if len(totals) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "bucket_start"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"request_count",
			"quota",
			"prompt_tokens",
			"completion_tokens",
		}),
	}).CreateInBatches(&totals, 500).Error
}

func replaceLogStatMinuteTotals(tx *gorm.DB, startTimestamp int64, endTimestamp int64, totals []LogStatMinuteTotal) error {
	if err := tx.Where("bucket_start >= ? AND bucket_start < ?", startTimestamp, endTimestamp).
		Delete(&LogStatMinuteTotal{}).Error; err != nil {
		return err
	}
	return upsertLogStatMinuteTotals(tx, totals)
}

func GetLogStatRollupState(ctx context.Context, name string) (*LogStatRollupState, error) {
	if DB == nil {
		return nil, errors.New("main database is not initialized")
	}
	var state LogStatRollupState
	err := DB.WithContext(ctx).Where("name = ?", name).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func SaveLogStatRollupState(ctx context.Context, state *LogStatRollupState) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if state == nil || state.Name == "" {
		return errors.New("log stat rollup state name is required")
	}
	state.UpdatedAt = common.GetTimestamp()
	return DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"coverage_start", "watermark", "backfill_cursor", "cleanup_pending", "cleanup_target", "updated_at"}),
	}).Create(state).Error
}

func BeginLogStatCleanup(ctx context.Context, targetTimestamp int64) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	return DB.WithContext(ctx).Model(&LogStatRollupState{}).
		Where("name = ?", LogStatRollupStateName).
		Updates(map[string]interface{}{
			"cleanup_pending": true,
			"cleanup_target":  targetTimestamp,
			"updated_at":      common.GetTimestamp(),
		}).Error
}

// ReplaceLogStatRollupsAndAdvanceWatermark 在同一事务内完成聚合覆盖写与
// watermark 推进，避免“聚合已写、水位未推进”的崩溃窗口。仅更新 watermark
// 字段，不触碰回填任务并发维护的 backfill_cursor。
func ReplaceLogStatRollupsAndAdvanceWatermark(ctx context.Context, startTimestamp int64, endTimestamp int64, rollups []LogStatRollup, name string, watermark int64) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp {
		return errors.New("invalid log stat rollup time range")
	}
	if name == "" || watermark <= 0 {
		return errors.New("invalid log stat rollup watermark")
	}
	fillLogStatRollupDimensionKeys(rollups)
	for i := range rollups {
		if rollups[i].BucketStart < startTimestamp || rollups[i].BucketStart >= endTimestamp {
			return errors.New("log stat rollup bucket is outside replacement range")
		}
	}
	totals, err := buildLogStatMinuteTotals(startTimestamp, endTimestamp, rollups)
	if err != nil {
		return err
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_start >= ? AND bucket_start < ?", startTimestamp, endTimestamp).
			Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := upsertLogStatRollups(tx, rollups); err != nil {
			return err
		}
		if err := replaceLogStatMinuteTotals(tx, startTimestamp, endTimestamp, totals); err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND watermark < ?", name, watermark).
			Updates(map[string]interface{}{
				"watermark":  watermark,
				"updated_at": common.GetTimestamp(),
			}).Error; err != nil {
			return err
		}
		return tx.Model(&LogStatRollupState{}).
			Where("name = ? AND watermark < ?", LogStatMinuteTotalStateName, watermark).
			Updates(map[string]interface{}{
				"watermark":  watermark,
				"updated_at": common.GetTimestamp(),
			}).Error
	})
}

// ReplaceLogStatRollupsAndLowerCursor atomically commits one backfill chunk
// and moves the continuous coverage lower bound. The state row is locked and
// compared with the chunk end, so a cleanup/prune generation change cannot be
// overwritten by stale in-flight aggregation.
func ReplaceLogStatRollupsAndLowerCursor(ctx context.Context, startTimestamp int64, endTimestamp int64, rollups []LogStatRollup, name string) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp || name == "" {
		return errors.New("invalid log stat backfill range")
	}
	fillLogStatRollupDimensionKeys(rollups)
	for i := range rollups {
		if rollups[i].BucketStart < startTimestamp || rollups[i].BucketStart >= endTimestamp {
			return errors.New("log stat rollup bucket is outside replacement range")
		}
	}
	totals, err := buildLogStatMinuteTotals(startTimestamp, endTimestamp, rollups)
	if err != nil {
		return err
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LogStatRollupState
		if err := lockForUpdate(tx).
			Where("name = ?", name).
			First(&state).Error; err != nil {
			return err
		}
		if state.BackfillCursor != endTimestamp || startTimestamp < state.CoverageStart {
			return ErrLogStatRollupStateChanged
		}
		if err := tx.Where("bucket_start >= ? AND bucket_start < ?", startTimestamp, endTimestamp).
			Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := upsertLogStatRollups(tx, rollups); err != nil {
			return err
		}
		if err := replaceLogStatMinuteTotals(tx, startTimestamp, endTimestamp, totals); err != nil {
			return err
		}
		result := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND backfill_cursor = ?", name, endTimestamp).
			Updates(map[string]interface{}{
				"backfill_cursor": startTimestamp,
				"updated_at":      common.GetTimestamp(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// SQLite 无行锁时的兜底：cursor 在读取与更新之间被并发改动，
			// 回滚整个事务而不是留下“聚合已写、cursor 未动”的中间态。
			return ErrLogStatRollupStateChanged
		}
		return tx.Model(&LogStatRollupState{}).
			Where("name = ? AND backfill_cursor = ?", LogStatMinuteTotalStateName, endTimestamp).
			Updates(map[string]interface{}{
				"backfill_cursor": startTimestamp,
				"updated_at":      common.GetTimestamp(),
			}).Error
	})
}

func ReplaceLogStatMinuteTotalsAndLowerCursor(ctx context.Context, startTimestamp int64, endTimestamp int64, totals []LogStatMinuteTotal, name string) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if startTimestamp < 0 || endTimestamp <= startTimestamp || name == "" ||
		startTimestamp%60 != 0 || endTimestamp%60 != 0 {
		return errors.New("invalid log stat minute total backfill range")
	}
	expectedCount := (endTimestamp - startTimestamp) / 60
	if int64(len(totals)) != expectedCount {
		return errors.New("log stat minute total backfill is incomplete")
	}
	for i := range totals {
		if totals[i].BucketStart != startTimestamp+int64(i)*60 {
			return errors.New("log stat minute total bucket is outside replacement range")
		}
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LogStatRollupState
		if err := lockForUpdate(tx).
			Where("name = ?", name).
			First(&state).Error; err != nil {
			return err
		}
		if state.BackfillCursor != endTimestamp || startTimestamp < state.CoverageStart {
			return ErrLogStatRollupStateChanged
		}
		if err := replaceLogStatMinuteTotals(tx, startTimestamp, endTimestamp, totals); err != nil {
			return err
		}
		result := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND backfill_cursor = ?", name, endTimestamp).
			Updates(map[string]interface{}{
				"backfill_cursor": startTimestamp,
				"updated_at":      common.GetTimestamp(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrLogStatRollupStateChanged
		}
		return nil
	})
}

// ReconcileLogStatRollupsAfterLogCleanup 在日志清理完成后让聚合数据与原始
// 日志重新一致：删除清理点之前的分钟桶、重算清理点所在的边界分钟，并把
// 覆盖下界（coverage_start / backfill_cursor）抬升到清理边界，防止统计
// 偏高或回填把已删除区间写成 0。
func ReconcileLogStatRollupsAfterLogCleanup(ctx context.Context, targetTimestamp int64) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	if targetTimestamp <= 0 {
		return nil
	}
	bucketCut := targetTimestamp - targetTimestamp%60
	boundaryEnd := bucketCut + 60

	// 边界分钟只从清理目标时间开始重算：成功路径下 [bucketCut, target) 的日志
	// 已全部删除，结果与整分钟聚合相同；清理中断时该区间处于“部分删除”的
	// 污染状态，计入会得到既非清理前也非清理后的任意中间值，必须排除。
	// 重跑清理后 reconcile 会再次收敛。
	boundaryRollups, err := AggregateLogStatRollups(ctx, targetTimestamp, boundaryEnd)
	if err != nil {
		return err
	}
	boundaryTotals, err := buildLogStatMinuteTotals(bucketCut, boundaryEnd, boundaryRollups)
	if err != nil {
		return err
	}

	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_start < ?", bucketCut).Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("bucket_start >= ? AND bucket_start < ?", bucketCut, boundaryEnd).
			Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := upsertLogStatRollups(tx, boundaryRollups); err != nil {
			return err
		}
		if err := tx.Where("bucket_start < ?", bucketCut).Delete(&LogStatMinuteTotal{}).Error; err != nil {
			return err
		}
		if err := replaceLogStatMinuteTotals(tx, bucketCut, boundaryEnd, boundaryTotals); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND coverage_start < ?", LogStatRollupStateName, bucketCut).
			Updates(map[string]interface{}{
				"coverage_start": bucketCut,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ?", LogStatRollupStateName).
			Updates(map[string]interface{}{
				"backfill_cursor": gorm.Expr("CASE WHEN backfill_cursor < ? THEN ? ELSE backfill_cursor END", bucketCut, bucketCut),
				"cleanup_pending": false,
				"cleanup_target":  0,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND coverage_start < ?", LogStatMinuteTotalStateName, bucketCut).
			Updates(map[string]interface{}{
				"coverage_start": bucketCut,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ?", LogStatMinuteTotalStateName).
			Updates(map[string]interface{}{
				"backfill_cursor": gorm.Expr("CASE WHEN backfill_cursor < ? THEN ? ELSE backfill_cursor END", bucketCut, bucketCut),
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return ReconcileTokenUsageSourceStateAfterLogCleanup(tx, targetTimestamp)
	})
}

// ClearLogStatCleanupPending 清除无目标信息的遗留清理标志。仅用于
// cleanup_target 为 0 的异常状态（例如旧版本升级期间中断的清理），
// 有目标时应走 ReconcileLogStatRollupsAfterLogCleanup 完成对账。
func ClearLogStatCleanupPending(ctx context.Context) error {
	if DB == nil {
		return errors.New("main database is not initialized")
	}
	return DB.WithContext(ctx).Model(&LogStatRollupState{}).
		Where("name = ? AND cleanup_pending = ?", LogStatRollupStateName, true).
		Updates(map[string]interface{}{
			"cleanup_pending": false,
			"cleanup_target":  0,
			"updated_at":      common.GetTimestamp(),
		}).Error
}

// PruneLogStatRollupsBefore 按保留边界删除过期分钟桶并推进 coverage_start。
// 即使回填尚未完成（或被停用）也照常执行：保留边界之下的区间已经出了
// 7 天窗口，收缩回填目标是正确行为，否则关闭回填后聚合表会无限增长。
// 调用方持有维护锁，与回填/清理天然互斥；删除与状态推进在同一事务内。
func PruneLogStatRollupsBefore(ctx context.Context, name string, coverageStart int64) (bool, error) {
	if DB == nil {
		return false, errors.New("main database is not initialized")
	}
	if name == "" || coverageStart <= 0 {
		return false, errors.New("invalid log stat rollup coverage start")
	}
	pruned := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LogStatRollupState
		if err := lockForUpdate(tx).
			Where("name = ?", name).
			First(&state).Error; err != nil {
			return err
		}
		if state.CoverageStart >= coverageStart {
			return nil
		}
		if err := tx.Where("bucket_start < ?", coverageStart).Delete(&LogStatRollup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("bucket_start < ?", coverageStart).Delete(&LogStatMinuteTotal{}).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ?", name).
			Updates(map[string]interface{}{
				"coverage_start": coverageStart,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND backfill_cursor < ?", name, coverageStart).
			Updates(map[string]interface{}{
				"backfill_cursor": coverageStart,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND coverage_start < ?", LogStatMinuteTotalStateName, coverageStart).
			Updates(map[string]interface{}{
				"coverage_start": coverageStart,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&LogStatRollupState{}).
			Where("name = ? AND backfill_cursor < ?", LogStatMinuteTotalStateName, coverageStart).
			Updates(map[string]interface{}{
				"backfill_cursor": coverageStart,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		pruned = true
		return nil
	})
	return pruned, err
}

func QueryLogStatRollups(ctx context.Context, filter LogStatRollupFilter) (LogStatRollupAggregate, error) {
	var aggregate LogStatRollupAggregate
	if DB == nil {
		return aggregate, errors.New("main database is not initialized")
	}
	tx := DB.WithContext(ctx).Model(&LogStatRollup{}).
		Select(`COALESCE(SUM(quota), 0) AS quota,
			COALESCE(SUM(request_count), 0) AS request_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens`)
	if filter.StartTimestamp != 0 {
		tx = tx.Where("bucket_start >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		tx = tx.Where("bucket_start < ?", filter.EndTimestamp)
	}
	var err error
	if tx, err = applyLogStatRollupTextFilter(tx, "username", filter.Username, filter.UsernameMatch); err != nil {
		return aggregate, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("token_name = ?", filter.TokenName)
	}
	if tx, err = applyLogStatRollupTextFilter(tx, "model_name", filter.ModelName, LogStatTextMatchPattern); err != nil {
		return aggregate, err
	}
	if filter.ChannelID != 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.Group != "" {
		groupColumn := commonGroupCol
		if groupColumn == "" {
			groupColumn = "`group`"
			if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
				groupColumn = `"group"`
			}
		}
		tx = tx.Where(groupColumn+" = ?", filter.Group)
	}
	return aggregate, tx.Scan(&aggregate).Error
}

func QueryLogStatMinuteTotals(ctx context.Context, startTimestamp int64, endTimestamp int64) (LogStatRollupAggregate, error) {
	var aggregate LogStatRollupAggregate
	if DB == nil {
		return aggregate, errors.New("main database is not initialized")
	}
	tx := DB.WithContext(ctx).Model(&LogStatMinuteTotal{}).
		Select(`COALESCE(SUM(quota), 0) AS quota,
			COALESCE(SUM(request_count), 0) AS request_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens`)
	if startTimestamp != 0 {
		tx = tx.Where("bucket_start >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("bucket_start < ?", endTimestamp)
	}
	return aggregate, tx.Scan(&aggregate).Error
}

func applyLogStatRollupTextFilter(tx *gorm.DB, column string, value string, matchMode LogStatTextMatchMode) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if !usesLogStatPattern(value, matchMode) {
		return tx.Where(column+" = ?", value), nil
	}
	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return nil, err
	}
	return tx.Where(column+" LIKE ? ESCAPE '!'", pattern), nil
}

func logStatRollupBucketExpression() (string, error) {
	switch common.LogDatabaseType() {
	case common.DatabaseTypeSQLite:
		return "(created_at - (created_at % 60))", nil
	case common.DatabaseTypeMySQL:
		return "(created_at - MOD(created_at, 60))", nil
	case common.DatabaseTypePostgreSQL:
		return "(created_at - MOD(created_at, 60))", nil
	case common.DatabaseTypeClickHouse:
		return "(intDiv(created_at, 60) * 60)", nil
	default:
		return "", fmt.Errorf("unsupported log database type %q", common.LogDatabaseType())
	}
}
