package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"maxkb-local-file-sync/internal/infra/file"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// FileScanner 文件扫描服务。
//
// 扫描只负责读取本地文件系统并计算差异，不执行任何远端操作。相对路径
// 在进入筛选器和结果集之前统一为使用 / 的路径，数据库中的历史路径也会
// 以同样规则规范化，以避免跨平台或旧版本路径格式造成重复记录。
type FileScanner struct {
	fileRepo   repository.SyncFileRepository
	folderRepo repository.SyncFolderRepository
}

// NewFileScanner 创建文件扫描服务。
func NewFileScanner(
	fileRepo repository.SyncFileRepository,
	folderRepo repository.SyncFolderRepository,
) *FileScanner {
	return &FileScanner{
		fileRepo:   fileRepo,
		folderRepo: folderRepo,
	}
}

// ScanResult 扫描结果。
type ScanResult struct {
	NewFiles       []string          // 新增文件的相对路径
	UpdatedFiles   []string          // 变更文件的相对路径
	DeletedFiles   []string          // 删除文件的相对路径
	RenamedFiles   map[string]string // 改名文件映射: oldPath -> newPath
	UnchangedFiles []string          // 未变更文件的相对路径
}

// PreviewResult describes the same filtering semantics used by DetectChanges.
// ExclusionReasons is keyed by normalized relative path and deliberately only
// contains safe, user-facing reason codes; it never includes file contents.
type PreviewResult struct {
	TotalFiles       int
	MatchedFiles     []string
	ExcludedFiles    []string
	ExclusionReasons map[string]string
	MinerUFiles      []string
	RegularFiles     []string
}

// PreviewMatch scans a folder without touching the database or performing any
// remote operation. It shares the production pathMatcher, including regular
// expressions, legacy glob compatibility, and exclude precedence.
func (s *FileScanner) PreviewMatch(ctx context.Context, rootPath, includePatterns, excludePatterns string, mineruEnabled bool, mineruExtensions []string) (*PreviewResult, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	root, err := file.NormalizePath(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize path: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect folder: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("preview path must be a real directory")
	}
	matcher, err := newPathMatcher(includePatterns, excludePatterns)
	if err != nil {
		return nil, err
	}
	result := &PreviewResult{
		MatchedFiles: make([]string, 0), ExcludedFiles: make([]string, 0),
		ExclusionReasons: make(map[string]string), MinerUFiles: make([]string, 0), RegularFiles: make([]string, 0),
	}
	err = filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry == nil {
			return nil
		}
		rel := ""
		if current != root {
			rel, err = filepath.Rel(root, current)
			if err != nil {
				return err
			}
			rel = normalizeRelativePath(rel)
		}
		if entry.IsDir() {
			if rel != "" && (entry.Type()&os.ModeSymlink != 0 || s.shouldSkipDirectory(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "" || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || s.shouldSkipFile(rel) {
			return nil
		}
		result.TotalFiles++
		if !matcher.Match(rel) {
			result.ExcludedFiles = append(result.ExcludedFiles, rel)
			if matcher.matches(matcher.exclude, rel) {
				result.ExclusionReasons[rel] = "excluded_by_exclude_pattern"
			} else {
				result.ExclusionReasons[rel] = "not_matched_by_include_pattern"
			}
			return nil
		}
		useMinerU := shouldUseMinerU(mineruEnabled, rel, mineruExtensions)
		if !isSupportedForSync(rel, mineruEnabled, mineruExtensions) {
			result.ExcludedFiles = append(result.ExcludedFiles, rel)
			result.ExclusionReasons[rel] = "unsupported_by_maxkb"
			return nil
		}
		result.MatchedFiles = append(result.MatchedFiles, rel)
		if useMinerU {
			result.MinerUFiles = append(result.MinerUFiles, rel)
		} else {
			result.RegularFiles = append(result.RegularFiles, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan directory: %w", err)
	}
	sort.Strings(result.MatchedFiles)
	sort.Strings(result.ExcludedFiles)
	sort.Strings(result.MinerUFiles)
	sort.Strings(result.RegularFiles)
	return result, nil
}

// pathPattern 同时支持需求中的正则表达式和历史版本已经暴露给用户的
// filepath-style glob。正则是首选；像 *.pdf、docs/**/*.txt 这样的旧配置
// 在正则编译失败时按 glob 兼容处理。这样既不把正则语义降级成 glob，
// 也不会让已有任务在升级后失效。
type pathPattern struct {
	raw   string
	regex *regexp.Regexp
	glob  *file.PatternMatcher
}

type pathMatcher struct {
	include []pathPattern
	exclude []pathPattern
}

func newPathMatcher(includePatterns, excludePatterns string) (*pathMatcher, error) {
	include, err := compilePathPatterns(includePatterns)
	if err != nil {
		return nil, fmt.Errorf("invalid include pattern: %w", err)
	}
	exclude, err := compilePathPatterns(excludePatterns)
	if err != nil {
		return nil, fmt.Errorf("invalid exclude pattern: %w", err)
	}
	return &pathMatcher{include: include, exclude: exclude}, nil
}

func compilePathPatterns(input string) ([]pathPattern, error) {
	parts := splitPatternList(input)
	patterns := make([]pathPattern, 0, len(parts))
	for _, raw := range parts {
		if expression, err := regexp.Compile(raw); err == nil {
			patterns = append(patterns, pathPattern{raw: raw, regex: expression})
			continue
		}

		// Compatibility with the existing UI/documentation, which historically
		// used glob examples. Validate the glob rather than silently accepting a
		// malformed expression such as "[invalid".
		if !looksLikeGlob(raw) {
			return nil, fmt.Errorf("%q is not a valid regular expression", raw)
		}
		globPattern := strings.ReplaceAll(raw, "\\", "/")
		if err := file.ValidatePattern(globPattern); err != nil {
			return nil, fmt.Errorf("%q: %w", raw, err)
		}
		matcher, err := file.NewPatternMatcher(globPattern, "")
		if err != nil {
			return nil, fmt.Errorf("%q: %w", raw, err)
		}
		patterns = append(patterns, pathPattern{raw: globPattern, glob: matcher})
	}
	return patterns, nil
}

func splitPatternList(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			// Keep backslashes intact for regular expressions (for example \.pdf).
			// Glob patterns are normalized separately after regex compilation fails.
			result = append(result, part)
		}
	}
	return result
}

func looksLikeGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func (m *pathMatcher) matches(patterns []pathPattern, relPath string) bool {
	for _, pattern := range patterns {
		if pattern.regex != nil && pattern.regex.MatchString(relPath) {
			return true
		}
		if pattern.glob != nil && pattern.glob.Match(relPath) {
			return true
		}
	}
	return false
}

func (m *pathMatcher) Match(relPath string) bool {
	relPath = normalizeRelativePath(relPath)
	// Exclude always wins, even when Include also matches.
	if m.matches(m.exclude, relPath) {
		return false
	}
	return len(m.include) == 0 || m.matches(m.include, relPath)
}

// NormalizeExtensions 将扩展名标签规范化为小写、带前导点且去重的列表。
// 输入可使用逗号分隔；空标签会被忽略。该函数不把扩展名当成路径或正则
// 解析，调用方可直接将其用于 MinerU 文件分类。
func NormalizeExtensions(input string) []string {
	parts := strings.Split(input, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		ext := strings.ToLower(strings.TrimSpace(part))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if _, exists := seen[ext]; exists {
			continue
		}
		seen[ext] = struct{}{}
		result = append(result, ext)
	}
	return result
}

// MatchExtension 判断文件是否匹配规范化后的扩展名列表。空列表表示所有
// 文件匹配，和产品配置中的“未限制扩展名”语义一致。
func MatchExtension(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, candidate := range extensions {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && !strings.HasPrefix(candidate, ".") {
			candidate = "." + candidate
		}
		if ext == candidate {
			return true
		}
	}
	return false
}

// ScanFolder 扫描文件夹变更。
func (s *FileScanner) ScanFolder(ctx context.Context, folderID string) (*ScanResult, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}
	if folder == nil {
		return nil, fmt.Errorf("folder not found: %s", folderID)
	}

	localPath, err := file.NormalizePath(folder.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize path: %w", err)
	}
	rootInfo, err := os.Lstat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("folder does not exist: %s", localPath)
		}
		return nil, fmt.Errorf("failed to inspect folder: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("folder must not be a symbolic link: %s", localPath)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", localPath)
	}

	matcher, err := newPathMatcher(folder.IncludePatterns, folder.ExcludePatterns)
	if err != nil {
		return nil, err
	}
	existingFiles, err := s.fileRepo.ListByFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing files: %w", err)
	}

	existingMap := make(map[string]*repository.SyncFile, len(existingFiles))
	for _, record := range existingFiles {
		if record == nil {
			continue
		}
		relPath := normalizeRelativePath(record.RelativePath)
		if relPath == "" {
			continue
		}
		// Keep the first record if an old database contains two spellings that
		// normalize to the same path. Scanning must not nondeterministically
		// choose which remote mapping to mutate.
		if _, exists := existingMap[relPath]; !exists {
			existingMap[relPath] = record
		}
	}

	mineruExtensions := file.ParseExtensions(folder.MinerUFileExtensions)
	diskFiles, allDiskPaths, err := s.scanDisk(ctx, localPath, matcher, folder.EnableMinerU, mineruExtensions)
	if err != nil {
		return nil, err
	}

	result := &ScanResult{
		NewFiles:       make([]string, 0),
		UpdatedFiles:   make([]string, 0),
		DeletedFiles:   make([]string, 0),
		RenamedFiles:   make(map[string]string),
		UnchangedFiles: make([]string, 0),
	}

	// New files are held until rename detection is complete. A hash failure
	// must not make a new file disappear from the result; it simply cannot be
	// considered for a content-based rename in this scan.
	newCandidates := make(map[string][]string)
	for _, relPath := range sortedKeys(diskFiles) {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		absPath := diskFiles[relPath]
		existing, exists := existingMap[relPath]
		if !exists {
			md5Hash, hashErr := file.CalculateMD5(absPath)
			if hashErr != nil {
				result.NewFiles = append(result.NewFiles, relPath)
				continue
			}
			newCandidates[md5Hash] = append(newCandidates[md5Hash], relPath)
			continue
		}

		changed, hashErr := s.hasFileChanged(absPath, existing)
		if hashErr != nil {
			// The file is still present and should be processed by the executor;
			// do not turn a transient read failure into a deletion.
			result.UpdatedFiles = append(result.UpdatedFiles, relPath)
		} else if changed {
			result.UpdatedFiles = append(result.UpdatedFiles, relPath)
		} else {
			result.UnchangedFiles = append(result.UnchangedFiles, relPath)
		}
		delete(existingMap, relPath)
	}

	// A rename is valid only when the MD5 match is one-to-one on both sides.
	// A map[string]string would overwrite duplicate candidates and incorrectly
	// classify one of several equal-content files as a rename.
	oldCandidates := make(map[string][]string)
	for oldPath, existing := range existingMap {
		if knownMD5 := syncFileMD5(existing); knownMD5 != "" {
			oldCandidates[knownMD5] = append(oldCandidates[knownMD5], oldPath)
		}
	}
	for md5Hash, oldPaths := range oldCandidates {
		newPaths := newCandidates[md5Hash]
		if len(oldPaths) != 1 || len(newPaths) != 1 {
			continue
		}
		oldPath, newPath := oldPaths[0], newPaths[0]
		result.RenamedFiles[oldPath] = newPath
		delete(existingMap, oldPath)
		delete(newCandidates, md5Hash)
	}

	for _, paths := range newCandidates {
		result.NewFiles = append(result.NewFiles, paths...)
	}
	// Existing records for files that are present on disk but excluded by the
	// current filters are retained. Changing Include/Exclude must not be
	// mistaken for a local deletion and must not cause a remote delete.
	for oldPath := range existingMap {
		if !allDiskPaths[oldPath] {
			result.DeletedFiles = append(result.DeletedFiles, oldPath)
		}
	}

	sort.Strings(result.NewFiles)
	sort.Strings(result.UpdatedFiles)
	sort.Strings(result.DeletedFiles)
	sort.Strings(result.UnchangedFiles)
	return result, nil
}

func (s *FileScanner) scanDisk(ctx context.Context, root string, matcher *pathMatcher, mineruEnabled bool, mineruExtensions []string) (map[string]string, map[string]bool, error) {
	diskFiles := make(map[string]string)
	allDiskPaths := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry == nil {
			return nil
		}

		relPath := ""
		if path != root {
			var err error
			relPath, err = filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("calculate relative path for %q: %w", path, err)
			}
			relPath = normalizeRelativePath(relPath)
		}

		if entry.IsDir() {
			if path != root && s.shouldSkipDirectory(relPath) {
				return filepath.SkipDir
			}
			return nil
		}
		if relPath == "" {
			return nil
		}
		allDiskPaths[relPath] = true

		// Never follow or upload a symbolic link, including links to regular
		// files. WalkDir does not descend into linked directories, but explicitly
		// checking the mode also handles linked files.
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if s.shouldSkipFile(relPath) || !matcher.Match(relPath) {
			return nil
		}
		if !isSupportedForSync(relPath, mineruEnabled, mineruExtensions) {
			// Keep the path in allDiskPaths so an unsupported file cannot be
			// mistaken for a local deletion of an existing mapping.
			return nil
		}
		diskFiles[relPath] = path
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to walk directory: %w", err)
	}
	return diskFiles, allDiskPaths, nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func shouldUseMinerU(mineruEnabled bool, relPath string, mineruExtensions []string) bool {
	if !mineruEnabled {
		return false
	}
	// An empty extension list means “自动处理 MaxKB 不支持直接上传的格式”：
	// MaxKB 原生支持的格式仍走直接上传，其他格式交给 MinerU。
	if len(mineruExtensions) == 0 {
		return !file.IsMaxKBDirectUploadSupported(relPath)
	}
	return file.MatchExtension(relPath, mineruExtensions)
}

func isSupportedForSync(relPath string, mineruEnabled bool, mineruExtensions []string) bool {
	return file.IsMaxKBDirectUploadSupported(relPath) || shouldUseMinerU(mineruEnabled, relPath, mineruExtensions)
}

func normalizeRelativePath(path string) string {
	return file.NormalizeRelativePath(path)
}

func syncFileMD5(existing *repository.SyncFile) string {
	if existing == nil {
		return ""
	}
	if existing.LastSuccessMD5 != "" {
		return existing.LastSuccessMD5
	}
	return existing.ObservedMD5
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// hasFileChanged 检查文件是否变更。MD5 通过 infra/file 的 io.Copy 流式
// 计算，不将整个文件读入内存。
func (s *FileScanner) hasFileChanged(absPath string, existing *repository.SyncFile) (bool, error) {
	currentMD5, err := file.CalculateMD5(absPath)
	if err != nil {
		return false, fmt.Errorf("failed to calculate MD5: %w", err)
	}
	knownMD5 := syncFileMD5(existing)
	if knownMD5 == "" {
		return true, nil
	}
	return currentMD5 != knownMD5, nil
}

// shouldSkipDirectory 判断目录是否属于默认忽略范围。目录在 WalkDir
// 中会返回 SkipDir，避免隐藏目录或临时目录中的文件被递归扫描。
func (s *FileScanner) shouldSkipDirectory(relPath string) bool {
	for _, component := range strings.Split(normalizeRelativePath(relPath), "/") {
		if isIgnoredName(component) {
			return true
		}
	}
	return false
}

// shouldSkipFile 判断是否应跳过文件。
func (s *FileScanner) shouldSkipFile(relPath string) bool {
	for _, component := range strings.Split(normalizeRelativePath(relPath), "/") {
		if isIgnoredName(component) {
			return true
		}
	}
	return false
}

func isIgnoredName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	lower := strings.ToLower(name)
	switch lower {
	case "thumbs.db", "desktop.ini", ".ds_store":
		return true
	}
	if strings.HasPrefix(lower, "~$") || strings.HasPrefix(lower, ".~lock.") {
		return true
	}
	if strings.HasSuffix(lower, "~") || strings.HasSuffix(lower, ".tmp") ||
		strings.HasSuffix(lower, ".temp") || strings.HasSuffix(lower, ".part") ||
		strings.HasSuffix(lower, ".crdownload") || strings.HasSuffix(lower, ".lock") {
		return true
	}
	return false
}

// DetectChanges 检测文件变更并更新状态。
func (s *FileScanner) DetectChangesWithResult(ctx context.Context, folderID string) (*ScanResult, error) {
	result, err := s.ScanFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	for _, relPath := range result.NewFiles {
		syncFile := &repository.SyncFile{
			FileID:       generateFileID(folderID, relPath),
			FolderID:     folderID,
			RelativePath: relPath,
			FileStatus:   types.FileStatusPending,
		}
		// Persist the observed hash when it can be read. This makes the
		// DetectChanges -> ScanFolder preview sequence deterministic and avoids
		// reporting a just-created record as changed solely because it has no
		// stored hash yet.
		if md5Hash, hashErr := file.CalculateMD5(filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))); hashErr == nil {
			syncFile.ObservedMD5 = md5Hash
		}
		if err := s.fileRepo.Create(ctx, syncFile); err != nil {
			return nil, fmt.Errorf("failed to create file record: %w", err)
		}
	}

	for _, relPath := range result.UpdatedFiles {
		existing, err := s.fileRepo.GetByPath(ctx, folderID, relPath)
		if err != nil {
			continue
		}
		newStatus := types.FileStatusPending
		if existing.RemoteDocID != "" {
			newStatus = types.FileStatusStaleRemoteExists
		}
		if err := s.fileRepo.UpdateStatus(ctx, existing.FileID, newStatus); err != nil {
			return nil, fmt.Errorf("failed to update file status: %w", err)
		}
	}

	// A locally missing file can reappear without requiring a new upload when
	// its content is unchanged. Restore the durable mapping to SYNCED so the
	// executor does not keep skipping it as a missing file forever. This also
	// handles a file that reappears while a remote delete is still queued: the
	// old mapping remains authoritative until an actual content change is
	// observed.
	for _, relPath := range result.UnchangedFiles {
		existing, err := s.fileRepo.GetByPath(ctx, folderID, relPath)
		if err != nil {
			continue
		}
		if existing.FileStatus != types.FileStatusLocalMissingRemoteKept && existing.FileStatus != types.FileStatusNeedsDelete {
			continue
		}
		if existing.RemoteDocID == "" {
			if err := s.fileRepo.UpdateStatus(ctx, existing.FileID, types.FileStatusPending); err != nil {
				return nil, fmt.Errorf("failed to restore reappeared file: %w", err)
			}
			continue
		}
		if err := s.fileRepo.UpdateStatus(ctx, existing.FileID, types.FileStatusSynced); err != nil {
			return nil, fmt.Errorf("failed to restore remote mapping: %w", err)
		}
	}

	for oldRelPath, newRelPath := range result.RenamedFiles {
		existing, err := s.fileRepo.GetByPath(ctx, folderID, oldRelPath)
		if err != nil {
			continue
		}
		// Keep the original FileID. It is the repository row identity; changing
		// it before Update would make the UPDATE target the new (non-existent)
		// ID and silently lose the rename.
		existing.RelativePath = newRelPath
		if existing.RemoteDocID != "" {
			existing.FileStatus = types.FileStatusStaleRemoteExists
		} else {
			existing.FileStatus = types.FileStatusPending
		}
		if err := s.fileRepo.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("failed to update renamed file: %w", err)
		}
	}

	for _, relPath := range result.DeletedFiles {
		existing, err := s.fileRepo.GetByPath(ctx, folderID, relPath)
		if err != nil {
			continue
		}
		if folder.SyncDeleteLocalRemoved {
			if existing.RemoteDocID != "" {
				if err := s.fileRepo.UpdateStatus(ctx, existing.FileID, types.FileStatusNeedsDelete); err != nil {
					return nil, fmt.Errorf("failed to update file status: %w", err)
				}
			} else if err := s.fileRepo.Delete(ctx, existing.FileID); err != nil {
				return nil, fmt.Errorf("failed to delete file record: %w", err)
			}
		} else if existing.RemoteDocID != "" {
			if err := s.fileRepo.UpdateStatus(ctx, existing.FileID, types.FileStatusLocalMissingRemoteKept); err != nil {
				return nil, fmt.Errorf("failed to update file status: %w", err)
			}
		} else if err := s.fileRepo.Delete(ctx, existing.FileID); err != nil {
			return nil, fmt.Errorf("failed to delete file record: %w", err)
		}
	}

	return result, nil
}

// DetectChanges scans the folder and persists the resulting diff.
func (s *FileScanner) DetectChanges(ctx context.Context, folderID string) (*types.ChangeType, error) {
	result, err := s.DetectChangesWithResult(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if len(result.NewFiles) > 0 || len(result.UpdatedFiles) > 0 || len(result.DeletedFiles) > 0 || len(result.RenamedFiles) > 0 {
		changeType := types.ChangeTypeUpdate
		return &changeType, nil
	}
	changeType := types.ChangeTypeNoChange
	return &changeType, nil
}

// generateFileID 生成文件 ID。
func generateFileID(folderID, relPath string) string {
	return fmt.Sprintf("%s:%s", folderID, normalizeRelativePath(relPath))
}
