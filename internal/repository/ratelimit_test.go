package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

func TestRateLimiterWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	l := NewRateLimiter(redis.New(mr.Addr(), redis.WithIdentity()))
	at := time.Unix(1_790_000_010, 0)
	l.now = func() time.Time { return at }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if ok, _, err := l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); err != nil || !ok {
			t.Fatalf("hit %d: %v %v", i, ok, err)
		}
	}
	ok, retry, _ := l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute)
	if ok || retry != 30 {
		t.Fatalf("over quota: ok=%v retry=%d", ok, retry)
	}
	if ok, _, _ := l.Allow(ctx, "solve", "192.0.2.2", 3, time.Minute); !ok {
		t.Fatal("other subject limited")
	}
	at = at.Add(time.Minute)
	if ok, _, _ := l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); !ok {
		t.Fatal("next window still limited")
	}
}

// Once Redis reports a subject over its limit, later hits in the window are
// answered locally: they still fail with Redis gone, while other subjects
// (which need Redis) surface its error.
func TestRateLimiterShadowsOverLimitSubjects(t *testing.T) {
	mr := miniredis.RunT(t)
	l := NewRateLimiter(redis.New(mr.Addr(), redis.WithIdentity()))
	at := time.Unix(1_790_000_010, 0)
	l.now = func() time.Time { return at }
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		_, _, _ = l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute)
	}
	if n, _, err := l.Count(ctx, "auth", "192.0.2.1", 2, time.Minute); err != nil || n != 0 {
		t.Fatalf("count: %d %v", n, err)
	}
	_, _, _ = l.Take(ctx, "auth", "192.0.2.1", 2, time.Minute)
	_, _, _ = l.Take(ctx, "auth", "192.0.2.1", 2, time.Minute)
	mr.Close()

	if ok, retry, err := l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); ok || err != nil || retry != 30 {
		t.Fatalf("shadowed subject: ok=%v retry=%d err=%v", ok, retry, err)
	}
	if n, _, err := l.Count(ctx, "auth", "192.0.2.1", 2, time.Minute); err != nil || n < 2 {
		t.Fatalf("shadowed count: %d %v", n, err)
	}
	if _, _, err := l.Allow(ctx, "solve", "192.0.2.2", 3, time.Minute); err == nil {
		t.Fatal("unshadowed subject did not reach Redis")
	}
	at = at.Add(time.Minute)
	if _, _, err := l.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); err == nil {
		t.Fatal("shadow outlived its window")
	}
}

// A subject at the limit but not over it must still go to Redis on Take,
// which is what allows a hit after a window rolls over elsewhere.
func TestRateLimiterShadowNeverRefusesWhatRedisAllows(t *testing.T) {
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	a, b := NewRateLimiter(rds), NewRateLimiter(rds)
	at := time.Unix(1_790_000_010, 0)
	a.now = func() time.Time { return at }
	b.now = a.now
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if ok, _, err := a.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); !ok || err != nil {
			t.Fatalf("hit %d: %v %v", i, ok, err)
		}
	}
	// Replica b has seen none of them, but Redis has: b refuses the fourth.
	if ok, _, _ := b.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); ok {
		t.Fatal("replica b allowed over the shared limit")
	}
	at = at.Add(time.Minute)
	if ok, _, _ := a.Allow(ctx, "solve", "192.0.2.1", 3, time.Minute); !ok {
		t.Fatal("next window refused")
	}
}
