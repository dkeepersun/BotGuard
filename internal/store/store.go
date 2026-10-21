// Package store opens BotGuard's configuration database: an embedded SQLite
// file by default, or MySQL when configured.
//
// SQLite needs no server: the file is created on first start next to the
// binary's working directory. Use MySQL when several hosts run the admin
// console or gateway against one database; SQLite only works when every
// process shares the same filesystem.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Driver string

const (
	SQLite Driver = "sqlite"
	MySQL  Driver = "mysql"

	// DefaultSQLitePath is relative to the working directory, like the
	// configs/ paths; give an absolute path in production.
	DefaultSQLitePath = "data/botguard.db"
)

// Config selects the database. Both fields usually come from the
// environment, so an empty value means "use the default".
type Config struct {
	// Driver is sqlite (default) or mysql.
	Driver string `json:",optional"`
	// DataSource is the SQLite file path (default data/botguard.db) or the
	// MySQL DSN, e.g. user:pass@tcp(127.0.0.1:3306)/botguard.
	DataSource string `json:",optional"`
}

// Resolve fills in the defaults. legacyMySQL is the DSN from the old
// "MySQL.DataSource" setting (BOTGUARD_MYSQL_DSN): when no driver is chosen it
// still selects MySQL, so upgrading never silently moves a deployment to an
// empty SQLite file.
func (c Config) Resolve(legacyMySQL string) (Config, error) {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	c.DataSource = strings.TrimSpace(c.DataSource)
	legacyMySQL = strings.TrimSpace(legacyMySQL)
	if c.Driver == "" {
		c.Driver = string(SQLite)
		if c.DataSource == "" && legacyMySQL != "" || looksLikeMySQL(c.DataSource) {
			c.Driver = string(MySQL)
		}
	}
	switch Driver(c.Driver) {
	case SQLite:
		if c.DataSource == "" {
			c.DataSource = DefaultSQLitePath
		}
		if strings.ContainsAny(c.DataSource, "?#") || strings.HasPrefix(c.DataSource, "file:") {
			return c, errors.New("SQLite 数据源应为文件路径（不要带 file: 前缀或 ? 参数）")
		}
	case MySQL:
		if c.DataSource == "" {
			c.DataSource = legacyMySQL
		}
		dsn, err := NormalizeMySQLDSN(c.DataSource)
		if err != nil {
			return c, err
		}
		c.DataSource = dsn
	default:
		return c, fmt.Errorf("不支持的数据库类型 %q（可选 sqlite、mysql）", c.Driver)
	}
	return c, nil
}

// looksLikeMySQL catches a MySQL DSN given without a driver, which would
// otherwise become an oddly named SQLite file.
func looksLikeMySQL(dsn string) bool {
	return strings.Contains(dsn, "@tcp(") || strings.Contains(dsn, "@unix(")
}

// DB is a connection pool plus what the repositories need to know about the
// SQL dialect behind it.
type DB struct {
	sqlx.SqlConn
	Driver Driver
	// Name identifies the database in messages: the SQLite file's absolute
	// path, or MySQL's address and schema (never the password).
	Name string
	// Created is set when Open made a new, empty SQLite file: worth telling
	// the operator, in case they meant another path.
	Created bool
}

// Open connects to a resolved Config. For SQLite it creates the directory
// and an owner-only file first: the database holds sealed secret keys and
// password hashes.
func Open(c Config) (*DB, error) {
	switch Driver(c.Driver) {
	case SQLite:
		path, err := filepath.Abs(c.DataSource)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("创建 SQLite 目录失败：%w", err)
		}
		created := false
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			created = true
		} else if errors.Is(err, os.ErrExist) {
			f, err = os.OpenFile(path, os.O_RDWR, 0)
		}
		if err != nil {
			return nil, fmt.Errorf("打开 SQLite 文件失败：%w", err)
		}
		f.Close()
		return &DB{SqlConn: sqlx.NewSqlConn("sqlite", sqliteDSN(path)), Driver: SQLite, Name: path, Created: created}, nil
	case MySQL:
		cfg, err := mysql.ParseDSN(c.DataSource)
		if err != nil {
			return nil, err
		}
		return &DB{SqlConn: sqlx.NewMysql(c.DataSource), Driver: MySQL, Name: cfg.Addr + "/" + cfg.DBName}, nil
	}
	return nil, fmt.Errorf("不支持的数据库类型 %q", c.Driver)
}

// sqliteDSN sets every connection up the same way:
//   - WAL lets the gateway read while the console writes, across processes;
//   - busy_timeout waits for another writer instead of failing at once;
//   - _txlock=immediate takes the write lock when a transaction begins, which
//     is what MySQL's SELECT ... FOR UPDATE gives the repositories;
//   - times are written and read as UTC text, like the MySQL schema;
//   - auto_vacuum=incremental (only takes effect on a new file) lets the
//     cleanup job return space freed by deleted rows.
func sqliteDSN(path string) string {
	q := url.Values{}
	q.Set("_busy_timeout", "10000")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "NORMAL")
	q.Set("_auto_vacuum", "INCREMENTAL")
	q.Set("_txlock", "immediate")
	q.Set("_time_format", "sqlite")
	q.Set("_timezone", "UTC")
	return "file:" + path + "?" + q.Encode()
}

// ForUpdate is the row-lock suffix for SELECTs inside a transaction. SQLite
// has none; its transactions already hold the database write lock.
func (db *DB) ForUpdate() string {
	if db.Driver == MySQL {
		return " FOR UPDATE"
	}
	return ""
}

// IsDuplicate reports a unique-key violation.
func (db *DB) IsDuplicate(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1062
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// IsMissingTable reports that a query named a table that does not exist yet.
func (db *DB) IsMissingTable(err error) bool { return IsMissingTable(db.Driver, err) }

func IsMissingTable(d Driver, err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1146
	}
	return d == SQLite && err != nil && strings.Contains(err.Error(), "no such table")
}

// Raw is the underlying *sql.DB.
func (db *DB) Raw() (*sql.DB, error) { return db.SqlConn.RawDB() }
