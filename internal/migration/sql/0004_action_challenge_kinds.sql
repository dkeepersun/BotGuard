-- Per-action list of interactive challenge kinds. The slider mode becomes
-- "interactive" with a one-kind list, so existing actions behave the same.

ALTER TABLE bg_app_action
  ADD COLUMN challenge_kinds VARCHAR(64) NOT NULL DEFAULT 'slider' COMMENT 'comma list: slider, click_text, click_shape' AFTER challenge_mode;

UPDATE bg_app_action SET challenge_mode = 'interactive' WHERE challenge_mode = 'slider';
