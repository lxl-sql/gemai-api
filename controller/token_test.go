package controller

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type tokenAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type tokenPageResponse struct {
	Items []tokenResponseItem `json:"items"`
}

type tokenResponseItem struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Key    string `json:"key"`
	Status int    `json:"status"`
}

type sqliteColumnInfo struct {
	Name string `gorm:"column:name"`
	Type string `gorm:"column:type"`
}

type legacyToken struct {
	Id                 int    `gorm:"primaryKey"`
	UserId             int    `gorm:"index"`
	Key                string `gorm:"column:key;type:char(48);uniqueIndex"`
	Status             int    `gorm:"default:1"`
	Name               string `gorm:"index"`
	CreatedTime        int64  `gorm:"bigint"`
	AccessedTime       int64  `gorm:"bigint"`
	ExpiredTime        int64  `gorm:"bigint;default:-1"`
	RemainQuota        int    `gorm:"default:0"`
	UnlimitedQuota     bool
	ModelLimitsEnabled bool
	ModelLimits        string  `gorm:"type:text"`
	AllowIps           *string `gorm:"default:''"`
	UsedQuota          int     `gorm:"default:0"`
	Group              string  `gorm:"column:group;default:''"`
	CrossGroupRetry    bool
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (legacyToken) TableName() string {
	return "tokens"
}

func openTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func migrateTokenControllerTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.AutoMigrate(&model.Token{}, &model.OperationLog{}); err != nil {
		t.Fatalf("failed to migrate token table: %v", err)
	}
}

func setupTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTokenControllerTestDB(t)
	migrateTokenControllerTestDB(t, db)
	return db
}

func openTokenControllerExternalDB(t *testing.T, dialect string, dsn string) (*gorm.DB, *bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	var (
		db     *gorm.DB
		dbType common.DatabaseType
		err    error
	)
	switch dialect {
	case "mysql":
		dbType = common.DatabaseTypeMySQL
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	case "postgres":
		dbType = common.DatabaseTypePostgreSQL
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	common.SetDatabaseTypes(dbType, dbType)
	if err != nil {
		t.Fatalf("failed to open %s db: %v", dialect, err)
	}

	model.DB = db
	model.LOG_DB = db

	if db.Migrator().HasTable("tokens") {
		t.Skipf("refusing to run %s migration compatibility test against external database because tokens table already exists", dialect)
	}

	managedTokensTable := new(bool)

	t.Cleanup(func() {
		if *managedTokensTable && db.Migrator().HasTable("tokens") {
			_ = db.Migrator().DropTable("tokens")
		}
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db, managedTokensTable
}

func seedToken(t *testing.T, db *gorm.DB, userID int, name string, rawKey string) *model.Token {
	t.Helper()

	token := &model.Token{
		UserId:         userID,
		Name:           name,
		Key:            rawKey,
		Status:         common.TokenStatusEnabled,
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	return token
}

func newAuthenticatedContext(t *testing.T, method string, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var requestBody *bytes.Reader
	if body != nil {
		payload, err := common.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		requestBody = bytes.NewReader(payload)
	} else {
		requestBody = bytes.NewReader(nil)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, requestBody)
	if body != nil {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Set("id", userID)
	return ctx, recorder
}

func decodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) tokenAPIResponse {
	t.Helper()

	var response tokenAPIResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode api response: %v", err)
	}
	return response
}

func TestUpsertTokenSecurityProfilePreservesOmittedRollingFields(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TokenSecurityProfile{}))
	require.NoError(t, model.UpsertTokenSecurityProfile(&model.TokenSecurityProfile{
		ScopeType:          model.TokenSecurityScopePlatform,
		SustainedRpm:       5,
		BurstCapacity:      2,
		UserSustainedRpm:   10,
		UserBurstCapacity:  3,
		UserMaxConcurrency: 4,
		UserHourlyQuota:    500,
		UserDailyQuota:     1000,
		MinimumRiskMode:    model.TokenRiskModeObserve,
	}))

	ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token-security-profile/", map[string]interface{}{
		"scope_type":             model.TokenSecurityScopePlatform,
		"scope_value":            "",
		"sustained_rps":          20,
		"burst_capacity":         40,
		"max_concurrency":        8,
		"max_quota_per_request":  0,
		"hourly_quota":           0,
		"daily_quota":            0,
		"max_distinct_models_5m": 0,
		"minimum_risk_mode":      model.TokenRiskModeNotify,
		"fail_closed":            false,
	}, 1)

	UpsertTokenSecurityProfile(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var stored model.TokenSecurityProfile
	require.NoError(t, db.Where("scope_type = ?", model.TokenSecurityScopePlatform).First(&stored).Error)
	assert.Zero(t, stored.SustainedRps)
	assert.Equal(t, 5, stored.SustainedRpm)
	assert.Equal(t, 10, stored.UserSustainedRpm)
	assert.Equal(t, 3, stored.UserBurstCapacity)
	assert.Equal(t, 4, stored.UserMaxConcurrency)
	assert.Equal(t, int64(500), stored.UserHourlyQuota)
	assert.Equal(t, int64(1000), stored.UserDailyQuota)
}

func TestUpsertTokenSecurityProfileTreatsNullAsOmittedAndZeroAsClear(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TokenSecurityProfile{}))
	require.NoError(t, model.UpsertTokenSecurityProfile(&model.TokenSecurityProfile{
		ScopeType:          model.TokenSecurityScopePlatform,
		SustainedRpm:       5,
		BurstCapacity:      2,
		UserSustainedRpm:   10,
		UserBurstCapacity:  3,
		UserMaxConcurrency: 4,
		UserHourlyQuota:    500,
		UserDailyQuota:     1000,
		MinimumRiskMode:    model.TokenRiskModeObserve,
	}))

	baseRequest := map[string]interface{}{
		"scope_type":             model.TokenSecurityScopePlatform,
		"scope_value":            "",
		"sustained_rps":          20,
		"burst_capacity":         40,
		"max_concurrency":        8,
		"max_quota_per_request":  0,
		"hourly_quota":           0,
		"daily_quota":            0,
		"max_distinct_models_5m": 0,
		"minimum_risk_mode":      model.TokenRiskModeNotify,
		"fail_closed":            false,
		"sustained_rpm":          nil,
		"user_sustained_rpm":     nil,
		"user_burst_capacity":    nil,
		"user_max_concurrency":   nil,
		"user_hourly_quota":      nil,
		"user_daily_quota":       nil,
	}
	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token-security-profile/",
		baseRequest,
		1,
	)
	UpsertTokenSecurityProfile(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var stored model.TokenSecurityProfile
	require.NoError(t, db.Where("scope_type = ?", model.TokenSecurityScopePlatform).First(&stored).Error)
	assert.Zero(t, stored.SustainedRps)
	assert.Equal(t, 5, stored.SustainedRpm)
	assert.Equal(t, 10, stored.UserSustainedRpm)
	assert.Equal(t, 3, stored.UserBurstCapacity)
	assert.Equal(t, 4, stored.UserMaxConcurrency)
	assert.Equal(t, int64(500), stored.UserHourlyQuota)
	assert.Equal(t, int64(1000), stored.UserDailyQuota)

	for _, field := range []string{
		"sustained_rpm",
		"user_sustained_rpm",
		"user_burst_capacity",
		"user_max_concurrency",
		"user_hourly_quota",
		"user_daily_quota",
	} {
		baseRequest[field] = 0
	}
	ctx, recorder = newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token-security-profile/",
		baseRequest,
		1,
	)
	UpsertTokenSecurityProfile(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	require.NoError(t, db.Where("scope_type = ?", model.TokenSecurityScopePlatform).First(&stored).Error)
	assert.Equal(t, 20, stored.SustainedRps)
	assert.Zero(t, stored.SustainedRpm)
	assert.Zero(t, stored.UserSustainedRpm)
	assert.Zero(t, stored.UserBurstCapacity)
	assert.Zero(t, stored.UserMaxConcurrency)
	assert.Zero(t, stored.UserHourlyQuota)
	assert.Zero(t, stored.UserDailyQuota)
}

func getSQLiteColumnType(t *testing.T, db *gorm.DB, tableName string, columnName string) string {
	t.Helper()

	var columns []sqliteColumnInfo
	if err := db.Raw("PRAGMA table_info(" + tableName + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("failed to inspect %s schema: %v", tableName, err)
	}

	for _, column := range columns {
		if column.Name == columnName {
			return strings.ToLower(column.Type)
		}
	}

	t.Fatalf("column %s not found in %s schema", columnName, tableName)
	return ""
}

func getTokenKeyColumnType(t *testing.T, db *gorm.DB, dialect string) string {
	t.Helper()

	switch dialect {
	case "sqlite":
		return getSQLiteColumnType(t, db, "tokens", "key")
	case "mysql":
		var columnType string
		if err := db.Raw(`SELECT COLUMN_TYPE FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			"tokens", "key").Scan(&columnType).Error; err != nil {
			t.Fatalf("failed to inspect mysql token key column: %v", err)
		}
		return strings.ToLower(columnType)
	case "postgres":
		var dataType string
		var maxLength sql.NullInt64
		if err := db.Raw(`SELECT data_type, character_maximum_length
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			"tokens", "key").Row().Scan(&dataType, &maxLength); err != nil {
			t.Fatalf("failed to inspect postgres token key column: %v", err)
		}
		switch strings.ToLower(dataType) {
		case "character varying":
			return fmt.Sprintf("varchar(%d)", maxLength.Int64)
		case "character":
			return fmt.Sprintf("char(%d)", maxLength.Int64)
		default:
			if maxLength.Valid {
				return fmt.Sprintf("%s(%d)", strings.ToLower(dataType), maxLength.Int64)
			}
			return strings.ToLower(dataType)
		}
	default:
		t.Fatalf("unsupported dialect %q", dialect)
		return ""
	}
}

func runTokenMigrationCompatibilityTest(t *testing.T, db *gorm.DB, dialect string, managedTokensTable *bool) {
	t.Helper()

	legacyKey := strings.Repeat("a", 48)
	longKey := strings.Repeat("b", 64)

	if err := db.AutoMigrate(&legacyToken{}); err != nil {
		t.Fatalf("failed to create legacy token schema: %v", err)
	}
	if managedTokensTable != nil {
		*managedTokensTable = true
	}
	if err := db.Create(&legacyToken{
		UserId:             7,
		Key:                legacyKey,
		Status:             common.TokenStatusEnabled,
		Name:               "legacy-token",
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		RemainQuota:        100,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		AllowIps:           common.GetPointer(""),
		UsedQuota:          0,
		Group:              "default",
		CrossGroupRetry:    false,
	}).Error; err != nil {
		t.Fatalf("failed to seed legacy token row: %v", err)
	}

	if got := getTokenKeyColumnType(t, db, dialect); got != "char(48)" {
		t.Fatalf("expected legacy key column type char(48), got %q", got)
	}

	migrateTokenControllerTestDB(t, db)

	if got := getTokenKeyColumnType(t, db, dialect); got != "varchar(128)" {
		t.Fatalf("expected migrated key column type varchar(128), got %q", got)
	}

	var migratedToken model.Token
	if err := db.First(&migratedToken, "name = ?", "legacy-token").Error; err != nil {
		t.Fatalf("failed to load migrated token row: %v", err)
	}
	if migratedToken.Key != legacyKey {
		t.Fatalf("expected migrated token key %q, got %q", legacyKey, migratedToken.Key)
	}
	if migratedToken.Name != "legacy-token" {
		t.Fatalf("expected migrated token name to be preserved, got %q", migratedToken.Name)
	}

	inserted := model.Token{
		UserId:             8,
		Name:               "long-token",
		Key:                longKey,
		Status:             common.TokenStatusEnabled,
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		RemainQuota:        200,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		AllowIps:           common.GetPointer(""),
		UsedQuota:          0,
		Group:              "default",
		CrossGroupRetry:    false,
	}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatalf("failed to insert long token after migration: %v", err)
	}

	var fetched model.Token
	if err := db.First(&fetched, "id = ?", inserted.Id).Error; err != nil {
		t.Fatalf("failed to fetch long token after migration: %v", err)
	}
	if fetched.Key != longKey {
		t.Fatalf("expected long token key %q, got %q", longKey, fetched.Key)
	}
}

func TestTokenAutoMigrateUsesVarchar128KeyColumn(t *testing.T) {
	db := setupTokenControllerTestDB(t)

	if got := getTokenKeyColumnType(t, db, "sqlite"); got != "varchar(128)" {
		t.Fatalf("expected key column type varchar(128), got %q", got)
	}
}

func TestTokenMigrationFromChar48ToVarchar128(t *testing.T) {
	db := openTokenControllerTestDB(t)
	runTokenMigrationCompatibilityTest(t, db, "sqlite", nil)
}

func TestTokenMigrationFromChar48ToVarchar128MySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run mysql migration compatibility test")
	}

	db, managedTokensTable := openTokenControllerExternalDB(t, "mysql", dsn)
	runTokenMigrationCompatibilityTest(t, db, "mysql", managedTokensTable)
}

func TestTokenMigrationFromChar48ToVarchar128Postgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres migration compatibility test")
	}

	db, managedTokensTable := openTokenControllerExternalDB(t, "postgres", dsn)
	runTokenMigrationCompatibilityTest(t, db, "postgres", managedTokensTable)
}

func TestGetAllTokensMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "list-token", "abcd1234efgh5678")
	seedToken(t, db, 2, "other-user-token", "zzzz1234yyyy5678")

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?p=1&size=10", nil, 1)
	GetAllTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var page tokenPageResponse
	if err := common.Unmarshal(response.Data, &page); err != nil {
		t.Fatalf("failed to decode token page response: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected exactly one token, got %d", len(page.Items))
	}
	if page.Items[0].Key != token.GetMaskedKey() {
		t.Fatalf("expected masked key %q, got %q", token.GetMaskedKey(), page.Items[0].Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("list response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestAddTokenNormalizesMissingExpirationToNeverExpire(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "token-owner"}).Error)

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/token/",
		map[string]any{
			"name":            "server-default-expiration",
			"unlimited_quota": true,
		},
		1,
	)
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var stored model.Token
	require.NoError(t, db.Where("user_id = ?", 1).First(&stored).Error)
	assert.Equal(t, int64(-1), stored.ExpiredTime)
}

func TestAddTokenRejectsPastExpiration(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "expired-token-owner"}).Error)

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/token/",
		map[string]any{
			"name":            "already-expired",
			"expired_time":    common.GetTimestamp() - 1,
			"unlimited_quota": true,
		},
		1,
	)
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSearchTokensMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "searchable-token", "ijkl1234mnop5678")

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/search?keyword=searchable-token&p=1&size=10", nil, 1)
	SearchTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var page tokenPageResponse
	if err := common.Unmarshal(response.Data, &page); err != nil {
		t.Fatalf("failed to decode search response: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected exactly one search result, got %d", len(page.Items))
	}
	if page.Items[0].Key != token.GetMaskedKey() {
		t.Fatalf("expected masked search key %q, got %q", token.GetMaskedKey(), page.Items[0].Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("search response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestGetTokenMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "detail-token", "qrst1234uvwx5678")

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/"+strconv.Itoa(token.Id), nil, 1)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	GetToken(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var detail tokenResponseItem
	if err := common.Unmarshal(response.Data, &detail); err != nil {
		t.Fatalf("failed to decode token detail response: %v", err)
	}
	if detail.Key != token.GetMaskedKey() {
		t.Fatalf("expected masked detail key %q, got %q", token.GetMaskedKey(), detail.Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("detail response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestUpdateTokenMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "editable-token", "yzab1234cdef5678")

	body := map[string]any{
		"id":                   token.Id,
		"name":                 "updated-token",
		"expired_time":         -1,
		"remain_quota":         100,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                "default",
		"cross_group_retry":    false,
	}

	ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, 1)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var detail tokenResponseItem
	if err := common.Unmarshal(response.Data, &detail); err != nil {
		t.Fatalf("failed to decode token update response: %v", err)
	}
	if detail.Key != token.GetMaskedKey() {
		t.Fatalf("expected masked update key %q, got %q", token.GetMaskedKey(), detail.Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("update response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestUpdateTokenRecordsQuotaAndSecurityPolicyChanges(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TokenSecurityPolicy{}))
	token := seedToken(t, db, 1, "audited-token", "audit1234token5678")
	require.NoError(t, db.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 50,
		HourlyQuota:        100,
		DailyQuota:         200,
		RiskMode:           model.TokenRiskModeObserve,
	}).Error)

	body := map[string]any{
		"id":                   token.Id,
		"name":                 token.Name,
		"expired_time":         -1,
		"remain_quota":         250,
		"unlimited_quota":      false,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                "default",
		"cross_group_retry":    false,
		"security_policy": map[string]any{
			"max_quota_per_request": 75,
			"hourly_quota":          150,
			"daily_quota":           300,
			"risk_mode":             model.TokenRiskModeNotify,
		},
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, 1)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)

	var operationLog model.OperationLog
	require.NoError(t, db.Where("action = ? AND target_id = ?", model.OpActionTokenUpdate, strconv.Itoa(token.Id)).
		Order("id desc").
		First(&operationLog).Error)
	var detail map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(operationLog.Detail, &detail))
	assert.Equal(t, float64(100), detail["remain_quota_before"])
	assert.Equal(t, float64(250), detail["remain_quota_after"])
	assert.Equal(t, true, detail["unlimited_quota_before"])
	assert.Equal(t, false, detail["unlimited_quota_after"])

	beforePolicy, ok := detail["security_policy_before"].(map[string]interface{})
	require.True(t, ok)
	afterPolicy, ok := detail["security_policy_after"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(200), beforePolicy["daily_quota"])
	assert.Equal(t, float64(300), afterPolicy["daily_quota"])
	assert.Equal(t, model.TokenRiskModeObserve, beforePolicy["risk_mode"])
	assert.Equal(t, model.TokenRiskModeNotify, afterPolicy["risk_mode"])
}

func TestDeleteTokenSecurityPolicyRecordsCommittedResetValues(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TokenSecurityPolicy{}))
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})
	token := seedToken(t, db, 1, "reset-audited-token", "reset1234token5678")
	require.NoError(t, db.Create(&model.TokenSecurityPolicy{
		TokenId:            token.Id,
		MaxQuotaPerRequest: 50,
		HourlyQuota:        100,
		DailyQuota:         200,
		RiskMode:           model.TokenRiskModeSuspend,
	}).Error)

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodDelete,
		"/api/token/"+strconv.Itoa(token.Id)+"/security-policy",
		nil,
		1,
	)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	DeleteTokenSecurityPolicy(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var responsePolicy model.TokenSecurityPolicy
	require.NoError(t, common.Unmarshal(response.Data, &responsePolicy))
	require.NotNil(t, responsePolicy.CacheSynchronized)
	assert.False(t, *responsePolicy.CacheSynchronized)

	var storedPolicy model.TokenSecurityPolicy
	require.NoError(t, db.First(&storedPolicy, "token_id = ?", token.Id).Error)
	assert.Equal(t, int64(0), storedPolicy.MaxQuotaPerRequest)
	assert.Equal(t, int64(0), storedPolicy.HourlyQuota)
	assert.Equal(t, int64(0), storedPolicy.DailyQuota)
	assert.Equal(t, model.TokenRiskModeObserve, storedPolicy.RiskMode)

	var operationLog model.OperationLog
	require.NoError(t, db.Where("action = ? AND target_id = ?", model.OpActionTokenUpdate, strconv.Itoa(token.Id)).
		Order("id desc").
		First(&operationLog).Error)
	var detail map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(operationLog.Detail, &detail))
	beforePolicy, ok := detail["security_policy_before"].(map[string]interface{})
	require.True(t, ok)
	afterPolicy, ok := detail["security_policy_after"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(200), beforePolicy["daily_quota"])
	assert.Equal(t, float64(0), afterPolicy["daily_quota"])
	assert.Equal(t, model.TokenRiskModeObserve, afterPolicy["risk_mode"])
	assert.Equal(t, false, afterPolicy["cache_synchronized"])
}

func TestUpdateTokenStatusRejectsSecurityPolicyPayload(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "status-token", "status1234token5678")

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/?status_only=true",
		map[string]any{
			"id":     token.Id,
			"status": common.TokenStatusDisabled,
			"security_policy": map[string]any{
				"fail_closed": false,
			},
		},
		1,
	)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "status_only")

	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
}

func TestUpdateTokenStatusRejectsUnsupportedState(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "invalid-status-token", "invalid1234status5678")

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/?status_only=true",
		map[string]any{"id": token.Id, "status": 99},
		1,
	)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
}

func TestTokenResponseUsesEffectiveStatus(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*model.Token)
		wantStatus int
	}{
		{
			name: "expired",
			configure: func(token *model.Token) {
				token.ExpiredTime = common.GetTimestamp() - 1
			},
			wantStatus: common.TokenStatusExpired,
		},
		{
			name: "exhausted",
			configure: func(token *model.Token) {
				token.UnlimitedQuota = false
				token.RemainQuota = 0
			},
			wantStatus: common.TokenStatusExhausted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			token := seedToken(t, db, 1, "effective-status-token", "effective1234token5678")
			test.configure(token)
			require.NoError(t, db.Save(token).Error)

			ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/"+strconv.Itoa(token.Id), nil, 1)
			ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
			GetToken(ctx)

			response := decodeAPIResponse(t, recorder)
			require.True(t, response.Success, response.Message)
			var detail tokenResponseItem
			require.NoError(t, common.Unmarshal(response.Data, &detail))
			assert.Equal(t, test.wantStatus, detail.Status)
		})
	}
}

func TestRotateTokenRejectsUnavailableEffectiveStates(t *testing.T) {
	require.NoError(t, i18n.Init())
	tests := []struct {
		name      string
		configure func(*model.Token)
	}{
		{
			name: "disabled",
			configure: func(token *model.Token) {
				token.Status = common.TokenStatusDisabled
			},
		},
		{
			name: "expired",
			configure: func(token *model.Token) {
				token.ExpiredTime = common.GetTimestamp() - 1
			},
		},
		{
			name: "exhausted",
			configure: func(token *model.Token) {
				token.UnlimitedQuota = false
				token.RemainQuota = 0
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			token := seedToken(t, db, 1, "unavailable-token", "unavailable1234token5678")
			test.configure(token)
			require.NoError(t, db.Save(token).Error)
			originalKey := token.Key

			ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/rotate", nil, 1)
			ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
			RotateToken(ctx)

			response := decodeAPIResponse(t, recorder)
			assert.False(t, response.Success)
			assert.NotEmpty(t, response.Message)

			var stored model.Token
			require.NoError(t, db.First(&stored, token.Id).Error)
			assert.Equal(t, originalKey, stored.Key)
		})
	}
}

func TestExpiredTokenMustBeRenewedBeforeRotation(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "rotation-recovery-owner"}).Error)

	createContext, createRecorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/token/",
		map[string]any{
			"name":            "rotation-recovery",
			"expired_time":    -1,
			"unlimited_quota": true,
		},
		1,
	)
	AddToken(createContext)
	createResponse := decodeAPIResponse(t, createRecorder)
	require.True(t, createResponse.Success, createResponse.Message)
	var issued struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	require.NoError(t, common.Unmarshal(createResponse.Data, &issued))
	require.NotZero(t, issued.ID)
	require.NotEmpty(t, issued.Key)

	var stored model.Token
	require.NoError(t, db.First(&stored, issued.ID).Error)
	require.NoError(t, db.Model(&model.Token{}).
		Where("id = ?", issued.ID).
		Update("expired_time", common.GetTimestamp()-1).Error)

	rejectedContext, rejectedRecorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/token/"+strconv.Itoa(issued.ID)+"/rotate",
		nil,
		1,
	)
	rejectedContext.Params = gin.Params{{Key: "id", Value: strconv.Itoa(issued.ID)}}
	RotateToken(rejectedContext)
	rejectedResponse := decodeAPIResponse(t, rejectedRecorder)
	assert.False(t, rejectedResponse.Success)
	require.NoError(t, db.First(&stored, issued.ID).Error)
	assert.Equal(t, issued.Key, stored.Key)

	updateContext, updateRecorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/",
		map[string]any{
			"id":                   issued.ID,
			"name":                 stored.Name,
			"expired_time":         -1,
			"remain_quota":         stored.RemainQuota,
			"unlimited_quota":      true,
			"model_limits_enabled": stored.ModelLimitsEnabled,
			"model_limits":         stored.ModelLimits,
			"allow_ips":            stored.AllowIps,
			"group":                stored.Group,
			"cross_group_retry":    stored.CrossGroupRetry,
		},
		1,
	)
	UpdateToken(updateContext)
	updateResponse := decodeAPIResponse(t, updateRecorder)
	require.True(t, updateResponse.Success, updateResponse.Message)

	rotateContext, rotateRecorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/token/"+strconv.Itoa(issued.ID)+"/rotate",
		nil,
		1,
	)
	rotateContext.Params = gin.Params{{Key: "id", Value: strconv.Itoa(issued.ID)}}
	RotateToken(rotateContext)
	rotateResponse := decodeAPIResponse(t, rotateRecorder)
	require.True(t, rotateResponse.Success, rotateResponse.Message)
	var rotated struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	require.NoError(t, common.Unmarshal(rotateResponse.Data, &rotated))
	assert.Equal(t, issued.ID, rotated.ID)
	assert.NotEqual(t, issued.Key, rotated.Key)

	_, err := model.ValidateUserToken(issued.Key)
	assert.ErrorIs(t, err, model.ErrTokenInvalid)
	authenticated, err := model.ValidateUserToken(rotated.Key)
	require.NoError(t, err)
	assert.Equal(t, issued.ID, authenticated.Id)
}

func TestEnableTokenRejectsDerivedExpiredState(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "expired-enabled-status", "expired1234enabled5678")
	token.ExpiredTime = common.GetTimestamp() - 1
	require.NoError(t, db.Save(token).Error)

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/?status_only=true",
		map[string]any{"id": token.Id, "status": common.TokenStatusEnabled},
		1,
	)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.NotEmpty(t, response.Message)
}

func TestUpdateTokenReportsSuccessWhenDatabaseCommittedButCacheIsUnavailable(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "cache-pending-update", "cache1234pending5678")
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/?status_only=true",
		map[string]any{"id": token.Id, "status": common.TokenStatusDisabled},
		1,
	)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.True(t, response.Success, response.Message)
	assert.NotEmpty(t, response.Message)
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
}

func TestDeleteTokenReportsSuccessWhenDatabaseCommittedButCacheIsUnavailable(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "cache-pending-delete", "delete1234pending5678")
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	closedRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	require.NoError(t, closedRedis.Close())
	common.RedisEnabled = true
	common.RDB = closedRedis
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodDelete,
		"/api/token/"+strconv.Itoa(token.Id),
		nil,
		1,
	)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	DeleteToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.True(t, response.Success, response.Message)
	assert.NotEmpty(t, response.Message)
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestTokenKeyMetadataBackfillPreservesRollingCompatibility(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	const rawKey = "owner1234token5678"
	token := seedToken(t, db, 1, "owned-token", rawKey)

	require.NoError(t, model.BackfillTokenKeyMetadata())

	var stored model.Token
	require.NoError(t, db.Unscoped().First(&stored, token.Id).Error)
	require.NotNil(t, stored.KeyHash)
	assert.Equal(t, rawKey, stored.Key)
	assert.Equal(t, common.GenerateHMAC(rawKey), *stored.KeyHash)
	assert.Equal(t, model.MaskTokenKey(rawKey), stored.KeyHint)

	authenticated, err := model.ValidateUserToken(rawKey)
	require.NoError(t, err)
	assert.Equal(t, token.Id, authenticated.Id)
	assert.Equal(t, rawKey, authenticated.PlainKey)
}

func TestTokenCredentialWritesRemainReadableDuringRollingDeployment(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	const rawKey = "created1234token5678"
	token := &model.Token{
		UserId:         1,
		Name:           "created-token",
		Key:            rawKey,
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}

	require.NoError(t, token.Insert())

	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.NotNil(t, stored.KeyHash)
	assert.Equal(t, rawKey, stored.Key)
	assert.Equal(t, common.GenerateHMAC(rawKey), *stored.KeyHash)
	assert.Equal(t, model.MaskTokenKey(rawKey), stored.KeyHint)

	const rotatedKey = "rotated1234token5678"
	rotated, err := model.RotateTokenKey(token.Id, token.UserId, rotatedKey)
	require.NoError(t, err)
	assert.Equal(t, rotatedKey, rotated.GetFullKey())

	require.NoError(t, db.First(&stored, token.Id).Error)
	require.NotNil(t, stored.KeyHash)
	assert.Equal(t, rotatedKey, stored.Key)
	assert.Equal(t, common.GenerateHMAC(rotatedKey), *stored.KeyHash)
	assert.Equal(t, model.MaskTokenKey(rotatedKey), stored.KeyHint)
}
