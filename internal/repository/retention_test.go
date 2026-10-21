package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/testdb"
)

func TestRetentionPurgesOnlyExpiredRows(t *testing.T) {
	_, db := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := 24 * time.Hour

	// 2500 old audit rows take several batches; recent ones stay.
	for i := range 2500 {
		if _, err := db.ExecCtx(ctx, "INSERT INTO botguard_audit_log (actor, operation, created_at) VALUES ('cli:t', 'old', ?)",
			now.Add(-200*day+time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if _, err := db.ExecCtx(ctx, "INSERT INTO botguard_audit_log (actor, operation, created_at) VALUES ('cli:t', 'new', ?)",
			now.Add(-10*day)); err != nil {
			t.Fatal(err)
		}
	}

	repo := repository.NewAppAdmin(db)
	if err := repo.CreateApp(ctx, "app_r", "r", ""); err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-40 * day)
	grace := now.Add(time.Hour)
	for id, until := range map[string]*time.Time{"sid_expired": &expired, "sid_grace": &grace, "sid_live": nil, "sid_revoked": nil} {
		if err := repo.CreateSecret(ctx, "app_r", repository.NewSecret{SecretID: id, Ciphertext: []byte{1}, KEKID: "k1", ExpiresAt: until}); err != nil {
			t.Fatal(err)
		}
	}
	// Revoking just now bumps updated_at (a trigger on SQLite).
	if err := repo.RevokeSecret(ctx, "sid_revoked"); err != nil {
		t.Fatal(err)
	}

	r := repository.NewRetention(db, 180*day, 30*day)
	p, err := r.Purge(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.AuditLog != 2500 || p.Secrets != 1 {
		t.Fatalf("first pass: %+v", p)
	}
	var left []string
	if err := db.QueryRowsCtx(ctx, &left, "SELECT DISTINCT operation FROM botguard_audit_log"); err != nil || len(left) != 1 || left[0] != "new" {
		t.Fatalf("audit rows left: %v %v", left, err)
	}
	secrets := func() []string {
		var ids []string
		if err := db.QueryRowsCtx(ctx, &ids, "SELECT secret_id FROM botguard_app_secret ORDER BY secret_id"); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got := secrets(); len(got) != 3 || got[0] != "sid_grace" || got[2] != "sid_revoked" {
		t.Fatalf("secrets after first pass: %v", got)
	}

	// A month on, the revoked pair and the one whose grace ended go too.
	if p, err = r.Purge(ctx, now.Add(32*day)); err != nil || p.Secrets != 2 {
		t.Fatalf("second pass: %+v %v", p, err)
	}
	if got := secrets(); len(got) != 1 || got[0] != "sid_live" {
		t.Fatalf("secrets after second pass: %v", got)
	}

	// Zero keeps everything.
	if p, err = repository.NewRetention(db, 0, 0).Purge(ctx, now.Add(10000*day)); err != nil || p.AuditLog+p.Secrets != 0 {
		t.Fatalf("disabled retention: %+v %v", p, err)
	}
}
