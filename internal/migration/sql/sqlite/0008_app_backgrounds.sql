-- Challenge backgrounds per app; same as MySQL migration 0008.

ALTER TABLE botguard_app ADD COLUMN background_mode TEXT NOT NULL DEFAULT 'mixed';
ALTER TABLE botguard_app ADD COLUMN background_builtin INTEGER NOT NULL DEFAULT 1;
ALTER TABLE botguard_app ADD COLUMN background_version INTEGER NOT NULL DEFAULT 0;

CREATE TABLE botguard_app_background (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_id INTEGER NOT NULL,
  image BLOB NOT NULL,
  sha256 TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now'))
);
CREATE UNIQUE INDEX uk_app_background ON botguard_app_background (app_id, sha256);
