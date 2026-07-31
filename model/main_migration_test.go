package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestValidateTokenKeyMetadataSchemaRejectsDDLOnPopulatedTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:token-key-schema-guard?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE tokens (id integer primary key, key text)").Error)
	require.NoError(t, db.Exec("INSERT INTO tokens (id, key) VALUES (1, 'legacy-key')").Error)

	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	err = validateTokenKeyMetadataSchema()
	require.ErrorContains(t, err, "apply the documented PostgreSQL online DDL")
}

func TestValidateTokenKeyMetadataSchemaAllowsEmptyTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:empty-token-key-schema-guard?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE tokens (id integer primary key, key text)").Error)

	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, validateTokenKeyMetadataSchema())
}

func TestValidateTokenKeyMetadataSchemaAllowsPopulatedReadyTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ready-token-key-schema-guard?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}))
	require.NoError(t, db.Create(&Token{UserId: 1, Key: "ready-key", Name: "ready"}).Error)

	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, validateTokenKeyMetadataSchema())
}
