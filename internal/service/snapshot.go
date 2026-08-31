package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"maxkb-local-file-sync/internal/infra/file"
)

// SnapshotService 文件快照服务。
// 快照是远端不可逆操作使用的稳定输入：先写入 snapshotDir 下的临时文件，
// 刷盘并校验后再原子重命名为最终文件名，避免执行器看到半成品。
type SnapshotService struct {
	snapshotDir string
}

// NewSnapshotService 创建快照服务。
func NewSnapshotService(snapshotDir string) *SnapshotService {
	return &SnapshotService{snapshotDir: snapshotDir}
}

// CreateSnapshot 创建文件快照。
func (s *SnapshotService) CreateSnapshot(ctx context.Context, sourcePath string) (*file.FileSnapshot, string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, "", fmt.Errorf("source path cannot be empty")
	}

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	before, err := sourceFile.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("failed to stat source file: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, "", fmt.Errorf("source path is not a regular file: %s", sourcePath)
	}

	if err := os.MkdirAll(s.snapshotDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// CreateTemp prevents a caller-controlled source name from influencing the
	// output path and ensures cleanup can safely target only this directory.
	tempFile, err := os.CreateTemp(s.snapshotDir, ".snapshot-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temporary snapshot: %w", err)
	}
	tempPath := tempFile.Name()
	keepTemp := false
	defer func() {
		_ = tempFile.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := sourceFile.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("failed to seek source file: %w", err)
	}

	hash := md5.New()
	writer := io.MultiWriter(tempFile, hash)
	buffer := make([]byte, 128*1024)
	for {
		if err := contextErr(ctx); err != nil {
			return nil, "", err
		}
		n, readErr := sourceFile.Read(buffer)
		if n > 0 {
			if _, err := writer.Write(buffer[:n]); err != nil {
				return nil, "", fmt.Errorf("failed to copy source file: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, "", fmt.Errorf("failed to read source file: %w", readErr)
		}
	}
	copiedMD5 := hex.EncodeToString(hash.Sum(nil))

	if err := tempFile.Sync(); err != nil {
		return nil, "", fmt.Errorf("failed to flush snapshot: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close snapshot: %w", err)
	}

	// A file can be replaced while it is being copied. Compare metadata and
	// then hash the still-open source descriptor from the beginning. The second
	// hash prevents a same-size mutation from being accepted merely because the
	// size stayed unchanged.
	afterCopy, err := sourceFile.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("failed to restat source file: %w", err)
	}
	if afterCopy.Size() != before.Size() || !afterCopy.ModTime().Equal(before.ModTime()) {
		return nil, "", fmt.Errorf("source changed while creating snapshot")
	}
	if _, err := sourceFile.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("failed to seek source file for verification: %w", err)
	}
	verifyHash := md5.New()
	if _, err := io.CopyBuffer(verifyHash, sourceFile, buffer); err != nil {
		return nil, "", fmt.Errorf("failed to verify source file: %w", err)
	}
	verifiedMD5 := hex.EncodeToString(verifyHash.Sum(nil))
	if verifiedMD5 != copiedMD5 {
		return nil, "", fmt.Errorf("source changed while creating snapshot")
	}

	finalPath := filepath.Join(s.snapshotDir, uuid.New().String())
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, "", fmt.Errorf("failed to finalize snapshot: %w", err)
	}
	keepTemp = true

	return &file.FileSnapshot{
		Path:       sourcePath,
		Size:       before.Size(),
		ModifiedAt: before.ModTime().Unix(),
		MD5:        copiedMD5,
	}, finalPath, nil
}

// ValidateSnapshot 验证快照是否仍然有效。
func (s *SnapshotService) ValidateSnapshot(ctx context.Context, sourcePath string, snapshot *file.FileSnapshot) (bool, error) {
	if err := contextErr(ctx); err != nil {
		return false, err
	}
	if snapshot == nil {
		return false, fmt.Errorf("snapshot cannot be nil")
	}
	if sourcePath != "" && snapshot.Path != "" && sourcePath != snapshot.Path {
		// The snapshot carries the immutable source path used at creation. Do
		// not silently validate a different path than the caller requested.
		return false, fmt.Errorf("snapshot source path mismatch")
	}
	return snapshot.Validate()
}

// CleanupSnapshot 清理快照文件。
func (s *SnapshotService) CleanupSnapshot(ctx context.Context, snapshotPath string) error {
	if snapshotPath == "" {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	root, err := filepath.Abs(s.snapshotDir)
	if err != nil {
		return fmt.Errorf("resolve snapshot directory: %w", err)
	}
	target, err := filepath.Abs(snapshotPath)
	if err != nil {
		return fmt.Errorf("resolve snapshot path: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("snapshot path outside snapshot directory")
	}

	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove snapshot: %w", err)
	}
	return nil
}

// CleanupOldSnapshots 清理过期快照。
func (s *SnapshotService) CleanupOldSnapshots(ctx context.Context, maxAge time.Duration) (int, error) {
	if err := contextErr(ctx); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	cleaned := 0
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return cleaned, err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(s.snapshotDir, entry.Name())
			if err := os.Remove(path); err == nil {
				cleaned++
			}
		}
	}
	return cleaned, nil
}
