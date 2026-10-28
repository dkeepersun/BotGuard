-- How an app's widgets look and what they say; same as MySQL migration 0009.

ALTER TABLE botguard_app ADD COLUMN ui_config TEXT NULL;
