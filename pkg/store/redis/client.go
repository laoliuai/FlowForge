package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/flowforge/flowforge/pkg/config"
)

type Client struct {
	rdb redis.UniversalClient
}

func NewClient(cfg *config.RedisConfig) (*Client, error) {
	var rdb redis.UniversalClient

	if cfg.ClusterMode {
		rdb = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Addresses,
			Password: cfg.Password,
			PoolSize: cfg.PoolSize,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.Addresses[0],
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})
	}

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

func (c *Client) Client() redis.UniversalClient {
	return c.rdb
}

func (c *Client) Close() error {
	return c.rdb.Close()
}
