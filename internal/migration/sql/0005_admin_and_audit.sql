-- Admin console accounts and the audit log (docs/admin.md).

CREATE TABLE sys_admin (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username VARCHAR(32) NOT NULL,
  display_name VARCHAR(64) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL COMMENT 'pbkdf2-sha256$iterations$salt$hash',
  role VARCHAR(16) NOT NULL COMMENT 'owner, operator or viewer',
  status TINYINT NOT NULL DEFAULT 1 COMMENT '1 enabled, 2 disabled',
  session_version INT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'bumped to end every session of this admin',
  must_change_password TINYINT NOT NULL DEFAULT 0,
  totp_ciphertext VARBINARY(128) NULL COMMENT 'TOTP secret sealed with the KEK; NULL when TOTP is off',
  totp_kek_id VARCHAR(32) NULL,
  totp_last_step BIGINT NULL COMMENT 'last accepted TOTP time step, so a code is never accepted twice',
  last_login_at DATETIME(3) NULL,
  last_login_ip VARCHAR(64) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE sys_audit_log (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  actor VARCHAR(64) NOT NULL COMMENT 'admin:<username> or cli:<os user>',
  operation VARCHAR(64) NOT NULL,
  target VARCHAR(160) NOT NULL DEFAULT '',
  detail TEXT NULL COMMENT 'redacted JSON; never secret keys, passwords or TOTP secrets',
  ip VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_created (created_at),
  KEY idx_actor (actor, created_at),
  KEY idx_operation (operation, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bg_app ADD COLUMN remark VARCHAR(255) NOT NULL DEFAULT '' AFTER name;
