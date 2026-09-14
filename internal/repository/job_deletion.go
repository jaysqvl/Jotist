package repository

import (
	"context"
	"fmt"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

// DeleteWithAssociations removes a recording's database aggregate atomically.
// Explicit child deletes also support legacy databases without cascade rules.
// The parent remains a soft-delete tombstone for incremental client sync.
// Callers own queue/recovery admission and remove media only after this commits.
func (r *jobRepository) DeleteWithAssociations(ctx context.Context, jobID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job models.TranscriptionJob
		if err := tx.First(&job, "id = ?", jobID).Error; err != nil {
			return err
		}

		sessions := tx.Model(&models.ChatSession{}).Select("id").
			Where("transcription_id = ? OR job_id = ?", jobID, jobID)
		if err := tx.Where("chat_session_id IN (?) OR session_id IN (?)", sessions, sessions).
			Delete(&models.ChatMessage{}).Error; err != nil {
			return fmt.Errorf("delete recording chat messages: %w", err)
		}
		if err := tx.Where("transcription_id = ? OR job_id = ?", jobID, jobID).
			Delete(&models.ChatSession{}).Error; err != nil {
			return fmt.Errorf("delete recording chat sessions: %w", err)
		}

		for _, child := range []struct {
			model any
			key   string
		}{
			{&models.Note{}, "transcription_id"},
			{&models.Summary{}, "transcription_id"},
			{&models.SpeakerMapping{}, "transcription_job_id"},
			{&models.TranscriptionQueueItem{}, "transcription_job_id"},
			{&models.TranscriptionJobExecution{}, "transcription_job_id"},
			{&models.MultiTrackFile{}, "transcription_job_id"},
		} {
			if err := tx.Where(child.key+" = ?", jobID).Delete(child.model).Error; err != nil {
				return fmt.Errorf("delete recording association %T: %w", child.model, err)
			}
		}
		return tx.Delete(&job).Error
	})
}
