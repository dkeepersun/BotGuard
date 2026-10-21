// Package testdb gives integration tests a migrated, empty database.
//
// By default that is a fresh SQLite file in the test's temporary directory.
// When BOTGUARD_TEST_MYSQL_DSN is set the tests run against MySQL instead, and
// every table in that database is dropped first: point it only at a
// dedicated, disposable database.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/migration"
	"github.com/dkeepersun/BotGuard/internal/store"
)

const Env = "BOTGUARD_TEST_MYSQL_DSN"

// Config picks the test database without opening it: MySQL from Env after
// dropping its tables, otherwise a new SQLite file.
func Config(t *testing.T) store.Config {
	t.Helper()
	raw := os.Getenv(Env)
	if raw == "" {
		c, err := store.Config{Driver: "sqlite", DataSource: filepath.Join(t.TempDir(), "botguard.db")}.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c, err := store.Config{Driver: "mysql", DataSource: raw}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var tables []string
	if err := db.QueryRowsCtx(ctx, &tables,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"); err != nil {
		t.Fatal(err)
	}
	for _, n := range tables {
		if _, err := db.ExecCtx(ctx, "DROP TABLE `"+n+"`"); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// Open returns the test database's configuration and a connection to it,
// with every migration applied.
func Open(t *testing.T) (store.Config, *store.DB) {
	t.Helper()
	c := Config(t)
	db, err := store.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.Raw()
	if err != nil {
		t.Fatal(err)
	}
	if db.Driver == store.SQLite {
		// go-zero shares pools by DSN; only the per-test SQLite file's pool
		// is ours to close.
		t.Cleanup(func() { raw.Close() })
	}
	ctx := context.Background()
	r, err := migration.Open(ctx, raw, db.Driver)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return c, db
}
