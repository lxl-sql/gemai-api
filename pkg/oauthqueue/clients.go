package oauthqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

type ClientSet struct {
	Shards      []redis.UniversalClient
	Coordinator redis.UniversalClient
	owned       []redis.UniversalClient
}

func NewClientSet(ctx context.Context, config Config, shared *redis.Client) (*ClientSet, error) {
	clients := &ClientSet{}
	switch {
	case len(config.QueueRedisURLs) > 0:
		for _, rawURL := range config.QueueRedisURLs {
			options, err := redis.ParseURL(rawURL)
			if err != nil {
				clients.Close()
				return nil, fmt.Errorf("parse OAuth queue Redis URL: %w", err)
			}
			client := redis.NewClient(options)
			clients.Shards = append(clients.Shards, client)
			clients.owned = append(clients.owned, client)
		}
	case len(config.ClusterAddrs) > 0:
		client := redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    config.ClusterAddrs,
			Username: config.RedisUsername,
			Password: config.RedisPassword,
		})
		clients.Shards = []redis.UniversalClient{client}
		clients.owned = append(clients.owned, client)
	case len(config.SentinelAddrs) > 0:
		client := redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       config.SentinelMaster,
			SentinelAddrs:    config.SentinelAddrs,
			SentinelUsername: config.SentinelUsername,
			SentinelPassword: config.SentinelPassword,
			Username:         config.RedisUsername,
			Password:         config.RedisPassword,
			DB:               config.RedisDB,
		})
		clients.Shards = []redis.UniversalClient{client}
		clients.owned = append(clients.owned, client)
	default:
		if shared == nil {
			return nil, fmt.Errorf("OAuth queue requires Redis")
		}
		clients.Shards = []redis.UniversalClient{shared}
	}
	if config.CoordinatorRedisURL != "" {
		options, err := redis.ParseURL(config.CoordinatorRedisURL)
		if err != nil {
			clients.Close()
			return nil, fmt.Errorf("parse OAuth queue coordinator Redis URL: %w", err)
		}
		client := redis.NewClient(options)
		clients.Coordinator = client
		clients.owned = append(clients.owned, client)
	} else {
		clients.Coordinator = clients.Shards[0]
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seen := make(map[redis.UniversalClient]struct{})
	for _, client := range append(append([]redis.UniversalClient{}, clients.Shards...), clients.Coordinator) {
		if _, ok := seen[client]; ok {
			continue
		}
		seen[client] = struct{}{}
		if err := client.Ping(pingCtx).Err(); err != nil {
			clients.Close()
			return nil, fmt.Errorf("ping OAuth queue Redis: %w", err)
		}
	}
	return clients, nil
}

func (clients *ClientSet) Close() {
	if clients == nil {
		return
	}
	seen := make(map[redis.UniversalClient]struct{})
	for _, client := range clients.owned {
		if client == nil {
			continue
		}
		if _, ok := seen[client]; ok {
			continue
		}
		seen[client] = struct{}{}
		_ = client.Close()
	}
}
