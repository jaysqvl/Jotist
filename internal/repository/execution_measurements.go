package repository

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"scriberr/internal/models"
)

// These numeric writes cannot publish output or change execution state. A
// deadline/cancellation may have elapsed; owner and deletion fences remain.
func measurementFence(tx *gorm.DB, recordingID, executionID string, generation int64) error {
	result := tx.Model(&models.TranscriptionJobExecution{}).
		Where("id = ? AND transcription_job_id = ? AND owner_generation = ?", executionID, recordingID, generation).
		Where("EXISTS (SELECT 1 FROM transcription_jobs WHERE id = ?)", recordingID).
		Where("NOT EXISTS (SELECT 1 FROM recovery_deletions WHERE recording_id = ?)", recordingID).
		UpdateColumn("owner_generation", generation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRecoveryStaleOwner
	}
	return nil
}

func (r *RecoveryRepository) ownedMeasurementAttempt(tx *gorm.DB, id string, generation int64) (*models.RecoveryAttempt, *models.RecoveryStage, error) {
	if err := tx.Model(&models.RecoveryAttempt{}).Where("id = ?", id).UpdateColumn("state", gorm.Expr("state")).Error; err != nil {
		return nil, nil, err
	}
	var attempt models.RecoveryAttempt
	if err := tx.First(&attempt, "id = ?", id).Error; err != nil {
		return nil, nil, err
	}
	var stage models.RecoveryStage
	if err := tx.First(&stage, "id = ?", attempt.StageID).Error; err != nil {
		return nil, nil, err
	}
	if err := measurementFence(tx, stage.RecordingID, stage.ExecutionID, generation); err != nil {
		return nil, nil, err
	}
	if attempt.OwnerGeneration != generation || stage.OwnerGeneration != generation || stage.AttemptNumber != attempt.AttemptNumber {
		return nil, nil, ErrRecoveryStaleOwner
	}
	return &attempt, &stage, nil
}

func (r *RecoveryRepository) AppendExecutionMeasurements(ctx context.Context, executionID string, generation int64, measurements models.StageMeasurements) error {
	if err := validateMeasurements(measurements); err != nil {
		return err
	}
	if _, err := uuid.Parse(measurements.InvocationID); err != nil {
		return ErrRecoveryConflict
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Reserve the writer before reading so retries cannot lose an append.
		if err := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ?", executionID).UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var execution models.TranscriptionJobExecution
		if err := tx.First(&execution, "id = ?", executionID).Error; err != nil {
			return err
		}
		if err := measurementFence(tx, execution.TranscriptionJobID, executionID, generation); err != nil {
			return err
		}
		for _, prior := range execution.ResourceMeasurements {
			if prior.InvocationID == measurements.InvocationID {
				return nil
			}
		}
		execution.ResourceMeasurements = append(execution.ResourceMeasurements, measurements)
		encoded, err := json.Marshal(execution.ResourceMeasurements)
		if err != nil {
			return err
		}
		return tx.Model(&execution).UpdateColumn("resource_measurements", string(encoded)).Error
	})
}
