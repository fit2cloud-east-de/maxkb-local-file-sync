package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// MinerUArtifactCleanupResult is a user-safe summary of a local cleanup.
type MinerUArtifactCleanupResult struct {
	Status       string `json:"status"`
	DeletedCount int    `json:"deletedCount"`
	SkippedCount int    `json:"skippedCount"`
	Error        string `json:"error,omitempty"`
	At           string `json:"at"`
}

type artifactRunInfo struct {
	ID        string
	FolderID  string
	TaskName  string
	Status    string
	CreatedAt time.Time
}

// MinerUArtifactCleanupService only removes artifacts previously associated
// with a durable sync run. It never deletes arbitrary directories below the
// configured root and never calls a remote adapter.
type MinerUArtifactCleanupService struct {
	db           *db.DB
	settingsRepo repository.SystemSettingsRepository
	logger       *logger.Logger
	cron         *cron.Cron
	mu           sync.Mutex
	running      bool
	started      bool
}

func NewMinerUArtifactCleanupService(database *db.DB, settingsRepo repository.SystemSettingsRepository, log *logger.Logger) *MinerUArtifactCleanupService {
	return &MinerUArtifactCleanupService{db: database, settingsRepo: settingsRepo, logger: log}
}

func (s *MinerUArtifactCleanupService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()
	return s.Reload(ctx)
}

func (s *MinerUArtifactCleanupService) Stop(ctx context.Context) error {
	s.mu.Lock()
	c := s.cron
	s.cron = nil
	s.started = false
	s.mu.Unlock()
	if c != nil {
		<-c.Stop().Done()
	}
	return nil
}

func (s *MinerUArtifactCleanupService) Reload(ctx context.Context) error {
	settings, err := s.settingsRepo.GetMinerUArtifactSettings(ctx)
	if err != nil {
		return err
	}
	var next *cron.Cron
	if (settings.CleanupPolicy == repository.MinerUCleanupPolicyAfterDuration ||
		settings.CleanupPolicy == repository.MinerUCleanupPolicyAfterDays ||
		settings.CleanupPolicy == repository.MinerUCleanupPolicyKeepBatches) && strings.TrimSpace(settings.CleanupCron) != "" {
		next = cron.New()
		if _, err := next.AddFunc(settings.CleanupCron, func() {
			if _, runErr := s.RunNow(context.Background()); runErr != nil && s.logger != nil {
				s.logger.Error("MinerU artifact cleanup failed: %v", runErr)
			}
		}); err != nil {
			return fmt.Errorf("invalid MinerU cleanup cron: %w", err)
		}
	}
	s.mu.Lock()
	old := s.cron
	s.cron = next
	started := s.started
	s.mu.Unlock()
	if old != nil {
		<-old.Stop().Done()
	}
	if next != nil && started {
		next.Start()
	}
	return nil
}

func (s *MinerUArtifactCleanupService) RunNow(ctx context.Context) (MinerUArtifactCleanupResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return MinerUArtifactCleanupResult{Status: "SKIPPED_ALREADY_RUNNING", At: time.Now().UTC().Format(time.RFC3339Nano)}, nil
	}
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()

	settings, err := s.settingsRepo.GetMinerUArtifactSettings(ctx)
	if err != nil {
		return MinerUArtifactCleanupResult{}, err
	}
	result := MinerUArtifactCleanupResult{Status: "SKIPPED", At: time.Now().UTC().Format(time.RFC3339Nano)}
	if settings.ResultSaveDir == "" {
		_ = s.record(ctx, result, nil)
		return result, nil
	}
	root, err := filepath.Abs(filepath.Clean(settings.ResultSaveDir))
	if err != nil || !isCrossPlatformAbsolutePath(settings.ResultSaveDir) {
		return s.finish(ctx, result, fmt.Errorf("MinerU result root must be an absolute path"))
	}
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			_ = s.record(ctx, result, nil)
			return result, nil
		}
		return s.finish(ctx, result, fmt.Errorf("stat MinerU result root: %w", err))
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return s.finish(ctx, result, fmt.Errorf("MinerU result root must be a real directory"))
	}

	runs, err := s.loadRuns(ctx)
	if err != nil {
		return s.finish(ctx, result, err)
	}
	candidates, err := s.findCandidates(root, runs)
	if err != nil {
		return s.finish(ctx, result, err)
	}
	keep := s.keepSet(settings, candidates, time.Now())
	for _, c := range candidates {
		if _, ok := keep[c.path]; ok {
			result.SkippedCount++
			continue
		}
		if err := ensureContainedPath(root, c.path); err != nil {
			result.SkippedCount++
			continue
		}
		if err := os.RemoveAll(c.path); err != nil {
			result.SkippedCount++
			if result.Error == "" {
				result.Error = fmt.Sprintf("remove artifact batch: %v", err)
			}
			continue
		}
		result.DeletedCount++
	}
	if result.Error != "" {
		result.Status = "PARTIAL"
	} else {
		result.Status = "SUCCESS"
	}
	if err := s.record(ctx, result, nil); err != nil {
		return result, err
	}
	return result, nil
}

type artifactCandidate struct {
	path    string
	info    artifactRunInfo
	modTime time.Time
}

func (s *MinerUArtifactCleanupService) loadRuns(ctx context.Context) (map[string]artifactRunInfo, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `SELECT r.id, r.folder_id, r.status, r.queued_at, COALESCE(f.name,'') FROM sync_runs r LEFT JOIN sync_folders f ON f.folder_id=r.folder_id`)
	if err != nil {
		return nil, fmt.Errorf("load MinerU artifact runs: %w", err)
	}
	defer rows.Close()
	out := map[string]artifactRunInfo{}
	for rows.Next() {
		var id, folderID, status, queued, name string
		if err := rows.Scan(&id, &folderID, &status, &queued, &name); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, queued)
		out[id] = artifactRunInfo{ID: id, FolderID: folderID, TaskName: name, Status: status, CreatedAt: t}
	}
	return out, rows.Err()
}

func (s *MinerUArtifactCleanupService) findCandidates(root string, runs map[string]artifactRunInfo) ([]artifactCandidate, error) {
	var out []artifactCandidate
	tasks, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if !task.IsDir() || task.Type()&os.ModeSymlink != 0 {
			continue
		}
		batches, err := os.ReadDir(filepath.Join(root, task.Name()))
		if err != nil {
			return nil, err
		}
		for _, batch := range batches {
			if !batch.IsDir() || batch.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, ok := runs[batch.Name()]
			if !ok {
				continue
			}
			if safePathComponent(info.TaskName, "task name") != task.Name() {
				continue
			}
			path := filepath.Join(root, task.Name(), batch.Name())
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			out = append(out, artifactCandidate{path: path, info: info, modTime: st.ModTime()})
		}
	}
	return out, nil
}

func (s *MinerUArtifactCleanupService) keepSet(settings repository.MinerUArtifactSettings, candidates []artifactCandidate, now time.Time) map[string]struct{} {
	keep := map[string]struct{}{}
	for _, c := range candidates {
		if protectedRun(c.info.Status) {
			keep[c.path] = struct{}{}
		}
	}
	switch settings.CleanupPolicy {
	case repository.MinerUCleanupPolicyAfterDays, repository.MinerUCleanupPolicyAfterDuration:
		value := settings.CleanupAfterValue
		if value <= 0 {
			value = settings.CleanupAfterDays
		}
		unit := settings.CleanupAfterUnit
		if unit == "hour" {
			// Keep the explicit hour granularity requested by the UI.
			cutoff := now.Add(-time.Duration(value) * time.Hour)
			for _, c := range candidates {
				if c.modTime.After(cutoff) {
					keep[c.path] = struct{}{}
				}
			}
		} else {
			cutoff := now.Add(-time.Duration(value) * 24 * time.Hour)
			for _, c := range candidates {
				if c.modTime.After(cutoff) {
					keep[c.path] = struct{}{}
				}
			}
		}
	case repository.MinerUCleanupPolicyKeepBatches:
		// Retention is scoped per sync task. A busy task must not consume the
		// retention slots of another task that uses the same system artifact root.
		byTask := make(map[string][]artifactCandidate)
		for _, c := range candidates {
			byTask[c.info.FolderID] = append(byTask[c.info.FolderID], c)
		}
		n := settings.CleanupKeepBatches
		if n < 0 {
			n = 0
		}
		for _, taskCandidates := range byTask {
			sort.Slice(taskCandidates, func(i, j int) bool {
				return taskCandidates[i].modTime.After(taskCandidates[j].modTime)
			})
			for i, c := range taskCandidates {
				if i < n {
					keep[c.path] = struct{}{}
				}
			}
		}
	case repository.MinerUCleanupPolicyImmediate, repository.MinerUCleanupPolicyNever:
		// Manual cleanup with either policy removes all non-active batches.
		// The immediate policy normally leaves no published copy because the
		// artifact store skips persistence, but this also cleans legacy copies.
	}
	return keep
}

func protectedRun(status string) bool {
	switch types.RunStatus(status) {
	case types.RunStatusQueued, types.RunStatusRunning, types.RunStatusPauseRequested, types.RunStatusPaused, types.RunStatusStopRequested, types.RunStatusInterrupted:
		return true
	default:
		return false
	}
}

func (s *MinerUArtifactCleanupService) finish(ctx context.Context, result MinerUArtifactCleanupResult, err error) (MinerUArtifactCleanupResult, error) {
	result.Status = "FAILED"
	result.Error = logger.SanitizeError(err)
	_ = s.record(ctx, result, err)
	return result, err
}
func (s *MinerUArtifactCleanupService) record(ctx context.Context, result MinerUArtifactCleanupResult, _ error) error {
	return s.settingsRepo.RecordMinerUArtifactCleanupResult(ctx, repository.MinerUArtifactCleanupResult{Status: result.Status, DeletedCount: result.DeletedCount, Error: result.Error})
}
