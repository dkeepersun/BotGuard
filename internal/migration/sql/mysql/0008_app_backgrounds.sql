-- Challenge backgrounds per app: where pictures come from, and photos the
-- app's admins uploaded. background_version changes on every upload or
-- delete, so gateways reload the app's photos only when they change.

ALTER TABLE botguard_app
  ADD COLUMN background_mode VARCHAR(16) NOT NULL DEFAULT 'mixed' COMMENT 'mixed, photo or procedural' AFTER active_policy_version,
  ADD COLUMN background_builtin TINYINT(1) NOT NULL DEFAULT 1 COMMENT '1: include the built-in photos' AFTER background_mode,
  ADD COLUMN background_version BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER background_builtin;

CREATE TABLE botguard_app_background (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  app_id BIGINT UNSIGNED NOT NULL,
  image MEDIUMBLOB NOT NULL COMMENT 'JPEG, 720x450, re-encoded on upload',
  sha256 CHAR(64) NOT NULL COMMENT 'of image, to refuse duplicates',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_app_background (app_id, sha256)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
