-- Add an explicit retention value/unit while keeping the legacy days column
-- for old clients and rollback-safe upgrades.
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_after_value INTEGER NOT NULL DEFAULT 30;
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_after_unit TEXT NOT NULL DEFAULT 'day';

-- Migrate the previous day-only setting into the new canonical fields.
UPDATE system_settings
SET mineru_cleanup_after_value = CASE
        WHEN mineru_cleanup_after_days > 0 THEN mineru_cleanup_after_days
        ELSE 30
    END,
    mineru_cleanup_after_unit = 'day'
WHERE id = 1;
