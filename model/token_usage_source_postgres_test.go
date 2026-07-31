//go:build integration

package model

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTokenUsageSourcePostgreSQLConcurrentMergeAndPurge(t *testing.T) {
	dsn := os.Getenv("TOKEN_USAGE_SOURCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TOKEN_USAGE_SOURCE_TEST_POSTGRES_DSN is not configured")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQLDB, err := adminDB.DB()
	require.NoError(t, err)
	require.NoError(t, adminSQLDB.Ping())
	schema := "token_source_test_" +
		strings.ToLower(strings.ReplaceAll(common.GetUUID(), "-", ""))
	require.NoError(t, adminDB.Exec(fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)).Error)

	isolatedDSN := dsn
	if parsed, parseErr := url.Parse(dsn); parseErr == nil &&
		(parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		isolatedDSN = parsed.String()
	} else {
		isolatedDSN += " search_path=" + schema
	}
	postgresDB, err := gorm.Open(postgres.Open(isolatedDSN), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := postgresDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping())

	previousDB := DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	DB = postgresDB
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, previousLogType)
	t.Cleanup(func() {
		DB = previousDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, sqlDB.Close())
		require.NoError(t, adminDB.Exec(fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)).Error)
		require.NoError(t, adminSQLDB.Close())
	})

	require.NoError(t, postgresDB.AutoMigrate(
		&TokenUsageSource{},
		&TokenUsageSourceMeta{},
		&LogStatRollupState{},
	))

	const (
		tokenID = 97101
		userID  = 97201
		workers = 12
		base    = int64(1_900_000_000)
	)
	require.NoError(t, postgresDB.Create(&TokenUsageSourceMeta{
		TokenID:         tokenID,
		UserID:          userID,
		TrackingEnabled: true,
		TrackingStart:   base - 60,
	}).Error)
	require.NoError(t, postgresDB.Create(&LogStatRollupState{
		Name:            TokenUsageSourceStateName,
		CoverageStart:   base - 60,
		Watermark:       base,
		BackfillCursor:  base - 60,
		CountGeneration: tokenUsageSourceInitialCountGeneration,
		UpdatedAt:       base,
	}).Error)

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			<-start
			occurredAt := base + int64(offset)
			errs <- MergeDirectTokenUsageSourceGroups(
				context.Background(),
				[]TokenUsageSourceGroup{{
					UserID:        userID,
					TokenID:       tokenID,
					SourceKey:     NewTokenUsageSourceKey("198.51.100.71", "client/1.0"),
					IP:            "198.51.100.71",
					UserAgent:     "client/1.0",
					FirstSeenAt:   occurredAt,
					LastSeenAt:    occurredAt,
					LastSuccessAt: occurredAt,
					LastErrorAt:   occurredAt,
					SuccessCount:  2,
					ErrorCount:    1,
				}},
				500,
			)
		}(worker)
	}
	close(start)
	wait.Wait()
	close(errs)
	for mergeErr := range errs {
		require.NoError(t, mergeErr)
	}

	var source TokenUsageSource
	require.NoError(t, postgresDB.First(&source, "token_id = ?", tokenID).Error)
	assert.Equal(t, int64(workers*2), source.SuccessCount)
	assert.Equal(t, int64(workers), source.ErrorCount)

	raceStart := make(chan struct{})
	raceErrs := make(chan error, workers+1)
	wait = sync.WaitGroup{}
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			<-raceStart
			occurredAt := base + 100 + int64(offset)
			raceErrs <- MergeDirectTokenUsageSourceGroups(
				context.Background(),
				[]TokenUsageSourceGroup{{
					UserID:        userID,
					TokenID:       tokenID,
					SourceKey:     source.SourceKey,
					IP:            source.IP,
					UserAgent:     source.UserAgent,
					FirstSeenAt:   occurredAt,
					LastSeenAt:    occurredAt,
					LastSuccessAt: occurredAt,
					SuccessCount:  1,
				}},
				500,
			)
		}(worker)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-raceStart
		raceErrs <- postgresDB.Transaction(func(tx *gorm.DB) error {
			return PurgeTokenUsageSourcesTx(tx, tokenID, userID)
		})
	}()
	close(raceStart)
	wait.Wait()
	close(raceErrs)
	for raceErr := range raceErrs {
		require.NoError(t, raceErr)
	}

	var sourceCount int64
	require.NoError(t, postgresDB.Model(&TokenUsageSource{}).
		Where("token_id = ?", tokenID).
		Count(&sourceCount).Error)
	assert.Zero(t, sourceCount)
	var meta TokenUsageSourceMeta
	require.NoError(t, postgresDB.First(&meta, "token_id = ?", tokenID).Error)
	assert.False(t, meta.TrackingEnabled)
	assert.Positive(t, meta.PurgedAt)
}
