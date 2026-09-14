package repository

import (
	"context"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func deletionFixture(t *testing.T) (*gorm.DB, JobRepository, []any) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	children := []any{&models.ChatMessage{}, &models.ChatSession{}, &models.Note{}, &models.Summary{}, &models.SpeakerMapping{}, &models.TranscriptionQueueItem{}, &models.TranscriptionJobExecution{}, &models.MultiTrackFile{}}
	require.NoError(t, db.AutoMigrate(append([]any{&models.TranscriptionJob{}}, children...)...))
	for _, id := range []string{"remove", "keep"} {
		require.NoError(t, db.Create(&models.TranscriptionJob{ID: id, AudioPath: id + ".wav", Status: models.StatusCompleted}).Error)
		require.NoError(t, db.Create(&models.ChatSession{ID: id + "-chat", JobID: id, TranscriptionID: id, Model: "test"}).Error)
		require.NoError(t, db.Create(&models.ChatMessage{ChatSessionID: id + "-chat", Role: "user", Content: "message"}).Error)
		require.NoError(t, db.Create(&models.Note{ID: id + "-note", TranscriptionID: id, Quote: "quote", Content: "note"}).Error)
		require.NoError(t, db.Create(&models.Summary{TranscriptionID: id, Model: "test", Content: "summary"}).Error)
		require.NoError(t, db.Create(&models.SpeakerMapping{TranscriptionJobID: id, OriginalSpeaker: "speaker", CustomName: "name"}).Error)
		require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: id + "-execution", TranscriptionJobID: id, StartedAt: time.Now(), Status: models.StatusCompleted}).Error)
		require.NoError(t, db.Create(&models.TranscriptionQueueItem{TranscriptionJobID: id, Status: models.QueueStatusCompleted}).Error)
		require.NoError(t, db.Create(&models.MultiTrackFile{TranscriptionJobID: id, FileName: "track", FilePath: id + ".wav"}).Error)
	}
	return db, NewJobRepository(db), children
}

func TestDeleteWithAssociationsPreservesOtherRecordingsAndTombstone(t *testing.T) {
	db, repo, children := deletionFixture(t)
	require.NoError(t, repo.DeleteWithAssociations(context.Background(), "remove"))
	_, err := repo.FindByID(context.Background(), "remove")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = repo.FindByID(context.Background(), "keep")
	require.NoError(t, err)
	for _, model := range children {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.Equal(t, int64(1), count, "association %T", model)
		key, value := "transcription_job_id", "keep"
		switch model.(type) {
		case *models.ChatMessage:
			key, value = "chat_session_id", "keep-chat"
		case *models.ChatSession, *models.Note, *models.Summary:
			key = "transcription_id"
		}
		require.NoError(t, db.Model(model).Where(key+" = ?", value).Count(&count).Error)
		require.Equal(t, int64(1), count, "unrelated association %T must remain", model)
	}
	var tombstone models.TranscriptionJob
	require.NoError(t, db.Unscoped().First(&tombstone, "id = ?", "remove").Error)
	require.True(t, tombstone.DeletedAt.Valid)
	require.Equal(t, "remove.wav", tombstone.AudioPath)
}

func TestDeleteWithAssociationsRollsBackEveryChildWhenParentDeleteFails(t *testing.T) {
	db, repo, children := deletionFixture(t)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_recording_delete BEFORE UPDATE OF deleted_at ON transcription_jobs
		WHEN NEW.id = 'remove' BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`).Error)
	require.ErrorContains(t, repo.DeleteWithAssociations(context.Background(), "remove"), "injected delete failure")
	_, err := repo.FindByID(context.Background(), "remove")
	require.NoError(t, err)
	for _, model := range children {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.Equal(t, int64(2), count, "association %T must roll back", model)
	}
}

func TestDeleteWithAssociationsHonorsCancelledContext(t *testing.T) {
	db, repo, _ := deletionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, repo.DeleteWithAssociations(ctx, "remove"), context.Canceled)
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}
