package common

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type verificationValue struct {
	code string
	time time.Time
}

const (
	EmailVerificationPurpose    = "v"
	PasswordResetPurpose        = "r"
	verificationRedisKeyPrefix  = "verification:"
)

var verificationMutex sync.Mutex
var verificationMap map[string]verificationValue
var verificationMapMaxSize = 10
var VerificationValidMinutes = 10

func verificationRedisKey(purpose, key string) string {
	return fmt.Sprintf("%s%s%s", verificationRedisKeyPrefix, purpose, key)
}

func GenerateVerificationCode(length int) string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	if length == 0 {
		return code
	}
	return code[:length]
}

func RegisterVerificationCodeWithKey(key string, code string, purpose string) {
	if RedisEnabled {
		rKey := verificationRedisKey(purpose, key)
		ttl := time.Duration(VerificationValidMinutes) * time.Minute
		err := RedisSet(rKey, code, ttl)
		if err != nil {
			SysError(fmt.Sprintf("failed to store verification code in Redis: %s", err.Error()))
		}
		return
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationValue{
		code: code,
		time: time.Now(),
	}
	if len(verificationMap) > verificationMapMaxSize {
		removeExpiredPairs()
	}
}

func VerifyCodeWithKey(key string, code string, purpose string) bool {
	if RedisEnabled {
		rKey := verificationRedisKey(purpose, key)
		storedCode, err := RedisGet(rKey)
		if err != nil {
			return false
		}
		return code == storedCode
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, okay := verificationMap[purpose+key]
	now := time.Now()
	if !okay || int(now.Sub(value.time).Seconds()) >= VerificationValidMinutes*60 {
		return false
	}
	return code == value.code
}

func DeleteKey(key string, purpose string) {
	if RedisEnabled {
		rKey := verificationRedisKey(purpose, key)
		err := RedisDel(rKey)
		if err != nil {
			SysError(fmt.Sprintf("failed to delete verification code from Redis: %s", err.Error()))
		}
		return
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

// no lock inside, so the caller must lock the verificationMap before calling!
func removeExpiredPairs() {
	now := time.Now()
	for key := range verificationMap {
		if int(now.Sub(verificationMap[key].time).Seconds()) >= VerificationValidMinutes*60 {
			delete(verificationMap, key)
		}
	}
}

func init() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationValue)
}
