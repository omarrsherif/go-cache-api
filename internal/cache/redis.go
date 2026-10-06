// Package cache is a thin Redis wrapper. Every operation is bounded by a short
// timeout and returns its error to the caller; the service layer decides to
// fall back to MySQL. Nothing in here is fatal.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache wraps a go-redis client with JSON encoding and per-operation timeouts.
type Cache struct {
	rdb       *redis.Client
	opTimeout time.Duration
}

// New builds a client. It does not connect; use Ping to check reachability.
// Retries are disabled so a slow or dead Redis fails fast and the request
// proceeds to MySQL.
func New(addr string, opTimeout time.Duration, poolSize int) *Cache {
	rdb := redis.NewClient(&redis.Options{
		Addr:                  addr,
		MaxRetries:            -1,
		DialTimeout:           opTimeout,
		ReadTimeout:           opTimeout,
		WriteTimeout:          opTimeout,
		PoolSize:              poolSize,
		PoolTimeout:           opTimeout,
		ContextTimeoutEnabled: true,
	})
	return &Cache{rdb: rdb, opTimeout: opTimeout}
}

// Ping checks that Redis answers.
func (c *Cache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

// Get decodes the JSON value stored at key into dst. It returns found=false
// with a nil error on a cache miss, and an error only when Redis or decoding
// failed.
func (c *Cache) Get(ctx context.Context, key string, dst any) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis get %s: %w", key, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return false, fmt.Errorf("redis decode %s: %w", key, err)
	}
	return true, nil
}

// Set stores v as JSON with the given TTL.
func (c *Cache) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("redis encode %s: %w", key, err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	if err := c.rdb.Set(ctx, key, b, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}

// Delete removes keys. Deleting a missing key is not an error.
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func (c *Cache) Close() error { return c.rdb.Close() }

// ProductKey is the cache key for one product. The version segment lets the
// stored JSON shape change without serving stale structures.
func ProductKey(id int64) string { return fmt.Sprintf("product:v1:%d", id) }
