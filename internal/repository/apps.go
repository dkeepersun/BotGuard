package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/zeromicro/go-zero/core/collection"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const (
	statusEnabled = 1
	// legacyModeSlider is the pre-0004 name of interactive + [slider].
	legacyModeSlider = "slider"
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
	Mode       string `db:"challenge_mode"`
	Kinds      string `db:"challenge_kinds"`
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
		"SELECT id, app_key, status, active_policy_version FROM botguard_app WHERE app_key = ?", appKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var origins []string
	if err := r.conn.QueryRowsCtx(ctx, &origins, "SELECT origin FROM botguard_app_origin WHERE app_id = ?", row.ID); err != nil {
		return nil, err
	}
	var actions []actionRow
	if err := r.conn.QueryRowsCtx(ctx, &actions,
		"SELECT action, enabled, challenge_mode, challenge_kinds, pow_puzzles, pow_difficulty FROM botguard_app_action WHERE app_id = ?", row.ID); err != nil {
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
		mode, kinds, known := storedMode(a.Mode, a.Kinds)
		if !known {
			logx.WithContext(ctx).Errorf("app %d action %s has unknown mode %q, using adaptive", row.ID, a.Action, a.Mode)
		}
		app.Actions[a.Action] = domain.ActionPolicy{Enabled: a.Enabled, Mode: mode, Kinds: kinds,
			Puzzles: a.Puzzles, Difficulty: a.Difficulty}
	}
	return app, nil
}

// SecretPair decrypts the secret key. A row without ciphertext (a revoked
// legacy Bearer key) is returned unusable; a ciphertext that does not open is
// an error, so a missing or wrong KEK fails closed instead of rejecting
// callers as if their signature were wrong.
func (r *MySQLApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	var row secretRow
	err := r.conn.QueryRowCtx(ctx, &row, "SELECT "+secretColumns+" FROM botguard_app_secret WHERE secret_id = ?", secretID)
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
// cached separately, so repeated unknown keys cost no query and a flood of
// random keys cannot evict the apps and secret pairs that are in use. Callers
// must still rate-limit unknown keys: each new one is one MySQL query.
type CachedApps struct {
	src    AppStore
	apps   *collection.Cache
	keys   *collection.Cache
	misses *collection.Cache
}

func NewCachedApps(src AppStore, ttl time.Duration, limit int) (*CachedApps, error) {
	apps, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-apps"))
	if err != nil {
		return nil, err
	}
	keys, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-secret-pairs"))
	if err != nil {
		return nil, err
	}
	misses, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-misses"))
	if err != nil {
		return nil, err
	}
	return &CachedApps{src: src, apps: apps, keys: keys, misses: misses}, nil
}

func take[T any](c *CachedApps, cache *collection.Cache, key string, fetch func() (*T, error)) (*T, error) {
	if _, ok := c.misses.Get(key); ok {
		return nil, domain.ErrNotFound
	}
	v, err := cache.Take(key, func() (any, error) { return fetch() })
	if errors.Is(err, domain.ErrNotFound) {
		c.misses.Set(key, struct{}{})
	}
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

func (c *CachedApps) AppByKey(ctx context.Context, appKey string) (*domain.App, error) {
	return take(c, c.apps, "app:"+appKey, func() (*domain.App, error) { return c.src.AppByKey(ctx, appKey) })
}

func (c *CachedApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	return take(c, c.keys, "secret:"+secretID, func() (*domain.SecretPair, error) { return c.src.SecretPair(ctx, secretID) })
}

// PeekApp returns an app only if it is already cached; it never queries.
func (c *CachedApps) PeekApp(appKey string) (*domain.App, bool) {
	v, ok := c.apps.Get("app:" + appKey)
	if !ok {
		return nil, false
	}
	app, ok := v.(*domain.App)
	return app, ok
}

// storedMode interprets a stored mode. The legacy "slider" was written by
// botguard-ctl before migration 0004 (or by an old ctl against a migrated
// database) and meant "always the slider". Anything unknown becomes adaptive,
// never invisible: a typo must not silently drop to the weakest mode.
func storedMode(mode, kinds string) (string, []string, bool) {
	switch {
	case mode == legacyModeSlider:
		return domain.ModeInteractive, []string{domain.KindSlider}, true
	case domain.ValidMode(mode):
		return mode, ParseKinds(kinds), true
	default:
		return domain.ModeAdaptive, ParseKinds(kinds), false
	}
}

// ParseKinds reads a stored kind list, dropping unknown kinds and falling
// back to the slider so an action can always show an interactive challenge.
func ParseKinds(stored string) []string {
	var out []string
	for _, k := range strings.Split(stored, ",") {
		k = strings.TrimSpace(k)
		if domain.ValidInteractiveKind(k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return []string{domain.KindSlider}
	}
	return out
}
