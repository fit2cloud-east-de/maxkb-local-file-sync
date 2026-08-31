package db

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func migrationFS(t *testing.T) fs.FS {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/infra/db -> project root
	return os.DirFS(filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")))
}

func TestMigrationSourcesMatch(t *testing.T) {
	root := migrationFS(t)
	entries, err := fs.ReadDir(root, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		seen[name] = struct{}{}
		rootData, err := fs.ReadFile(root, filepath.ToSlash(filepath.Join("migrations", name)))
		if err != nil {
			t.Fatalf("read root migration %s: %v", name, err)
		}
		embeddedData, err := fs.ReadFile(MigrationsFS, filepath.ToSlash(filepath.Join("migrations", name)))
		if err != nil {
			t.Fatalf("read embedded migration %s: %v", name, err)
		}
		if !bytes.Equal(rootData, embeddedData) {
			t.Errorf("root and embedded migration differ: %s", name)
		}
	}

	embeddedEntries, err := fs.ReadDir(MigrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range embeddedEntries {
		if entry.IsDir() {
			continue
		}
		if _, ok := seen[entry.Name()]; !ok {
			t.Errorf("embedded migration is missing from root migrations: %s", entry.Name())
		}
	}
}

func TestMigrationChainV1ToV10(t *testing.T) {
	database, err := New(Config{DataDir: t.TempDir(), DBName: "migration.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Use the embedded filesystem, which is the same source used by production
	// startup. The root migrations directory is checked separately above.
	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	version, dirty, err := database.GetMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 10 || dirty {
		t.Fatalf("version=%d dirty=%v", version, dirty)
	}

	for _, column := range []struct{ table, name string }{
		{"sync_runs", "recovery_count"}, {"sync_runs", "last_interrupted_at"},
		{"sync_runs", "last_recovery_at"}, {"sync_runs", "parent_run_id"},
	} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", column.table, column.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("missing %s.%s", column.table, column.name)
		}
	}

	for _, column := range []string{
		"mineru_cleanup_policy", "mineru_cleanup_after_days", "mineru_cleanup_keep_batches",
		"mineru_cleanup_cron", "mineru_last_cleanup_at", "mineru_last_cleanup_status",
		"mineru_last_cleanup_deleted_count", "mineru_last_cleanup_error",
	} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pragma_table_info('system_settings') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatalf("inspect system_settings.%s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("missing system_settings.%s", column)
		}
	}

	var runIDForeignKey int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM pragma_foreign_key_list('active_task_locks')
		WHERE "from"='run_id' AND "table"='sync_runs' AND on_delete='CASCADE'
	`).Scan(&runIDForeignKey); err != nil {
		t.Fatal(err)
	}
	if runIDForeignKey != 1 {
		t.Fatal("active_task_locks.run_id foreign key is missing")
	}

	for _, indexName := range []string{
		"uq_active_task_locks_folder",
		"uq_active_task_locks_run",
		"idx_active_task_locks_status",
		"uq_sync_folders_normalized_local_path",
		"uq_sync_folders_remote_binding",
		"uq_sync_files_normalized_path",
		"idx_job_queue_claimable",
		"idx_sync_runs_checkpoint",
	} {
		var indexCount int
		if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&indexCount); err != nil {
			t.Fatal(err)
		}
		if indexCount != 1 {
			t.Fatalf("missing index %s", indexName)
		}
	}

	if err := database.CheckForeignKeys(); err != nil {
		t.Fatalf("foreign key check failed after migration: %v", err)
	}
	// Running the production migration entry point again must be a no-op.
	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("second migrate up: %v", err)
	}
}

func TestMigrateUpRepairsV8DatabaseMissingMinerUArtifactColumn(t *testing.T) {
	database, err := New(Config{DataDir: t.TempDir(), DBName: "missing-mineru-column.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Simulate a user database created by the previously shipped v8 migration:
	// it has a clean v8 marker, but mineru_save_full_result was never recorded.
	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	if _, err := database.Exec(`UPDATE system_settings SET mineru_cleanup_policy='keep_batches', mineru_cleanup_keep_batches=7 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{
		"mineru_save_full_result",
		"mineru_result_save_dir",
		"mineru_cleanup_temp_results",
		"mineru_cleanup_after_value",
		"mineru_cleanup_after_unit",
	} {
		if _, err := database.Exec(`ALTER TABLE system_settings DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop simulated legacy column %s: %v", column, err)
		}
	}
	if _, err := database.Exec(`UPDATE schema_migrations SET version=8, dirty=0`); err != nil {
		t.Fatal(err)
	}

	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("repair migrate up: %v", err)
	}

	version, dirty, err := database.GetMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 10 || dirty {
		t.Fatalf("version=%d dirty=%v, want v10 clean", version, dirty)
	}

	for _, column := range []string{
		"mineru_save_full_result",
		"mineru_result_save_dir",
		"mineru_cleanup_temp_results",
	} {
		var columnCount int
		if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('system_settings') WHERE name = ?`, column).Scan(&columnCount); err != nil {
			t.Fatalf("inspect repaired column %s: %v", column, err)
		}
		if columnCount != 1 {
			t.Fatalf("%s was not restored", column)
		}
	}

	var policy string
	var keepBatches int
	if err := database.QueryRow(`SELECT mineru_cleanup_policy, mineru_cleanup_keep_batches FROM system_settings WHERE id=1`).Scan(&policy, &keepBatches); err != nil {
		t.Fatal(err)
	}
	if policy != "keep_batches" || keepBatches != 7 {
		t.Fatalf("existing settings changed during repair: policy=%q keepBatches=%d", policy, keepBatches)
	}

	var defaultValue int
	if err := database.QueryRow(`SELECT mineru_save_full_result FROM system_settings WHERE id=1`).Scan(&defaultValue); err != nil {
		t.Fatal(err)
	}
	if defaultValue != 0 {
		t.Fatalf("repaired column default=%d, want 0", defaultValue)
	}
}

func TestSQLiteProductionPragmas(t *testing.T) {
	database, err := New(Config{DataDir: t.TempDir(), DBName: "pragma.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var foreignKeys int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, want 1", foreignKeys)
	}

	var journalMode string
	if err := database.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode=%q, want wal", journalMode)
	}
}

func TestMigrationFailureLeavesDirtyAndRollsBackFailedStep(t *testing.T) {
	migrationSource := fstest.MapFS{
		"migrations/000001_base.up.sql":     &fstest.MapFile{Data: []byte("CREATE TABLE stable (id INTEGER PRIMARY KEY);\n")},
		"migrations/000001_base.down.sql":   &fstest.MapFile{Data: []byte("DROP TABLE stable;\n")},
		"migrations/000002_broken.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE transient (id INTEGER PRIMARY KEY);\nTHIS IS INVALID SQL;\n")},
		"migrations/000002_broken.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE transient;\n")},
	}

	database, err := New(Config{DataDir: t.TempDir(), DBName: "failure.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := database.MigrateUp(migrationSource); err == nil {
		t.Fatal("expected migration failure")
	}
	version, dirty, err := database.GetMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 || !dirty {
		t.Fatalf("version=%d dirty=%v, want version 2 dirty=true", version, dirty)
	}

	var stableCount, transientCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stable'").Scan(&stableCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='transient'").Scan(&transientCount); err != nil {
		t.Fatal(err)
	}
	if stableCount != 1 || transientCount != 0 {
		t.Fatalf("failed migration left unexpected schema: stable=%d transient=%d", stableCount, transientCount)
	}

	if err := database.MigrateUp(migrationSource); err == nil {
		t.Fatal("expected dirty database to refuse a second migration attempt")
	}
}

func TestInitSchemaIsNotAProductionMigrationSource(t *testing.T) {
	database, err := New(Config{DataDir: t.TempDir(), DBName: "legacy.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateUp(MigrationsFS); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected a schema-without-migration-history database to be rejected, got %v", err)
	}
}

func TestMigrateUpRepairsKnownV1DirtyV4Schema(t *testing.T) {
	database, err := New(Config{DataDir: t.TempDir(), DBName: "dirty-recovery.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	if err := database.MigrateDown(MigrationsFS, 6); err != nil {
		t.Fatalf("migrate down to v4: %v", err)
	}
	version, dirty, err := database.GetMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 4 || dirty {
		t.Fatalf("version=%d dirty=%v, want v4 clean", version, dirty)
	}
	if _, err := database.Exec("UPDATE schema_migrations SET version=1, dirty=1"); err != nil {
		t.Fatal(err)
	}

	if err := database.MigrateUp(MigrationsFS); err != nil {
		t.Fatalf("migrate up after known dirty state: %v", err)
	}
	version, dirty, err = database.GetMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 10 || dirty {
		t.Fatalf("version=%d dirty=%v, want v10 clean", version, dirty)
	}

	for _, column := range []string{"mineru_save_full_result", "mineru_result_save_dir", "mineru_cleanup_temp_results"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pragma_table_info('system_settings') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatalf("inspect system_settings.%s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("missing system_settings.%s", column)
		}
	}
}
