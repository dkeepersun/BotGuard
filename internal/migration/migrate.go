// Package migration applies the embedded, forward-only SQL migrations.
//
// Files are named NNNN_name.sql and applied in version order. Each applied
// version is recorded with a checksum; editing an applied file, or running a
// binary older than the database, is an error rather than a silent skip.
//
// MySQL DDL commits implicitly, so a migration that fails part-way leaves its
// earlier statements applied and is not recorded. Fix the schema by hand and
// rerun; keep migrations small to limit that window.
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

	"github.com/go-sql-driver/mysql"
)

//go:embed sql/*.sql
var files embed.FS

const lockName = "botguard_migrate"

var namePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

const trackingTable = `CREATE TABLE IF NOT EXISTS bg_schema_migration (
  version INT UNSIGNED NOT NULL,
  name VARCHAR(128) NOT NULL,
  checksum CHAR(64) NOT NULL,
  applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

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

// Load parses the embedded migrations in version order.
func Load() ([]Migration, error) {
	return load(files, "sql")
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
	migrations []Migration
}

// Open takes the migration lock; Close releases it.
func Open(ctx context.Context, db *sql.DB) (*Runner, error) {
	ms, err := Load()
	if err != nil {
		return nil, err
	}
	return open(ctx, db, ms)
}

func open(ctx context.Context, db *sql.DB, ms []Migration) (*Runner, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
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
	if _, err := conn.ExecContext(ctx, trackingTable); err != nil {
		conn.Close()
		return nil, err
	}
	return &Runner{conn: conn, migrations: ms}, nil
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
func Latest() (int, error) {
	ms, err := Load()
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	return ms[len(ms)-1].Version, nil
}

// Applied returns the newest applied version, 0 for a fresh database. It
// takes no lock, so the gateway can call it from its readiness probe.
func Applied(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM bg_schema_migration").Scan(&v)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1146 { // table does not exist
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

func (r *Runner) Close() error {
	_, _ = r.conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName)
	return r.conn.Close()
}

// Status lists every known migration with its applied time, after checking
// that the database history matches this binary.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	rows, err := r.conn.QueryContext(ctx, "SELECT version, checksum, applied_at FROM bg_schema_migration ORDER BY version")
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
	status, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	var done []Migration
	for _, s := range status {
		if s.AppliedAt != nil {
			continue
		}
		for i, stmt := range s.Statements {
			if _, err := r.conn.ExecContext(ctx, stmt); err != nil {
				return done, fmt.Errorf("迁移 %04d_%s 第 %d 条语句失败（之前的 DDL 已生效，需人工处理后重试）：%w",
					s.Version, s.Name, i+1, err)
			}
		}
		if _, err := r.conn.ExecContext(ctx,
			"INSERT INTO bg_schema_migration (version, name, checksum) VALUES (?, ?, ?)",
			s.Version, s.Name, s.Checksum); err != nil {
			return done, err
		}
		done = append(done, s.Migration)
	}
	return done, nil
}
