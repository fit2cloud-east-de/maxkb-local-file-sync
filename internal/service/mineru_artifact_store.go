package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"maxkb-local-file-sync/internal/repository"
)

const mineruTempResultPrefix = "maxkb-mineru-result-"

// MinerUArtifactStore optionally persists the ZIP returned by MinerU.
// It deliberately lives in the service layer: repository/config code only
// stores the user's policy and path, while this type owns filesystem safety,
// atomic publication and temporary-result cleanup.
type minerUArtifactSettingsProvider interface {
	GetMinerUArtifactSettings(context.Context) (repository.MinerUArtifactSettings, error)
}

type MinerUArtifactStore struct {
	settingsProvider minerUArtifactSettingsProvider
}

func NewMinerUArtifactStore() *MinerUArtifactStore { return &MinerUArtifactStore{} }

// SetSystemSettingsRepository wires the non-secret, system-wide artifact
// policy. It is intentionally a setter so existing SyncExecutor construction
// and service tests remain source-compatible.
func (s *MinerUArtifactStore) SetSystemSettingsRepository(repo repository.SystemSettingsRepository) {
	s.settingsProvider = repo
}

// Persist copies the opaque MinerU result ZIP to the configured result
// directory without extracting or rewriting it. The final layout is:
//
//	{root}/{safe task name}/{safe batch id}/{source relative directory}/{source name}/
//
// A batch id is required so two runs of the same task cannot overwrite each
// other's result. The returned path points to the ZIP in the published tree.
// When result-ZIP retention is disabled, the temporary input path is returned
// unchanged and no filesystem operation is performed.
func (s *MinerUArtifactStore) Persist(ctx context.Context, folder *repository.SyncFolder, batchID, sourceRelativePath, artifactRoot, resultPath string) (string, error) {
	if folder == nil {
		return "", errors.New("MinerU artifact folder is required")
	}
	retainResult, saveDir, err := s.artifactPolicy(ctx, folder)
	if err != nil {
		return "", err
	}
	if !retainResult {
		return resultPath, nil
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	root, err := absoluteConfiguredRoot(saveDir)
	if err != nil {
		return "", err
	}
	artifactRoot, err = absoluteExistingDirectory(artifactRoot, "MinerU artifact root")
	if err != nil {
		return "", err
	}
	resultPath, err = absoluteExistingFile(resultPath, "MinerU result ZIP")
	if err != nil {
		return "", err
	}
	resultRel, err := filepath.Rel(artifactRoot, resultPath)
	if err != nil || resultRel == ".." || strings.HasPrefix(resultRel, ".."+string(os.PathSeparator)) || filepath.IsAbs(resultRel) {
		return "", errors.New("MinerU result ZIP is outside the artifact root")
	}

	components, err := safeSourceComponents(sourceRelativePath)
	if err != nil {
		return "", err
	}
	if batchID == "" {
		return "", errors.New("MinerU batch id is required for artifact isolation")
	}
	taskComponent := safePathComponent(folder.Name, "task name")
	batchComponent := safePathComponent(batchID, "batch id")
	destination := filepath.Join(append([]string{root, taskComponent, batchComponent}, components...)...)
	if err := ensureContainedPath(root, destination); err != nil {
		return "", fmt.Errorf("invalid MinerU artifact destination: %w", err)
	}
	if err := ensureDirectoryPath(root, filepath.Dir(destination)); err != nil {
		return "", fmt.Errorf("create MinerU artifact parent: %w", err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(destination), ".mineru-artifact-staging-")
	if err != nil {
		return "", fmt.Errorf("create MinerU artifact staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := copyArtifactTree(ctx, artifactRoot, staging); err != nil {
		return "", fmt.Errorf("copy MinerU artifact: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if err := replacePublishedDirectory(destination, staging); err != nil {
		return "", fmt.Errorf("publish MinerU artifact: %w", err)
	}
	published = true
	return filepath.Join(destination, filepath.FromSlash(filepath.Clean(filepath.ToSlash(resultRel)))), nil
}

// CleanupTemporaryResult removes only directories created by waitMinerU. It
// keeps the original one-argument service contract used by callers that do not
// have system settings wired (notably tests). The production executor uses
// CleanupTemporaryResultWithPolicy below so the system setting is honored.
func (s *MinerUArtifactStore) CleanupTemporaryResult(resultRoot string) error {
	return s.cleanupTemporaryResult(resultRoot)
}

// CleanupTemporaryResultWithPolicy removes the private temporary result after
// the current file reaches a safe checkpoint. Published ZIP retention is
// handled independently by Persist and the system cleanup service.
func (s *MinerUArtifactStore) CleanupTemporaryResultWithPolicy(ctx context.Context, folder *repository.SyncFolder, resultRoot string) error {
	if strings.TrimSpace(resultRoot) == "" {
		return nil
	}
	cleanup, err := s.cleanupEnabled(ctx, folder)
	if err != nil {
		return err
	}
	if !cleanup {
		return nil
	}
	return s.cleanupTemporaryResult(resultRoot)
}

func (s *MinerUArtifactStore) cleanupTemporaryResult(resultRoot string) error {
	if strings.TrimSpace(resultRoot) == "" {
		return nil
	}
	abs, err := filepath.Abs(resultRoot)
	if err != nil {
		return fmt.Errorf("resolve MinerU temporary result: %w", err)
	}
	abs = filepath.Clean(abs)
	if filepath.Base(abs) == "" || !strings.HasPrefix(filepath.Base(abs), mineruTempResultPrefix) {
		return errors.New("refusing to clean an unknown MinerU temporary result")
	}
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve temporary directory: %w", err)
	}
	if err := ensureContainedPath(tempRoot, abs); err != nil {
		return fmt.Errorf("refusing to clean outside temporary directory: %w", err)
	}
	return os.RemoveAll(abs)
}

func (s *MinerUArtifactStore) artifactPolicy(ctx context.Context, folder *repository.SyncFolder) (bool, string, error) {
	if s != nil && s.settingsProvider != nil {
		settings, err := s.settingsProvider.GetMinerUArtifactSettings(ctx)
		if err != nil {
			return false, "", fmt.Errorf("load MinerU artifact settings: %w", err)
		}
		// The default (never) retains the ZIP. Only the explicit immediate
		// policy skips publishing a copy to the configured retention root.
		return settings.CleanupPolicy != repository.MinerUCleanupPolicyImmediate, settings.ResultSaveDir, nil
	}
	if folder == nil {
		return false, "", errors.New("MinerU artifact folder is required")
	}
	return folder.MinerUSaveFullResult, folder.MinerUResultSaveDir, nil
}

func (s *MinerUArtifactStore) cleanupEnabled(ctx context.Context, folder *repository.SyncFolder) (bool, error) {
	// Private temporary result directories are implementation details and are
	// always removed after the current file reaches its safe checkpoint. The
	// user-facing retention policy only controls the published ZIP copy.
	return true, nil
}

func absoluteConfiguredRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("MinerU result save directory is required when result-ZIP retention is enabled")
	}
	if isCrossPlatformAbsolutePath(raw) == false {
		return "", errors.New("MinerU result save directory must be an absolute path")
	}
	root, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve MinerU result save directory: %w", err)
	}
	if err := ensureDirectoryPath(root, root); err != nil {
		return "", fmt.Errorf("prepare MinerU result save directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("stat MinerU result save directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("MinerU result save directory must be a real directory")
	}
	return root, nil
}

func absoluteExistingDirectory(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s must be a real directory", label)
	}
	return filepath.Clean(abs), nil
}

func absoluteExistingFile(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file", label)
	}
	return filepath.Clean(abs), nil
}

func safeSourceComponents(raw string) ([]string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if normalized == "" {
		return nil, errors.New("MinerU source relative path is required")
	}
	if isCrossPlatformAbsolutePath(normalized) || strings.HasPrefix(normalized, "/") {
		return nil, errors.New("MinerU source path must be relative")
	}
	parts := strings.Split(normalized, "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("unsafe MinerU source path component %q", part)
		}
		result = append(result, safePathComponent(part, "source path component"))
	}
	return result, nil
}

func safePathComponent(raw, label string) string {
	original := strings.TrimSpace(raw)
	if original == "" {
		return "unnamed"
	}
	var b strings.Builder
	for _, r := range original {
		if r == '/' || r == '\\' || unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.Trim(b.String(), " .")
	if clean == "" || clean == "." || clean == ".." {
		clean = "unnamed"
	}
	if isWindowsReservedName(clean) {
		clean = "_" + clean
	}
	if clean != original {
		digest := sha256.Sum256([]byte(original))
		clean += "-" + hex.EncodeToString(digest[:])[:8]
	}
	if clean == "" {
		return label
	}
	return clean
}

func isWindowsReservedName(name string) bool {
	base := strings.ToLower(strings.TrimSuffix(name, "."))
	switch base {
	case "con", "prn", "aux", "nul", "clock$", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	default:
		return false
	}
}

func isCrossPlatformAbsolutePath(path string) bool {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return true
	}
	return len(path) >= 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':'
}

func ensureContainedPath(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(targetAbs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return errors.New("path escapes root")
	}
	return nil
}

func ensureDirectoryPath(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := ensureContainedPath(root, target); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("root is not a real directory")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("path component is not a real directory: %s", current)
		}
	}
	return nil
}

func copyArtifactTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in MinerU artifact: %s", rel)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported MinerU artifact entry: %s", rel)
		}
		return copyRegularFile(ctx, path, target)
	})
}

func copyRegularFile(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(out, in, make([]byte, 128*1024))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return contextErr(ctx)
}

func replacePublishedDirectory(destination, staging string) error {
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("existing MinerU artifact destination is not a directory")
		}
		backup := destination + ".old-" + uuid.NewString()
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
		if err := os.Rename(staging, destination); err != nil {
			_ = os.Rename(backup, destination)
			return err
		}
		return os.RemoveAll(backup)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(staging, destination)
}
