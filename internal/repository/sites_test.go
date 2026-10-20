package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
)

type countingStore struct {
	siteCalls int
	fail      bool
}

func (s *countingStore) SiteByKey(_ context.Context, key string) (*domain.Site, error) {
	s.siteCalls++
	if s.fail {
		return nil, errors.New("db down")
	}
	if key == "known-site" {
		return &domain.Site{ID: 1, PublicKey: key}, nil
	}
	return nil, domain.ErrNotFound
}

func (s *countingStore) APIKey(context.Context, string) (*domain.APIKey, error) {
	return nil, domain.ErrNotFound
}

func TestCachedSitesCachesHitsAndMisses(t *testing.T) {
	src := &countingStore{}
	c, err := NewCachedSites(src, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if s, err := c.SiteByKey(ctx, "known-site"); err != nil || s.ID != 1 {
			t.Fatalf("hit: %v %v", s, err)
		}
		if _, err := c.SiteByKey(ctx, "random-site"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("miss: %v", err)
		}
	}
	if src.siteCalls != 2 {
		t.Fatalf("%d source calls", src.siteCalls)
	}
}

func TestCachedSitesDoesNotCacheErrors(t *testing.T) {
	src := &countingStore{fail: true}
	c, _ := NewCachedSites(src, time.Minute, 100)
	ctx := context.Background()
	if _, err := c.SiteByKey(ctx, "known-site"); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("db error became %v", err)
	}
	src.fail = false
	if s, err := c.SiteByKey(ctx, "known-site"); err != nil || s.ID != 1 {
		t.Fatalf("after recovery: %v %v", s, err)
	}
}
