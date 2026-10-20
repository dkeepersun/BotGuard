// Package testdb gives integration tests a migrated, empty MySQL database.
//
// Tests run only when BOTGUARD_TEST_MYSQL_DSN is set, and every table in that
// database is dropped first: point it only at a dedicated, disposable database.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/migration"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const Env = "BOTGUARD_TEST_MYSQL_DSN"

// Open skips the test when Env is unset; otherwise it resets and migrates the
// database and returns its normalized DSN and a connection.
func Open(t *testing.T) (string, sqlx.SqlConn) {
	t.Helper()
	raw := os.Getenv(Env)
	if raw == "" {
		t.Skip(Env + " not set")
	}
	dsn, err := config.NormalizeMySQLDSN(raw)
	if err != nil {
		t.Fatal(err)
	}
	conn := sqlx.NewMysql(dsn)
	db, err := conn.RawDB()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var tables []string
	if err := conn.QueryRowsCtx(ctx, &tables,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"); err != nil {
		t.Fatal(err)
	}
	for _, n := range tables {
		if _, err := db.ExecContext(ctx, "DROP TABLE `"+n+"`"); err != nil {
			t.Fatal(err)
		}
	}
	r, err := migration.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return dsn, conn
}
