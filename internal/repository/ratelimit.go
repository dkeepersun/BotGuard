package repository

import (
	"context"
	"fmt"
	"hash/maphash"
	"strconv"
	"sync"
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
//
// Subjects already over their limit are answered from a per-process shadow
// without a Redis round trip, so a flood from one subject costs Redis one
// command per window instead of one per request.
type RateLimiter struct {
	rds    *redis.Redis
	now    func() time.Time
	shadow *rateShadow
}

func NewRateLimiter(rds *redis.Redis) *RateLimiter {
	return &RateLimiter{rds: rds, now: time.Now, shadow: newRateShadow()}
}

// Allow counts one hit for subject and reports whether it fits within limit.
// retryAfter is the number of seconds until the current window ends.
func (l *RateLimiter) Allow(ctx context.Context, dimension, subject string, limit int, window time.Duration) (ok bool, retryAfter int, err error) {
	n, retryAfter, err := l.Take(ctx, dimension, subject, limit, window)
	return err == nil && n <= int64(limit), retryAfter, err
}

// Count returns the hits so far in the current window without adding one.
// Once the count has reached limit it may be answered from the shadow, so
// callers must treat count >= limit as over.
func (l *RateLimiter) Count(ctx context.Context, dimension, subject string, limit int, window time.Duration) (count int64, retryAfter int, err error) {
	key, end, retryAfter := l.window(dimension, subject, window)
	if n := l.shadow.get(key, l.now().Unix()); n >= int64(limit) {
		return n, retryAfter, nil
	}
	v, err := l.rds.GetCtx(ctx, key)
	if err != nil {
		return 0, 0, err
	}
	if v != "" {
		if count, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, 0, err
		}
	}
	l.shadow.observe(key, count, int64(limit), end, l.now().Unix())
	return count, retryAfter, nil
}

// Take counts one hit and returns the count so far in the current window.
// Once the count is over limit the hit may be answered from the shadow, so
// callers must treat count > limit as over.
func (l *RateLimiter) Take(ctx context.Context, dimension, subject string, limit int, window time.Duration) (count int64, retryAfter int, err error) {
	key, end, retryAfter := l.window(dimension, subject, window)
	if n := l.shadow.get(key, l.now().Unix()); n > int64(limit) {
		return n, retryAfter, nil
	}
	res, err := l.rds.EvalCtx(ctx, rateScript, []string{key}, ceilSeconds(window))
	if err != nil {
		return 0, 0, err
	}
	n, isInt := res.(int64)
	if !isInt {
		return 0, 0, fmt.Errorf("rate script returned %T", res)
	}
	l.shadow.observe(key, n, int64(limit), end, l.now().Unix())
	return n, retryAfter, nil
}

// refundScript takes back one hit without going below zero.
const refundScript = `
local n = tonumber(redis.call('GET', KEYS[1]) or '0')
if n > 0 then redis.call('DECR', KEYS[1]) end
return 1
`

// Refund takes back one hit in the current window, for an attempt that was
// counted up front (so concurrent requests cannot all pass the check) and
// turned out legitimate.
func (l *RateLimiter) Refund(ctx context.Context, dimension, subject string, window time.Duration) error {
	key, _, _ := l.window(dimension, subject, window)
	_, err := l.rds.EvalCtx(ctx, refundScript, []string{key})
	return err
}

// window returns the Redis key of the current window, the Unix second it
// ends and the seconds left until then.
func (l *RateLimiter) window(dimension, subject string, window time.Duration) (key string, end int64, retryAfter int) {
	sec := int64(ceilSeconds(window))
	now := l.now().Unix()
	bucket := now / sec
	end = (bucket + 1) * sec
	return fmt.Sprintf("bg:rate:%s:%s:%d", dimension, subject, bucket), end, int(end - now)
}

const (
	rateShadowShards = 64
	// rateShadowShardSize caps each shard; only subjects at or over their
	// limit are kept, so the cap is reached only by a flood of offenders.
	rateShadowShardSize = 2048
)

// rateShadow remembers the highest count Redis has returned for a window key,
// for keys at or over their limit. A Redis counter only grows within its
// window, so a shadow over the limit means Redis is over it too: answering
// from the shadow never refuses a hit Redis would allow, however many
// replicas share the counter. (If Redis loses its data mid-window, a subject
// already over stays refused until the window ends.)
type rateShadow struct {
	seed   maphash.Seed
	shards [rateShadowShards]rateShadowShard
}

type rateShadowShard struct {
	mu      sync.Mutex
	entries map[string]rateShadowEntry
	// swept is the Unix second of the last purge of ended windows.
	swept int64
}

type rateShadowEntry struct {
	count, end int64
}

func newRateShadow() *rateShadow {
	s := &rateShadow{seed: maphash.MakeSeed()}
	for i := range s.shards {
		s.shards[i].entries = make(map[string]rateShadowEntry)
	}
	return s
}

func (s *rateShadow) shard(key string) *rateShadowShard {
	return &s.shards[maphash.String(s.seed, key)%rateShadowShards]
}

// get returns the remembered count for key, or 0 once its window has ended.
func (s *rateShadow) get(key string, now int64) int64 {
	sh := s.shard(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.entries[key]
	if !ok || e.end <= now {
		return 0
	}
	return e.count
}

// observe records a count Redis returned. Counts below limit are not kept:
// the shadow is only consulted at or over the limit.
func (s *rateShadow) observe(key string, count, limit, end, now int64) {
	sh := s.shard(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if e, ok := sh.entries[key]; ok {
		if count > e.count {
			sh.entries[key] = rateShadowEntry{count: count, end: end}
		}
		return
	}
	if count < limit {
		return
	}
	if len(sh.entries) >= rateShadowShardSize {
		// Windows end on whole seconds, so purging more than once a second
		// finds nothing new.
		if now > sh.swept {
			sh.swept = now
			for k, e := range sh.entries {
				if e.end <= now {
					delete(sh.entries, k)
				}
			}
		}
		if len(sh.entries) >= rateShadowShardSize {
			// Full of live offenders: this one keeps going to Redis.
			return
		}
	}
	sh.entries[key] = rateShadowEntry{count: count, end: end}
}
