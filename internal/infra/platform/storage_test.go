package platform

import (
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
