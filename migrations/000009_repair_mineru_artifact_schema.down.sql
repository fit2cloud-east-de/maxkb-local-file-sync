-- The v8 repair is intentionally not reversible: it only adds missing columns
-- to preserve user settings and avoid destructive rollback of user databases.
SELECT 1;
