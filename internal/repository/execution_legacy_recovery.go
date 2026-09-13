package repository

import (
	"context"
	"time"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

// ReconcileLegacyOrphans is startup-only, after prior worker termination has
// been proven and before workers or API dispatch start. Older versions could
// leave active execution history behind an already terminal parent recording.
// Such rows have no recoverable ownership and must not block a new execution.
// The conditional update leaves parent output, resolved history, versioned
// executions and any admitted queue work untouched. It records reconciliation
// time without inventing a processing duration for an unknown stop time.
func (r *executionLifecycleRepository) ReconcileLegacyOrphans(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Model(&models.TranscriptionJobExecution{}).
		Where("COALESCE(recovery_version, 0) = 0 AND cancelled_at IS NULL").
		Where("status IN ?", []models.JobStatus{models.StatusPending, models.StatusProcessing}).
		Where("EXISTS (SELECT 1 FROM transcription_jobs WHERE transcription_jobs.id = transcription_job_executions.transcription_job_id AND transcription_jobs.deleted_at IS NULL AND transcription_jobs.status IN ?)", []models.JobStatus{models.StatusCompleted, models.StatusFailed}).
		Where("NOT EXISTS (SELECT 1 FROM transcription_queue_items WHERE transcription_queue_items.transcription_job_id = transcription_job_executions.transcription_job_id AND transcription_queue_items.status IN ?)", []models.TranscriptionQueueStatus{models.QueueStatusPending, models.QueueStatusProcessing}).
		Updates(map[string]interface{}{
			"status":         models.StatusFailed,
			"recovery_state": "interrupted",
			"error_message":  "Legacy execution interrupted before durable recovery was available; start a new run.",
			"completed_at":   gorm.Expr("COALESCE(completed_at, ?)", time.Now()),
		})
	return result.RowsAffected, result.Error
}
