package repository

import (
	"context"

	"github.com/jaysqvl/Jotist/internal/models"
)

// ListAttemptSummaries reads only picker evidence, in one indexed query. It
// never opens checkpoints or loads provenance or transcripts. The measurement
// JSON contains numeric telemetry and whitelisted worker recovery actions.
func (r *RecoveryRepository) ListAttemptSummaries(ctx context.Context, executionIDs []string) ([]models.RecoveryAttempt, error) {
	rows := []models.RecoveryAttempt{}
	if len(executionIDs) == 0 {
		return rows, nil
	}
	err := r.db.WithContext(ctx).Model(&models.RecoveryAttempt{}).
		Select("execution_id", "stage_id", "attempt_number", "device", "reason", "measurements").
		Where("execution_id IN ?", executionIDs).
		Order("execution_id, stage_id, attempt_number").Find(&rows).Error
	return rows, err
}
