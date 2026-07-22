package ali

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForAliTaskPollReturnsWhenRequestIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForAliTaskPoll(ctx, time.Hour)

	require.ErrorIs(t, err, context.Canceled)
}
