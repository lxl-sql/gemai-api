package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestCachedAuthObjectsRejectIncompleteHashes(t *testing.T) {
	assert.True(t, validCachedUserBase(7, &UserBase{
		CacheVersion: authCacheSchemaVersion,
		Id:           7,
		Username:     "alice",
		Group:        "default",
		Role:         common.RoleCommonUser,
		Status:       common.UserStatusEnabled,
	}))
	assert.False(t, validCachedUserBase(7, &UserBase{
		CacheVersion: authCacheSchemaVersion,
		Id:           7,
		Role:         common.RoleCommonUser,
		Status:       common.UserStatusEnabled,
	}))
	assert.False(t, validCachedUserBase(7, &UserBase{
		CacheVersion: authCacheSchemaVersion,
		Id:           8,
		Username:     "alice",
		Group:        "default",
		Role:         common.RoleCommonUser,
		Status:       common.UserStatusEnabled,
	}))

	assert.True(t, validCachedToken(&Token{
		CacheVersion: authCacheSchemaVersion,
		Id:           11,
		UserId:       7,
		Status:       common.TokenStatusEnabled,
	}))
	assert.False(t, validCachedToken(&Token{
		CacheVersion: authCacheSchemaVersion,
		UserId:       7,
		Status:       common.TokenStatusEnabled,
	}))
	assert.False(t, validCachedToken(&Token{
		CacheVersion: authCacheSchemaVersion,
		Id:           11,
		UserId:       7,
	}))
}
