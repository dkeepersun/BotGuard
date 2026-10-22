-- Whether the page may choose the challenge kinds (kinds / levels in
-- POST /v1/challenges). Off by default: a client-chosen kind lets a bot
-- always ask for the easiest one, so it is an explicit per-action opt-in.

ALTER TABLE botguard_app_action
  ADD COLUMN client_select TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1: honour kinds/levels sent by the page' AFTER challenge_kinds;
