-- System-wide MinerU artifact retention policy. These values are local,
-- non-secret settings; credentials remain in the OS credential store.
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_policy TEXT NOT NULL DEFAULT 'never';
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_after_days INTEGER NOT NULL DEFAULT 30;
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_keep_batches INTEGER NOT NULL DEFAULT 10;
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_cron TEXT NOT NULL DEFAULT '0 3 * * *';
ALTER TABLE system_settings ADD COLUMN mineru_last_cleanup_at TEXT;
ALTER TABLE system_settings ADD COLUMN mineru_last_cleanup_summary TEXT NOT NULL DEFAULT '';
