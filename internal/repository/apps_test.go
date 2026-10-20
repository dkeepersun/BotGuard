package repository

import (
	"context"
	"errors"
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
