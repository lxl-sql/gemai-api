package oauthqueue

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled             bool
	Namespace           string
	Partitions          int
	Capacity            int
	WorkersPerInstance  int
	UserLimit           int
	UserBurst           int
	UserDuration        time.Duration
	JobTTL              time.Duration
	ResultTTL           time.Duration
	LeaseTTL            time.Duration
	SyncWaitMax         time.Duration
	PollInterval        time.Duration
	MinConcurrency      int
	InitialConcurrency  int
	MaxConcurrency      int
	IncreaseStep        int
	AdjustInterval      time.Duration
	TargetP95           time.Duration
	MaxAttempts         int
	QueueRedisURLs      []string
	ClusterAddrs        []string
	SentinelAddrs       []string
	SentinelMaster      string
	SentinelUsername    string
	SentinelPassword    string
	RedisUsername       string
	RedisPassword       string
	RedisDB             int
	CoordinatorRedisURL string
}

func DefaultConfig() Config {
	return Config{
		Enabled:            false,
		Namespace:          "oauth-exchange",
		Partitions:         16,
		Capacity:           15000,
		WorkersPerInstance: 64,
		UserLimit:          10,
		UserBurst:          10,
		UserDuration:       10 * time.Minute,
		JobTTL:             60 * time.Second,
		ResultTTL:          120 * time.Second,
		LeaseTTL:           15 * time.Second,
		SyncWaitMax:        55 * time.Second,
		PollInterval:       time.Second,
		MinConcurrency:     8,
		InitialConcurrency: 32,
		MaxConcurrency:     256,
		IncreaseStep:       4,
		AdjustInterval:     5 * time.Second,
		TargetP95:          200 * time.Millisecond,
		MaxAttempts:        3,
	}
}

func LoadConfigFromEnv() (Config, error) {
	config := DefaultConfig()
	var err error
	if config.Enabled, err = envBool("OAUTH_QUEUE_ENABLE", config.Enabled); err != nil {
		return Config{}, err
	}
	config.QueueRedisURLs = splitEnv("OAUTH_QUEUE_REDIS_URLS", ";")
	if namespace := strings.TrimSpace(os.Getenv("OAUTH_QUEUE_NAMESPACE")); namespace != "" {
		config.Namespace = namespace
	}
	config.ClusterAddrs = splitEnv("OAUTH_QUEUE_REDIS_CLUSTER_ADDRS", ",")
	config.SentinelAddrs = splitEnv("OAUTH_QUEUE_REDIS_SENTINEL_ADDRS", ",")
	config.SentinelMaster = strings.TrimSpace(os.Getenv("OAUTH_QUEUE_REDIS_SENTINEL_MASTER"))
	config.SentinelUsername = os.Getenv("OAUTH_QUEUE_REDIS_SENTINEL_USERNAME")
	config.SentinelPassword = os.Getenv("OAUTH_QUEUE_REDIS_SENTINEL_PASSWORD")
	config.RedisUsername = os.Getenv("OAUTH_QUEUE_REDIS_USERNAME")
	config.RedisPassword = os.Getenv("OAUTH_QUEUE_REDIS_PASSWORD")
	if config.RedisDB, err = envInt("OAUTH_QUEUE_REDIS_DB", config.RedisDB); err != nil {
		return Config{}, err
	}
	config.CoordinatorRedisURL = strings.TrimSpace(os.Getenv("OAUTH_QUEUE_COORDINATOR_REDIS_URL"))
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if !config.Enabled {
		return nil
	}
	if config.Partitions < 1 || config.Partitions > 256 {
		return fmt.Errorf("OAuth queue partitions must be between 1 and 256")
	}
	if len(config.Namespace) < 1 || len(config.Namespace) > 48 {
		return fmt.Errorf("OAuth queue namespace must contain between 1 and 48 characters")
	}
	for _, char := range config.Namespace {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return fmt.Errorf("OAuth queue namespace contains an invalid character")
	}
	if config.Capacity < 100 || config.Capacity > 1000000 {
		return fmt.Errorf("OAuth queue capacity must be between 100 and 1000000")
	}
	if config.Capacity < config.Partitions {
		return fmt.Errorf("OAuth queue capacity must be greater than or equal to partitions")
	}
	if config.WorkersPerInstance < 1 || config.WorkersPerInstance > 1024 {
		return fmt.Errorf("OAuth queue workers per instance must be between 1 and 1024")
	}
	if config.WorkersPerInstance > config.Capacity {
		return fmt.Errorf("OAuth queue workers per instance must not exceed capacity")
	}
	if config.UserLimit < 1 || config.UserLimit > 10000 || config.UserBurst < 1 || config.UserBurst > config.UserLimit {
		return fmt.Errorf("OAuth queue user limit must satisfy 1 <= burst <= limit <= 10000")
	}
	if config.UserDuration < time.Second || config.UserDuration > 24*time.Hour {
		return fmt.Errorf("OAuth queue user duration must be between 1 second and 24 hours")
	}
	if config.JobTTL < time.Second || config.JobTTL > time.Minute {
		return fmt.Errorf("OAuth queue job TTL must be between 1 and 60 seconds")
	}
	if config.ResultTTL < config.JobTTL || config.ResultTTL > 15*time.Minute {
		return fmt.Errorf("OAuth queue result TTL must be between the job TTL and 15 minutes")
	}
	if config.LeaseTTL < 5*time.Second || config.LeaseTTL >= config.JobTTL {
		return fmt.Errorf("OAuth queue lease TTL must be at least 5 seconds and shorter than the job TTL")
	}
	if config.SyncWaitMax < 0 || config.SyncWaitMax > 55*time.Second {
		return fmt.Errorf("OAuth queue synchronous wait must be between 0 and 55 seconds")
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > 5*time.Second {
		return fmt.Errorf("OAuth queue poll interval must be between 100 milliseconds and 5 seconds")
	}
	if config.MinConcurrency < 1 || config.InitialConcurrency < config.MinConcurrency || config.MaxConcurrency < config.InitialConcurrency {
		return fmt.Errorf("OAuth queue concurrency must satisfy 1 <= min <= initial <= max")
	}
	if config.MaxConcurrency > 4096 {
		return fmt.Errorf("OAuth queue maximum concurrency must not exceed 4096")
	}
	if config.IncreaseStep < 1 || config.IncreaseStep > config.MaxConcurrency {
		return fmt.Errorf("OAuth queue increase step must be between 1 and max concurrency")
	}
	if config.AdjustInterval < time.Second || config.AdjustInterval > time.Minute {
		return fmt.Errorf("OAuth queue adjustment interval must be between 1 second and 1 minute")
	}
	if config.TargetP95 < 10*time.Millisecond || config.TargetP95 > 10*time.Second {
		return fmt.Errorf("OAuth queue target P95 must be between 10 milliseconds and 10 seconds")
	}
	if config.MaxAttempts < 1 || config.MaxAttempts > 10 {
		return fmt.Errorf("OAuth queue max attempts must be between 1 and 10")
	}
	modeCount := 0
	if len(config.QueueRedisURLs) > 0 {
		modeCount++
	}
	if len(config.ClusterAddrs) > 0 {
		modeCount++
	}
	if len(config.SentinelAddrs) > 0 {
		modeCount++
		if config.SentinelMaster == "" {
			return fmt.Errorf("OAUTH_QUEUE_REDIS_SENTINEL_MASTER is required with Sentinel addresses")
		}
	}
	if modeCount > 1 {
		return fmt.Errorf("configure only one of OAuth queue Redis URLs, Cluster addresses, or Sentinel addresses")
	}
	return nil
}

func envBool(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return value, nil
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func splitEnv(name string, separator string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	values := make([]string, 0)
	for _, value := range strings.Split(raw, separator) {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}
