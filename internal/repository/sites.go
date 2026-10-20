package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/zeromicro/go-zero/core/collection"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const (
	statusEnabled = 1
)

// SiteStore is what the gateway needs from site configuration.
type SiteStore interface {
	SiteByKey(ctx context.Context, publicKey string) (*domain.Site, error)
	APIKey(ctx context.Context, keyID string) (*domain.APIKey, error)
}

// MySQLSites reads site configuration. It is read-only; writes belong to admin.
type MySQLSites struct {
	conn sqlx.SqlConn
}

func NewMySQLSites(conn sqlx.SqlConn) *MySQLSites { return &MySQLSites{conn: conn} }

type siteRow struct {
	ID            int64  `db:"id"`
	PublicKey     string `db:"public_key"`
	Status        int    `db:"status"`
	PolicyVersion int64  `db:"active_policy_version"`
}

type actionRow struct {
	Action     string `db:"action"`
	Enabled    bool   `db:"enabled"`
	Puzzles    int    `db:"pow_puzzles"`
	Difficulty uint64 `db:"pow_difficulty"`
}

type keyRow struct {
	KeyID      string       `db:"key_id"`
	SiteID     int64        `db:"site_id"`
	SecretHash []byte       `db:"secret_hash"`
	Status     int          `db:"status"`
	ExpiresAt  sql.NullTime `db:"expires_at"`
}

func (r *MySQLSites) SiteByKey(ctx context.Context, publicKey string) (*domain.Site, error) {
	var row siteRow
	err := r.conn.QueryRowCtx(ctx, &row,
		"SELECT id, public_key, status, active_policy_version FROM bg_site WHERE public_key = ?", publicKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var origins []string
	if err := r.conn.QueryRowsCtx(ctx, &origins, "SELECT origin FROM bg_site_origin WHERE site_id = ?", row.ID); err != nil {
		return nil, err
	}
	var actions []actionRow
	if err := r.conn.QueryRowsCtx(ctx, &actions,
		"SELECT action, enabled, pow_puzzles, pow_difficulty FROM bg_site_action WHERE site_id = ?", row.ID); err != nil {
		return nil, err
	}
	site := &domain.Site{
		ID:            row.ID,
		PublicKey:     row.PublicKey,
		Enabled:       row.Status == statusEnabled,
		PolicyVersion: row.PolicyVersion,
		Actions:       make(map[string]domain.ActionPolicy, len(actions)),
	}
	for _, o := range origins {
		n, ok := domain.NormalizeOrigin(o)
		if !ok || n != o {
			// Admin writes normalized origins; anything else is ignored, not guessed at.
			logx.WithContext(ctx).Errorf("site %d has non-normalized origin, ignored", row.ID)
			continue
		}
		site.Origins = append(site.Origins, n)
	}
	for _, a := range actions {
		site.Actions[a.Action] = domain.ActionPolicy{Enabled: a.Enabled, Puzzles: a.Puzzles, Difficulty: a.Difficulty}
	}
	return site, nil
}

func (r *MySQLSites) APIKey(ctx context.Context, keyID string) (*domain.APIKey, error) {
	var row keyRow
	err := r.conn.QueryRowCtx(ctx, &row,
		"SELECT key_id, site_id, secret_hash, status, expires_at FROM bg_api_key WHERE key_id = ?", keyID)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k := &domain.APIKey{KeyID: row.KeyID, SiteID: row.SiteID, SecretHash: row.SecretHash, Enabled: row.Status == statusEnabled}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		k.ExpiresAt = &t
	}
	return k, nil
}

// CachedSites bounds both staleness (ttl) and memory (limit). Misses are
// cached too, so random site keys on the public endpoint cannot turn into one
// MySQL query each.
type CachedSites struct {
	src   SiteStore
	sites *collection.Cache
	keys  *collection.Cache
}

type miss struct{}

func NewCachedSites(src SiteStore, ttl time.Duration, limit int) (*CachedSites, error) {
	sites, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-sites"))
	if err != nil {
		return nil, err
	}
	keys, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-api-keys"))
	if err != nil {
		return nil, err
	}
	return &CachedSites{src: src, sites: sites, keys: keys}, nil
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

func (c *CachedSites) SiteByKey(ctx context.Context, publicKey string) (*domain.Site, error) {
	return take(c.sites, publicKey, func() (*domain.Site, error) { return c.src.SiteByKey(ctx, publicKey) })
}

func (c *CachedSites) APIKey(ctx context.Context, keyID string) (*domain.APIKey, error) {
	return take(c.keys, keyID, func() (*domain.APIKey, error) { return c.src.APIKey(ctx, keyID) })
}
