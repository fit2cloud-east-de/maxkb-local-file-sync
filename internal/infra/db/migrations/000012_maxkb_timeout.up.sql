-- MaxKB parsing and smart-segmentation can take longer than a normal API read.
-- This is non-secret system configuration; credentials remain in the OS store.
ALTER TABLE system_settings ADD COLUMN maxkb_timeout_seconds INTEGER NOT NULL DEFAULT 30;
