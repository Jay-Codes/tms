// Package ratelimit implements the Redis fixed-window counters guarding the
// auth surface (SPEC §8). Redis is ephemeral: if it is unreachable the limiter
// fails open (allow) and logs a warning, because losing the cache must not
// lock users out of the product.
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter counts requests in fixed windows.
type Limiter struct {
	Redis  *redis.Client
	Logger *slog.Logger
}

// New builds a Limiter. A nil client yields a limiter that always allows.
func New(client *redis.Client, logger *slog.Logger) *Limiter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Limiter{Redis: client, Logger: logger}
}

// Result describes a limiter decision.
type Result struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
	Degraded   bool // Redis unavailable: the decision was fail-open
}

// Allow increments the counter for key and reports whether the caller is
// within limit for the current window.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) Result {
	if l == nil || l.Redis == nil {
		return Result{Allowed: true, Remaining: limit, Degraded: true}
	}

	// Fixed window: the bucket rolls over with wall-clock time, so the key
	// carries the window index and expires on its own.
	bucket := time.Now().UnixNano() / int64(window)
	redisKey := fmt.Sprintf("rl:%s:%d", key, bucket)

	pipe := l.Redis.TxPipeline()
	incr := pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		l.Logger.Warn("rate limiter degraded: redis unavailable, allowing request",
			"key", key, "error", err)
		return Result{Allowed: true, Remaining: limit, Degraded: true}
	}

	count := int(incr.Val())
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	if count > limit {
		// Time left in the current window.
		next := time.Unix(0, (bucket+1)*int64(window))
		return Result{Allowed: false, Remaining: 0, RetryAfter: time.Until(next)}
	}
	return Result{Allowed: true, Remaining: remaining}
}

// Reset clears the current window for a key (used after a successful login so
// a legitimate user is not penalised by earlier typos).
func (l *Limiter) Reset(ctx context.Context, key string, window time.Duration) {
	if l == nil || l.Redis == nil {
		return
	}
	bucket := time.Now().UnixNano() / int64(window)
	if err := l.Redis.Del(ctx, fmt.Sprintf("rl:%s:%d", key, bucket)).Err(); err != nil {
		l.Logger.Warn("rate limiter reset failed", "key", key, "error", err)
	}
}
