package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Cache provides Redis caching functionality
type Cache struct {
	client     *redis.Client
	defaultTTL time.Duration
}

// NewCache creates a new Cache instance
func NewCache(redisURL string, ttl time.Duration) (*Cache, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	// 只记录地址，不记录完整 URL（可能包含密码）
	log.Info().Str("addr", opt.Addr).Msg("✅ Redis connected")

	return &Cache{
		client:     client,
		defaultTTL: ttl,
	}, nil
}

// Get retrieves a value from cache
func (c *Cache) Get(ctx context.Context, key string, dest interface{}) error {
	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return ErrCacheMiss
		}
		return fmt.Errorf("redis get error: %w", err)
	}

	if err := json.Unmarshal([]byte(val), dest); err != nil {
		return fmt.Errorf("failed to unmarshal cached value: %w", err)
	}

	return nil
}

// Set stores a value in cache
func (c *Cache) Set(ctx context.Context, key string, value interface{}, ttl ...time.Duration) error {
	expiration := c.defaultTTL
	if len(ttl) > 0 {
		expiration = ttl[0]
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	if err := c.client.Set(ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("redis set error: %w", err)
	}

	return nil
}

// Delete removes a value from cache
func (c *Cache) Delete(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del error: %w", err)
	}
	return nil
}

// DeletePattern deletes all keys matching a pattern
func (c *Cache) DeletePattern(ctx context.Context, pattern string) (int64, error) {
	keys, err := c.client.Keys(ctx, pattern).Result()
	if err != nil {
		return 0, fmt.Errorf("redis keys error: %w", err)
	}

	if len(keys) == 0 {
		return 0, nil
	}

	deleted, err := c.client.Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis del error: %w", err)
	}

	return deleted, nil
}

// Exists checks if a key exists
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists error: %w", err)
	}
	return result == 1, nil
}

// TTL returns the remaining time to live of a key
func (c *Cache) TTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := c.client.TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis ttl error: %w", err)
	}
	return ttl, nil
}

// Close closes the Redis connection
func (c *Cache) Close() error {
	return c.client.Close()
}

// ErrCacheMiss is returned when a cache key is not found
var ErrCacheMiss = fmt.Errorf("cache miss")

// IsCacheMiss checks if an error is a cache miss
func IsCacheMiss(err error) bool {
	return err == ErrCacheMiss
}
