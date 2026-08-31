package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"maxkb-local-file-sync/internal/infra/file"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

type scannerFolderRepo struct {
	folder *repository.SyncFolder
	err    error
}

func (r *scannerFolderRepo) Create(context.Context, *repository.SyncFolder) error { return nil }
func (r *scannerFolderRepo) Update(context.Context, *repository.SyncFolder) error { return nil }
func (r *scannerFolderRepo) Delete(context.Context, string) error                 { return nil }
func (r *scannerFolderRepo) GetByID(context.Context, string) (*repository.SyncFolder, error) {
	return r.folder, r.err
}
func (r *scannerFolderRepo) GetByLocalPath(context.Context, string) (*repository.SyncFolder, error) {
	return r.folder, r.err
}
func (r *scannerFolderRepo) List(context.Context) ([]*repository.SyncFolder, error) {
	return []*repository.SyncFolder{r.folder}, r.err
}
func (r *scannerFolderRepo) ListByKBId(context.Context, string) ([]*repository.SyncFolder, error) {
	return []*repository.SyncFolder{r.folder}, r.err
}
func (r *scannerFolderRepo) ListCronEnabled(context.Context) ([]*repository.SyncFolder, error) {
	return []*repository.SyncFolder{r.folder}, r.err
}
func (r *scannerFolderRepo) SetEnabled(context.Context, string, bool) error { return nil }

type scannerFileRepo struct {
	files       map[string]*repository.SyncFile
	createCalls []*repository.SyncFile
	updates     []*repository.SyncFile
	statuses    map[string]types.FileStatus
	deleted     []string
}

func newScannerFileRepo(files ...*repository.SyncFile) *scannerFileRepo {
	r := &scannerFileRepo{files: make(map[string]*repository.SyncFile), statuses: make(map[string]types.FileStatus)}
	for _, f := range files {
		r.files[f.RelativePath] = cloneSyncFile(f)
	}
	return r
}
func cloneSyncFile(f *repository.SyncFile) *repository.SyncFile {
	if f == nil {
		return nil
	}
	copy := *f
	return &copy
}
func (r *scannerFileRepo) Create(_ context.Context, f *repository.SyncFile) error {
	copy := cloneSyncFile(f)
	r.createCalls = append(r.createCalls, copy)
	r.files[copy.RelativePath] = copy
	return nil
}
func (r *scannerFileRepo) BatchCreate(ctx context.Context, files []*repository.SyncFile) error {
	for _, f := range files {
		if err := r.Create(ctx, f); err != nil {
			return err
		}
	}
	return nil
}
func (r *scannerFileRepo) Update(_ context.Context, f *repository.SyncFile) error {
	copy := cloneSyncFile(f)
	r.updates = append(r.updates, copy)
	for path, existing := range r.files {
		if existing.FileID == copy.FileID {
			delete(r.files, path)
		}
	}
	r.files[copy.RelativePath] = copy
	return nil
}
func (r *scannerFileRepo) UpdateStatus(_ context.Context, fileID string, status types.FileStatus) error {
	r.statuses[fileID] = status
	for _, f := range r.files {
		if f.FileID == fileID {
			f.FileStatus = status
		}
	}
	return nil
}
func (r *scannerFileRepo) UpdateMD5(context.Context, string, string, string) error { return nil }
func (r *scannerFileRepo) UpdateRemoteDocID(context.Context, string, string) error { return nil }
func (r *scannerFileRepo) Delete(_ context.Context, fileID string) error {
	r.deleted = append(r.deleted, fileID)
	for path, f := range r.files {
		if f.FileID == fileID {
			delete(r.files, path)
		}
	}
	return nil
}
func (r *scannerFileRepo) DeleteByFolder(context.Context, string) error { return nil }
func (r *scannerFileRepo) GetByID(_ context.Context, fileID string) (*repository.SyncFile, error) {
	for _, f := range r.files {
		if f.FileID == fileID {
			return cloneSyncFile(f), nil
		}
	}
	return nil, errors.New("not found")
}
func (r *scannerFileRepo) GetByPath(_ context.Context, _, path string) (*repository.SyncFile, error) {
	f, ok := r.files[normalizeRelativePath(path)]
	if !ok {
		return nil, errors.New("not found")
	}
	return cloneSyncFile(f), nil
}
func (r *scannerFileRepo) ListByFolder(context.Context, string) ([]*repository.SyncFile, error) {
	result := make([]*repository.SyncFile, 0, len(r.files))
	for _, f := range r.files {
		result = append(result, cloneSyncFile(f))
	}
	return result, nil
}
func (r *scannerFileRepo) ListByStatus(_ context.Context, _ string, status types.FileStatus) ([]*repository.SyncFile, error) {
	result := make([]*repository.SyncFile, 0)
	for _, f := range r.files {
		if f.FileStatus == status {
			result = append(result, cloneSyncFile(f))
		}
	}
	return result, nil
}
func (r *scannerFileRepo) ListPendingChanges(context.Context, string) ([]*repository.SyncFile, error) {
	return nil, nil
}
func (r *scannerFileRepo) CountByFolder(context.Context, string) (int, error) {
	return len(r.files), nil
}
func (r *scannerFileRepo) CountByStatus(_ context.Context, _ string, status types.FileStatus) (int, error) {
	count := 0
	for _, f := range r.files {
		if f.FileStatus == status {
			count++
		}
	}
	return count, nil
}

func writeScannerFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newTestScanner(t *testing.T, root string, folder *repository.SyncFolder, files ...*repository.SyncFile) (*FileScanner, *scannerFileRepo) {
	t.Helper()
	fileRepo := newScannerFileRepo(files...)
	folderRepo := &scannerFolderRepo{folder: folder}
	return NewFileScanner(fileRepo, folderRepo), fileRepo
}

func TestNormalizeExtensionsAndMatchExtension(t *testing.T) {
	got := NormalizeExtensions(" PDF, .pdf, docx, ,DOCX ")
	want := []string{".pdf", ".docx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeExtensions() = %#v, want %#v", got, want)
	}
	if !MatchExtension("report.PDF", got) || MatchExtension("report.txt", got) {
		t.Fatal("extension matching is not case-insensitive or is matching an unrelated extension")
	}
	if !MatchExtension("README", nil) {
		t.Fatal("empty extension list should match all files")
	}
}

func TestPathMatcherRegexAndGlobCompatibility(t *testing.T) {
	matcher, err := newPathMatcher(`^docs/.+\.pdf$`, `^docs/private/`)
	if err != nil {
		t.Fatal(err)
	}
	if !matcher.Match("docs/readme.pdf") || matcher.Match("docs/private/readme.pdf") || matcher.Match("docs/readme.txt") {
		t.Fatal("regex include/exclude matching is incorrect")
	}

	glob, err := newPathMatcher("docs/**/*.txt", "docs/tmp/*")
	if err != nil {
		t.Fatal(err)
	}
	if !glob.Match("docs/a/b/readme.txt") || glob.Match("docs/tmp/readme.txt") {
		t.Fatal("legacy glob compatibility is incorrect")
	}
}

func TestPathMatcherRejectsInvalidRegexAndGlob(t *testing.T) {
	if _, err := newPathMatcher("[invalid", ""); err == nil {
		t.Fatal("expected malformed pattern to be rejected")
	}
	if _, err := newPathMatcher("", "["); err == nil {
		t.Fatal("expected malformed exclude pattern to be rejected")
	}
}

func TestScanFolderFiltersSymlinksAndSortsResults(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "keep/a.txt", "a")
	writeScannerFile(t, root, "keep/b.txt", "b")
	writeScannerFile(t, root, "ignored/.hidden.txt", "hidden")
	writeScannerFile(t, root, "ignored/Thumbs.db", "system")
	writeScannerFile(t, root, "ignored/partial.part", "partial")
	if err := os.MkdirAll(filepath.Join(root, "link-target"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScannerFile(t, root, "link-target/target.txt", "target")
	if err := os.Symlink(filepath.Join(root, "link-target"), filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "keep/a.txt"), filepath.Join(root, "linked-file.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root, IncludePatterns: `^keep/`, ExcludePatterns: `b\.txt$`}
	scanner, _ := newTestScanner(t, root, folder)
	result, err := scanner.ScanFolder(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.NewFiles, []string{"keep/a.txt"}) {
		t.Fatalf("new files = %#v", result.NewFiles)
	}
	if !sort.StringsAreSorted(result.NewFiles) || !sort.StringsAreSorted(result.UpdatedFiles) || !sort.StringsAreSorted(result.DeletedFiles) {
		t.Fatal("scan result slices must be deterministic")
	}
}

func TestScanFolderDiffAndUniqueRename(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "same.txt", "same")
	writeScannerFile(t, root, "new.txt", "new")
	writeScannerFile(t, root, "modified.txt", "changed")

	sameMD5 := md5ForTest(t, "same")
	oldMD5 := md5ForTest(t, "old")
	repoFiles := []*repository.SyncFile{
		{FileID: "same-id", RelativePath: "same.txt", LastSuccessMD5: sameMD5, FileStatus: types.FileStatusSynced},
		{FileID: "renamed-id", RelativePath: "old.txt", LastSuccessMD5: sameMD5, FileStatus: types.FileStatusSynced},
		{FileID: "modified-id", RelativePath: "modified.txt", LastSuccessMD5: oldMD5, FileStatus: types.FileStatusSynced},
		{FileID: "deleted-id", RelativePath: "deleted.txt", LastSuccessMD5: oldMD5, FileStatus: types.FileStatusSynced},
	}
	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder, repoFiles...)
	result, err := scanner.ScanFolder(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	// same.txt is already occupied by an existing mapping, so the equal-content
	// new candidate does not exist; old.txt cannot be renamed to same.txt.
	if len(result.RenamedFiles) != 0 {
		t.Fatalf("expected no rename, got %#v", result.RenamedFiles)
	}
	if !reflect.DeepEqual(result.NewFiles, []string{"new.txt"}) {
		t.Fatalf("new files = %#v", result.NewFiles)
	}
	if !reflect.DeepEqual(result.UpdatedFiles, []string{"modified.txt"}) {
		t.Fatalf("updated files = %#v", result.UpdatedFiles)
	}
	if !reflect.DeepEqual(result.DeletedFiles, []string{"deleted.txt", "old.txt"}) {
		t.Fatalf("deleted files = %#v", result.DeletedFiles)
	}
}

func TestScanFolderIdentifiesUniqueRename(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "new-name.txt", "same")
	old := &repository.SyncFile{FileID: "old-id", RelativePath: "old-name.txt", LastSuccessMD5: md5ForTest(t, "same")}
	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder, old)
	result, err := scanner.ScanFolder(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.RenamedFiles, map[string]string{"old-name.txt": "new-name.txt"}) {
		t.Fatalf("renames = %#v", result.RenamedFiles)
	}
	if len(result.NewFiles) != 0 || len(result.DeletedFiles) != 0 {
		t.Fatalf("rename must not also be new/deleted: %#v %#v", result.NewFiles, result.DeletedFiles)
	}
}

func TestScanFolderDoesNotDeleteFilesExcludedByCurrentFilter(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "keep.txt", "keep")
	writeScannerFile(t, root, "excluded.txt", "excluded")
	existing := &repository.SyncFile{FileID: "excluded-id", RelativePath: "excluded.txt", LastSuccessMD5: md5ForTest(t, "excluded")}
	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root, IncludePatterns: `^keep\.txt$`}
	scanner, _ := newTestScanner(t, root, folder, existing)
	result, err := scanner.ScanFolder(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DeletedFiles) != 0 {
		t.Fatalf("excluded files must not be reported as deleted: %#v", result.DeletedFiles)
	}
}

func TestDetectChangesKeepsRenameFileID(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "new.txt", "same")
	old := &repository.SyncFile{FileID: "stable-id", RelativePath: "old.txt", LastSuccessMD5: md5ForTest(t, "same"), RemoteDocID: "doc-1", FileStatus: types.FileStatusSynced}
	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root}
	scanner, repo := newTestScanner(t, root, folder, old)
	if _, err := scanner.DetectChanges(context.Background(), folder.FolderID); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 1 || repo.updates[0].FileID != "stable-id" || repo.updates[0].RelativePath != "new.txt" {
		t.Fatalf("rename update must preserve row id: %#v", repo.updates)
	}
	if repo.updates[0].FileStatus != types.FileStatusStaleRemoteExists {
		t.Fatalf("remote rename should require remote reconciliation, got %s", repo.updates[0].FileStatus)
	}
}

func TestScanFolderHonorsContextCancellation(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "a.txt", "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	folder := &repository.SyncFolder{FolderID: "folder-1", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder)
	if _, err := scanner.ScanFolder(ctx, folder.FolderID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func md5ForTest(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "file")
	writeScannerFile(t, root, "file", content)
	hash, err := file.CalculateMD5(path)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestDetectChangesRestoresMissingRemoteMappingWhenFileReappearsUnchanged(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "doc.md", "same")
	folder := &repository.SyncFolder{FolderID: "folder-restore", LocalPath: root}
	old := &repository.SyncFile{FileID: "file-restore", FolderID: folder.FolderID, RelativePath: "doc.md", FileStatus: types.FileStatusLocalMissingRemoteKept, RemoteDocID: "doc-remote", LastSuccessMD5: md5ForTest(t, "same")}
	scanner, repo := newTestScanner(t, root, folder, old)
	change, err := scanner.DetectChanges(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if *change != types.ChangeTypeNoChange {
		t.Fatalf("expected no content change, got %s", *change)
	}
	got, ok := repo.files["doc.md"]
	if !ok || got.FileStatus != types.FileStatusSynced {
		t.Fatalf("reappeared mapping was not restored: %#v", got)
	}
}

func TestScanFolderDoesNotInferRenameForDuplicateMD5Candidates(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "new-a.txt", "same")
	writeScannerFile(t, root, "new-b.txt", "same")
	old := &repository.SyncFile{FileID: "old-duplicate", RelativePath: "old.txt", LastSuccessMD5: md5ForTest(t, "same"), FileStatus: types.FileStatusSynced}
	folder := &repository.SyncFolder{FolderID: "folder-duplicate", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder, old)
	result, err := scanner.ScanFolder(context.Background(), folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RenamedFiles) != 0 {
		t.Fatalf("duplicate MD5 candidates must not be treated as rename: %#v", result.RenamedFiles)
	}
	if !reflect.DeepEqual(result.NewFiles, []string{"new-a.txt", "new-b.txt"}) || !reflect.DeepEqual(result.DeletedFiles, []string{"old.txt"}) {
		t.Fatalf("duplicate candidates diff = new=%#v deleted=%#v", result.NewFiles, result.DeletedFiles)
	}
}

func TestPreviewMatchUsesProductionRegexAndReportsExclusionReasons(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "docs/readme.md", "readme")
	writeScannerFile(t, root, "docs/secret.md", "secret")
	writeScannerFile(t, root, "notes.txt", "notes")
	folder := &repository.SyncFolder{FolderID: "preview", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder)
	preview, err := scanner.PreviewMatch(context.Background(), root, `^docs/.*\.md$`, `secret`, true, []string{".md"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TotalFiles != 3 {
		t.Fatalf("total files = %d", preview.TotalFiles)
	}
	if !reflect.DeepEqual(preview.MatchedFiles, []string{"docs/readme.md"}) {
		t.Fatalf("matched files = %#v", preview.MatchedFiles)
	}
	if !reflect.DeepEqual(preview.MinerUFiles, []string{"docs/readme.md"}) {
		t.Fatalf("MinerU files = %#v", preview.MinerUFiles)
	}
	if preview.ExclusionReasons["docs/secret.md"] != "excluded_by_exclude_pattern" || preview.ExclusionReasons["notes.txt"] != "not_matched_by_include_pattern" {
		t.Fatalf("exclusion reasons = %#v", preview.ExclusionReasons)
	}
}

func TestPreviewMatchExcludesUnsupportedDirectUploadFiles(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "readme.md", "markdown")
	writeScannerFile(t, root, "diagram.png", "image")
	writeScannerFile(t, root, "presentation.pptx", "slides")
	folder := &repository.SyncFolder{FolderID: "preview-supported-types", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder)

	preview, err := scanner.PreviewMatch(context.Background(), root, "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.MatchedFiles, []string{"readme.md"}) {
		t.Fatalf("matched files = %#v", preview.MatchedFiles)
	}
	if preview.ExclusionReasons["diagram.png"] != "unsupported_by_maxkb" || preview.ExclusionReasons["presentation.pptx"] != "unsupported_by_maxkb" {
		t.Fatalf("unsupported file reasons = %#v", preview.ExclusionReasons)
	}

	preview, err = scanner.PreviewMatch(context.Background(), root, "", "", true, []string{".pptx"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.MinerUFiles, []string{"presentation.pptx"}) || !reflect.DeepEqual(preview.RegularFiles, []string{"readme.md"}) {
		t.Fatalf("MinerU/direct classification = mineru=%#v regular=%#v", preview.MinerUFiles, preview.RegularFiles)
	}
}

func TestPreviewMatchMinerUEmptyExtensionsConvertsUnsupportedOnly(t *testing.T) {
	root := t.TempDir()
	writeScannerFile(t, root, "readme.md", "markdown")
	writeScannerFile(t, root, "presentation.pptx", "slides")
	folder := &repository.SyncFolder{FolderID: "preview-mineru-default", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder)

	preview, err := scanner.PreviewMatch(context.Background(), root, "", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.MatchedFiles, []string{"presentation.pptx", "readme.md"}) {
		t.Fatalf("matched files = %#v", preview.MatchedFiles)
	}
	if !reflect.DeepEqual(preview.MinerUFiles, []string{"presentation.pptx"}) {
		t.Fatalf("MinerU files = %#v", preview.MinerUFiles)
	}
	if !reflect.DeepEqual(preview.RegularFiles, []string{"readme.md"}) {
		t.Fatalf("regular files = %#v", preview.RegularFiles)
	}
}

func TestPreviewMatchRejectsInvalidRegex(t *testing.T) {
	root := t.TempDir()
	folder := &repository.SyncFolder{FolderID: "preview-invalid", LocalPath: root}
	scanner, _ := newTestScanner(t, root, folder)
	if _, err := scanner.PreviewMatch(context.Background(), root, "[", "", false, nil); err == nil {
		t.Fatal("expected invalid regex error")
	}
}
