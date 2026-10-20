package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/zeromicro/go-zero/core/collection"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const (
	statusEnabled = 1
)

// AppStore is what the gateway needs from app configuration.
type AppStore interface {
	AppByKey(ctx context.Context, appKey string) (*domain.App, error)
	SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error)
}

// MySQLApps reads app configuration. It is read-only; writes belong to admin.
type MySQLApps struct {
	conn   sqlx.SqlConn
	sealer *sealer.Sealer
}

func NewMySQLApps(conn sqlx.SqlConn, s *sealer.Sealer) *MySQLApps {
	return &MySQLApps{conn: conn, sealer: s}
}

type appRow struct {
	ID            int64  `db:"id"`
	AppKey        string `db:"app_key"`
	Status        int    `db:"status"`
	PolicyVersion int64  `db:"active_policy_version"`
}

type actionRow struct {
	Action     string `db:"action"`
	Enabled    bool   `db:"enabled"`
	Puzzles    int    `db:"pow_puzzles"`
	Difficulty uint64 `db:"pow_difficulty"`
}

type secretRow struct {
	SecretID   string         `db:"secret_id"`
	AppID      int64          `db:"app_id"`
	Ciphertext []byte         `db:"secret_ciphertext"`
	KEKID      sql.NullString `db:"kek_id"`
	Status     int            `db:"status"`
	ExpiresAt  sql.NullTime   `db:"expires_at"`
}

const secretColumns = "secret_id, app_id, secret_ciphertext, kek_id, status, expires_at"

func (r *MySQLApps) AppByKey(ctx context.Context, appKey string) (*domain.App, error) {
	var row appRow
	err := r.conn.QueryRowCtx(ctx, &row,
		"SELECT id, app_key, status, active_policy_version FROM bg_app WHERE app_key = ?", appKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var origins []string
	if err := r.conn.QueryRowsCtx(ctx, &origins, "SELECT origin FROM bg_app_origin WHERE app_id = ?", row.ID); err != nil {
		return nil, err
	}
	var actions []actionRow
	if err := r.conn.QueryRowsCtx(ctx, &actions,
		"SELECT action, enabled, pow_puzzles, pow_difficulty FROM bg_app_action WHERE app_id = ?", row.ID); err != nil {
		return nil, err
	}
	app := &domain.App{
		ID:            row.ID,
		AppKey:        row.AppKey,
		Enabled:       row.Status == statusEnabled,
		PolicyVersion: row.PolicyVersion,
		Actions:       make(map[string]domain.ActionPolicy, len(actions)),
	}
	for _, o := range origins {
		n, ok := domain.NormalizeOrigin(o)
		if !ok || n != o {
			// Admin writes normalized origins; anything else is ignored, not guessed at.
			logx.WithContext(ctx).Errorf("app %d has non-normalized origin, ignored", row.ID)
			continue
		}
		app.Origins = append(app.Origins, n)
	}
	for _, a := range actions {
		app.Actions[a.Action] = domain.ActionPolicy{Enabled: a.Enabled, Puzzles: a.Puzzles, Difficulty: a.Difficulty}
	}
	return app, nil
}

// SecretPair decrypts the secret key. A row without ciphertext (a revoked
// legacy Bearer key) is returned unusable; a ciphertext that does not open is
// an error, so a missing or wrong KEK fails closed instead of rejecting
// callers as if their signature were wrong.
func (r *MySQLApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	var row secretRow
	err := r.conn.QueryRowCtx(ctx, &row, "SELECT "+secretColumns+" FROM bg_app_secret WHERE secret_id = ?", secretID)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k := &domain.SecretPair{SecretID: row.SecretID, AppID: row.AppID, Enabled: row.Status == statusEnabled}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		k.ExpiresAt = &t
	}
	if len(row.Ciphertext) == 0 || !row.KEKID.Valid {
		k.Enabled = false
		return k, nil
	}
	if k.SecretKey, err = r.sealer.Open(row.KEKID.String, row.SecretID, row.Ciphertext); err != nil {
		return nil, fmt.Errorf("secret %s: %w", row.SecretID, err)
	}
	return k, nil
}

// CachedApps bounds both staleness (ttl) and memory (limit). Misses are
// cached too, so random app keys on the public endpoint cannot turn into one
// MySQL query each.
type CachedApps struct {
	src  AppStore
	apps *collection.Cache
	keys *collection.Cache
}

type miss struct{}

func NewCachedApps(src AppStore, ttl time.Duration, limit int) (*CachedApps, error) {
	apps, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-apps"))
	if err != nil {
		return nil, err
	}
	keys, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-secret-pairs"))
	if err != nil {
		return nil, err
	}
	return &CachedApps{src: src, apps: apps, keys: keys}, nil
}

func take[T any](c *collection.Cache, key string, fetch func() (*T, error)) (*T, error) {
	v, err := c.Take(key, func() (any, error) {
		got, err := fetch()
		if errors.Is(err, domain.ErrNotFound) {
			return miss{}, nil
		}
		if err != nil {
			return nil, err
		}
		return got, nil
	})
	if err != nil {
		return nil, err
	}
	if got, ok := v.(*T); ok {
		return got, nil
	}
	return nil, domain.ErrNotFound
}

func (c *CachedApps) AppByKey(ctx context.Context, appKey string) (*domain.App, error) {
	return take(c.apps, appKey, func() (*domain.App, error) { return c.src.AppByKey(ctx, appKey) })
}

func (c *CachedApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	return take(c.keys, secretID, func() (*domain.SecretPair, error) { return c.src.SecretPair(ctx, secretID) })
}
