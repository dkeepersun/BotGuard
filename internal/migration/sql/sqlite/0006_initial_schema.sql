-- SQLite schema, equal to the MySQL schema after its migration 0006. SQLite
-- history starts here, so both dialects share version numbers from now on:
-- every later migration adds one file to sql/mysql and one to sql/sqlite.
--
-- Times are UTC text 'YYYY-MM-DD HH:MM:SS.SSS'. AUTOINCREMENT keeps ids of
-- deleted rows from being reused, as MySQL does: Redis statistics are keyed
-- by app id. Triggers stand in for MySQL's ON UPDATE CURRENT_TIMESTAMP and
-- must stay on one line (statements are split at a ";" ending a line).

CREATE TABLE botguard_app (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_key TEXT NOT NULL,
  name TEXT NOT NULL,
  remark TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 1,
  active_policy_version INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now')),
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_app_key ON botguard_app (app_key);
CREATE TRIGGER trg_app_updated AFTER UPDATE ON botguard_app FOR EACH ROW WHEN NEW.updated_at IS OLD.updated_at BEGIN UPDATE botguard_app SET updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') WHERE id = NEW.id; END;

CREATE TABLE botguard_app_origin (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_id INTEGER NOT NULL,
  origin TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_app_origin ON botguard_app_origin (app_id, origin);

CREATE TABLE botguard_app_action (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_id INTEGER NOT NULL,
  action TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  challenge_mode TEXT NOT NULL DEFAULT 'invisible',
  challenge_kinds TEXT NOT NULL DEFAULT 'slider',
  pow_puzzles INTEGER NOT NULL DEFAULT 8,
  pow_difficulty INTEGER NOT NULL DEFAULT 2000,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now')),
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_app_action ON botguard_app_action (app_id, action);
CREATE TRIGGER trg_app_action_updated AFTER UPDATE ON botguard_app_action FOR EACH ROW WHEN NEW.updated_at IS OLD.updated_at BEGIN UPDATE botguard_app_action SET updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') WHERE id = NEW.id; END;

CREATE TABLE botguard_app_secret (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  secret_id TEXT NOT NULL,
  app_id INTEGER NOT NULL,
  secret_ciphertext BLOB NULL,
  kek_id TEXT NULL,
  status INTEGER NOT NULL DEFAULT 1,
  expires_at DATETIME NULL,
  rotated_from TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now')),
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_secret_id ON botguard_app_secret (secret_id);
CREATE INDEX idx_secret_app ON botguard_app_secret (app_id);
CREATE TRIGGER trg_app_secret_updated AFTER UPDATE ON botguard_app_secret FOR EACH ROW WHEN NEW.updated_at IS OLD.updated_at BEGIN UPDATE botguard_app_secret SET updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') WHERE id = NEW.id; END;

CREATE TABLE botguard_admin (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  status INTEGER NOT NULL DEFAULT 1,
  session_version INTEGER NOT NULL DEFAULT 1,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  totp_ciphertext BLOB NULL,
  totp_kek_id TEXT NULL,
  totp_last_step INTEGER NULL,
  last_login_at DATETIME NULL,
  last_login_ip TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now')),
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_admin_username ON botguard_admin (username);
CREATE TRIGGER trg_admin_updated AFTER UPDATE ON botguard_admin FOR EACH ROW WHEN NEW.updated_at IS OLD.updated_at BEGIN UPDATE botguard_admin SET updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') WHERE id = NEW.id; END;

CREATE TABLE botguard_audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  operation TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NULL,
  ip TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE INDEX idx_audit_created ON botguard_audit_log (created_at);
CREATE INDEX idx_audit_actor ON botguard_audit_log (actor, created_at);
CREATE INDEX idx_audit_operation ON botguard_audit_log (operation, created_at);
