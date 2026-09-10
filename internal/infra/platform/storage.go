package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	Config    string
	Data      string
	Snapshots string
	Logs      string
	Temp      string
	Backups   string
}

// ResolveStoragePaths returns platform-appropriate application data
// directories. Installed Windows builds use the installer-selected root;
// development builds and other platforms retain their existing locations.
func ResolveStoragePaths(homeDir string) (StoragePaths, error) {
	if homeDir == "" {
		return StoragePaths{}, errors.New("home directory is empty")
	}

	root := filepath.Join(homeDir, legacyRootName)
	switch runtime.GOOS {
	case "windows":
		legacyWindowsRoot := filepath.Join(homeDir, "AppData", "Local", applicationVendor, applicationName)
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			legacyWindowsRoot = filepath.Join(localAppData, applicationVendor, applicationName)
		}
		if executablePath, err := os.Executable(); err == nil {
			if installRoot, ok := windowsInstallRoot(executablePath); ok {
				paths := installedStoragePaths(installRoot)
				for _, sourceRoot := range []string{legacyWindowsRoot, filepath.Join(homeDir, legacyRootName)} {
					if samePath(sourceRoot, paths.Root) {
						continue
					}
					if err := migrateInstalledStorage(sourceRoot, paths); err != nil {
						return StoragePaths{}, fmt.Errorf("migrate Windows application storage: %w", err)
					}
				}
				return paths, nil
			}
		}
		root = legacyWindowsRoot
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
		Config:    filepath.Join(root, "config"),
		Data:      filepath.Join(root, "data"),
		Snapshots: filepath.Join(root, "snapshots"),
		Logs:      filepath.Join(root, "logs"),
		Temp:      filepath.Join(root, "temp"),
		Backups:   filepath.Join(root, "backups"),
	}, nil
}

func windowsInstallRoot(executablePath string) (string, bool) {
	appDir := filepath.Dir(filepath.Clean(executablePath))
	if !strings.EqualFold(filepath.Base(appDir), "app") {
		return "", false
	}
	root := filepath.Dir(appDir)
	if root == appDir {
		return "", false
	}
	return root, true
}

func installedStoragePaths(root string) StoragePaths {
	dataDir := filepath.Join(root, "data")
	return StoragePaths{
		Root:      root,
		Config:    filepath.Join(root, "config"),
		Data:      dataDir,
		Snapshots: filepath.Join(dataDir, "snapshots"),
		Logs:      filepath.Join(root, "logs"),
		Temp:      filepath.Join(dataDir, "temp"),
		Backups:   filepath.Join(dataDir, "backups"),
	}
}

func migrateInstalledStorage(sourceRoot string, destination StoragePaths) error {
	sourceInfo, err := os.Stat(sourceRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !sourceInfo.IsDir() {
		return errors.New("legacy application storage path is not a directory")
	}

	mappings := [][2]string{
		{filepath.Join(sourceRoot, "config"), destination.Config},
		{filepath.Join(sourceRoot, "data"), destination.Data},
		{filepath.Join(sourceRoot, "snapshots"), destination.Snapshots},
		{filepath.Join(sourceRoot, "logs"), destination.Logs},
		{filepath.Join(sourceRoot, "temp"), destination.Temp},
		{filepath.Join(sourceRoot, "backups"), destination.Backups},
	}
	for _, mapping := range mappings {
		if err := mergeStoragePath(mapping[0], mapping[1]); err != nil {
			return err
		}
	}
	if err := os.Remove(sourceRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Unknown or conflicting legacy files remain in place for manual recovery.
		return nil
	}
	return nil
}

func mergeStoragePath(source, target string) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	targetInfo, targetErr := os.Lstat(target)
	if errors.Is(targetErr, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Rename(source, target); err == nil {
			return nil
		}
		return copyStoragePath(source, target, sourceInfo)
	}
	if targetErr != nil {
		return targetErr
	}
	if !sourceInfo.IsDir() || !targetInfo.IsDir() {
		// Keep both sides intact when an existing destination conflicts.
		return nil
	}

	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := mergeStoragePath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.Remove(source); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return nil
}

func copyStoragePath(source, target string, sourceInfo os.FileInfo) error {
	if sourceInfo.IsDir() {
		if err := os.Mkdir(target, sourceInfo.Mode().Perm()); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := mergeStoragePath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return os.Remove(source)
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("unsupported legacy storage entry: %s", source)
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceInfo.Mode().Perm())
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := errors.Join(input.Close(), output.Close())
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		return errors.Join(copyErr, closeErr)
	}
	return os.Remove(source)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
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
