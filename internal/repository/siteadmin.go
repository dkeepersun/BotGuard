package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/go-sql-driver/mysql"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const statusDisabled = 2

// SiteAdmin writes site configuration. Every change that affects issued
// challenges bumps active_policy_version in the same transaction.
type SiteAdmin struct {
	conn sqlx.SqlConn
}

func NewSiteAdmin(conn sqlx.SqlConn) *SiteAdmin { return &SiteAdmin{conn: conn} }

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func siteIDForUpdate(ctx context.Context, s sqlx.Session, publicKey string) (int64, error) {
	var id int64
	err := s.QueryRowCtx(ctx, &id, "SELECT id FROM bg_site WHERE public_key = ? FOR UPDATE", publicKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return 0, domain.ErrNotFound
	}
	return id, err
}

func bumpPolicy(ctx context.Context, s sqlx.Session, siteID int64) error {
	_, err := s.ExecCtx(ctx, "UPDATE bg_site SET active_policy_version = active_policy_version + 1 WHERE id = ?", siteID)
	return err
}

func (r *SiteAdmin) CreateSite(ctx context.Context, publicKey, name string) error {
	_, err := r.conn.ExecCtx(ctx, "INSERT INTO bg_site (public_key, name, status) VALUES (?, ?, ?)", publicKey, name, statusEnabled)
	if isDuplicate(err) {
		return domain.ErrDuplicate
	}
	return err
}

func (r *SiteAdmin) SetSiteEnabled(ctx context.Context, publicKey string, enabled bool) error {
	status := statusDisabled
	if enabled {
		status = statusEnabled
	}
	res, err := r.conn.ExecCtx(ctx, "UPDATE bg_site SET status = ? WHERE public_key = ?", status, publicKey)
	if err != nil {
		return err
	}
	return requireRow(ctx, r.conn, res, "SELECT COUNT(*) FROM bg_site WHERE public_key = ?", publicKey)
}

// requireRow distinguishes "no such row" from "row already had this value",
// since MySQL reports zero affected rows for both.
func requireRow(ctx context.Context, conn sqlx.SqlConn, res sql.Result, countQuery string, args ...any) error {
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	var count int
	if err := conn.QueryRowCtx(ctx, &count, countQuery, args...); err != nil {
		return err
	}
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SiteAdmin) AddOrigin(ctx context.Context, publicKey, origin string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := siteIDForUpdate(ctx, s, publicKey)
		if err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, "INSERT INTO bg_site_origin (site_id, origin) VALUES (?, ?)", id, origin); err != nil {
			if isDuplicate(err) {
				return domain.ErrDuplicate
			}
			return err
		}
		return bumpPolicy(ctx, s, id)
	})
}

func (r *SiteAdmin) RemoveOrigin(ctx context.Context, publicKey, origin string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := siteIDForUpdate(ctx, s, publicKey)
		if err != nil {
			return err
		}
		res, err := s.ExecCtx(ctx, "DELETE FROM bg_site_origin WHERE site_id = ? AND origin = ?", id, origin)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		return bumpPolicy(ctx, s, id)
	})
}

func (r *SiteAdmin) UpsertAction(ctx context.Context, publicKey, action string, p domain.ActionPolicy) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := siteIDForUpdate(ctx, s, publicKey)
		if err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, `INSERT INTO bg_site_action (site_id, action, enabled, pow_puzzles, pow_difficulty)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), pow_puzzles = VALUES(pow_puzzles), pow_difficulty = VALUES(pow_difficulty)`,
			id, action, p.Enabled, p.Puzzles, p.Difficulty); err != nil {
			return err
		}
		return bumpPolicy(ctx, s, id)
	})
}

type NewAPIKey struct {
	KeyID       string
	SecretHash  []byte
	ExpiresAt   *time.Time
	RotatedFrom string
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func (r *SiteAdmin) CreateAPIKey(ctx context.Context, publicKey string, k NewAPIKey) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := siteIDForUpdate(ctx, s, publicKey)
		if err != nil {
			return err
		}
		return insertKey(ctx, s, id, k)
	})
}

func insertKey(ctx context.Context, s sqlx.Session, siteID int64, k NewAPIKey) error {
	_, err := s.ExecCtx(ctx,
		"INSERT INTO bg_api_key (key_id, site_id, secret_hash, status, expires_at, rotated_from) VALUES (?, ?, ?, ?, ?, ?)",
		k.KeyID, siteID, k.SecretHash, statusEnabled, nullTime(k.ExpiresAt), nullString(k.RotatedFrom))
	if isDuplicate(err) {
		return domain.ErrDuplicate
	}
	return err
}

func (r *SiteAdmin) RevokeAPIKey(ctx context.Context, keyID string) error {
	res, err := r.conn.ExecCtx(ctx, "UPDATE bg_api_key SET status = ? WHERE key_id = ?", statusDisabled, keyID)
	if err != nil {
		return err
	}
	return requireRow(ctx, r.conn, res, "SELECT COUNT(*) FROM bg_api_key WHERE key_id = ?", keyID)
}

// RotateAPIKey adds a replacement for an enabled key and shortens the old
// key's life to oldUntil (never extends it), in one transaction.
func (r *SiteAdmin) RotateAPIKey(ctx context.Context, oldKeyID string, k NewAPIKey, oldUntil time.Time) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		var old keyRow
		err := s.QueryRowCtx(ctx, &old,
			"SELECT key_id, site_id, secret_hash, status, expires_at FROM bg_api_key WHERE key_id = ? FOR UPDATE", oldKeyID)
		if errors.Is(err, sqlx.ErrNotFound) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if old.Status != statusEnabled {
			return domain.ErrInvalidState
		}
		k.RotatedFrom = oldKeyID
		if err := insertKey(ctx, s, old.SiteID, k); err != nil {
			return err
		}
		until := oldUntil.UTC()
		if old.ExpiresAt.Valid && old.ExpiresAt.Time.Before(until) {
			until = old.ExpiresAt.Time
		}
		_, err = s.ExecCtx(ctx, "UPDATE bg_api_key SET expires_at = ? WHERE key_id = ?", until, oldKeyID)
		return err
	})
}

type SiteSummary struct {
	PublicKey     string    `db:"public_key"`
	Name          string    `db:"name"`
	Enabled       bool      `db:"enabled"`
	PolicyVersion int64     `db:"active_policy_version"`
	CreatedAt     time.Time `db:"created_at"`
}

func (r *SiteAdmin) ListSites(ctx context.Context) ([]SiteSummary, error) {
	var out []SiteSummary
	err := r.conn.QueryRowsCtx(ctx, &out,
		"SELECT public_key, name, status = 1 AS enabled, active_policy_version, created_at FROM bg_site ORDER BY id")
	return out, err
}

type ActionDetail struct {
	Action     string `db:"action"`
	Enabled    bool   `db:"enabled"`
	Puzzles    int    `db:"pow_puzzles"`
	Difficulty uint64 `db:"pow_difficulty"`
}

type KeyDetail struct {
	KeyID       string         `db:"key_id"`
	Enabled     bool           `db:"enabled"`
	ExpiresAt   sql.NullTime   `db:"expires_at"`
	RotatedFrom sql.NullString `db:"rotated_from"`
	CreatedAt   time.Time      `db:"created_at"`
}

type SiteDetail struct {
	SiteSummary
	Origins []string
	Actions []ActionDetail
	Keys    []KeyDetail
}

// SiteDetail never returns secret hashes.
func (r *SiteAdmin) SiteDetail(ctx context.Context, publicKey string) (*SiteDetail, error) {
	var d SiteDetail
	var id int64
	err := r.conn.QueryRowCtx(ctx, &id, "SELECT id FROM bg_site WHERE public_key = ?", publicKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowCtx(ctx, &d.SiteSummary,
		"SELECT public_key, name, status = 1 AS enabled, active_policy_version, created_at FROM bg_site WHERE id = ?", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Origins, "SELECT origin FROM bg_site_origin WHERE site_id = ? ORDER BY origin", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Actions,
		"SELECT action, enabled, pow_puzzles, pow_difficulty FROM bg_site_action WHERE site_id = ? ORDER BY action", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Keys,
		"SELECT key_id, status = 1 AS enabled, expires_at, rotated_from, created_at FROM bg_api_key WHERE site_id = ? ORDER BY id", id); err != nil {
		return nil, err
	}
	return &d, nil
}
