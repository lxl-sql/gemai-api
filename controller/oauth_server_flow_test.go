package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type oauthServerFlowFixture struct {
	app    *model.OAuthApp
	code   *model.OAuthAuthorizationCode
	secret string
	userID int
}

func newOAuthServerFlowFixture(t *testing.T, appLimit int) oauthServerFlowFixture {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open("file:"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.OAuthApp{},
		&model.OAuthAuthorizationCode{},
		&model.OAuthGrant{},
		&model.OAuthRefreshTokenHistory{},
		&model.OperationLog{},
	))
	secret := "oauth-flow-secret"
	secretHash, err := common.Password2Hash(secret)
	require.NoError(t, err)
	app := &model.OAuthApp{
		Name:             "Flow Test",
		ClientId:         "gai_flow_test",
		ClientSecretHash: secretHash,
		ClientType:       model.OAuthClientTypeLegacy,
		RedirectUris:     `["https://tool.example.com/callback"]`,
		UserId:           1,
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(app).Error)
	user := &model.User{
		Username:    "oauth-flow-user",
		Password:    secretHash,
		DisplayName: "OAuth Flow User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
	}
	require.NoError(t, db.Create(user).Error)
	code := &model.OAuthAuthorizationCode{
		Code:        "oauth-flow-code",
		ClientId:    app.ClientId,
		UserId:      user.Id,
		RedirectUri: "https://tool.example.com/callback",
		Scope:       "profile",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	require.NoError(t, db.Create(code).Error)
	restore := func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	}
	t.Cleanup(restore)
	return oauthServerFlowFixture{app: app, code: code, secret: secret, userID: user.Id}
}

func performOAuthTokenRequest(t *testing.T, fixture oauthServerFlowFixture) *httptest.ResponseRecorder {
	return performOAuthTokenRequestWithSecret(t, fixture, fixture.secret)
}

func performOAuthTokenRequestWithSecret(
	t *testing.T,
	fixture oauthServerFlowFixture,
	secret string,
) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {secret},
		"redirect_uri":  {fixture.code.RedirectUri},
	}
	return performOAuthTokenFormRequest(t, form, "", "")
}

func performOAuthTokenFormRequest(
	t *testing.T,
	form url.Values,
	basicClientId string,
	basicClientSecret string,
) *httptest.ResponseRecorder {
	return performOAuthTokenFormRequestWithPrefer(t, form, basicClientId, basicClientSecret, "")
}

func performOAuthTokenFormRequestWithPrefer(
	t *testing.T,
	form url.Values,
	basicClientId string,
	basicClientSecret string,
	prefer string,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/oauth-server/token", middleware.DisableCache(), OAuthServerToken)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/oauth-server/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicClientId != "" || basicClientSecret != "" {
		request.SetBasicAuth(basicClientId, basicClientSecret)
	}
	if prefer != "" {
		request.Header.Set("Prefer", prefer)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func startOAuthQueueForControllerTest(t *testing.T) {
	t.Helper()
	redisURL := os.Getenv("RATE_LIMIT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("RATE_LIMIT_REDIS_TEST_URL is not configured")
	}
	namespace := "oauth_controller_test_" + common.GetRandomString(8)
	for key, value := range map[string]string{
		"OAUTH_QUEUE_ENABLE":                "true",
		"OAUTH_QUEUE_NAMESPACE":             namespace,
		"OAUTH_QUEUE_REDIS_URLS":            redisURL,
		"OAUTH_QUEUE_REDIS_CLUSTER_ADDRS":   "",
		"OAUTH_QUEUE_REDIS_SENTINEL_ADDRS":  "",
		"OAUTH_QUEUE_REDIS_SENTINEL_MASTER": "",
		"OAUTH_QUEUE_COORDINATOR_REDIS_URL": "",
	} {
		t.Setenv(key, value)
	}
	require.NoError(t, service.StartOAuthExchangeQueue())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.StopOAuthExchangeQueue(ctx)
		options, err := redis.ParseURL(redisURL)
		if err != nil {
			return
		}
		client := redis.NewClient(options)
		defer client.Close()
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(ctx, cursor, namespace+":*", 100).Result()
			if scanErr != nil {
				return
			}
			if len(keys) > 0 {
				_ = client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
}

func TestOAuthUserInfoLimitsOnlyTheSameApplicationUser(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 1)
	startOAuthQueueForControllerTest(t)
	firstGrant, err := model.UpsertOAuthGrant(fixture.userID, fixture.app.ClientId, "profile")
	require.NoError(t, err)
	secondUser := &model.User{
		Username: "oauth-flow-second-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  common.GetRandomString(8),
	}
	require.NoError(t, model.DB.Create(secondUser).Error)
	secondGrant, err := model.UpsertOAuthGrant(secondUser.Id, fixture.app.ClientId, "profile")
	require.NoError(t, err)
	firstToken, _, err := service.SignOAuthDelegatedAccessToken(
		fixture.userID,
		fixture.app.ClientId,
		firstGrant.Id,
		firstGrant.AuthorizationVersion,
		"profile",
		time.Now(),
	)
	require.NoError(t, err)
	secondToken, _, err := service.SignOAuthDelegatedAccessToken(
		secondUser.Id,
		fixture.app.ClientId,
		secondGrant.Id,
		secondGrant.AuthorizationVersion,
		"profile",
		time.Now(),
	)
	require.NoError(t, err)
	request := func(accessToken string) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/oauth-server/userinfo", middleware.DisableCache(), OAuthServerUserInfo)
		recorder := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(http.MethodGet, "/api/oauth-server/userinfo", nil)
		httpRequest.RemoteAddr = "198.51.100.77:40000"
		httpRequest.Header.Set("Authorization", "Bearer "+accessToken)
		router.ServeHTTP(recorder, httpRequest)
		return recorder
	}

	for attempt := 0; attempt < 10; attempt++ {
		response := request(firstToken)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	limited := request(firstToken)
	require.Equal(t, http.StatusTooManyRequests, limited.Code, limited.Body.String())
	otherUser := request(secondToken)
	require.Equal(t, http.StatusOK, otherUser.Code, otherUser.Body.String())
}

func TestOAuthAuthorizationCodeQueueReturnsAcceptedThenSucceeds(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 1)
	startOAuthQueueForControllerTest(t)

	config, enabled := service.OAuthExchangeQueueConfig()
	require.True(t, enabled)
	blockers := make([]*service.OAuthExchangeAdmission, 0, config.InitialConcurrency)
	for index := 0; index < config.InitialConcurrency; index++ {
		blocker, err := service.AcquireOAuthExchangeAdmission(context.Background())
		require.NoError(t, err)
		blockers = append(blockers, blocker)
	}
	t.Cleanup(func() {
		for _, blocker := range blockers {
			blocker.Finish(false)
		}
	})
	accepted := httptest.NewRecorder()
	queueContext, _ := gin.CreateTestContext(accepted)
	queueContext.Request = httptest.NewRequest(http.MethodPost, "/api/oauth-server/token", nil)
	queueContext.Request.Header.Set("Prefer", "respond-async, wait=0")
	require.True(t, handleQueuedOAuthAuthorizationCode(queueContext, fixture.app, fixture.code))
	require.Equal(t, http.StatusAccepted, accepted.Code, accepted.Body.String())
	var pending struct {
		Status     string `json:"status"`
		ExchangeID string `json:"exchange_id"`
		StatusURL  string `json:"status_url"`
		PollToken  string `json:"poll_token"`
	}
	require.NoError(t, common.Unmarshal(accepted.Body.Bytes(), &pending))
	assert.Equal(t, "pending", pending.Status)
	assert.NotEmpty(t, pending.ExchangeID)
	assert.Equal(t, "/api/oauth-server/token-exchanges/"+pending.ExchangeID, pending.StatusURL)
	assert.NotEmpty(t, pending.PollToken)

	for _, blocker := range blockers {
		blocker.Finish(false)
	}
	var completed struct {
		Status       string `json:"status"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.Eventually(t, func() bool {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/oauth-server/token-exchanges/:id", middleware.DisableCache(), OAuthTokenExchangeStatus)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, pending.StatusURL, nil)
		request.Header.Set("Authorization", "Bearer "+pending.PollToken)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			return false
		}
		if err := common.Unmarshal(recorder.Body.Bytes(), &completed); err != nil {
			return false
		}
		return completed.Status == "succeeded"
	}, 5*time.Second, 25*time.Millisecond)
	assert.NotEmpty(t, completed.AccessToken)
	assert.NotEmpty(t, completed.RefreshToken)
	storedCode, err := model.GetOAuthAuthorizationCode(fixture.code.Code)
	require.NoError(t, err)
	assert.True(t, storedCode.Used)

	secondCode := &model.OAuthAuthorizationCode{
		Code:        "oauth-flow-code-fast-async",
		ClientId:    fixture.app.ClientId,
		UserId:      fixture.userID,
		RedirectUri: fixture.code.RedirectUri,
		Scope:       fixture.code.Scope,
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, model.DB.Create(secondCode).Error)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {secondCode.Code},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {fixture.secret},
		"redirect_uri":  {fixture.code.RedirectUri},
	}
	fast := performOAuthTokenFormRequestWithPrefer(t, form, "", "", "respond-async, wait=5")
	require.Equal(t, http.StatusOK, fast.Code, fast.Body.String())
	var fastResult map[string]interface{}
	require.NoError(t, common.Unmarshal(fast.Body.Bytes(), &fastResult))
	assert.NotEmpty(t, fastResult["access_token"])
	assert.NotContains(t, fastResult, "status")
}

func TestInvalidClientSecretDoesNotConsumeAuthorizationCode(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 1)
	invalid := performOAuthTokenRequestWithSecret(t, fixture, "invalid-secret")
	require.Equal(t, http.StatusUnauthorized, invalid.Code)

	valid := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusOK, valid.Code, valid.Body.String())
}

func TestOAuthTokenGrantsIsolateIndependentClientApplications(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 1)
	secondApp := &model.OAuthApp{
		Name:             "Second Tool",
		ClientId:         "gai_second_tool",
		ClientSecretHash: fixture.app.ClientSecretHash,
		ClientType:       model.OAuthClientTypeLegacy,
		RedirectUris:     fixture.app.RedirectUris,
		UserId:           1,
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(secondApp).Error)
	secondCode := &model.OAuthAuthorizationCode{
		Code:        "oauth-flow-code-second-tool",
		ClientId:    secondApp.ClientId,
		UserId:      fixture.userID,
		RedirectUri: fixture.code.RedirectUri,
		Scope:       fixture.code.Scope,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	require.NoError(t, model.DB.Create(secondCode).Error)
	secondFixture := oauthServerFlowFixture{
		app:    secondApp,
		code:   secondCode,
		secret: fixture.secret,
		userID: fixture.userID,
	}

	first := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := performOAuthTokenRequest(t, secondFixture)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	var grantCount int64
	require.NoError(t, model.DB.Model(&model.OAuthGrant{}).
		Where("user_id = ?", fixture.userID).
		Count(&grantCount).Error)
	assert.Equal(t, int64(2), grantCount)
}

func TestOAuthTokenExchangeCommitsOneAuthorizationGeneration(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	recorder := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")

	storedCode, err := model.GetOAuthAuthorizationCode(fixture.code.Code)
	require.NoError(t, err)
	assert.True(t, storedCode.Used)
	var grant model.OAuthGrant
	require.NoError(t, model.DB.Where("user_id = ? AND client_id = ?", fixture.userID, fixture.app.ClientId).First(&grant).Error)
	assert.Equal(t, int64(1), grant.AuthorizationVersion)
	assert.NotEmpty(t, grant.RefreshTokenHash)
}

func TestOAuthTokenDatabaseFailureDoesNotMasqueradeAsInvalidGrant(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	require.NoError(t, model.DB.Migrator().DropTable(&model.OAuthAuthorizationCode{}))

	recorder := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"error":"server_error"`)
	var grantCount int64
	require.NoError(t, model.DB.Model(&model.OAuthGrant{}).Count(&grantCount).Error)
	assert.Zero(t, grantCount)
}

func TestOAuthTokenClientDatabaseFailureDoesNotMasqueradeAsInvalidClient(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	require.NoError(t, model.DB.Migrator().DropTable(&model.OAuthApp{}))

	recorder := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"error":"server_error"`)
	storedCode, err := model.GetOAuthAuthorizationCode(fixture.code.Code)
	require.NoError(t, err)
	assert.False(t, storedCode.Used)
}

func TestOAuthRefreshDatabaseFailureDoesNotMasqueradeAsInvalidGrant(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	issued := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	var tokenResponse struct {
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, common.Unmarshal(issued.Body.Bytes(), &tokenResponse))
	require.NoError(t, model.DB.Migrator().DropTable(&model.OAuthGrant{}))

	refresh := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {fixture.secret},
		"refresh_token": {tokenResponse.RefreshToken},
	}, "", "")
	require.Equal(t, http.StatusInternalServerError, refresh.Code, refresh.Body.String())
	assert.Contains(t, refresh.Body.String(), `"error":"server_error"`)
}

func TestOAuthRefreshRotationFailureKeepsPresentedTokenValid(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	issued := performOAuthTokenRequest(t, fixture)
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	var tokenResponse struct {
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, common.Unmarshal(issued.Body.Bytes(), &tokenResponse))
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER reject_oauth_grant_update
		BEFORE UPDATE ON oauth_grants
		BEGIN
			SELECT RAISE(FAIL, 'forced oauth grant update failure');
		END
	`).Error)

	refresh := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {fixture.secret},
		"refresh_token": {tokenResponse.RefreshToken},
	}, "", "")
	require.Equal(t, http.StatusInternalServerError, refresh.Code, refresh.Body.String())
	assert.Contains(t, refresh.Body.String(), `"error":"server_error"`)
	require.NoError(t, model.DB.Exec("DROP TRIGGER reject_oauth_grant_update").Error)
	_, err := model.GetActiveOAuthGrantByRefreshToken(fixture.app.ClientId, tokenResponse.RefreshToken)
	require.NoError(t, err)
}

func TestLegacyOAuthClientKeepsPKCEWithoutSecretCompatibility(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	verifier := "legacy-short-verifier"
	sum := sha256.Sum256([]byte(verifier))
	fixture.code.CodeChallenge = base64.RawURLEncoding.EncodeToString(sum[:])
	fixture.code.CodeChallengeMethod = oauthCodeChallengeMethodS256
	require.NoError(t, model.DB.Save(fixture.code).Error)

	recorder := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"redirect_uri":  {fixture.code.RedirectUri},
		"code_verifier": {verifier},
	}, "", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func TestConfidentialOAuthClientRequiresSecretEvenWithPKCE(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	fixture.app.ClientType = model.OAuthClientTypeConfidential
	fixture.code.CodeChallenge = base64.RawURLEncoding.EncodeToString(sum[:])
	fixture.code.CodeChallengeMethod = oauthCodeChallengeMethodS256
	require.NoError(t, model.DB.Save(fixture.app).Error)
	require.NoError(t, model.DB.Save(fixture.code).Error)

	recorder := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"redirect_uri":  {fixture.code.RedirectUri},
		"code_verifier": {verifier},
	}, "", "")
	require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
	stored, err := model.GetOAuthAuthorizationCode(fixture.code.Code)
	require.NoError(t, err)
	assert.False(t, stored.Used)
}

func TestPublicOAuthClientUsesStrictPKCEWithoutSecret(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	verifier := strings.Repeat("b", 43)
	sum := sha256.Sum256([]byte(verifier))
	fixture.app.ClientType = model.OAuthClientTypePublic
	fixture.app.ClientSecretHash = ""
	fixture.code.CodeChallenge = base64.RawURLEncoding.EncodeToString(sum[:])
	fixture.code.CodeChallengeMethod = oauthCodeChallengeMethodS256
	require.NoError(t, model.DB.Save(fixture.app).Error)
	require.NoError(t, model.DB.Save(fixture.code).Error)

	recorder := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"redirect_uri":  {fixture.code.RedirectUri},
		"code_verifier": {verifier},
	}, "", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var tokenResponse struct {
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &tokenResponse))
	require.NotEmpty(t, tokenResponse.RefreshToken)
	refreshed := performOAuthTokenFormRequest(t, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {fixture.app.ClientId},
		"refresh_token": {tokenResponse.RefreshToken},
	}, "", "")
	require.Equal(t, http.StatusOK, refreshed.Code, refreshed.Body.String())
}

func TestOAuthTokenAcceptsMatchingBasicAndBodyCredentials(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {fixture.secret},
		"redirect_uri":  {fixture.code.RedirectUri},
	}
	recorder := performOAuthTokenFormRequest(t, form, fixture.app.ClientId, fixture.secret)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func TestOAuthTokenRejectsConflictingBasicAndBodyCredentialsWithoutConsumingCode(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fixture.code.Code},
		"client_id":     {fixture.app.ClientId},
		"client_secret": {fixture.secret},
		"redirect_uri":  {fixture.code.RedirectUri},
	}
	recorder := performOAuthTokenFormRequest(t, form, "gai_other", fixture.secret)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	stored, err := model.GetOAuthAuthorizationCode(fixture.code.Code)
	require.NoError(t, err)
	assert.False(t, stored.Used)
}

func TestOAuthUserInfoLegacyGrantCompatibilityCanBeRetired(t *testing.T) {
	fixture := newOAuthServerFlowFixture(t, 10)
	grant, err := model.UpsertOAuthGrant(fixture.userID, fixture.app.ClientId, "profile")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.OAuthGrant{}).
		Where("id = ?", grant.Id).
		Update("authorization_version", 0).Error)
	now := time.Now()
	legacyToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       fixture.userID,
		"client_id": fixture.app.ClientId,
		"grant_id":  grant.Id,
		"aud":       fixture.app.ClientId,
		"scope":     "profile",
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
		"iss":       common.OAuthTokenIssuerGemaiAPI,
		"jti":       "legacy-controller-test",
		"typ":       common.OAuthAccessTokenType,
		"token_use": common.OAuthTokenUseDelegatedAPI,
	}).SignedString([]byte(common.CryptoSecret))
	require.NoError(t, err)

	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/oauth-server/userinfo", nil)
		ctx.Request.Header.Set("Authorization", "Bearer "+legacyToken)
		OAuthServerUserInfo(ctx)
		return recorder
	}

	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "true")
	accepted := request()
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "false")
	rejected := request()
	require.Equal(t, http.StatusUnauthorized, rejected.Code, rejected.Body.String())
	t.Setenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS", "true")
	require.NoError(t, model.DB.Migrator().DropTable(&model.OAuthGrant{}))
	unavailable := request()
	require.Equal(t, http.StatusInternalServerError, unavailable.Code, unavailable.Body.String())
}
