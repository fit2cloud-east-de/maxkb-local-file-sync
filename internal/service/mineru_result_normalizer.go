package service

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// normalizeMinerUResultZIP rewrites a MinerU result archive in place. MinerU
// normally returns a directory containing full.md, images, and auxiliary
// metadata. MaxKB should receive only the Markdown and extracted images while
// preserving the archive's original filename and, when present, its single
// result directory.
func normalizeMinerUResultZIP(ctx context.Context, archivePath string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	archivePath, err := absoluteExistingFile(archivePath, "MinerU result ZIP")
	if err != nil {
		return err
	}

	input, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open MinerU result ZIP: %w", err)
	}
	info, err := input.Stat()
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("stat MinerU result ZIP: %w", err)
	}
	reader, err := zip.NewReader(input, info.Size())
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("read MinerU result ZIP: %w", err)
	}

	workDir, err := os.MkdirTemp(filepath.Dir(archivePath), ".mineru-normalize-")
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create MinerU ZIP workspace: %w", err)
	}
	defer os.RemoveAll(workDir)

	if err := extractMinerUArchive(ctx, reader.File, workDir); err != nil {
		_ = input.Close()
		return err
	}
	if err := input.Close(); err != nil {
		return fmt.Errorf("close MinerU result ZIP: %w", err)
	}

	resultRoot, err := findMinerUResultRoot(workDir)
	if err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	tempArchive, err := os.CreateTemp(filepath.Dir(archivePath), ".mineru-normalized-*.zip")
	if err != nil {
		return fmt.Errorf("create normalized MinerU ZIP: %w", err)
	}
	tempPath := tempArchive.Name()
	defer os.Remove(tempPath)
	if err := tempArchive.Chmod(0o600); err != nil {
		_ = tempArchive.Close()
		return fmt.Errorf("set normalized MinerU ZIP permissions: %w", err)
	}

	writer := zip.NewWriter(tempArchive)
	if err := writeMinerUResultArchive(ctx, writer, resultRoot, workDir); err != nil {
		_ = writer.Close()
		_ = tempArchive.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		_ = tempArchive.Close()
		return fmt.Errorf("close normalized MinerU ZIP: %w", err)
	}
	if err := tempArchive.Close(); err != nil {
		return fmt.Errorf("close normalized MinerU ZIP file: %w", err)
	}
	if err := replaceFileAtomically(archivePath, tempPath); err != nil {
		return fmt.Errorf("publish normalized MinerU ZIP: %w", err)
	}
	return nil
}

func extractMinerUArchive(ctx context.Context, files []*zip.File, destination string) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := contextErr(ctx); err != nil {
			return err
		}
		name, isDirectory, err := safeZipEntryName(file.Name)
		if err != nil {
			return err
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("MinerU result ZIP contains duplicate entry: %s", name)
		}
		seen[name] = struct{}{}

		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := ensureContainedPath(destination, target); err != nil {
			return fmt.Errorf("MinerU result ZIP entry escapes workspace: %w", err)
		}
		if isDirectory {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create MinerU result directory %s: %w", name, err)
			}
			continue
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("MinerU result ZIP symlink is not allowed: %s", name)
		}
		if err := ensureDirectoryPath(destination, filepath.Dir(target)); err != nil {
			return fmt.Errorf("create MinerU result parent for %s: %w", name, err)
		}
		input, err := file.Open()
		if err != nil {
			return fmt.Errorf("open MinerU result entry %s: %w", name, err)
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("create MinerU result entry %s: %w", name, err)
		}
		_, copyErr := copyWithContext(ctx, output, input)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil || closeInputErr != nil || closeOutputErr != nil {
			_ = os.Remove(target)
			if copyErr != nil {
				return fmt.Errorf("extract MinerU result entry %s: %w", name, copyErr)
			}
			if closeInputErr != nil {
				return fmt.Errorf("close MinerU result entry %s: %w", name, closeInputErr)
			}
			return fmt.Errorf("close extracted MinerU result entry %s: %w", name, closeOutputErr)
		}
	}
	return nil
}

func safeZipEntryName(raw string) (string, bool, error) {
	if strings.IndexByte(raw, 0) >= 0 {
		return "", false, errors.New("MinerU result ZIP contains a NUL byte in an entry name")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	isDirectory := strings.HasSuffix(normalized, "/")
	normalized = strings.TrimSuffix(normalized, "/")
	if normalized == "" {
		return "", true, errors.New("MinerU result ZIP contains an empty entry name")
	}
	if isCrossPlatformAbsolutePath(normalized) || strings.HasPrefix(normalized, "/") {
		return "", false, fmt.Errorf("MinerU result ZIP entry must be relative: %s", raw)
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		return "", false, fmt.Errorf("MinerU result ZIP contains an unsafe entry: %s", raw)
	}
	return clean, isDirectory, nil
}

func findMinerUResultRoot(workDir string) (string, error) {
	_, rootHasFull, err := minerURootHasFullMD(workDir)
	if err != nil {
		return "", err
	}
	if rootHasFull {
		return workDir, nil
	}

	candidates, err := minerUTopLevelDirectoriesWithFullMD(workDir)
	if err != nil {
		return "", err
	}
	if len(candidates) != 1 {
		if len(candidates) == 0 {
			return "", errors.New("MinerU result ZIP does not contain a root full.md")
		}
		return "", fmt.Errorf("MinerU result ZIP has multiple result roots: %s", strings.Join(candidates, ", "))
	}
	return filepath.Join(workDir, filepath.FromSlash(candidates[0])), nil
}

func minerURootHasFullMD(root string) (string, bool, error) {
	fullPath := filepath.Join(root, "full.md")
	info, err := os.Lstat(fullPath)
	if os.IsNotExist(err) {
		return filepath.Base(root), false, nil
	}
	if err != nil {
		return filepath.Base(root), false, fmt.Errorf("inspect MinerU full.md: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return filepath.Base(root), false, errors.New("MinerU full.md must be a regular file")
	}
	return filepath.Base(root), true, nil
}

func minerUTopLevelDirectoriesWithFullMD(workDir string) ([]string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("read MinerU result workspace: %w", err)
	}
	var candidates []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		_, hasFull, err := minerURootHasFullMD(filepath.Join(workDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if hasFull {
			candidates = append(candidates, entry.Name())
		}
	}
	return candidates, nil
}

func writeMinerUResultArchive(ctx context.Context, writer *zip.Writer, resultRoot, workDir string) error {
	fullPath := filepath.Join(resultRoot, "full.md")
	if err := addMinerUFileToArchive(ctx, writer, fullPath, archivePathForRoot(resultRoot, workDir, "full.md")); err != nil {
		return err
	}
	imagesPath := filepath.Join(resultRoot, "images")
	imagesInfo, err := os.Lstat(imagesPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect MinerU images directory: %w", err)
	}
	if imagesInfo.Mode()&os.ModeSymlink != 0 || !imagesInfo.IsDir() {
		return errors.New("MinerU images must be a real directory")
	}
	imagesArchivePath := archivePathForRoot(resultRoot, workDir, "images") + "/"
	header := &zip.FileHeader{Name: imagesArchivePath, Method: zip.Store}
	header.SetMode(0o700 | os.ModeDir)
	if _, err := writer.CreateHeader(header); err != nil {
		return fmt.Errorf("write MinerU images directory: %w", err)
	}
	return filepath.WalkDir(imagesPath, func(pathName string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
		if pathName == imagesPath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("MinerU images symlink is not allowed: %s", filepath.Base(pathName))
		}
		rel, err := filepath.Rel(resultRoot, pathName)
		if err != nil {
			return err
		}
		archivePath := archivePathForRoot(resultRoot, workDir, filepath.ToSlash(rel))
		if entry.IsDir() {
			header := &zip.FileHeader{Name: archivePath + "/", Method: zip.Store}
			header.SetMode(0o700 | os.ModeDir)
			_, err = writer.CreateHeader(header)
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported MinerU images entry: %s", rel)
		}
		return addMinerUFileToArchive(ctx, writer, pathName, archivePath)
	})
}

func archivePathForRoot(resultRoot, workDir, relative string) string {
	if resultRoot == workDir {
		return filepath.ToSlash(relative)
	}
	rootName := filepath.Base(resultRoot)
	return path.Join(rootName, filepath.ToSlash(relative))
}

func addMinerUFileToArchive(ctx context.Context, writer *zip.Writer, sourcePath, archivePath string) error {
	header := &zip.FileHeader{Name: filepath.ToSlash(archivePath), Method: zip.Deflate}
	header.SetMode(0o600)
	output, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create normalized MinerU entry %s: %w", archivePath, err)
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open normalized MinerU entry %s: %w", archivePath, err)
	}
	defer input.Close()
	if _, err := copyWithContext(ctx, output, input); err != nil {
		return fmt.Errorf("write normalized MinerU entry %s: %w", archivePath, err)
	}
	return nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	var total int64
	buffer := make([]byte, 128*1024)
	for {
		if err := contextErr(ctx); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func replaceFileAtomically(destination, replacement string) error {
	backup := destination + ".raw-backup"
	_ = os.Remove(backup)
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("move original ZIP aside: %w", err)
	}
	if err := os.Rename(replacement, destination); err != nil {
		_ = os.Rename(backup, destination)
		return fmt.Errorf("move normalized ZIP into place: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("remove original ZIP backup: %w", err)
	}
	return nil
}
