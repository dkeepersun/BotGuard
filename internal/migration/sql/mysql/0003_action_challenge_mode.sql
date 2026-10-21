-- Per-action challenge mode. Existing actions keep today's behaviour
-- (invisible proof of work); botguard-ctl creates new actions as adaptive.

ALTER TABLE botguard_app_action
  ADD COLUMN challenge_mode VARCHAR(16) NOT NULL DEFAULT 'invisible' COMMENT 'invisible | slider | adaptive' AFTER enabled;
