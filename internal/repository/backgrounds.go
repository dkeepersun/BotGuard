package repository

import (
	"context"
	"errors"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// ErrTooMany means the app already has domain.MaxBackgroundUploads photos.
var ErrTooMany = errors.New("too many backgrounds")

// BackgroundPhoto describes one uploaded photo, without its bytes.
type BackgroundPhoto struct {
	ID        int64     `db:"id"`
	Size      int       `db:"size"`
	CreatedAt time.Time `db:"created_at"`
}

func bumpBackgrounds(ctx context.Context, s sqlx.Session, appID int64) error {
	_, err := s.ExecCtx(ctx, "UPDATE botguard_app SET background_version = background_version + 1 WHERE id = ?", appID)
	return err
}

// SetBackground changes where the app's pictures come from. Gateways see it
// when their app cache refreshes; issued challenges are not affected, so the
// policy version stays.
func (r *AppAdmin) SetBackground(ctx context.Context, appKey, mode string, builtin bool) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		if _, err := r.appIDForUpdate(ctx, s, appKey); err != nil {
			return err
		}
		if _, err := s.ExecCtx(ctx, "UPDATE botguard_app SET background_mode = ?, background_builtin = ? WHERE app_key = ?", mode, builtin, appKey); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

// AddBackground stores a normalized photo and returns its id. The same
// picture twice is ErrDuplicate; past the limit it is ErrTooMany.
func (r *AppAdmin) AddBackground(ctx context.Context, appKey string, image []byte, sha256 string) (int64, error) {
	var id int64
	err := r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		appID, err := r.appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		var n int
		if err := s.QueryRowCtx(ctx, &n, "SELECT COUNT(*) FROM botguard_app_background WHERE app_id = ?", appID); err != nil {
			return err
		}
		if n >= domain.MaxBackgroundUploads {
			return ErrTooMany
		}
		// Look before inserting: a failed INSERT is logged with its
		// arguments, the whole picture included. The app row is locked, so
		// nothing can slip in between.
		var dup int
		if err := s.QueryRowCtx(ctx, &dup, "SELECT COUNT(*) FROM botguard_app_background WHERE app_id = ? AND sha256 = ?", appID, sha256); err != nil {
			return err
		}
		if dup > 0 {
			return domain.ErrDuplicate
		}
		res, err := s.ExecCtx(ctx, "INSERT INTO botguard_app_background (app_id, image, sha256) VALUES (?, ?, ?)", appID, image, sha256)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := bumpBackgrounds(ctx, s, appID); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
	return id, err
}

func (r *AppAdmin) DeleteBackground(ctx context.Context, appKey string, id int64) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		appID, err := r.appIDForUpdate(ctx, s, appKey)
		if err != nil {
			return err
		}
		res, err := s.ExecCtx(ctx, "DELETE FROM botguard_app_background WHERE app_id = ? AND id = ?", appID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		if err := bumpBackgrounds(ctx, s, appID); err != nil {
			return err
		}
		return writeAudit(ctx, s, appKey)
	})
}

// Backgrounds lists the app's photos, oldest first.
func (r *AppAdmin) Backgrounds(ctx context.Context, appKey string) ([]BackgroundPhoto, error) {
	var out []BackgroundPhoto
	err := r.conn.QueryRowsCtx(ctx, &out, `SELECT b.id, LENGTH(b.image) AS size, b.created_at FROM botguard_app_background b
JOIN botguard_app a ON a.id = b.app_id WHERE a.app_key = ? ORDER BY b.id`, appKey)
	return out, err
}

// BackgroundImage returns one photo's JPEG bytes.
func (r *AppAdmin) BackgroundImage(ctx context.Context, appKey string, id int64) ([]byte, error) {
	var row struct {
		Image []byte `db:"image"`
	}
	err := r.conn.QueryRowCtx(ctx, &row, `SELECT b.image FROM botguard_app_background b
JOIN botguard_app a ON a.id = b.app_id WHERE a.app_key = ? AND b.id = ?`, appKey, id)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	return row.Image, err
}
