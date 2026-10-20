-- Rename sites to apps, and turn Bearer API keys into Yidun-style secret
-- pairs (secret_id + secret_key) used for HMAC request signatures.

RENAME TABLE bg_site TO bg_app,
  bg_site_origin TO bg_app_origin,
  bg_site_action TO bg_app_action,
  bg_api_key TO bg_app_secret;

ALTER TABLE bg_app
  RENAME COLUMN public_key TO app_key,
  RENAME INDEX uk_public_key TO uk_app_key;

ALTER TABLE bg_app_origin
  RENAME COLUMN site_id TO app_id,
  RENAME INDEX uk_site_origin TO uk_app_origin;

ALTER TABLE bg_app_action
  RENAME COLUMN site_id TO app_id,
  RENAME INDEX uk_site_action TO uk_app_action;

-- Bearer keys stored only a SHA-256 of the secret, which cannot verify an
-- HMAC signature. They are revoked; issue new secret pairs with botguard-ctl.
UPDATE bg_app_secret SET status = 2;

ALTER TABLE bg_app_secret
  RENAME COLUMN key_id TO secret_id,
  RENAME COLUMN site_id TO app_id,
  RENAME INDEX uk_key_id TO uk_secret_id,
  RENAME INDEX idx_site TO idx_app,
  DROP COLUMN secret_hash,
  ADD COLUMN secret_ciphertext VARBINARY(128) NULL COMMENT 'AES-256-GCM nonce||ciphertext of secret_key; NULL for revoked legacy keys' AFTER app_id,
  ADD COLUMN kek_id VARCHAR(32) NULL COMMENT 'key-encryption key that sealed secret_ciphertext' AFTER secret_ciphertext;
