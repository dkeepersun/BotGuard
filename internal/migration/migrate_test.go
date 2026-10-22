package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dkeepersun/BotGuard/internal/store"
)

// mysqlEnv is mysqlEnv; testdb imports this package, so it cannot be used.
const mysqlEnv = "BOTGUARD_TEST_MYSQL_DSN"

func TestSplit(t *testing.T) {
	got := Split("-- header\nCREATE TABLE a (\n  x INT COMMENT 'a, b'\n);\n\n  -- note\nCREATE TABLE b (y INT);\nSELECT 1")
	if len(got) != 3 || !strings.HasPrefix(got[0], "CREATE TABLE a") || strings.HasSuffix(got[0], ";") || got[2] != "SELECT 1" {
		t.Fatalf("%q", got)
	}
}

func TestLoadEmbedded(t *testing.T) {
	ms, err := Load(store.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 || len(ms[0].Statements) != 4 {
		t.Fatalf("%+v", ms)
	}
	lite, err := Load(store.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if len(lite) == 0 || lite[0].Version != 6 {
		t.Fatalf("%+v", lite)
	}
}

// Both dialects must describe the same schema version, or a gateway built
// with a new migration for one of them would never become ready on the other.
func TestDialectsShareLatestVersion(t *testing.T) {
	my, err1 := Latest(store.MySQL)
	lite, err2 := Latest(store.SQLite)
	if err1 != nil || err2 != nil || my != lite {
		t.Fatalf("mysql %d, sqlite %d: %v %v", my, lite, err1, err2)
	}
}

// A CRLF checkout (Windows, autocrlf) must not look like edited migrations.
func TestChecksumIgnoresLineEndings(t *testing.T) {
	body := "CREATE TABLE a (x INT);\nCREATE TABLE b (y INT);\n"
	lf, err1 := load(fstest.MapFS{"sql/0001_a.sql": {Data: []byte(body)}}, "sql")
	crlf, err2 := load(fstest.MapFS{"sql/0001_a.sql": {Data: []byte(strings.ReplaceAll(body, "\n", "\r\n"))}}, "sql")
	if err1 != nil || err2 != nil || lf[0].Checksum != crlf[0].Checksum {
		t.Fatalf("%v %v", err1, err2)
	}
}

// The legacy checksums must be today's files with the old table names, so
// adoptLegacy only ever re-labels the same migrations.
func TestLegacyChecksumsMatchRenamedFiles(t *testing.T) {
	ms, err := Load(store.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	pairs := append([][2]string(nil), legacyTables...)
	// Longest new name first, so botguard_app_origin is not cut at botguard_app.
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i][1]) > len(pairs[j][1]) })
	for _, m := range ms {
		want, ok := legacyChecksums[m.Version]
		if !ok {
			continue
		}
		body, err := fs.ReadFile(files, fmt.Sprintf("sql/mysql/%04d_%s.sql", m.Version, m.Name))
		if err != nil {
			t.Fatal(err)
		}
		old := string(body)
		for _, p := range pairs {
			old = regexp.MustCompile(`\b`+p[1]+`\b`).ReplaceAllString(old, p[0])
		}
		sum := sha256.Sum256([]byte(old))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%04d: legacy checksum %s, renamed file gives %s", m.Version, want, got)
		}
		if strings.Contains(string(body), "bg_") || strings.Contains(string(body), "sys_") {
			t.Errorf("%04d still uses an unprefixed table name", m.Version)
		}
	}
}

func TestCheckServerVersion(t *testing.T) {
	for v, ok := range map[string]bool{
		"8.0.36":                 true,
		"8.4.9":                  true,
		"9.1.0-commercial":       true,
		"5.7.44-log":             false,
		"10.5.2-MariaDB":         true,
		"10.4.32-MariaDB-1:10.4": false,
		"11.4.2-MariaDB-ubu2404": true,
	} {
		if err := CheckServerVersion(v); (err == nil) != ok {
			t.Errorf("%s: %v", v, err)
		}
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

// testConfig picks an empty test database like testdb.Config: a new SQLite
// file, or BOTGUARD_TEST_MYSQL_DSN with every table dropped. Point that only
// at a dedicated, disposable database.
func testConfig(t *testing.T) store.Config {
	t.Helper()
	c, err := store.Config{Driver: "sqlite", DataSource: t.TempDir() + "/botguard.db"}.Resolve("")
	if raw := os.Getenv(mysqlEnv); raw != "" {
		c, err = store.Config{Driver: "mysql", DataSource: raw}.Resolve("")
	}
	if err != nil {
		t.Fatal(err)
	}
	if c.Driver == "mysql" {
		db, err := store.Open(c)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		if err := db.QueryRowsCtx(context.Background(), &tables,
			"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"); err != nil {
			t.Fatal(err)
		}
		for _, n := range tables {
			if _, err := db.ExecCtx(context.Background(), "DROP TABLE `"+n+"`"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return c
}

// emptyDB opens testConfig's database.
func emptyDB(t *testing.T) (*sql.DB, store.Driver) {
	t.Helper()
	db, err := store.Open(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw, db.Driver
}

// testDB is emptyDB for the tests of MySQL-only history (the legacy table
// names, upgrades from 0001).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv(mysqlEnv) == "" {
		t.Skip(mysqlEnv + " not set")
	}
	db, _ := emptyDB(t)
	return db
}

func TestUpIsIdempotentAndDetectsDrift(t *testing.T) {
	db, driver := emptyDB(t)
	ctx := context.Background()
	ms := []Migration{
		{Version: 1, Name: "a", Checksum: "c1", Statements: []string{"CREATE TABLE t_a (id INT PRIMARY KEY)"}},
		{Version: 2, Name: "b", Checksum: "c2", Statements: []string{"CREATE TABLE t_b (id INT PRIMARY KEY)"}},
	}
	run := func(ms []Migration) ([]Migration, error) {
		r, err := open(ctx, db, driver, ms)
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
	db, driver := emptyDB(t)
	ctx := context.Background()
	if v, err := Applied(ctx, db, driver); err != nil || v != 0 {
		t.Fatalf("no tracking table: %d %v", v, err)
	}
	r, err := Open(ctx, db, driver)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := Applied(ctx, db, driver); err != nil || v != 0 {
		t.Fatalf("fresh database: %d %v", v, err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	latest, _ := Latest(driver)
	if v, err := Applied(ctx, db, driver); err != nil || v != latest {
		t.Fatalf("applied %d, latest %d: %v", v, latest, err)
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

// A database from before the botguard_ prefix is renamed in place and its
// history still matches; data survives.
func TestAdoptsLegacyTableNames(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	up := func() []Migration {
		t.Helper()
		r, err := Open(ctx, db, store.MySQL)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		done, err := r.Up(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return done
	}
	up()
	if _, err := db.Exec("INSERT INTO botguard_app (app_key, name) VALUES ('app_legacy', 'legacy')"); err != nil {
		t.Fatal(err)
	}
	// Turn it back into a pre-prefix database.
	var back []string
	for _, p := range legacyTables {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", p[1]).Scan(&n)
		if n == 1 {
			back = append(back, p[1]+" TO "+p[0])
		}
	}
	if _, err := db.Exec("RENAME TABLE " + strings.Join(back, ", ")); err != nil {
		t.Fatal(err)
	}
	for v, sum := range legacyChecksums {
		if _, err := db.Exec("UPDATE bg_schema_migration SET checksum = ? WHERE version = ?", sum, v); err != nil {
			t.Fatal(err)
		}
	}

	if done := up(); len(done) != 0 {
		t.Fatalf("re-applied %v", done)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM botguard_app WHERE app_key = 'app_legacy'").Scan(&name); err != nil || name != "legacy" {
		t.Fatalf("data lost: %q %v", name, err)
	}
	var left int
	_ = db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND (table_name LIKE 'bg\\_%' OR table_name LIKE 'sys\\_%')").Scan(&left)
	if left != 0 {
		t.Fatalf("%d legacy tables left", left)
	}
}

// Sharing a database: another application's sys_admin is never touched.
func TestLeavesOtherApplicationsTablesAlone(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE sys_admin (id INT PRIMARY KEY, owner VARCHAR(16))"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sys_admin VALUES (1, 'other-app')"); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, db, store.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := db.QueryRow("SELECT owner FROM sys_admin WHERE id = 1").Scan(&owner); err != nil || owner != "other-app" {
		t.Fatalf("other application's table changed: %q %v", owner, err)
	}
	if _, err := db.Exec("SELECT username FROM botguard_admin LIMIT 1"); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeFromV1 applies 0001, writes a site and a legacy Bearer key, then
// applies the rest: data must survive the rename and the key must be revoked.
func TestUpgradeFromV1(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	all, err := Load(store.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	run := func(ms []Migration) {
		t.Helper()
		r, err := open(ctx, db, store.MySQL, ms)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if _, err := r.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run(all[:1])
	for _, q := range []string{
		"INSERT INTO botguard_site (id, public_key, name) VALUES (1, 'pk_legacy_site', 'legacy')",
		"INSERT INTO botguard_site_origin (site_id, origin) VALUES (1, 'https://legacy.example')",
		"INSERT INTO botguard_site_action (site_id, action) VALUES (1, 'login')",
		"INSERT INTO botguard_api_key (key_id, site_id, secret_hash) VALUES ('sk_legacy_key', 1, UNHEX(SHA2('x', 256)))",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// A phase-one slider action (0003) must become interactive + [slider] (0004).
	run(all[:3])
	if _, err := db.ExecContext(ctx, "UPDATE botguard_app_action SET challenge_mode = 'slider'"); err != nil {
		t.Fatal(err)
	}
	run(all)
	var mode, kinds string
	if err := db.QueryRowContext(ctx, "SELECT challenge_mode, challenge_kinds FROM botguard_app_action").Scan(&mode, &kinds); err != nil {
		t.Fatal(err)
	}
	if mode != "interactive" || kinds != "slider" {
		t.Fatalf("slider mode after 0004: %s %s", mode, kinds)
	}

	var appKey, origin, action string
	if err := db.QueryRowContext(ctx, `SELECT a.app_key, o.origin, x.action FROM botguard_app a
JOIN botguard_app_origin o ON o.app_id = a.id JOIN botguard_app_action x ON x.app_id = a.id`).Scan(&appKey, &origin, &action); err != nil {
		t.Fatal(err)
	}
	if appKey != "pk_legacy_site" || origin != "https://legacy.example" || action != "login" {
		t.Fatalf("renamed data: %s %s %s", appKey, origin, action)
	}
	var status int
	var ct []byte
	if err := db.QueryRowContext(ctx, "SELECT status, secret_ciphertext FROM botguard_app_secret WHERE secret_id = 'sk_legacy_key' AND app_id = 1").Scan(&status, &ct); err != nil {
		t.Fatal(err)
	}
	if status != 2 || ct != nil {
		t.Fatalf("legacy key: status=%d ciphertext=%v", status, ct)
	}
}

// SQLite runs a migration batch in one transaction: a failing statement
// leaves nothing behind, so a rerun starts clean.
func TestSQLiteFailedMigrationRollsBack(t *testing.T) {
	if os.Getenv(mysqlEnv) != "" {
		t.Skip("SQLite only")
	}
	db, driver := emptyDB(t)
	ctx := context.Background()
	ms := []Migration{{Version: 1, Name: "a", Checksum: "c1", Statements: []string{
		"CREATE TABLE t_a (id INT PRIMARY KEY)", "CREATE TABLE broken (",
	}}}
	r, err := open(ctx, db, driver, ms)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Up(ctx); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("broken migration: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 't_a'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("t_a left behind: %d %v", n, err)
	}
	if v, err := Applied(ctx, db, driver); err != nil || v != 0 {
		t.Fatalf("recorded: %d %v", v, err)
	}
}

// The gateway, the console and botguard-ctl may all start on a new SQLite
// file at once; each must come up with the schema applied exactly once.
func TestSQLiteConcurrentPrepare(t *testing.T) {
	if os.Getenv(mysqlEnv) != "" {
		t.Skip("SQLite only")
	}
	c := testConfig(t)
	ctx := context.Background()
	db, err := store.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	// Each Prepare takes its own SQLite connection from the pool, so they
	// contend for the file lock just as separate processes do.
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := Prepare(ctx, db)
			errs <- err
		}()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := db.Raw()
	lite, _ := Load(store.SQLite)
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM botguard_schema_migration").Scan(&n); err != nil || n != len(lite) {
		t.Fatalf("history rows: %d (want %d, one per migration) %v", n, len(lite), err)
	}
}
