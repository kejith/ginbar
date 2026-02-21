// Package cache provides a Redis-backed write-behind vote buffer.
// Vote writes skip PostgreSQL entirely (sub-millisecond); a background flush
// worker periodically drains dirty entities into Postgres and keeps the
// denormalised score columns up-to-date.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClient parses redisURL (e.g. "redis://redis:6379") and returns a
// connected *redis.Client.  It pings the server during setup so startup
// fails fast if Redis is unavailable.
func NewClient(ctx context.Context, redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url %q: %w", redisURL, err)
	}

	// Sensible connection-pool defaults for a moderate-traffic site.
	opts.PoolSize = 20
	opts.MinIdleConns = 4
	opts.ConnMaxIdleTime = 5 * time.Minute

	rdb := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}

	return rdb, nil
}
