-- Whether the page may choose the challenge kinds (kinds / levels in
-- POST /v1/challenges); same as MySQL migration 0007.

ALTER TABLE botguard_app_action ADD COLUMN client_select INTEGER NOT NULL DEFAULT 0;
