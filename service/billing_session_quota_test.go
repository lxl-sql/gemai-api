package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWalletPrecheckQuotaUsesSufficientSnapshot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 100)

	quota, err := resolveWalletPrecheckQuota(c, 987654321, 80)

	require.NoError(t, err)
	assert.Equal(t, 100, quota)
}

func TestResolveWalletPrecheckQuotaRefreshesInsufficientSnapshot(t *testing.T) {
	user := &model.User{
		Username:  "billing-quota-fallback-" + common.GetRandomString(8),
		Password:  "unused",
		Role:      common.RoleCommonUser,
		Status:    common.UserStatusEnabled,
		Group:     "default",
		Quota:     320,
		GiftQuota: 25,
	}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() {
		model.DB.Unscoped().Delete(user)
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 0)

	quota, err := resolveWalletPrecheckQuota(c, user.Id, 100)

	require.NoError(t, err)
	assert.Equal(t, 345, quota)
}

func TestResolveWalletPrecheckQuotaFallsBackWhenSnapshotMissing(t *testing.T) {
	user := &model.User{
		Username: "billing-quota-missing-" + common.GetRandomString(8),
		Password: "unused",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    210,
	}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() {
		model.DB.Unscoped().Delete(user)
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	quota, err := resolveWalletPrecheckQuota(c, user.Id, 100)

	require.NoError(t, err)
	assert.Equal(t, 210, quota)
}

func TestResolveWalletPrecheckQuotaHonorsCanceledRequestWhenRefreshing(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestCtx)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 0)

	_, err := resolveWalletPrecheckQuota(c, 987654321, 100)

	require.ErrorIs(t, err, context.Canceled)
}
