-- Windows close behavior is intentionally a system-wide preference.  The
-- default keeps the application available in the tray so background syncs can
-- continue after the main window is closed.
ALTER TABLE system_settings ADD COLUMN close_behavior TEXT NOT NULL DEFAULT 'tray';

