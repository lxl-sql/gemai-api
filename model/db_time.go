package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
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
	return ts, nil
}
