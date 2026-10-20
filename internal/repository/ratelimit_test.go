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
