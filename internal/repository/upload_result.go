package repository

import (
	"context"
	"errors"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

var ErrUploadSessionChanged = errors.New("upload session is no longer active")

// CommitUploadResult publishes a prepared recording and its resumable-upload
// result together. File preparation happens before this short transaction;
// dispatch must happen only after it commits.
func CommitUploadResult(ctx context.Context, db *gorm.DB, sessionID string, job *models.TranscriptionJob) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		return completeUploadResult(tx, sessionID, job.ID, "transcription")
	})
}

// CommitQuickUploadResult records the result before the quick service launches
// its worker. Quick jobs remain temporary; this binding does not make them
// resumable across server restarts.
func CommitQuickUploadResult(ctx context.Context, db *gorm.DB, sessionID, jobID string) error {
	return completeUploadResult(db.WithContext(ctx), sessionID, jobID, "quick")
}

func completeUploadResult(db *gorm.DB, sessionID, resultID, resultType string) error {
	result := db.Model(&models.UploadSession{}).
		Where("id = ? AND status = ? AND result_id IS NULL", sessionID, models.UploadSessionActive).
		Updates(map[string]any{
			"status":      models.UploadSessionCompleted,
			"result_id":   resultID,
			"result_type": resultType,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrUploadSessionChanged
	}
	return nil
}
