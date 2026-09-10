package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveStoragePathsUsesLegacyOnLinuxAndCreatesStableSubdirs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this assertion covers the development fallback path")
	}

	home := t.TempDir()
	paths, err := ResolveStoragePaths(home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, legacyRootName)
	if paths.Root != want {
		t.Fatalf("root=%q want %q", paths.Root, want)
	}
	if paths.Data != filepath.Join(paths.Root, "data") || paths.Temp != filepath.Join(paths.Root, "temp") {
		t.Fatalf("unexpected subdirectories: %+v", paths)
	}
	if paths.Config != filepath.Join(paths.Root, "config") {
		t.Fatalf("unexpected config directory: %+v", paths)
	}
}

func TestInstalledStoragePathsUseSelectedInstallRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MaxKB")
	executable := filepath.Join(root, "app", "MaxKB-Local-File-Sync.exe")
	gotRoot, ok := windowsInstallRoot(executable)
	if !ok || gotRoot != root {
		t.Fatalf("install root=%q ok=%v, want %q", gotRoot, ok, root)
	}
	paths := installedStoragePaths(gotRoot)
	if paths.Config != filepath.Join(root, "config") || paths.Logs != filepath.Join(root, "logs") {
		t.Fatalf("unexpected writable paths: %+v", paths)
	}
	if paths.Data != filepath.Join(root, "data") || paths.Snapshots != filepath.Join(root, "data", "snapshots") {
		t.Fatalf("unexpected data paths: %+v", paths)
	}
}

func TestMigrateInstalledStorageIntoInstallerScaffold(t *testing.T) {
	parent := t.TempDir()
	legacy := filepath.Join(parent, "legacy")
	installRoot := filepath.Join(parent, "installed")
	paths := installedStoragePaths(installRoot)
	for _, dir := range []string{filepath.Join(installRoot, "app"), paths.Config, paths.Logs, paths.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := map[string]string{
		filepath.Join(legacy, "data", "app.db"):              "database",
		filepath.Join(legacy, "logs", "app.log"):             "log",
		filepath.Join(legacy, "snapshots", "state.json"):     "snapshot",
		filepath.Join(legacy, "temp", "pending.tmp"):         "temp",
		filepath.Join(legacy, "backups", "app.db.backup"):    "backup",
		filepath.Join(legacy, "config", "installation.json"): "config",
	}
	for path, content := range fixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateInstalledStorage(legacy, paths); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		filepath.Join(paths.Data, "app.db"):              "database",
		filepath.Join(paths.Logs, "app.log"):             "log",
		filepath.Join(paths.Snapshots, "state.json"):     "snapshot",
		filepath.Join(paths.Temp, "pending.tmp"):         "temp",
		filepath.Join(paths.Backups, "app.db.backup"):    "backup",
		filepath.Join(paths.Config, "installation.json"): "config",
	}
	for path, content := range want {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("migrated %s=%q err=%v, want %q", path, got, err, content)
		}
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy storage still exists: %v", err)
	}
}

func TestMigrateLegacyRoot(t *testing.T) {
	parent := t.TempDir()
	legacy := filepath.Join(parent, ".maxkb-sync")
	current := filepath.Join(parent, "Library", "Application Support", "MaxKB", applicationName)
	if err := os.MkdirAll(filepath.Join(legacy, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "data", "app.db")
	if err := os.WriteFile(marker, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyRoot(legacy, current); err != nil {
		t.Fatal(err)
	}
	migratedMarker := filepath.Join(current, "data", "app.db")
	if _, err := os.Stat(migratedMarker); err != nil {
		t.Fatalf("migrated data missing: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists: %v", err)
	}
}
