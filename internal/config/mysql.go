package config

import (
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

// NormalizeMySQLDSN forces the session settings the schema relies on, so a
// DSN missing them cannot silently store or read local times.
func NormalizeMySQLDSN(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("MySQL DSN is empty")
	}
	cfg, err := mysql.ParseDSN(raw)
	if err != nil {
		return "", err
	}
	if cfg.DBName == "" {
		return "", errors.New("MySQL DSN must name a database, e.g. user:pass@tcp(host:3306)/botguard")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	// Migrations are split into single statements; never allow batches.
	cfg.MultiStatements = false
	return cfg.FormatDSN(), nil
}
