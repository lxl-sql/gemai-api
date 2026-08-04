package model

import (
	"context"
	"fmt"

	"golang.org/x/sync/singleflight"
)

const (
	authCacheLoadNamespaceToken = "token"
	authCacheLoadNamespaceUser  = "user"
)

var authCacheLoadFlight singleflight.Group

// coalesceAuthCacheLoad merges concurrent cache-miss rebuilds for one logical
// auth object. The namespace prevents token fingerprints and user IDs from
// sharing a flight. A canceled caller stops waiting without canceling the one
// shared rebuild that other callers may still need.
func coalesceAuthCacheLoad[T any](
	ctx context.Context,
	namespace string,
	key string,
	load func() (T, error),
) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if namespace == "" || key == "" {
		return zero, fmt.Errorf("auth cache load key is empty")
	}

	resultCh := authCacheLoadFlight.DoChan(namespace+":"+key, func() (any, error) {
		return load()
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return zero, result.Err
		}
		value, ok := result.Val.(T)
		if !ok {
			return zero, fmt.Errorf("auth cache load returned an unexpected type")
		}
		return value, nil
	}
}
