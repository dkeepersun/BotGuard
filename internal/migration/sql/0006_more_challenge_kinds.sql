-- Phase three adds four interactive kinds (docs/challenge-catalog.md); the
-- full kind list no longer fits in 64 characters.

ALTER TABLE bg_app_action
  MODIFY COLUMN challenge_kinds VARCHAR(255) NOT NULL DEFAULT 'slider' COMMENT 'comma list of interactive kinds, see internal/domain';
