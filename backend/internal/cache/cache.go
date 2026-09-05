// Package cache holds the Redis client used for sessions, rate limits and the
// notification job queue. Redis is ephemeral (TECHSTACK): every consumer must
// tolerate an empty or unreachable cache.
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Client wraps the go-redis client.
type Client struct {
	*redis.Client
}

// Open parses a redis:// URL and constructs a client. It does not dial — a
// failure to reach Redis surfaces on first use and via Ping.
func Open(url string) (*Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("cache: parse url: %w", err)
	}
	return &Client{Client: redis.NewClient(opt)}, nil
}

// Ping checks reachability (implements httpserver.Pinger).
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("cache: no client")
	}
	return c.Client.Ping(ctx).Err()
}

// Close releases the connection pool.
func (c *Client) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	return c.Client.Close()
}
