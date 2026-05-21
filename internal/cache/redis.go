package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeySeedPayloadV1 is the Redis key for the embedded JSON snapshot shared by
// GET /api/v1/seed and the slice list handlers. After any successful write that
// changes procurement domain data, call [*Client.InvalidateSeedPayload] on the
// same request context (or bump this key to a new version constant and deploy).
const KeySeedPayloadV1 = "procurement:api:seed:v1"

type Client struct {
	rdb *redis.Client
}

func New(redisURL string) (*Client, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) GetJSON(ctx context.Context, key string, dest any) (bool, error) {
	s, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(s), dest); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, ttl).Err()
}

// InvalidateSeedPayload deletes the cached seed payload so the next read
// repopulates from Postgres. Use after INSERT/UPDATE/DELETE on any table that
// feeds the seed API.
func (c *Client) InvalidateSeedPayload(ctx context.Context) error {
	return c.rdb.Del(ctx, KeySeedPayloadV1).Err()
}

// Redis exposes the underlying client for queues and pub/sub.
func (c *Client) Redis() *redis.Client {
	return c.rdb
}

// QueueLPush pushes a string job onto the head of a Redis list (FIFO with BRPOP).
func (c *Client) QueueLPush(ctx context.Context, key, value string) error {
	return c.rdb.LPush(ctx, key, value).Err()
}

// QueueBRPop blocks until a job is available on any of the given list keys.
func (c *Client) QueueBRPop(ctx context.Context, timeout time.Duration, keys ...string) (string, error) {
	res, err := c.rdb.BRPop(ctx, timeout, keys...).Result()
	if err == redis.Nil {
		return "", err
	}
	if err != nil {
		return "", err
	}
	if len(res) < 2 {
		return "", fmt.Errorf("brpop: unexpected reply")
	}
	return res[1], nil
}
