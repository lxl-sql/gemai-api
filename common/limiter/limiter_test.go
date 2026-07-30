package limiter

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noScriptScripter struct {
	evalCalls    int
	evalShaCalls int
}

func (s *noScriptScripter) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	s.evalCalls++
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(int64(1))
	return cmd
}

func (s *noScriptScripter) EvalSha(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	s.evalShaCalls++
	cmd := redis.NewCmd(ctx)
	cmd.SetErr(errors.New("NOSCRIPT No matching script. Please use EVAL."))
	return cmd
}

func (s *noScriptScripter) ScriptExists(ctx context.Context, _ ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceCmd(ctx)
}

func (s *noScriptScripter) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return redis.NewStringCmd(ctx)
}

func TestRedisLimiterReloadsScriptAfterRedisForgetsIt(t *testing.T) {
	client := &noScriptScripter{}
	rateLimiter := &RedisLimiter{
		client:      client,
		limitScript: redis.NewScript(rateLimitScript),
	}

	allowed, err := rateLimiter.Allow(context.Background(), "user:1")

	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 1, client.evalShaCalls)
	assert.Equal(t, 1, client.evalCalls)
}
