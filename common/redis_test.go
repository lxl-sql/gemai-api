package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedisHGetObjContextHonorsCanceledContextBeforeClientAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var target struct{ Value string }
	err := RedisHGetObjContext(ctx, "canceled-hash-read", &target)

	require.ErrorIs(t, err, context.Canceled)
}
