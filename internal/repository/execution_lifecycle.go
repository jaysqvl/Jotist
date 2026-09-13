package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"scriberr/internal/models"
)

var ErrExecutionOwnership = errors.New("execution ownership or state changed")
var ErrExecutionDeadline = errors.New("execution deadline expired; start a new run to reuse compatible checkpoints")

type ExecutionLifecycleRepository interface {
	ReconcileLegacyOrphans(context.Context) (int64, error)
	Begin(context.Context, *models.TranscriptionJobExecution) error
	ClaimResume(context.Context, string, string) (*models.TranscriptionJobExecution, error)
	RequestResume(context.Context, string, string, int64, models.WhisperXParams) (*models.TranscriptionJobExecution, error)
	Cancel(context.Context, string, string, string) error
	ExpirePending(context.Context, string, string, int64) error
	Finish(context.Context, string, int64, models.JobStatus, *string, string) error
	FinishWithDetails(context.Context, string, int64, models.JobStatus, *string, string, *models.TranscriptionJobExecution) error
	TryClaimCompletionCallback(context.Context, string, int64) (bool, error)
}

type executionLifecycleRepository struct{ db *gorm.DB }

func NewExecutionLifecycleRepository(db *gorm.DB) ExecutionLifecycleRepository {
	return &executionLifecycleRepository{db: db}
}

func (r *executionLifecycleRepository) Begin(ctx context.Context, e *models.TranscriptionJobExecution) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fence := tx.Model(&models.TranscriptionJob{}).Where("id = ? AND status = ?", e.TranscriptionJobID, models.StatusProcessing).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		var job models.TranscriptionJob
		if err := tx.First(&job, "id = ?", e.TranscriptionJobID).Error; err != nil {
			return err
		}
		if job.Status != models.StatusProcessing {
			return ErrExecutionOwnership
		}
		if e.DeadlineAt != nil && !time.Now().Before(*e.DeadlineAt) {
			return ErrExecutionDeadline
		}
		var competing int64
		if err := tx.Model(&models.TranscriptionJobExecution{}).Where("transcription_job_id = ? AND status IN ?", e.TranscriptionJobID, []models.JobStatus{models.StatusPending, models.StatusProcessing}).Count(&competing).Error; err != nil {
			return err
		}
		if competing > 0 {
			return ErrExecutionOwnership
		}
		e.OwnerGeneration = 1
		e.RecoveryState = "running"
		e.Status = models.StatusProcessing
		parameters := e.ActualParameters.WithoutSecrets()
		e.ActualParameters = parameters
		if err := tx.Create(e).Error; err != nil {
			return err
		}
		// Create applies embedded defaults; restore the exact immutable snapshot,
		// including explicit false, zero and nil values.
		e.ActualParameters = parameters
		if err := tx.Model(e).Select("*").Omit("id", "created_at").Updates(e).Error; err != nil {
			return err
		}
		if e.QueueItemID != nil && *e.QueueItemID != "" {
			result := tx.Model(&models.TranscriptionQueueItem{}).Where("id = ? AND transcription_job_id = ? AND status = ? AND (execution_id IS NULL OR execution_id = ?)", *e.QueueItemID, e.TranscriptionJobID, models.QueueStatusProcessing, e.ID).Update("execution_id", e.ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrExecutionOwnership
			}
		}
		return nil
	})
}

func (r *executionLifecycleRepository) ClaimResume(ctx context.Context, jobID, id string) (*models.TranscriptionJobExecution, error) {
	var execution models.TranscriptionJobExecution
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExecutionRow(tx, jobID, id); err != nil {
			return err
		}
		if err := tx.First(&execution, "id = ? AND transcription_job_id = ?", id, jobID).Error; err != nil {
			return err
		}
		if execution.RecoveryVersion == 0 || execution.CancelledAt != nil || execution.RecoveryState != "pending" || execution.Status != models.StatusPending {
			return ErrExecutionOwnership
		}
		if execution.DeadlineAt != nil && !time.Now().Before(*execution.DeadlineAt) {
			return ErrExecutionDeadline
		}
		result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND recovery_state = ? AND cancelled_at IS NULL", id, execution.OwnerGeneration, "pending").Updates(map[string]any{"status": models.StatusProcessing, "recovery_state": "running", "completed_at": nil, "error_message": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		execution.Status = models.StatusProcessing
		execution.RecoveryState = "running"
		execution.CompletedAt = nil
		execution.ErrorMessage = nil
		return nil
	})
	return &execution, err
}

func (r *executionLifecycleRepository) RequestResume(ctx context.Context, jobID, id string, generation int64, params models.WhisperXParams) (*models.TranscriptionJobExecution, error) {
	var execution models.TranscriptionJobExecution
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExecutionRow(tx, jobID, id); err != nil {
			return err
		}
		if err := tx.First(&execution, "id = ? AND transcription_job_id = ?", id, jobID).Error; err != nil {
			return err
		}
		if execution.RecoveryVersion == 0 || execution.CancelledAt != nil || execution.OwnerGeneration != generation {
			return ErrExecutionOwnership
		}
		if execution.RecoveryState != "failed" && execution.RecoveryState != "interrupted" && execution.RecoveryState != "blocked" {
			return ErrExecutionOwnership
		}
		if execution.DeadlineAt != nil && !time.Now().Before(*execution.DeadlineAt) {
			return ErrExecutionDeadline
		}
		var job models.TranscriptionJob
		if err := tx.First(&job, "id = ?", jobID).Error; err != nil {
			return err
		}
		var active int64
		query := tx.Model(&models.TranscriptionQueueItem{}).Where("transcription_job_id = ? AND status IN ?", jobID, []models.TranscriptionQueueStatus{models.QueueStatusPending, models.QueueStatusProcessing})
		if execution.QueueItemID != nil {
			query = query.Where("id <> ?", *execution.QueueItemID)
		}
		if err := query.Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrExecutionOwnership
		}
		if job.Status == models.StatusPending {
			return ErrExecutionOwnership
		}
		var competing int64
		if err := tx.Model(&models.TranscriptionJobExecution{}).Where("transcription_job_id = ? AND id <> ? AND status IN ?", jobID, id, []models.JobStatus{models.StatusPending, models.StatusProcessing}).Count(&competing).Error; err != nil {
			return err
		}
		if competing > 0 {
			return ErrExecutionOwnership
		}
		result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND recovery_state IN ? AND cancelled_at IS NULL", id, generation, []string{"failed", "interrupted", "blocked"}).Updates(map[string]any{"owner_generation": generation + 1, "recovery_state": "pending", "status": models.StatusPending, "completed_at": nil, "error_message": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		if execution.QueueItemID != nil {
			var item models.TranscriptionQueueItem
			if err := tx.First(&item, "id = ? AND transcription_job_id = ? AND execution_id = ?", *execution.QueueItemID, jobID, id).Error; err != nil {
				return err
			}
			if item.Status != models.QueueStatusFailed && item.Status != models.QueueStatusProcessing {
				return ErrExecutionOwnership
			}
			item.Status = models.QueueStatusPending
			item.CompletedAt = nil
			item.ErrorMessage = nil
			item.Parameters = params
			if err := tx.Save(&item).Error; err != nil {
				return err
			}
		}
		job.Parameters = params
		job.Status = models.StatusPending
		job.ErrorMessage = nil
		if err := tx.Save(&job).Error; err != nil {
			return err
		}
		execution.OwnerGeneration++
		execution.RecoveryState = "pending"
		execution.Status = models.StatusPending
		execution.CompletedAt = nil
		execution.ErrorMessage = nil
		return nil
	})
	return &execution, err
}

func (r *executionLifecycleRepository) Cancel(ctx context.Context, jobID, id, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExecutionRow(tx, jobID, id); err != nil {
			return err
		}
		var e models.TranscriptionJobExecution
		if err := tx.First(&e, "id = ? AND transcription_job_id = ?", id, jobID).Error; err != nil {
			return err
		}
		if e.CancelledAt != nil {
			return nil
		}
		if e.Status == models.StatusCompleted {
			return ErrExecutionOwnership
		}
		now := time.Now()
		result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND cancelled_at IS NULL", id, e.OwnerGeneration).Updates(map[string]any{"cancelled_at": now, "owner_generation": e.OwnerGeneration + 1, "recovery_state": "cancelled", "status": models.StatusFailed, "error_message": reason, "completed_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		result = tx.Model(&models.TranscriptionJob{}).Where("id = ?", jobID).Updates(map[string]any{"status": models.StatusFailed, "error_message": reason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		return nil
	})
}

// ExpirePending terminalizes an admitted resume whose original deadline passed
// before a worker could claim it. Call with a short cleanup context, not the
// expired processing context. No stage was started under this owner generation.
func (r *executionLifecycleRepository) ExpirePending(ctx context.Context, jobID, id string, generation int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExecutionRow(tx, jobID, id); err != nil {
			return err
		}
		var e models.TranscriptionJobExecution
		if err := tx.First(&e, "id = ? AND transcription_job_id = ?", id, jobID).Error; err != nil {
			return err
		}
		if e.RecoveryVersion == 0 || e.OwnerGeneration != generation || e.CancelledAt != nil {
			return ErrExecutionOwnership
		}
		if e.Status == models.StatusFailed && e.RecoveryState == "failed" && e.ErrorMessage != nil && *e.ErrorMessage == ErrExecutionDeadline.Error() {
			return nil
		}
		if e.Status != models.StatusPending || e.RecoveryState != "pending" || e.DeadlineAt == nil || time.Now().Before(*e.DeadlineAt) {
			return ErrExecutionOwnership
		}
		now, reason := time.Now(), ErrExecutionDeadline.Error()
		result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND status = ? AND recovery_state = ? AND cancelled_at IS NULL", id, generation, models.StatusPending, "pending").Updates(map[string]any{"status": models.StatusFailed, "recovery_state": "failed", "completed_at": now, "processing_duration": now.Sub(e.StartedAt).Milliseconds(), "error_message": reason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		result = tx.Model(&models.TranscriptionJob{}).Where("id = ?", jobID).Updates(map[string]any{"status": models.StatusFailed, "error_message": reason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		if e.QueueItemID != nil {
			result = tx.Model(&models.TranscriptionQueueItem{}).Where("id = ? AND transcription_job_id = ? AND execution_id = ? AND status IN ?", *e.QueueItemID, jobID, id, []models.TranscriptionQueueStatus{models.QueueStatusPending, models.QueueStatusProcessing}).Updates(map[string]any{"status": models.QueueStatusFailed, "completed_at": now, "position": 0, "error_message": reason, "parameters_json": gorm.Expr("json_remove(parameters_json, '$.hf_token', '$.api_key')")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrExecutionOwnership
			}
		}
		return nil
	})
}

func (r *executionLifecycleRepository) Finish(ctx context.Context, id string, generation int64, status models.JobStatus, transcript *string, message string) error {
	return r.FinishWithDetails(ctx, id, generation, status, transcript, message, nil)
}

func (r *executionLifecycleRepository) FinishWithDetails(ctx context.Context, id string, generation int64, status models.JobStatus, transcript *string, message string, details *models.TranscriptionJobExecution) error {
	if status != models.StatusCompleted && status != models.StatusFailed {
		return ErrExecutionOwnership
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fence := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ?", id).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		var e models.TranscriptionJobExecution
		if err := tx.First(&e, "id = ?", id).Error; err != nil {
			return err
		}
		if e.OwnerGeneration != generation || e.CancelledAt != nil {
			return ErrExecutionOwnership
		}
		if e.Status == status && (e.RecoveryState == "completed" || e.RecoveryState == "failed") {
			return nil
		}
		if e.Status != models.StatusProcessing || e.RecoveryState != "running" {
			return ErrExecutionOwnership
		}
		if status == models.StatusCompleted && e.DeadlineAt != nil && !time.Now().Before(*e.DeadlineAt) {
			return ErrExecutionDeadline
		}
		now := time.Now()
		state := "failed"
		if status == models.StatusCompleted {
			state = "completed"
		}
		updates := map[string]any{"status": status, "recovery_state": state, "completed_at": now, "processing_duration": now.Sub(e.StartedAt).Milliseconds(), "error_message": nil}
		if message != "" {
			updates["error_message"] = message
		}
		if details != nil {
			updates["multi_track_timings"] = details.MultiTrackTimings
			updates["merge_start_time"] = details.MergeStartTime
			updates["merge_end_time"] = details.MergeEndTime
			updates["merge_duration"] = details.MergeDuration
		}
		if status == models.StatusCompleted {
			updates["transcript"] = transcript
			if details != nil {
				updates["individual_transcripts"] = details.IndividualTranscripts
			}
		}
		result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND status = ? AND recovery_state = ? AND cancelled_at IS NULL", id, generation, models.StatusProcessing, "running").Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		jobUpdates := map[string]any{"status": status, "error_message": updates["error_message"]}
		if status == models.StatusCompleted {
			jobUpdates["transcript"] = transcript
			jobUpdates["summary"] = nil
			if details != nil {
				jobUpdates["individual_transcripts"] = details.IndividualTranscripts
			}
		}
		result = tx.Model(&models.TranscriptionJob{}).Where("id = ?", e.TranscriptionJobID).Updates(jobUpdates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrExecutionOwnership
		}
		if e.QueueItemID != nil {
			queueStatus := models.QueueStatusFailed
			if status == models.StatusCompleted {
				queueStatus = models.QueueStatusCompleted
			}
			result = tx.Model(&models.TranscriptionQueueItem{}).Where("id = ? AND transcription_job_id = ? AND execution_id = ? AND status = ?", *e.QueueItemID, e.TranscriptionJobID, id, models.QueueStatusProcessing).Updates(map[string]any{"status": queueStatus, "completed_at": now, "position": 0, "error_message": updates["error_message"], "parameters_json": gorm.Expr("json_remove(parameters_json, '$.hf_token', '$.api_key')")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrExecutionOwnership
			}
		}
		return nil
	})
}

// Obtain SQLite's writer reservation before reading state. This avoids a stale
// read transaction failing to upgrade while another stage commits concurrently.
func lockExecutionRow(tx *gorm.DB, jobID, id string) error {
	result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND transcription_job_id = ?", id, jobID).UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrExecutionOwnership
	}
	return nil
}

func (r *executionLifecycleRepository) TryClaimCompletionCallback(ctx context.Context, id string, generation int64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND status = ? AND recovery_state = ? AND cancelled_at IS NULL AND callback_claimed_at IS NULL", id, generation, models.StatusCompleted, "completed").Update("callback_claimed_at", time.Now())
	return result.RowsAffected == 1, result.Error
}
