package model

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelWhenDoneContext struct {
	done     chan struct{}
	once     sync.Once
	canceled atomic.Bool
}

func newCancelWhenDoneContext() *cancelWhenDoneContext {
	return &cancelWhenDoneContext{done: make(chan struct{})}
}

func (c *cancelWhenDoneContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelWhenDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		c.canceled.Store(true)
		close(c.done)
	})
	return c.done
}

func (c *cancelWhenDoneContext) Err() error {
	if c.canceled.Load() {
		return context.Canceled
	}
	return nil
}

func (c *cancelWhenDoneContext) Value(any) any { return nil }

func TestCoalesceAuthCacheLoadSharesRebuildAndLetsWaiterCancel(t *testing.T) {
	key := "test:" + common.GetRandomString(16)
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	firstDone := make(chan struct{})
	var firstValue int
	var firstErr error
	var calls atomic.Int32

	go func() {
		defer close(firstDone)
		firstValue, firstErr = coalesceAuthCacheLoad(context.Background(), authCacheLoadNamespaceUser, key, func() (int, error) {
			calls.Add(1)
			close(loadStarted)
			<-releaseLoad
			return 42, nil
		})
	}()
	<-loadStarted

	waiterCtx := newCancelWhenDoneContext()
	_, err := coalesceAuthCacheLoad(waiterCtx, authCacheLoadNamespaceUser, key, func() (int, error) {
		calls.Add(1)
		return 99, nil
	})
	require.ErrorIs(t, err, context.Canceled)

	close(releaseLoad)
	<-firstDone
	require.NoError(t, firstErr)
	assert.Equal(t, 42, firstValue)
	assert.Equal(t, int32(1), calls.Load())
}

func TestCloneAuthTokenDoesNotShareMutableFields(t *testing.T) {
	keyHash := "original-hash"
	allowIps := "10.0.0.1"
	original := Token{KeyHash: &keyHash, AllowIps: &allowIps}

	clone := cloneAuthToken(original)
	require.NotNil(t, clone.KeyHash)
	require.NotNil(t, clone.AllowIps)
	*clone.KeyHash = "changed-hash"
	*clone.AllowIps = "10.0.0.2"

	assert.Equal(t, "original-hash", *original.KeyHash)
	assert.Equal(t, "10.0.0.1", *original.AllowIps)
}

func TestAuthCacheLoadsHonorCanceledRequestBeforeCacheOrDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, tokenErr := GetTokenByKeyContext(ctx, "canceled-token-load", false)
	_, userErr := GetUserCacheContext(ctx, 987654321)

	require.ErrorIs(t, tokenErr, context.Canceled)
	require.ErrorIs(t, userErr, context.Canceled)
}
