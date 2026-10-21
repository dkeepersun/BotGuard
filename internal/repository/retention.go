package repository

import (
	"context"
	"time"

	"github.com/dkeepersun/BotGuard/internal/store"
	"github.com/zeromicro/go-zero/core/logx"
)

// Retention deletes database rows that have outlived their use, so a
// long-running install does not grow without bound. Redis needs no such job:
// every key BotGuard writes there has a TTL, and event lists are capped.
//
// A zero duration keeps those rows forever.
type Retention struct {
	db *store.DB
	// AuditLog is how long audit rows are kept.
	AuditLog time.Duration
	// RetiredSecrets is how long a secret pair is kept after it expired
	// (rotation grace ended) or was revoked.
	RetiredSecrets time.Duration
}

func NewRetention(db *store.DB, auditLog, retiredSecrets time.Duration) *Retention {
	return &Retention{db: db, AuditLog: auditLog, RetiredSecrets: retiredSecrets}
}

type Purged struct {
	AuditLog int64
	Secrets  int64
}

// purgeBatch bounds each DELETE, so the console and SQLite readers are never
// blocked for long behind one huge statement.
const purgeBatch = 1000

// cutoff formats a time the way both dialects compare DATETIME values: MySQL
// converts the literal, SQLite compares the UTC text.
func cutoff(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// Purge runs one cleanup pass.
func (r *Retention) Purge(ctx context.Context, now time.Time) (Purged, error) {
	var p Purged
	if r.AuditLog > 0 {
		before := cutoff(now.Add(-r.AuditLog))
		for {
			var ids []int64
			if err := r.db.QueryRowsCtx(ctx, &ids,
				"SELECT id FROM botguard_audit_log WHERE created_at < ? ORDER BY id LIMIT ?", before, purgeBatch); err != nil {
				return p, err
			}
			if len(ids) == 0 {
				break
			}
			// Ids grow with time, so this deletes exactly the rows selected.
			res, err := r.db.ExecCtx(ctx, "DELETE FROM botguard_audit_log WHERE id <= ? AND created_at < ?", ids[len(ids)-1], before)
			if err != nil {
				return p, err
			}
			n, _ := res.RowsAffected()
			p.AuditLog += n
			if len(ids) < purgeBatch {
				break
			}
		}
	}
	if r.RetiredSecrets > 0 {
		before := cutoff(now.Add(-r.RetiredSecrets))
		res, err := r.db.ExecCtx(ctx,
			"DELETE FROM botguard_app_secret WHERE (expires_at IS NOT NULL AND expires_at < ?) OR (status = ? AND updated_at < ?)",
			before, statusDisabled, before)
		if err != nil {
			return p, err
		}
		p.Secrets, _ = res.RowsAffected()
	}
	if r.db.Driver == store.SQLite {
		r.compactSQLite(ctx, p.AuditLog+p.Secrets > 0)
	}
	return p, nil
}

// compactSQLite returns freed pages to the filesystem and keeps the WAL file
// from staying at its high-water mark. Failures only cost disk space.
func (r *Retention) compactSQLite(ctx context.Context, deleted bool) {
	stmts := []string{"PRAGMA wal_checkpoint(TRUNCATE)", "PRAGMA optimize"}
	if deleted {
		stmts = append([]string{"PRAGMA incremental_vacuum"}, stmts...)
	}
	for _, s := range stmts {
		if _, err := r.db.ExecCtx(ctx, s); err != nil {
			logx.WithContext(ctx).Errorf("sqlite maintenance %q: %v", s, err)
		}
	}
}

// Run purges once now and then every interval until ctx ends.
func (r *Retention) Run(ctx context.Context, every time.Duration, now func() time.Time) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p, err := r.Purge(ctx, now())
		switch {
		case err != nil:
			logx.WithContext(ctx).Errorf("data cleanup failed: %v", err)
		case p.AuditLog+p.Secrets > 0:
			logx.WithContext(ctx).Infof("data cleanup: deleted %d audit rows, %d retired secret pairs", p.AuditLog, p.Secrets)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
