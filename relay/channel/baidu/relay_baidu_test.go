package baidu

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestBaiduAccessTokenRequestHonorsCanceledContext(t *testing.T) {
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	accessToken, err := getBaiduAccessTokenHelper(ctx, "key|secret")

	require.Nil(t, accessToken)
	require.ErrorIs(t, err, context.Canceled)
}
