-- How an app's widgets look and what they say (domain.AppUI as JSON), set in
-- the console and served by GET /v1/ui. NULL leaves the widget's defaults.

ALTER TABLE botguard_app
  ADD COLUMN ui_config TEXT NULL COMMENT 'domain.AppUI as JSON' AFTER background_version;
