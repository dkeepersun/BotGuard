package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	const my = "u:p@tcp(127.0.0.1:3306)/botguard"
	for _, tc := range []struct {
		name         string
		in           Config
		legacy       string
		driver, path string
		bad          bool
	}{
		{name: "nothing set", driver: "sqlite", path: DefaultSQLitePath},
		{name: "sqlite path", in: Config{DataSource: "/var/lib/bg.db"}, driver: "sqlite", path: "/var/lib/bg.db"},
		{name: "legacy DSN keeps MySQL", legacy: my, driver: "mysql"},
		{name: "explicit sqlite beats legacy", in: Config{Driver: "SQLite"}, legacy: my, driver: "sqlite", path: DefaultSQLitePath},
		{name: "explicit mysql uses legacy DSN", in: Config{Driver: "mysql"}, legacy: my, driver: "mysql"},
		{name: "MySQL DSN without driver", in: Config{DataSource: my}, driver: "mysql"},
		{name: "mysql without DSN", in: Config{Driver: "mysql"}, bad: true},
		{name: "unknown driver", in: Config{Driver: "oracle"}, bad: true},
		{name: "sqlite URI", in: Config{DataSource: "file:x.db?mode=memory"}, bad: true},
	} {
		got, err := tc.in.Resolve(tc.legacy)
		if tc.bad {
			if err == nil {
				t.Errorf("%s: accepted %+v", tc.name, got)
			}
			continue
		}
		if err != nil || got.Driver != tc.driver || (tc.path != "" && got.DataSource != tc.path) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}

func TestSQLiteOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "bg.db")
	c, err := Config{DataSource: path}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if !db.Created || db.Name != path || db.ForUpdate() != "" {
		t.Fatalf("%+v", db)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", st.Mode(), err)
	}
	ctx := context.Background()
	if _, err := db.ExecCtx(ctx, "CREATE TABLE t (k TEXT NOT NULL UNIQUE, at DATETIME NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 21, 8, 30, 0, 123e6, time.FixedZone("CST", 8*3600))
	if _, err := db.ExecCtx(ctx, "INSERT INTO t VALUES ('a', ?)", at); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecCtx(ctx, "INSERT INTO t VALUES ('a', ?)", at)
	if !db.IsDuplicate(err) {
		t.Fatalf("duplicate: %v", err)
	}
	raw, err := db.Raw()
	if err != nil {
		t.Fatal(err)
	}
	// go-zero cannot scan a bare time.Time; database/sql can.
	var got time.Time
	if err := raw.QueryRowContext(ctx, "SELECT at FROM t").Scan(&got); err != nil || !got.Equal(at) || got.Location() != time.UTC {
		t.Fatalf("time round trip: %v %v", got, err)
	}
	var text string
	if err := db.QueryRowCtx(ctx, &text, "SELECT CAST(at AS TEXT) FROM t"); err != nil || text != "2026-10-21 00:30:00.123+00:00" {
		t.Fatalf("stored as %q %v", text, err)
	}
	if again, err := Open(c); err != nil || again.Created {
		t.Fatalf("reopen: %+v %v", again, err)
	}
	if _, err := db.ExecCtx(ctx, "SELECT * FROM missing"); !db.IsMissingTable(err) {
		t.Fatalf("missing table: %v", err)
	}
}
