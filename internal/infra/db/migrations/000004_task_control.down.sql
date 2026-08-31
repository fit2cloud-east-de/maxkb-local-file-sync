-- Rollback task control features
DROP INDEX IF EXISTS idx_sync_folders_enabled;

-- Note: SQLite does not support DROP COLUMN directly
-- To properly rollback, you would need to:
-- 1. Create new table without the columns
-- 2. Copy data
-- 3. Drop old table
-- 4. Rename new table

-- For safety, we keep the columns but mark them as deprecated
-- Real rollback would require manual intervention
