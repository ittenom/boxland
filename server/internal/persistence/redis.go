package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

// RedisClient wraps rueidis.Client with the small surface we use plus a
// Ping helper compatible with httpserver.Pinger.
type RedisClient struct {
	rueidis.Client
}

// NewRedis builds a rueidis client from a redis:// URL. Verifies connectivity
// with a single PING. The caller owns Close.
func NewRedis(ctx context.Context, url string) (*RedisClient, error) {
	opt, err := rueidis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	// Defaults are fine for dev; production tuning happens later if profiling
	// shows pressure (see PLAN.md §10).
	cli, err := rueidis.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	rc := &RedisClient{Client: cli}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rc.Ping(pingCtx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rc, nil
}

// Ping satisfies httpserver.Pinger.
func (r *RedisClient) Ping(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return errors.New("redis client not initialized")
	}
	return r.Do(ctx, r.B().Ping().Build()).Error()
}
