package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
)

type countingStore struct {
	appCalls int
	fail     bool
}

func (s *countingStore) AppByKey(_ context.Context, key string) (*domain.App, error) {
	s.appCalls++
	if s.fail {
		return nil, errors.New("db down")
	}
	if key == "known-app" {
		return &domain.App{ID: 1, AppKey: key}, nil
	}
	return nil, domain.ErrNotFound
}

func (s *countingStore) SecretPair(context.Context, string) (*domain.SecretPair, error) {
	return nil, domain.ErrNotFound
}

func TestCachedAppsCachesHitsAndMisses(t *testing.T) {
	src := &countingStore{}
	c, err := NewCachedApps(src, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if s, err := c.AppByKey(ctx, "known-app"); err != nil || s.ID != 1 {
			t.Fatalf("hit: %v %v", s, err)
		}
		if _, err := c.AppByKey(ctx, "random-app"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("miss: %v", err)
		}
	}
	if src.appCalls != 2 {
		t.Fatalf("%d source calls", src.appCalls)
	}
}

func TestCachedAppsDoesNotCacheErrors(t *testing.T) {
	src := &countingStore{fail: true}
	c, _ := NewCachedApps(src, time.Minute, 100)
	ctx := context.Background()
	if _, err := c.AppByKey(ctx, "known-app"); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("db error became %v", err)
	}
	src.fail = false
	if s, err := c.AppByKey(ctx, "known-app"); err != nil || s.ID != 1 {
		t.Fatalf("after recovery: %v %v", s, err)
	}
}

// Random keys must not evict the apps in use, and PeekApp never queries.
func TestCachedAppsMissesDoNotEvictHits(t *testing.T) {
	src := &countingStore{}
	c, _ := NewCachedApps(src, time.Minute, 100)
	ctx := context.Background()
	if _, ok := c.PeekApp("known-app"); ok || src.appCalls != 0 {
		t.Fatal("peek before load")
	}
	if _, err := c.AppByKey(ctx, "known-app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		_, _ = c.AppByKey(ctx, fmt.Sprintf("random-%d", i))
	}
	before := src.appCalls
	if _, err := c.AppByKey(ctx, "known-app"); err != nil || src.appCalls != before {
		t.Fatalf("known app evicted by misses: %v, %d calls", err, src.appCalls-before)
	}
	if a, ok := c.PeekApp("known-app"); !ok || a.ID != 1 {
		t.Fatal("peek after load")
	}
}

func TestStoredMode(t *testing.T) {
	cases := []struct {
		mode, kinds string
		want        string
		wantKinds   []string
		known       bool
	}{
		{"slider", "click_text", domain.ModeInteractive, []string{domain.KindSlider}, true},
		{"adaptive", "slider,click_shape", domain.ModeAdaptive, []string{domain.KindSlider, domain.KindClickShape}, true},
		{"invisible", "", domain.ModeInvisible, []string{domain.KindSlider}, true},
		{"strict", "click_text", domain.ModeAdaptive, []string{domain.KindClickText}, false},
		{"", "", domain.ModeAdaptive, []string{domain.KindSlider}, false},
	}
	for _, c := range cases {
		mode, kinds, known := storedMode(c.mode, c.kinds)
		if mode != c.want || !slices.Equal(kinds, c.wantKinds) || known != c.known {
			t.Errorf("%q/%q: %s %v %v", c.mode, c.kinds, mode, kinds, known)
		}
	}
}

type listingStore struct {
	countingStore
	appKeys  []string
	listFail atomic.Bool
}

func (s *listingStore) KnownKeys(context.Context) ([]string, []string, error) {
	if s.listFail.Load() {
		return nil, nil, errors.New("db down")
	}
	return s.appKeys, nil, nil
}

// Keys missing from the index share a small budget, so a flood of made-up
// keys cannot become one query each; indexed keys are always looked up.
func TestCachedAppsIndexBoundsUnknownKeyQueries(t *testing.T) {
	src := &listingStore{appKeys: []string{"known-app"}}
	c, _ := NewCachedApps(src, time.Minute, 1000)
	at := time.Unix(1_790_000_000, 0)
	c.now = func() time.Time { return at }
	c.unknown = newTokenBucket(3, c.now)
	ctx := context.Background()
	if err := c.reload(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := c.AppByKey(ctx, fmt.Sprintf("random-%d", i)); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("random key: %v", err)
		}
	}
	if src.appCalls != 3 {
		t.Fatalf("unknown keys queried %d times, want the budget of 3", src.appCalls)
	}
	if a, err := c.AppByKey(ctx, "known-app"); err != nil || a.ID != 1 || src.appCalls != 4 {
		t.Fatalf("indexed key with the budget spent: %v %v, %d calls", a, err, src.appCalls)
	}
	// The budget refills, so a key created since the last reload gets through.
	at = at.Add(time.Second)
	if _, err := c.AppByKey(ctx, "random-new"); !errors.Is(err, domain.ErrNotFound) || src.appCalls != 5 {
		t.Fatalf("after refill: %v, %d calls", err, src.appCalls)
	}
}

// An index that has not reloaded for indexMaxAgeTTLs is ignored.
func TestCachedAppsIgnoresStaleIndex(t *testing.T) {
	src := &listingStore{appKeys: []string{"known-app"}}
	c, _ := NewCachedApps(src, time.Minute, 1000)
	at := time.Unix(1_790_000_000, 0)
	c.now = func() time.Time { return at }
	c.unknown = newTokenBucket(1, c.now)
	ctx := context.Background()
	if err := c.reload(ctx); err != nil {
		t.Fatal(err)
	}
	src.listFail.Store(true)
	at = at.Add(indexMaxAgeTTLs * time.Minute)
	for i := 0; i < 10; i++ {
		_, _ = c.AppByKey(ctx, fmt.Sprintf("random-%d", i))
	}
	if src.appCalls != 10 {
		t.Fatalf("stale index still limited queries: %d calls", src.appCalls)
	}
}
