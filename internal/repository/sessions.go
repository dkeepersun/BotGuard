package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// AdminSession is the server side of a console login. The cookie holds only
// the random session id; Redis stores it under the id's SHA-256, so a Redis
// dump cannot be replayed as cookies.
type AdminSession struct {
	ID        string // cookie value; only set on creation
	AdminID   int64
	Version   int64 // the admin's session_version at login
	CSRF      string
	CreatedAt time.Time
}

type Sessions struct {
	rds *redis.Redis
	// Idle ends a session after inactivity; Absolute caps its whole life.
	Idle, Absolute time.Duration
}

func NewSessions(rds *redis.Redis, idle, absolute time.Duration) *Sessions {
	return &Sessions{rds: rds, Idle: idle, Absolute: absolute}
}

func sessionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "bg:admin:session:" + hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create starts a session with a fresh id and CSRF token.
func (s *Sessions) Create(ctx context.Context, adminID, version int64, now time.Time) (*AdminSession, error) {
	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}
	key := sessionKey(id)
	err = s.rds.HmsetCtx(ctx, key, map[string]string{
		"admin": strconv.FormatInt(adminID, 10), "version": strconv.FormatInt(version, 10),
		"csrf": csrf, "created": strconv.FormatInt(now.Unix(), 10),
	})
	if err == nil {
		err = s.rds.ExpireCtx(ctx, key, int(s.Idle/time.Second))
	}
	if err != nil {
		return nil, err
	}
	return &AdminSession{ID: id, AdminID: adminID, Version: version, CSRF: csrf, CreatedAt: now}, nil
}

// Get returns the session and extends its idle timeout, or nil when it does
// not exist or has passed its absolute lifetime.
func (s *Sessions) Get(ctx context.Context, id string, now time.Time) (*AdminSession, error) {
	if id == "" || len(id) > 64 {
		return nil, nil
	}
	key := sessionKey(id)
	m, err := s.rds.HgetallCtx(ctx, key)
	if err != nil || len(m) == 0 {
		return nil, err
	}
	created, _ := strconv.ParseInt(m["created"], 10, 64)
	if now.Sub(time.Unix(created, 0)) > s.Absolute {
		_, _ = s.rds.DelCtx(ctx, key)
		return nil, nil
	}
	adminID, _ := strconv.ParseInt(m["admin"], 10, 64)
	version, _ := strconv.ParseInt(m["version"], 10, 64)
	if err := s.rds.ExpireCtx(ctx, key, int(s.Idle/time.Second)); err != nil {
		return nil, err
	}
	return &AdminSession{AdminID: adminID, Version: version, CSRF: m["csrf"], CreatedAt: time.Unix(created, 0)}, nil
}

func (s *Sessions) Delete(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := s.rds.DelCtx(ctx, sessionKey(id))
	return err
}
