package migration

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dkeepersun/BotGuard/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

func TestSplit(t *testing.T) {
	got := Split("-- header\nCREATE TABLE a (\n  x INT COMMENT 'a, b'\n);\n\n  -- note\nCREATE TABLE b (y INT);\nSELECT 1")
	if len(got) != 3 || !strings.HasPrefix(got[0], "CREATE TABLE a") || strings.HasSuffix(got[0], ";") || got[2] != "SELECT 1" {
		t.Fatalf("%q", got)
	}
}

func TestLoadEmbedded(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 || len(ms[0].Statements) != 4 {
		t.Fatalf("%+v", ms)
	}
}

func TestLoadRejectsBadNames(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"bad name":  {"sql/1_x.sql": {Data: []byte("SELECT 1;")}},
		"duplicate": {"sql/0001_a.sql": {Data: []byte("SELECT 1;")}, "sql/0001_b.sql": {Data: []byte("SELECT 1;")}},
		"empty":     {"sql/0001_a.sql": {Data: []byte("-- nothing\n")}},
	} {
		if _, err := load(fsys, "sql"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// testDB returns a connection to BOTGUARD_TEST_MYSQL_DSN after dropping every
// table in it. Point it only at a dedicated, disposable database.
func testDB(t *testing.T) *sql.DB {
	raw := os.Getenv("BOTGUARD_TEST_MYSQL_DSN")
	if raw == "" {
		t.Skip("BOTGUARD_TEST_MYSQL_DSN not set")
	}
	dsn, err := config.NormalizeMySQLDSN(raw)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	for _, n := range tables {
		if _, err := db.Exec("DROP TABLE `" + n + "`"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestUpIsIdempotentAndDetectsDrift(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ms := []Migration{
		{Version: 1, Name: "a", Checksum: "c1", Statements: []string{"CREATE TABLE t_a (id INT PRIMARY KEY)"}},
		{Version: 2, Name: "b", Checksum: "c2", Statements: []string{"CREATE TABLE t_b (id INT PRIMARY KEY)"}},
	}
	run := func(ms []Migration) ([]Migration, error) {
		r, err := open(ctx, db, ms)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return r.Up(ctx)
	}
	if done, err := run(ms); err != nil || len(done) != 2 {
		t.Fatalf("first up: %v %v", done, err)
	}
	if done, err := run(ms); err != nil || len(done) != 0 {
		t.Fatalf("second up: %v %v", done, err)
	}

	edited := append([]Migration(nil), ms...)
	edited[0].Checksum = "changed"
	if _, err := run(edited); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("edited migration: %v", err)
	}
	if _, err := run(ms[:1]); err == nil || !strings.Contains(err.Error(), "不认识") {
		t.Fatalf("older binary: %v", err)
	}
	next := append(append([]Migration(nil), ms...),
		Migration{Version: 3, Name: "c", Checksum: "c3", Statements: []string{"CREATE TABLE t_c (id INT PRIMARY KEY)"}})
	if done, err := run(next); err != nil || len(done) != 1 || done[0].Version != 3 {
		t.Fatalf("new version: %v %v", done, err)
	}
	// A version below the newest applied one, added later, is out of order.
	late := append(append([]Migration(nil), next...), Migration{Version: 0, Name: "late", Checksum: "c0", Statements: []string{"SELECT 1"}})
	late[0], late[len(late)-1] = late[len(late)-1], late[0]
	if _, err := run(late); err == nil || !strings.Contains(err.Error(), "乱序") {
		t.Fatalf("out of order: %v", err)
	}
}

func TestEmbeddedMigrationsApply(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range status {
		if s.AppliedAt == nil {
			t.Fatalf("%d not applied", s.Version)
		}
	}
}
