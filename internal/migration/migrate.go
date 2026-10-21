// Package migration applies the embedded, forward-only SQL migrations.
//
// Files are named NNNN_name.sql and applied in version order. Each applied
// version is recorded with a checksum; editing an applied file, or running a
// binary older than the database, is an error rather than a silent skip.
//
// Every table is prefixed botguard_, so BotGuard can share a database with
// another application. Databases from before the prefix (bg_* and sys_*
// tables) are renamed in place the next time migrations run.
//
// Each dialect has its own directory, sql/mysql and sql/sqlite, with the same
// version numbers. SQLite history starts at 0006 (the schema MySQL reached
// in 0006), so from then on a migration adds one file to each directory.
//
// MySQL DDL commits implicitly, so a migration that fails part-way leaves its
// earlier statements applied and is not recorded. Fix the schema by hand and
// rerun; keep migrations small to limit that window. SQLite DDL is
// transactional: a run applies all pending migrations or none.
package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/store"
)

//go:embed sql
var files embed.FS

const lockName = "botguard_migrate"

var namePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

const sqliteTrackingTable = `CREATE TABLE IF NOT EXISTS botguard_schema_migration (
  version INTEGER NOT NULL PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
)`

const mysqlTrackingTable = `CREATE TABLE IF NOT EXISTS botguard_schema_migration (
  version INT UNSIGNED NOT NULL,
  name VARCHAR(128) NOT NULL,
  checksum CHAR(64) NOT NULL,
  applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

// legacyTables maps each table name used before the botguard_ prefix to its
// current name.
var legacyTables = [][2]string{
	{"bg_schema_migration", "botguard_schema_migration"},
	{"bg_site", "botguard_site"},
	{"bg_site_origin", "botguard_site_origin"},
	{"bg_site_action", "botguard_site_action"},
	{"bg_api_key", "botguard_api_key"},
	{"bg_app", "botguard_app"},
	{"bg_app_origin", "botguard_app_origin"},
	{"bg_app_action", "botguard_app_action"},
	{"bg_app_secret", "botguard_app_secret"},
	{"sys_admin", "botguard_admin"},
	{"sys_audit_log", "botguard_audit_log"},
}

// legacyChecksums are the checksums the migrations had before the prefix.
// The files changed only in table names, so an applied version recorded with
// its legacy checksum is the same migration.
var legacyChecksums = map[int]string{
	1: "5231644877c5a7cd2745d4526c005dfb832520e0de004dd12b7ecf5dfaa90215",
	2: "7fa414baac06afce024afe5b5ba864b05d218322aa949120c7800e7e5ead76f8",
	3: "74da1dc6aff06ebf7a805fccd2a0714ea1e2a202b7462fb161acafe54e764215",
	4: "c056f4a9ba86b833de221ffc1fc1dee7831237ff8ed4c9dbba3360b8d366ec3a",
	5: "cba30eaf462a35463f4bfb660c7ab41f85407d688d3d00d624fc5cef7c04f8cc",
	6: "a3826d6629ffaf881cdba84564a6ce2475d939079dfac0d1f11b86ffcb3ef283",
}

type Migration struct {
	Version    int
	Name       string
	Checksum   string
	Statements []string
}

type Status struct {
	Migration
	AppliedAt *time.Time
}

// Load parses the embedded migrations for one dialect in version order.
func Load(d store.Driver) ([]Migration, error) {
	return load(files, "sql/"+string(d))
}

func load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		m := namePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNN_name.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migration version %d used by %s and %s", version, prev, e.Name())
		}
		seen[version] = e.Name()
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		// A Windows checkout with autocrlf must produce the same checksum.
		body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
		stmts := Split(string(body))
		if len(stmts) == 0 {
			return nil, fmt.Errorf("migration %s has no statements", e.Name())
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: version, Name: m[2], Checksum: hex.EncodeToString(sum[:]), Statements: stmts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Split drops whole-line "--" comments and splits on ";" at end of line.
// Migrations must not put ";" at a line end inside string literals.
func Split(sqlText string) []string {
	var stmts []string
	var cur strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmts = append(stmts, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		stmts = append(stmts, rest)
	}
	return stmts
}

// Runner holds one dedicated connection so the advisory lock and the
// migration statements share a session.
type Runner struct {
	conn       *sql.Conn
	driver     store.Driver
	migrations []Migration
}

// querier is a *sql.Conn, or the *sql.Tx a SQLite run happens in.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Open takes the migration lock (MySQL); Close releases it.
func Open(ctx context.Context, db *sql.DB, d store.Driver) (*Runner, error) {
	ms, err := Load(d)
	if err != nil {
		return nil, err
	}
	return open(ctx, db, d, ms)
}

func open(ctx context.Context, db *sql.DB, d store.Driver, ms []Migration) (*Runner, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if d == store.SQLite {
		// No lock to take: Up runs in one write transaction, which SQLite
		// serializes across processes.
		if _, err := conn.ExecContext(ctx, sqliteTrackingTable); err != nil {
			conn.Close()
			return nil, err
		}
		return &Runner{conn: conn, driver: d, migrations: ms}, nil
	}
	var version string
	if err := conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		conn.Close()
		return nil, err
	}
	if err := CheckServerVersion(version); err != nil {
		conn.Close()
		return nil, err
	}
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&got); err != nil {
		conn.Close()
		return nil, err
	}
	if !got.Valid || got.Int64 != 1 {
		conn.Close()
		return nil, errors.New("另一个迁移正在执行（30 秒内未获得锁）")
	}
	if err := adoptLegacy(ctx, conn, ms); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, mysqlTrackingTable); err != nil {
		conn.Close()
		return nil, err
	}
	return &Runner{conn: conn, driver: d, migrations: ms}, nil
}

// adoptLegacy renames a database from before the botguard_ prefix. It acts
// only when the old tracking table exists, which proves the bg_ and sys_
// tables are BotGuard's and not another application's.
func adoptLegacy(ctx context.Context, conn *sql.Conn, ms []Migration) error {
	rows, err := conn.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		present[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !present["bg_schema_migration"] || present["botguard_schema_migration"] {
		return nil
	}
	var pairs []string
	for _, t := range legacyTables {
		if present[t[0]] {
			pairs = append(pairs, fmt.Sprintf("`%s` TO `%s`", t[0], t[1]))
		}
	}
	// One statement: MySQL renames all of them atomically.
	if _, err := conn.ExecContext(ctx, "RENAME TABLE "+strings.Join(pairs, ", ")); err != nil {
		return fmt.Errorf("旧表改名为 botguard_ 前缀失败：%w", err)
	}
	for _, m := range ms {
		if old, ok := legacyChecksums[m.Version]; ok {
			if _, err := conn.ExecContext(ctx, "UPDATE botguard_schema_migration SET checksum = ? WHERE version = ? AND checksum = ?",
				m.Checksum, m.Version, old); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckServerVersion rejects servers the migrations do not run on: they use
// RENAME COLUMN (MySQL 8.0, MariaDB 10.5.2) and are not transactional, so an
// old server would fail half-way and leave a database no rerun can fix.
func CheckServerVersion(version string) error {
	nums := func(s string) [3]int {
		var out [3]int
		for i, part := range strings.SplitN(s, ".", 3) {
			end := 0
			for end < len(part) && part[end] >= '0' && part[end] <= '9' {
				end++
			}
			out[i], _ = strconv.Atoi(part[:end])
		}
		return out
	}
	less := func(a, b [3]int) bool {
		for i := range a {
			if a[i] != b[i] {
				return a[i] < b[i]
			}
		}
		return false
	}
	v := nums(version)
	if strings.Contains(strings.ToLower(version), "mariadb") {
		if less(v, [3]int{10, 5, 2}) {
			return fmt.Errorf("需要 MariaDB ≥ 10.5.2，当前为 %s", version)
		}
		return nil
	}
	if less(v, [3]int{8, 0, 0}) {
		return fmt.Errorf("需要 MySQL ≥ 8.0，当前为 %s", version)
	}
	return nil
}

// Latest is the newest migration version built into this binary.
func Latest(d store.Driver) (int, error) {
	ms, err := Load(d)
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	return ms[len(ms)-1].Version, nil
}

// Applied returns the newest applied version, 0 for a fresh database. It
// takes no lock, so the gateway can call it from its readiness probe.
func Applied(ctx context.Context, db *sql.DB, d store.Driver) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM botguard_schema_migration").Scan(&v)
	if store.IsMissingTable(d, err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

func (r *Runner) Close() error {
	if r.driver == store.MySQL {
		_, _ = r.conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName)
	}
	return r.conn.Close()
}

// Status lists every known migration with its applied time, after checking
// that the database history matches this binary.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	return r.status(ctx, r.conn)
}

func (r *Runner) status(ctx context.Context, q querier) ([]Status, error) {
	rows, err := q.QueryContext(ctx, "SELECT version, checksum, applied_at FROM botguard_schema_migration ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		checksum string
		at       time.Time
	}
	applied := map[int]row{}
	maxApplied := 0
	for rows.Next() {
		var v int
		var rw row
		if err := rows.Scan(&v, &rw.checksum, &rw.at); err != nil {
			return nil, err
		}
		applied[v] = rw
		maxApplied = max(maxApplied, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	known := map[int]bool{}
	out := make([]Status, 0, len(r.migrations))
	for _, m := range r.migrations {
		known[m.Version] = true
		s := Status{Migration: m}
		if a, ok := applied[m.Version]; ok {
			if a.checksum != m.Checksum {
				return nil, fmt.Errorf("迁移 %04d_%s 已执行，但文件内容已被修改（checksum 不一致）", m.Version, m.Name)
			}
			at := a.at
			s.AppliedAt = &at
		} else if m.Version < maxApplied {
			return nil, fmt.Errorf("迁移 %04d_%s 早于已执行的版本 %d，但尚未执行；不支持乱序迁移", m.Version, m.Name, maxApplied)
		}
		out = append(out, s)
	}
	for v := range applied {
		if !known[v] {
			return nil, fmt.Errorf("数据库已执行版本 %d，当前程序不认识；请使用更新的 botguard-ctl", v)
		}
	}
	return out, nil
}

// Up applies pending migrations in order and returns the ones applied.
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	if r.driver == store.SQLite {
		// The DSN begins transactions IMMEDIATE: a second process starting at
		// the same time waits here, then finds nothing left to apply.
		tx, err := r.conn.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		done, err := r.up(ctx, tx)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		return done, tx.Commit()
	}
	return r.up(ctx, r.conn)
}

func (r *Runner) up(ctx context.Context, q querier) ([]Migration, error) {
	status, err := r.status(ctx, q)
	if err != nil {
		return nil, err
	}
	var done []Migration
	for _, s := range status {
		if s.AppliedAt != nil {
			continue
		}
		for i, stmt := range s.Statements {
			if _, err := q.ExecContext(ctx, stmt); err != nil {
				if r.driver == store.SQLite {
					return nil, fmt.Errorf("迁移 %04d_%s 第 %d 条语句失败（已整体回滚）：%w", s.Version, s.Name, i+1, err)
				}
				return done, fmt.Errorf("迁移 %04d_%s 第 %d 条语句失败（之前的 DDL 已生效，需人工处理后重试）：%w",
					s.Version, s.Name, i+1, err)
			}
		}
		if _, err := q.ExecContext(ctx,
			"INSERT INTO botguard_schema_migration (version, name, checksum) VALUES (?, ?, ?)",
			s.Version, s.Name, s.Checksum); err != nil {
			return done, err
		}
		done = append(done, s.Migration)
	}
	return done, nil
}

// Prepare brings a SQLite database up to date at startup: BotGuard owns the
// file and there is no separate install step. MySQL schemas are left to
// "botguard-ctl migrate up", so an upgrade stays an explicit operator step.
func Prepare(ctx context.Context, db *store.DB) ([]Migration, error) {
	if db.Driver != store.SQLite {
		return nil, nil
	}
	raw, err := db.Raw()
	if err != nil {
		return nil, err
	}
	r, err := Open(ctx, raw, db.Driver)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Up(ctx)
}
