-- Repair marker for databases that reached v8 before the complete MinerU
-- artifact schema was shipped. The additive repair runs in MigrateUp after
-- this no-op migration so existing user databases are never rebuilt.
SELECT 1;
