package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func cacheSetToken(token Token) error {
	rawKey := token.PlainKey
	if rawKey == "" {
		rawKey = token.Key
	}
	if rawKey == "" || strings.HasPrefix(rawKey, "h:") {
		return fmt.Errorf("raw token credential is unavailable")
	}
	key := common.GenerateHMAC(rawKey)
	token.Clean()
	err := common.RedisHSetObj(fmt.Sprintf("token:%s", key), &token, time.Duration(common.RedisKeyCacheSeconds())*time.Second)
	if err != nil {
		return err
	}
	return nil
}

func cacheDeleteToken(key string) error {
	key = common.GenerateHMAC(key)
	return cacheDeleteTokenHash(key)
}

func cacheDeleteTokenHash(keyHash string) error {
	if keyHash == "" {
		return nil
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = common.RedisDelKey(fmt.Sprintf("token:%s", keyHash))
		if err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
		}
	}
	return err
}

func cacheDeleteStoredTokenCredential(key string, keyHash *string) error {
	if keyHash != nil && *keyHash != "" {
		return cacheDeleteTokenHash(*keyHash)
	}
	if key == "" {
		return nil
	}
	return cacheDeleteToken(key)
}

func cacheDeleteTokenCredential(token *Token) error {
	if token == nil {
		return nil
	}
	if token.PlainKey != "" {
		return cacheDeleteToken(token.PlainKey)
	}
	return cacheDeleteStoredTokenCredential(token.Key, token.KeyHash)
}

func cacheMarkTokenDisabledHash(keyHash string) error {
	if keyHash == "" {
		return nil
	}
	if common.RDB == nil {
		return errors.New("redis client is unavailable")
	}
	ctx := context.Background()
	key := fmt.Sprintf("token:%s", keyHash)
	txn := common.RDB.TxPipeline()
	txn.HSet(ctx, key, "Status", common.TokenStatusDisabled)
	txn.Expire(ctx, key, time.Duration(common.RedisKeyCacheSeconds())*time.Second)
	_, err := txn.Exec(ctx)
	return err
}

func cacheDisableTokenHash(keyHash string) error {
	if common.RDB == nil {
		return errors.New("redis client is unavailable")
	}
	markErr := cacheMarkTokenDisabledHash(keyHash)
	deleteErr := cacheDeleteTokenHash(keyHash)
	if markErr == nil || deleteErr == nil {
		return nil
	}
	return errors.Join(markErr, deleteErr)
}

func cacheDisableTokenCredential(token *Token) error {
	if token == nil {
		return nil
	}
	if token.PlainKey != "" {
		return cacheDisableTokenHash(common.GenerateHMAC(token.PlainKey))
	}
	if token.KeyHash != nil && *token.KeyHash != "" {
		return cacheDisableTokenHash(*token.KeyHash)
	}
	if token.Key == "" {
		return nil
	}
	return cacheDisableTokenHash(common.GenerateHMAC(token.Key))
}

func cacheMarkTokenDisabled(token *Token) error {
	if token == nil {
		return nil
	}
	if token.PlainKey != "" {
		return cacheMarkTokenDisabledHash(common.GenerateHMAC(token.PlainKey))
	}
	if token.KeyHash != nil && *token.KeyHash != "" {
		return cacheMarkTokenDisabledHash(*token.KeyHash)
	}
	if token.Key == "" {
		return nil
	}
	return cacheMarkTokenDisabledHash(common.GenerateHMAC(token.Key))
}

func cacheIncrTokenQuota(key string, increment int64) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHIncrBy(fmt.Sprintf("token:%s", key), constant.TokenFiledRemainQuota, increment)
	if err != nil {
		return err
	}
	return nil
}

func cacheDecrTokenQuota(key string, decrement int64) error {
	return cacheIncrTokenQuota(key, -decrement)
}

func cacheSetTokenField(key string, field string, value string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHSetField(fmt.Sprintf("token:%s", key), field, value)
	if err != nil {
		return err
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	hmacKey := common.GenerateHMAC(key)
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	err := common.RedisHGetObj(fmt.Sprintf("token:%s", hmacKey), &token)
	if err != nil {
		return nil, err
	}
	token.Key = key
	token.PlainKey = key
	return &token, nil
}
