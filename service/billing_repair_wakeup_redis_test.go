//go:build integration

package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRepairWakeupPublishesWithoutBusinessData(t *testing.T) {
	dsn := os.Getenv("BILLING_TEST_REDIS_DSN")
	if dsn == "" {
		t.Skip("BILLING_TEST_REDIS_DSN is not configured")
	}
	options, err := redis.ParseURL(dsn)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())
	previousClient := common.RDB
	previousEnabled := common.RedisEnabled
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = previousClient
		common.RedisEnabled = previousEnabled
		require.NoError(t, client.Close())
	})

	pubsub := client.Subscribe(context.Background(), billingRepairWakeupChannel)
	t.Cleanup(func() { require.NoError(t, pubsub.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)
	publishBillingRepairWakeup()
	message, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.Equal(t, billingRepairWakeupChannel, message.Channel)
	assert.Equal(t, "1", message.Payload)
}
