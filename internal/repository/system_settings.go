package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/infra/logger"
)

var ErrSystemSettingsNotFound = errors.New("system settings not found")

const (
	// MinerUCleanupPolicyImmediate means the downloaded MinerU ZIP is used for
	// the current sync only and is not retained in the configured artifact root.
	MinerUCleanupPolicyImmediate     = "immediate"
	MinerUCleanupPolicyNever         = "never"
	MinerUCleanupPolicyAfterDuration = "after_duration"
	// MinerUCleanupPolicyAfterDays is kept as a wire/database compatibility
	// alias for installations created before hour/day retention was added.
	MinerUCleanupPolicyAfterDays   = "after_days"
	MinerUCleanupPolicyKeepBatches = "keep_batches"
)

// MinerUArtifactSettings contains only non-secret, system-wide MinerU result
// retention settings. Credentials and service connection details are managed
// separately and are never included in this model.
type MinerUArtifactSettings struct {
	// SaveFullResult and CleanupTemporaryResults are retained for database and
	// API compatibility only. ZIP retention is now determined by CleanupPolicy
	// and private temporary files are always removed after processing.
	SaveFullResult          bool
	ResultSaveDir           string
	CleanupTemporaryResults bool

	CleanupPolicy     string
	CleanupAfterValue int
	CleanupAfterUnit  string
	// CleanupAfterDays is a deprecated compatibility projection for older
	// clients. New code must use CleanupAfterValue/CleanupAfterUnit.
	CleanupAfterDays   int
	CleanupKeepBatches int
	CleanupCron        string

	LastCleanupAt           *time.Time
	LastCleanupStatus       string
	LastCleanupDeletedCount int
	LastCleanupError        string
}

// MinerUArtifactCleanupResult is the local summary written after a cleanup
// attempt. It contains counts and sanitized diagnostics, never file content or
// credentials.
type MinerUArtifactCleanupResult struct {
	At           *time.Time
	Status       string
	DeletedCount int
	Error        string
}

// SystemSettingsRepository persists the singleton system_settings row. The
// interface deliberately exposes only the MinerU artifact subset needed by
// this feature so callers cannot accidentally persist credentials here.
type SystemSettingsRepository interface {
	GetMinerUArtifactSettings(ctx context.Context) (MinerUArtifactSettings, error)
	UpdateMinerUArtifactSettings(ctx context.Context, settings MinerUArtifactSettings) error
	RecordMinerUArtifactCleanupResult(ctx context.Context, result MinerUArtifactCleanupResult) error
}

type systemSettingsRepo struct {
	db *db.DB
}

func NewSystemSettingsRepository(database *db.DB) SystemSettingsRepository {
	return &systemSettingsRepo{db: database}
}

func (r *systemSettingsRepo) GetMinerUArtifactSettings(ctx context.Context) (MinerUArtifactSettings, error) {
	if r == nil || r.db == nil {
		return MinerUArtifactSettings{}, fmt.Errorf("system settings repository is not initialized")
	}

	var saveFullResult, cleanupTemporaryResults int
	var resultSaveDir, cleanupPolicy, cleanupCron, cleanupAfterUnit, lastCleanupStatus, lastCleanupError string
	var cleanupAfterValue, cleanupAfterDays, cleanupKeepBatches, lastCleanupDeletedCount int
	var lastCleanupAt sql.NullString
	err := r.db.Conn().QueryRowContext(ctx, `
		SELECT mineru_save_full_result, mineru_result_save_dir, mineru_cleanup_temp_results,
		       mineru_cleanup_policy, mineru_cleanup_after_value, mineru_cleanup_after_unit,
		       mineru_cleanup_after_days, mineru_cleanup_keep_batches,
		       mineru_cleanup_cron, mineru_last_cleanup_at, mineru_last_cleanup_status,
		       mineru_last_cleanup_deleted_count, mineru_last_cleanup_error
		FROM system_settings
		WHERE id = 1
	`).Scan(
		&saveFullResult, &resultSaveDir, &cleanupTemporaryResults,
		&cleanupPolicy, &cleanupAfterValue, &cleanupAfterUnit, &cleanupAfterDays, &cleanupKeepBatches, &cleanupCron,
		&lastCleanupAt, &lastCleanupStatus, &lastCleanupDeletedCount, &lastCleanupError,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MinerUArtifactSettings{}, ErrSystemSettingsNotFound
		}
		return MinerUArtifactSettings{}, fmt.Errorf("load MinerU artifact settings: %w", err)
	}

	if cleanupAfterValue <= 0 && cleanupAfterDays > 0 {
		cleanupAfterValue = cleanupAfterDays
	}
	if cleanupAfterUnit == "" {
		cleanupAfterUnit = "day"
	}
	return MinerUArtifactSettings{
		SaveFullResult:          saveFullResult == 1,
		ResultSaveDir:           resultSaveDir,
		CleanupTemporaryResults: cleanupTemporaryResults == 1,
		CleanupPolicy:           cleanupPolicy,
		CleanupAfterValue:       cleanupAfterValue,
		CleanupAfterUnit:        cleanupAfterUnit,
		CleanupAfterDays:        cleanupAfterDays,
		CleanupKeepBatches:      cleanupKeepBatches,
		CleanupCron:             cleanupCron,
		LastCleanupAt:           parseSettingsTime(lastCleanupAt),
		LastCleanupStatus:       lastCleanupStatus,
		LastCleanupDeletedCount: lastCleanupDeletedCount,
		LastCleanupError:        lastCleanupError,
	}, nil
}

func (r *systemSettingsRepo) UpdateMinerUArtifactSettings(ctx context.Context, settings MinerUArtifactSettings) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("system settings repository is not initialized")
	}

	result, err := r.db.Conn().ExecContext(ctx, `
		UPDATE system_settings
		SET mineru_save_full_result = 1,
		    mineru_result_save_dir = ?,
		    mineru_cleanup_temp_results = 1,
		    mineru_cleanup_policy = ?,
		    mineru_cleanup_after_value = ?,
		    mineru_cleanup_after_unit = ?,
		    mineru_cleanup_after_days = ?,
		    mineru_cleanup_keep_batches = ?,
		    mineru_cleanup_cron = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, settings.ResultSaveDir, settings.CleanupPolicy,
		settings.CleanupAfterValue, settings.CleanupAfterUnit, settings.CleanupAfterDays,
		settings.CleanupKeepBatches, settings.CleanupCron)
	if err != nil {
		return fmt.Errorf("save MinerU artifact settings: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("verify MinerU artifact settings update: %w", err)
	} else if affected != 1 {
		return ErrSystemSettingsNotFound
	}
	return nil
}

func (r *systemSettingsRepo) RecordMinerUArtifactCleanupResult(ctx context.Context, result MinerUArtifactCleanupResult) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("system settings repository is not initialized")
	}
	at := result.At
	if at == nil {
		now := time.Now().UTC()
		at = &now
	}
	resultAt := at.UTC().Format(time.RFC3339Nano)

	updated, err := r.db.Conn().ExecContext(ctx, `
		UPDATE system_settings
		SET mineru_last_cleanup_at = ?,
		    mineru_last_cleanup_status = ?,
		    mineru_last_cleanup_deleted_count = ?,
		    mineru_last_cleanup_error = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, resultAt, result.Status, result.DeletedCount, logger.SanitizeDiagnostic(result.Error))
	if err != nil {
		return fmt.Errorf("save MinerU cleanup result: %w", err)
	}
	if affected, err := updated.RowsAffected(); err != nil {
		return fmt.Errorf("verify MinerU cleanup result update: %w", err)
	} else if affected != 1 {
		return ErrSystemSettingsNotFound
	}
	return nil
}

func parseSettingsTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}
