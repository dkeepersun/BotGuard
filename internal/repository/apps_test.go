package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
