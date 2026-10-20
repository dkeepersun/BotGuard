package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

const rateScript = `
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return n
`

// RateLimiter is a fixed-window counter. It runs its script with EVAL for the
// same breaker reason as State.
type RateLimiter struct {
	rds *redis.Redis
	now func() time.Time
}

func NewRateLimiter(rds *redis.Redis) *RateLimiter {
	return &RateLimiter{rds: rds, now: time.Now}
}

// Allow counts one hit for subject and reports whether it fits within limit.
// retryAfter is the number of seconds until the current window ends.
func (l *RateLimiter) Allow(ctx context.Context, dimension, subject string, limit int, window time.Duration) (ok bool, retryAfter int, err error) {
	n, retryAfter, err := l.Take(ctx, dimension, subject, window)
	return err == nil && n <= int64(limit), retryAfter, err
}

// Count returns the hits so far in the current window without adding one.
func (l *RateLimiter) Count(ctx context.Context, dimension, subject string, window time.Duration) (count int64, retryAfter int, err error) {
	sec := int64(ceilSeconds(window))
	now := l.now().Unix()
	bucket := now / sec
	v, err := l.rds.GetCtx(ctx, fmt.Sprintf("bg:rate:%s:%s:%d", dimension, subject, bucket))
	if err != nil {
		return 0, 0, err
	}
	if v != "" {
		if count, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, 0, err
		}
	}
	return count, int((bucket+1)*sec - now), nil
}

// Take counts one hit and returns the count so far in the current window.
func (l *RateLimiter) Take(ctx context.Context, dimension, subject string, window time.Duration) (count int64, retryAfter int, err error) {
	sec := int64(ceilSeconds(window))
	now := l.now().Unix()
	bucket := now / sec
	key := fmt.Sprintf("bg:rate:%s:%s:%d", dimension, subject, bucket)
	res, err := l.rds.EvalCtx(ctx, rateScript, []string{key}, sec)
	if err != nil {
		return 0, 0, err
	}
	n, isInt := res.(int64)
	if !isInt {
		return 0, 0, fmt.Errorf("rate script returned %T", res)
	}
	return n, int((bucket+1)*sec - now), nil
}
