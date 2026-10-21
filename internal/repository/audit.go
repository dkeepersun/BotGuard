package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// AuditEntry describes who is changing what. It travels in the context so
// every repository write records it in the same transaction as the change:
// a committed change always has its audit row, a failed one never does.
type AuditEntry struct {
	Actor     string // admin:<username> or cli:<os user>
	Operation string // e.g. app.create, secret.rotate
	Target    string // e.g. the app_key or secret_id
	Detail    map[string]any
	IP        string
}

type auditKey struct{}
type actorKey struct{}

type actor struct{ name, ip string }

// WithActor records who is acting (admin:<username> or cli:<user>) and from
// where; Audited then turns each operation into an audit entry.
func WithActor(ctx context.Context, name, ip string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor{name, ip})
}

// ActorFrom returns the actor name set by WithActor, or "".
func ActorFrom(ctx context.Context) string {
	a, _ := ctx.Value(actorKey{}).(actor)
	return a.name
}

// Audited attaches an audit entry for operation when ctx carries an actor;
// without one (tests, internal calls) nothing is audited.
func Audited(ctx context.Context, operation, target string, detail map[string]any) context.Context {
	a, ok := ctx.Value(actorKey{}).(actor)
	if !ok || a.name == "" {
		return ctx
	}
	return WithAudit(ctx, AuditEntry{Actor: a.name, Operation: operation, Target: target, Detail: detail, IP: a.ip})
}

// WithAudit attaches the entry that the next repository write records.
func WithAudit(ctx context.Context, e AuditEntry) context.Context {
	return context.WithValue(ctx, auditKey{}, &e)
}

// AuditFrom returns the entry attached to ctx, if any.
func AuditFrom(ctx context.Context) (*AuditEntry, bool) {
	e, ok := ctx.Value(auditKey{}).(*AuditEntry)
	return e, ok
}

// secretish detail keys are dropped wherever they appear: the audit log
// must never hold credentials, even if a caller passes one by mistake.
func redact(detail map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range detail {
		l := strings.ToLower(k)
		// "mfa" (was a second factor used) is a fact, not a secret, and stays.
		if strings.Contains(l, "secret_key") || strings.Contains(l, "password") || strings.Contains(l, "totp") ||
			strings.Contains(l, "token") || strings.Contains(l, "otp_code") {
			continue
		}
		out[k] = v
	}
	return out
}

// writeAudit inserts the context's audit entry, if any, in session s.
// target overrides the entry's target when the write itself decides it
// (a new secret_id, for example).
func writeAudit(ctx context.Context, s sqlx.Session, target string) error {
	e, ok := AuditFrom(ctx)
	if !ok {
		return nil
	}
	if target == "" {
		target = e.Target
	}
	var detail any
	if clean := redact(e.Detail); len(clean) > 0 {
		b, err := json.Marshal(clean)
		if err != nil {
			return err
		}
		detail = string(b)
	}
	_, err := s.ExecCtx(ctx, "INSERT INTO botguard_audit_log (actor, operation, target, detail, ip) VALUES (?, ?, ?, ?, ?)",
		truncate(e.Actor, 64), truncate(e.Operation, 64), truncate(target, 160), detail, truncate(e.IP, 64))
	return err
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// AuditRecord is one audit log row as the console shows it.
type AuditRecord struct {
	ID        int64     `db:"id"`
	Actor     string    `db:"actor"`
	Operation string    `db:"operation"`
	Target    string    `db:"target"`
	Detail    string    `db:"detail"`
	IP        string    `db:"ip"`
	CreatedAt time.Time `db:"created_at"`
}

// AuditQuery filters the audit log; empty fields match everything.
type AuditQuery struct {
	Actor, Operation, Target string
	Offset, Limit            int
}

// Audit reads the audit log, newest first, with the total for paging.
func (r *AppAdmin) Audit(ctx context.Context, q AuditQuery) ([]AuditRecord, int, error) {
	where, args := []string{"1=1"}, []any{}
	if q.Actor != "" {
		where, args = append(where, "actor = ?"), append(args, q.Actor)
	}
	if q.Operation != "" {
		where, args = append(where, "operation LIKE ?"), append(args, q.Operation+"%")
	}
	if q.Target != "" {
		where, args = append(where, "target = ?"), append(args, q.Target)
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := r.conn.QueryRowCtx(ctx, &total, "SELECT COUNT(*) FROM botguard_audit_log WHERE "+cond, args...); err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 20
	}
	var out []AuditRecord
	err := r.conn.QueryRowsCtx(ctx, &out, "SELECT id, actor, operation, target, COALESCE(detail, '') AS detail, ip, created_at FROM botguard_audit_log WHERE "+
		cond+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, q.Limit, max(0, q.Offset))...)
	return out, total, err
}

// RecordAudit writes a standalone audit entry (logins, failed logins).
func (r *AppAdmin) RecordAudit(ctx context.Context, e AuditEntry) error {
	return r.conn.TransactCtx(ctx, func(ctx context.Context, s sqlx.Session) error {
		return writeAudit(WithAudit(ctx, e), s, "")
	})
}
