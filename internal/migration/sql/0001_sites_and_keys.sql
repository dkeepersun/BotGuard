-- Sites, origins, actions and server API keys for the verification path.
-- Connections must use time_zone '+00:00'; all DATETIME values are UTC.

CREATE TABLE bg_site (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_key VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  status TINYINT NOT NULL DEFAULT 1 COMMENT '1 enabled, 2 disabled',
  active_policy_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_public_key (public_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE bg_site_origin (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  site_id BIGINT UNSIGNED NOT NULL,
  origin VARCHAR(255) NOT NULL COMMENT 'normalized scheme://host[:port]',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_site_origin (site_id, origin)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE bg_site_action (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  site_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  pow_puzzles SMALLINT UNSIGNED NOT NULL DEFAULT 8,
  pow_difficulty BIGINT UNSIGNED NOT NULL DEFAULT 2000 COMMENT 'expected attempts per puzzle',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_site_action (site_id, action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE bg_api_key (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  key_id VARCHAR(64) NOT NULL,
  site_id BIGINT UNSIGNED NOT NULL,
  secret_hash BINARY(32) NOT NULL COMMENT 'SHA-256 of the secret, shown once at creation',
  status TINYINT NOT NULL DEFAULT 1 COMMENT '1 enabled, 2 revoked',
  expires_at DATETIME(3) NULL,
  rotated_from VARCHAR(64) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_key_id (key_id),
  KEY idx_site (site_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
