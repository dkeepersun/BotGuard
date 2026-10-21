package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/go-sql-driver/mysql"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const statusDisabled = 2

// AppAdmin writes app configuration. Every change that affects issued
// challenges bumps active_policy_version in the same transaction.
type AppAdmin struct {
	conn sqlx.SqlConn
}

func NewAppAdmin(conn sqlx.SqlConn) *AppAdmin { return &AppAdmin{conn: conn} }

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func appIDForUpdate(ctx context.Context, s sqlx.Session, appKey string) (int64, error) {
	var id int64
	err := s.QueryRowCtx(ctx, &id, "SELECT id FROM bg_app WHERE app_key = ? FOR UPDATE", appKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return 0, domain.ErrNotFound
	}
	return id, err
}

func bumpPolicy(ctx context.Context, s sqlx.Session, appID int64) error {
	_, err := s.ExecCtx(ctx, "UPDATE bg_app SET active_policy_version = active_policy_version + 1 WHERE id = ?", appID)
	return err
}

func (r *AppAdmin) CreateApp(ctx context.Context, appKey, name, remark string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		_, err := s.ExecCtx(ctx, "INSERT INTO bg_app (app_key, name, remark, status) VALUES (?, ?, ?, ?)", appKey, name, remark, statusEnabled)
		if isDuplicate(err) {
			return domain.ErrDuplicate
		}
		if err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

func (r *AppAdmin) SetAppEnabled(ctx context.Context, appKey string, enabled bool) error {
	status := statusDisabled
	if enabled {
		status = statusEnabled
	}
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		if _, err := appIDForUpdate(ctx, s, appKey); err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, "UPDATE bg_app SET status = ? WHERE app_key = ?", status, appKey); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

// UpdateApp changes the display name and remark; neither affects issued
// challenges, so the policy version stays.
func (r *AppAdmin) UpdateApp(ctx context.Context, appKey, name, remark string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		if _, err := appIDForUpdate(ctx, s, appKey); err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, "UPDATE bg_app SET name = ?, remark = ? WHERE app_key = ?", name, remark, appKey); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

func (r *AppAdmin) DeleteAction(ctx context.Context, appKey, action string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		res, err := s.ExecCtx(ctx, "DELETE FROM bg_app_action WHERE app_id = ? AND action = ?", id, action)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		if err := bumpPolicy(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

func (r *AppAdmin) AddOrigin(ctx context.Context, appKey, origin string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, "INSERT INTO bg_app_origin (app_id, origin) VALUES (?, ?)", id, origin); err != nil {
			if isDuplicate(err) {
				return domain.ErrDuplicate
			}
			return err
		}
		if err := bumpPolicy(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

func (r *AppAdmin) RemoveOrigin(ctx context.Context, appKey, origin string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		res, err := s.ExecCtx(ctx, "DELETE FROM bg_app_origin WHERE app_id = ? AND origin = ?", id, origin)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		if err := bumpPolicy(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

func (r *AppAdmin) UpsertAction(ctx context.Context, appKey, action string, p domain.ActionPolicy) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		// Saving the same policy again is a no-op: no new policy version, no
		// audit row (seed scripts and retries re-apply configuration).
		var cur []actionRow
		if err := s.QueryRowsCtx(ctx, &cur, "SELECT action, enabled, challenge_mode, challenge_kinds, pow_puzzles, pow_difficulty FROM bg_app_action WHERE app_id = ? AND action = ? FOR UPDATE", id, action); err != nil {
			return err
		}
		if len(cur) == 1 && cur[0].Enabled == p.Enabled && cur[0].Mode == p.Mode && cur[0].Kinds == strings.Join(p.Kinds, ",") &&
			cur[0].Puzzles == p.Puzzles && cur[0].Difficulty == p.Difficulty {
			return nil
		}
		if _, err := s.ExecCtx(ctx, `INSERT INTO bg_app_action (app_id, action, enabled, challenge_mode, challenge_kinds, pow_puzzles, pow_difficulty)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), challenge_mode = VALUES(challenge_mode),
  challenge_kinds = VALUES(challenge_kinds), pow_puzzles = VALUES(pow_puzzles), pow_difficulty = VALUES(pow_difficulty)`,
			id, action, p.Enabled, p.Mode, strings.Join(p.Kinds, ","), p.Puzzles, p.Difficulty); err != nil {
			return err
		}
		if err := bumpPolicy(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

// NewSecret is a sealed secret pair ready to store; the plaintext key is
// never passed to the repository.
type NewSecret struct {
	SecretID    string
	Ciphertext  []byte
	KEKID       string
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

func (r *AppAdmin) CreateSecret(ctx context.Context, appKey string, k NewSecret) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		id, err := appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		if err := insertSecret(ctx, s, id, k); err != nil {
			return err
		}
		return writeAudit(ctx, s, k.SecretID)
	})
}

func insertSecret(ctx context.Context, s sqlx.Session, appID int64, k NewSecret) error {
	_, err := s.ExecCtx(ctx,
		"INSERT INTO bg_app_secret (secret_id, app_id, secret_ciphertext, kek_id, status, expires_at, rotated_from) VALUES (?, ?, ?, ?, ?, ?, ?)",
		k.SecretID, appID, k.Ciphertext, k.KEKID, statusEnabled, nullTime(k.ExpiresAt), nullString(k.RotatedFrom))
	if isDuplicate(err) {
		return domain.ErrDuplicate
	}
	return err
}

func (r *AppAdmin) RevokeSecret(ctx context.Context, secretID string) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		var n int
		if err := s.QueryRowCtx(ctx, &n, "SELECT COUNT(*) FROM bg_app_secret WHERE secret_id = ? FOR UPDATE", secretID); err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrNotFound
		}
		if _, err := s.ExecCtx(ctx, "UPDATE bg_app_secret SET status = ? WHERE secret_id = ?", statusDisabled, secretID); err != nil {
			return err
		}
		return writeAudit(ctx, s, secretID)
	})
}

// RotateSecret adds a replacement for an enabled secret pair and shortens the
// old pair's life to oldUntil (never extends it), in one transaction. It
// returns when the old pair actually stops working.
func (r *AppAdmin) RotateSecret(ctx context.Context, oldSecretID string, k NewSecret, oldUntil time.Time) (time.Time, error) {
	until := oldUntil.UTC()
	err := r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		var old secretRow
		err := s.QueryRowCtx(ctx, &old,
			"SELECT "+secretColumns+" FROM bg_app_secret WHERE secret_id = ? FOR UPDATE", oldSecretID)
		if errors.Is(err, sqlx.ErrNotFound) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if old.Status != statusEnabled {
			return domain.ErrInvalidState
		}
		k.RotatedFrom = oldSecretID
		if err := insertSecret(ctx, s, old.AppID, k); err != nil {
			return err
		}
		if old.ExpiresAt.Valid && old.ExpiresAt.Time.Before(until) {
			until = old.ExpiresAt.Time.UTC()
		}
		if _, err = s.ExecCtx(ctx, "UPDATE bg_app_secret SET expires_at = ? WHERE secret_id = ?", until, oldSecretID); err != nil {
			return err
		}
		return writeAudit(ctx, s, oldSecretID)
	})
	return until, err
}

// SealedSample is the newest secret sealed under one KEK id, used to check
// that an operator's KEK is the one the gateway decrypts with.
type SealedSample struct {
	SecretID   string `db:"secret_id"`
	Ciphertext []byte `db:"secret_ciphertext"`
}

// SampleSealed returns the newest secret sealed under kekID, and whether any
// secret is sealed under another id (normal during a KEK rotation).
func (r *AppAdmin) SampleSealed(ctx context.Context, kekID string) (*SealedSample, bool, error) {
	var others int
	if err := r.conn.QueryRowCtx(ctx, &others,
		"SELECT COUNT(*) FROM bg_app_secret WHERE secret_ciphertext IS NOT NULL AND kek_id <> ?", kekID); err != nil {
		return nil, false, err
	}
	var s SealedSample
	err := r.conn.QueryRowCtx(ctx, &s,
		"SELECT secret_id, secret_ciphertext FROM bg_app_secret WHERE kek_id = ? AND secret_ciphertext IS NOT NULL ORDER BY id DESC LIMIT 1", kekID)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, others > 0, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &s, others > 0, nil
}

type AppSummary struct {
	ID            int64     `db:"id"`
	AppKey        string    `db:"app_key"`
	Name          string    `db:"name"`
	Remark        string    `db:"remark"`
	Enabled       bool      `db:"enabled"`
	PolicyVersion int64     `db:"active_policy_version"`
	CreatedAt     time.Time `db:"created_at"`
}

func (r *AppAdmin) ListApps(ctx context.Context) ([]AppSummary, error) {
	var out []AppSummary
	err := r.conn.QueryRowsCtx(ctx, &out,
		"SELECT id, app_key, name, remark, status = 1 AS enabled, active_policy_version, created_at FROM bg_app ORDER BY id")
	return out, err
}

type ActionDetail struct {
	Action     string `db:"action"`
	Enabled    bool   `db:"enabled"`
	Mode       string `db:"challenge_mode"`
	Kinds      string `db:"challenge_kinds"`
	Puzzles    int    `db:"pow_puzzles"`
	Difficulty uint64 `db:"pow_difficulty"`
}

type SecretDetail struct {
	SecretID    string         `db:"secret_id"`
	Enabled     bool           `db:"enabled"`
	ExpiresAt   sql.NullTime   `db:"expires_at"`
	RotatedFrom sql.NullString `db:"rotated_from"`
	CreatedAt   time.Time      `db:"created_at"`
}

type AppDetail struct {
	AppSummary
	Origins []string
	Actions []ActionDetail
	Secrets []SecretDetail
}

// AppDetail never returns secret keys or ciphertexts.
func (r *AppAdmin) AppDetail(ctx context.Context, appKey string) (*AppDetail, error) {
	var d AppDetail
	var id int64
	err := r.conn.QueryRowCtx(ctx, &id, "SELECT id FROM bg_app WHERE app_key = ?", appKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowCtx(ctx, &d.AppSummary,
		"SELECT id, app_key, name, remark, status = 1 AS enabled, active_policy_version, created_at FROM bg_app WHERE id = ?", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Origins, "SELECT origin FROM bg_app_origin WHERE app_id = ? ORDER BY origin", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Actions,
		"SELECT action, enabled, challenge_mode, challenge_kinds, pow_puzzles, pow_difficulty FROM bg_app_action WHERE app_id = ? ORDER BY action", id); err != nil {
		return nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &d.Secrets,
		"SELECT secret_id, status = 1 AS enabled, expires_at, rotated_from, created_at FROM bg_app_secret WHERE app_id = ? ORDER BY id", id); err != nil {
		return nil, err
	}
	return &d, nil
}
