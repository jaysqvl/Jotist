package repository

import (
	"context"

	"scriberr/internal/models"
)

// ListAttemptSummaries reads only picker evidence, in one indexed query. It
// never opens checkpoints or loads measurements, provenance, or transcripts.
func (r *RecoveryRepository) ListAttemptSummaries(ctx context.Context, executionIDs []string) ([]models.RecoveryAttempt, error) {
	rows := []models.RecoveryAttempt{}
	if len(executionIDs) == 0 {
		return rows, nil
	}
	err := r.db.WithContext(ctx).Model(&models.RecoveryAttempt{}).
		Select("execution_id", "stage_id", "attempt_number", "device", "reason").
		Where("execution_id IN ?", executionIDs).
		Order("execution_id, stage_id, attempt_number").Find(&rows).Error
	return rows, err
}
