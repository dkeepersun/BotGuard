package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

func newSessions(t *testing.T, key []byte) (*Sessions, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	s := NewSessions(redis.New(mr.Addr(), redis.WithIdentity()), 30*time.Minute, 8*time.Hour)
	s.Key = key
	return s, mr
}

// Whoever can write to Redis cannot log in: a record needs a signature made
// with the KEK-derived key, bound to the key it is stored under.
func TestSessionsSigned(t *testing.T) {
	ctx, at := context.Background(), time.Unix(1_790_000_000, 0)
	s, mr := newSessions(t, SessionKey([]byte("kek-of-32-bytes-kek-of-32-bytes!")))
	owner, err := s.Create(ctx, 1, 3, at)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, owner.ID, at); err != nil || got == nil || got.AdminID != 1 || got.Version != 3 || got.CSRF != owner.CSRF {
		t.Fatalf("signed session: %+v %v", got, err)
	}

	// A record written straight into Redis, without the signing key.
	forged := "attacker-chosen-session-id"
	mr.HSet(sessionKey(forged), "admin", "1", "version", "3", "csrf", "x", "created", "1790000000")
	if got, _ := s.Get(ctx, forged, at); got != nil {
		t.Fatalf("forged record accepted: %+v", got)
	}
	if mr.Exists(sessionKey(forged)) {
		t.Fatal("forged record kept")
	}
	// The owner's real record copied under an id the attacker knows.
	for _, f := range []string{"admin", "version", "csrf", "created", "mac"} {
		mr.HSet(sessionKey(forged), f, mr.HGet(sessionKey(owner.ID), f))
	}
	if got, _ := s.Get(ctx, forged, at); got != nil {
		t.Fatalf("moved record accepted: %+v", got)
	}
	// A signed record with a field changed, e.g. another admin.
	mr.HSet(sessionKey(owner.ID), "admin", "2")
	if got, _ := s.Get(ctx, owner.ID, at); got != nil {
		t.Fatalf("edited record accepted: %+v", got)
	}

	// Another key (a new KEK) ends the sessions signed with the old one.
	other, _ := s.Create(ctx, 1, 3, at)
	s.Key = SessionKey([]byte("another-kek-another-kek-another!"))
	if got, _ := s.Get(ctx, other.ID, at); got != nil {
		t.Fatal("session survived a key change")
	}
}

func TestSessionsUnsigned(t *testing.T) {
	ctx, at := context.Background(), time.Unix(1_790_000_000, 0)
	s, _ := newSessions(t, nil)
	sess, err := s.Create(ctx, 4, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, sess.ID, at); got == nil || got.AdminID != 4 {
		t.Fatalf("unsigned session: %+v", got)
	}
}
