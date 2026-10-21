package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// Admin is one console account. PasswordHash and the TOTP ciphertext never
// leave the server.
type Admin struct {
	ID                 int64          `db:"id"`
	Username           string         `db:"username"`
	DisplayName        string         `db:"display_name"`
	PasswordHash       string         `db:"password_hash"`
	Role               string         `db:"role"`
	Status             int            `db:"status"`
	SessionVersion     int64          `db:"session_version"`
	MustChangePassword bool           `db:"must_change_password"`
	TOTPCiphertext     []byte         `db:"totp_ciphertext"`
	TOTPKEKID          sql.NullString `db:"totp_kek_id"`
	TOTPLastStep       sql.NullInt64  `db:"totp_last_step"`
	LastLoginAt        sql.NullTime   `db:"last_login_at"`
	LastLoginIP        sql.NullString `db:"last_login_ip"`
	CreatedAt          time.Time      `db:"created_at"`
}

func (a *Admin) Enabled() bool     { return a.Status == statusEnabled }
func (a *Admin) TOTPEnabled() bool { return len(a.TOTPCiphertext) > 0 && a.TOTPKEKID.Valid }

const adminColumns = "id, username, display_name, password_hash, role, status, session_version, must_change_password, " +
	"totp_ciphertext, totp_kek_id, totp_last_step, last_login_at, last_login_ip, created_at"

// Admins stores console accounts. Writes are audited like AppAdmin's.
type Admins struct {
	conn sqlx.SqlConn
}

func NewAdmins(conn sqlx.SqlConn) *Admins { return &Admins{conn: conn} }

func (r *Admins) get(ctx context.Context, where string, arg any) (*Admin, error) {
	var a Admin
	err := r.conn.QueryRowCtx(ctx, &a, "SELECT "+adminColumns+" FROM sys_admin WHERE "+where, arg)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Admins) ByUsername(ctx context.Context, username string) (*Admin, error) {
	return r.get(ctx, "username = ?", username)
}

func (r *Admins) ByID(ctx context.Context, id int64) (*Admin, error) {
	return r.get(ctx, "id = ?", id)
}

func (r *Admins) List(ctx context.Context) ([]Admin, error) {
	var out []Admin
	err := r.conn.QueryRowsCtx(ctx, &out, "SELECT "+adminColumns+" FROM sys_admin ORDER BY id")
	return out, err
}

// CountEnabledOwners guards against locking everyone out.
func (r *Admins) CountEnabledOwners(ctx context.Context) (int, error) {
	var n int
	err := r.conn.QueryRowCtx(ctx, &n, "SELECT COUNT(*) FROM sys_admin WHERE role = 'owner' AND status = 1")
	return n, err
}

func (r *Admins) Create(ctx context.Context, username, displayName, passwordHash, role string, mustChange bool) (int64, error) {
	var id int64
	err := r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		res, err := s.ExecCtx(ctx, "INSERT INTO sys_admin (username, display_name, password_hash, role, status, must_change_password) VALUES (?, ?, ?, ?, 1, ?)",
			username, displayName, passwordHash, role, mustChange)
		if isDuplicate(err) {
			return domain.ErrDuplicate
		}
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return writeAudit(ctx, s, "admin:"+username)
	})
	return id, err
}

// update runs one UPDATE on an existing admin plus its audit row; with
// endSessions it also bumps session_version, signing the admin out
// everywhere.
func (r *Admins) update(ctx context.Context, id int64, endSessions bool, set string, args ...any) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		var username string
		err := s.QueryRowCtx(ctx, &username, "SELECT username FROM sys_admin WHERE id = ? FOR UPDATE", id)
		if errors.Is(err, sqlx.ErrNotFound) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if endSessions {
			set += ", session_version = session_version + 1"
		}
		if _, err := s.ExecCtx(ctx, "UPDATE sys_admin SET "+set+" WHERE id = ?", append(args, id)...); err != nil {
			return err
		}
		return writeAudit(ctx, s, "admin:"+username)
	})
}

// SetPassword replaces the hash and ends every session of the admin.
func (r *Admins) SetPassword(ctx context.Context, id int64, hash string, mustChange bool) error {
	return r.update(ctx, id, true, "password_hash = ?, must_change_password = ?", hash, mustChange)
}

func (r *Admins) SetStatus(ctx context.Context, id int64, enabled bool) error {
	status := statusDisabled
	if enabled {
		status = statusEnabled
	}
	return r.update(ctx, id, !enabled, "status = ?", status)
}

func (r *Admins) SetRole(ctx context.Context, id int64, role string) error {
	return r.update(ctx, id, true, "role = ?", role)
}

func (r *Admins) SetDisplayName(ctx context.Context, id int64, name string) error {
	return r.update(ctx, id, false, "display_name = ?", name)
}

// SetTOTP stores a sealed TOTP secret with the step it was confirmed at, or
// clears it (nil ciphertext). Both end other sessions.
func (r *Admins) SetTOTP(ctx context.Context, id int64, ciphertext []byte, kekID string, step int64) error {
	if ciphertext == nil {
		return r.update(ctx, id, true, "totp_ciphertext = NULL, totp_kek_id = NULL, totp_last_step = NULL")
	}
	return r.update(ctx, id, true, "totp_ciphertext = ?, totp_kek_id = ?, totp_last_step = ?", ciphertext, kekID, step)
}

// UseTOTPStep records an accepted step; false when an equal or later step
// was already used, so two concurrent logins cannot share one code.
func (r *Admins) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	res, err := r.conn.ExecCtx(ctx, "UPDATE sys_admin SET totp_last_step = ? WHERE id = ? AND (totp_last_step IS NULL OR totp_last_step < ?)", step, id, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordLogin is bookkeeping, not an audited change: the login itself is
// audited separately.
func (r *Admins) RecordLogin(ctx context.Context, id int64, ip string, at time.Time) error {
	_, err := r.conn.ExecCtx(ctx, "UPDATE sys_admin SET last_login_at = ?, last_login_ip = ? WHERE id = ?", at.UTC(), truncate(ip, 64), id)
	return err
}
