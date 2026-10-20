package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	red "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// newState uses miniredis unless BOTGUARD_TEST_REDIS_ADDR names a dedicated
// Redis, which is flushed before each test. Real Redis matters here because
// the verify script's semantics must not rely on miniredis's Lua emulation.
func newState(t *testing.T) (*State, *miniredis.Miniredis) {
	if addr := os.Getenv("BOTGUARD_TEST_REDIS_ADDR"); addr != "" {
		c := red.NewClient(&red.Options{Addr: addr})
		defer c.Close()
		if err := c.FlushDB(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		return NewState(redis.New(addr)), nil
	}
	mr := miniredis.RunT(t)
	// miniredis lacks CLIENT SETINFO, which go-zero's breaker would count as failures.
	return NewState(redis.New(mr.Addr(), redis.WithIdentity())), mr
}

const now = int64(1_790_000_000)

func put(t *testing.T, s *State, hash string, exp int64) {
	t.Helper()
	err := s.PutToken(context.Background(), 7, hash, TokenRecord{Action: "login", Origin: "https://a.example", ExpiresAt: exp,
		Kind: "slider", RiskLevel: "medium", Reasons: []string{"NO_SIGNALS", "FAST_CLICK"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
}

func consume(s *State, requestID, digest, hash, action string) (string, bool, error) {
	return s.Consume(context.Background(), ConsumeInput{AppID: 7, RequestID: requestID, RequestDigest: digest,
		TokenHash: hash, Action: action, AllowedOrigins: []string{"https://a.example"}, Now: now, ResultTTL: time.Minute})
}

func TestConsumeLifecycle(t *testing.T) {
	s, _ := newState(t)
	put(t, s, "h1", now+60)

	pass := fmt.Sprintf("PASS:%d:slider:medium:NO_SIGNALS,FAST_CLICK", now)
	if out, replay, err := consume(s, "r1", "d1", "h1", "login"); err != nil || replay || out != pass {
		t.Fatalf("first: %q %v %v", out, replay, err)
	}
	if out, replay, err := consume(s, "r1", "d1", "h1", "login"); err != nil || !replay || out != pass {
		t.Fatalf("replay: %q %v %v", out, replay, err)
	}
	if out, _, err := consume(s, "r2", "d2", "h1", "login"); err != nil || out != ReasonConsumed {
		t.Fatalf("second request: %q %v", out, err)
	}
	if _, _, err := consume(s, "r1", "other", "h1", "login"); err != ErrIdempotencyConflict {
		t.Fatalf("conflict: %v", err)
	}
}

func TestConsumeRejections(t *testing.T) {
	s, _ := newState(t)
	put(t, s, "expired", now)
	put(t, s, "ok", now+60)
	if err := s.PutToken(context.Background(), 7, "elsewhere",
		TokenRecord{Action: "login", Origin: "https://removed.example", ExpiresAt: now + 60}, time.Minute); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct{ hash, action, want string }{
		"unknown":         {"missing", "login", ReasonInvalid},
		"expired":         {"expired", "login", ReasonExpired},
		"action mismatch": {"ok", "signup", ReasonAction},
		"origin removed":  {"elsewhere", "login", ReasonOrigin},
	} {
		if out, _, err := consume(s, "req-"+c.hash+c.action, "d-"+c.hash+c.action, c.hash, c.action); err != nil || out != c.want {
			t.Errorf("%s: %q %v", name, out, err)
		}
	}
	// A mismatched action must not burn the token for the correct action.
	if out, _, _ := consume(s, "fresh", "fresh", "ok", "login"); out[:5] != PassPrefix {
		t.Fatalf("token burned by mismatch: %q", out)
	}
}

func TestConcurrentConsumeSingleWinner(t *testing.T) {
	s, _ := newState(t)
	put(t, s, "h", now+60)
	var wg sync.WaitGroup
	var mu sync.Mutex
	passes := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, _, err := consume(s, fmt.Sprintf("r%d", i), fmt.Sprintf("d%d", i), "h", "login")
			if err != nil {
				t.Error(err)
				return
			}
			if out[:5] == PassPrefix {
				mu.Lock()
				passes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if passes != 1 {
		t.Fatalf("%d passes", passes)
	}
}

func TestMarkChallengeUsed(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewState(redis.New(mr.Addr(), redis.WithIdentity()))
	ctx := context.Background()
	if ok, err := s.MarkChallengeUsed(ctx, "c1", 90*time.Second); err != nil || !ok {
		t.Fatalf("first mark: %v %v", ok, err)
	}
	if ok, _ := s.MarkChallengeUsed(ctx, "c1", 90*time.Second); ok {
		t.Fatal("second mark succeeded")
	}
	if ttl := mr.TTL("bg:pow:used:c1"); ttl != 90*time.Second {
		t.Fatalf("ttl %s", ttl)
	}
}

func TestFailureCounter(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewState(redis.New(mr.Addr(), redis.WithIdentity()))
	ctx := context.Background()
	if n, err := s.Failures(ctx, "192.0.2.1"); err != nil || n != 0 {
		t.Fatalf("empty: %d %v", n, err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordFailure(ctx, "192.0.2.1", 10*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.Failures(ctx, "192.0.2.1"); n != 3 {
		t.Fatalf("count %d", n)
	}
	if ttl := mr.TTL("bg:ipfail:192.0.2.1"); ttl != 10*time.Minute {
		t.Fatalf("ttl %s", ttl)
	}
}
