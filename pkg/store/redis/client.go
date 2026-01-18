package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/flowforge/flowforge/pkg/config"
)

type Client struct {
	client redis.UniversalClient
}

func NewClient(cfg *config.RedisConfig) (*Client, error) {
	var client redis.UniversalClient
	if cfg.ClusterMode {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Addresses,
			Password: cfg.Password,
			PoolSize: cfg.PoolSize,
		})
	} else {
		addr := "localhost:6379"
		if len(cfg.Addresses) > 0 {
			addr = cfg.Addresses[0]
		}
		client = redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})
	}

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Client{client: client}, nil
}

func (c *Client) Client() redis.UniversalClient {
	return c.client
}

func (c *Client) Close() error {
	return c.client.Close()
}
