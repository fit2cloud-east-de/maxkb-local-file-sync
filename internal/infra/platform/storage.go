package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const (
	applicationVendor = "MaxKB"
	applicationName   = "MaxKB 本地文件同步工具"
	legacyRootName    = ".maxkb-sync"
)

// StoragePaths contains the directories used by the application. The program
// directory is intentionally separate from user data so upgrades and
// uninstallers cannot remove the SQLite database or logs accidentally.
type StoragePaths struct {
	Root      string
	Data      string
	Snapshots string
	Logs      string
	Temp      string
	Backups   string
}

// ResolveStoragePaths returns platform-appropriate per-user application data
// directories. On macOS and Windows it also migrates the old ~/.maxkb-sync
// root once, when the new root does not exist yet. Linux keeps the legacy
// location for compatibility with existing development environments.
func ResolveStoragePaths(homeDir string) (StoragePaths, error) {
	if homeDir == "" {
		return StoragePaths{}, errors.New("home directory is empty")
	}

	root := filepath.Join(homeDir, legacyRootName)
	switch runtime.GOOS {
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			root = filepath.Join(localAppData, applicationVendor, applicationName)
		} else {
			root = filepath.Join(homeDir, "AppData", "Local", applicationVendor, applicationName)
		}
	case "darwin":
		root = filepath.Join(homeDir, "Library", "Application Support", applicationVendor, applicationName)
	}

	legacyRoot := filepath.Join(homeDir, legacyRootName)
	if root != legacyRoot {
		if err := migrateLegacyRoot(legacyRoot, root); err != nil {
			return StoragePaths{}, err
		}
	}

	return StoragePaths{
		Root:      root,
		Data:      filepath.Join(root, "data"),
		Snapshots: filepath.Join(root, "snapshots"),
		Logs:      filepath.Join(root, "logs"),
		Temp:      filepath.Join(root, "temp"),
		Backups:   filepath.Join(root, "backups"),
	}, nil
}

func migrateLegacyRoot(legacyRoot, root string) error {
	legacyInfo, legacyErr := os.Stat(legacyRoot)
	if legacyErr != nil {
		if errors.Is(legacyErr, os.ErrNotExist) {
			return nil
		}
		return legacyErr
	}
	if !legacyInfo.IsDir() {
		return errors.New("legacy application storage path is not a directory")
	}

	if _, err := os.Stat(root); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// The old and new locations are both inside the user's home/profile. A
	// rename preserves the database and credentials references atomically on
	// the normal local filesystem and avoids copying a live SQLite database.
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	return os.Rename(legacyRoot, root)
}
