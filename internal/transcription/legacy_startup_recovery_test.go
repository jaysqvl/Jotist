package transcription

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/serverlock"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func legacyStartupFixture(t *testing.T) (*stageTestFixture, *UnifiedTranscriptionService, models.TranscriptionJobExecution) {
	t.Helper()
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	f := newStageTestFixture(t)
	require.NoError(t, f.db.AutoMigrate(&models.TranscriptionQueueItem{}))
	published, summary := "published output retained", "summary retained"
	require.NoError(t, f.db.Model(&models.TranscriptionJob{}).Where("id = ?", f.recording).Updates(map[string]interface{}{
		"status": models.StatusCompleted, "transcript": published, "summary": summary,
	}).Error)
	legacy := models.TranscriptionJobExecution{ID: uuid.NewString(), TranscriptionJobID: f.recording, Status: models.StatusProcessing, StartedAt: time.Now().Add(-30 * 24 * time.Hour)}
	require.NoError(t, f.db.Create(&legacy).Error)
	// Existing rows get NULL when the new nullable columns are first added.
	require.NoError(t, f.db.Model(&legacy).UpdateColumn("recovery_version", nil).Error)
	service := NewUnifiedTranscriptionService(repository.NewJobRepository(f.db), t.TempDir(), t.TempDir())
	require.NoError(t, service.recoveryInitError)
	return f, service, legacy
}

func TestStartupReconcilesLegacyOrphanWithoutReadingAudioAndUnblocksNewRun(t *testing.T) {
	f, service, legacy := legacyStartupFixture(t)
	ctx := context.Background()
	var before models.TranscriptionJob
	require.NoError(t, f.db.First(&before, "id = ?", f.recording).Error)
	// No audio exists at fixture.wav, and no adapter is initialized by recovery.
	protected, err := service.RecoverExecutions(ctx)
	require.NoError(t, err)
	require.Empty(t, protected)
	var recovered models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&recovered, "id = ?", legacy.ID).Error)
	require.Equal(t, models.StatusFailed, recovered.Status)
	require.Equal(t, "interrupted", recovered.RecoveryState)
	require.Zero(t, recovered.RecoveryVersion)
	require.Contains(t, *recovered.ErrorMessage, "Legacy execution")
	require.Nil(t, recovered.ProcessingDuration, "startup must not invent a month-long processing duration")
	var after models.TranscriptionJob
	require.NoError(t, f.db.First(&after, "id = ?", f.recording).Error)
	require.Equal(t, before, after, "historical status, outputs and parent metadata remain unchanged")
	_, err = service.RecoverExecutions(ctx)
	require.NoError(t, err)
	var twice models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&twice, "id = ?", legacy.ID).Error)
	require.Equal(t, recovered, twice, "startup reconciliation is idempotent")
	require.ErrorContains(t, service.PrepareExecutionResume(ctx, f.recording, legacy.ID), "older execution")

	// A later explicit request can acquire a new owner despite retained history.
	require.NoError(t, f.db.Model(&models.TranscriptionJob{}).Where("id = ?", f.recording).Update("status", models.StatusProcessing).Error)
	deadline := time.Now().Add(time.Hour)
	fresh := models.TranscriptionJobExecution{ID: uuid.NewString(), TranscriptionJobID: f.recording, RecoveryVersion: 1, StartedAt: time.Now(), DeadlineAt: &deadline}
	require.NoError(t, service.lifecycle.Begin(ctx, &fresh))
	require.Equal(t, "running", fresh.RecoveryState)
}

func TestStartupDoesNotReconcileLegacyOrphansBeforeWorkerStopProof(t *testing.T) {
	f, service, legacy := legacyStartupFixture(t)
	lockPath := filepath.Join(t.TempDir(), "ownership.db")
	first, err := serverlock.Acquire(lockPath)
	require.NoError(t, err)
	require.NoError(t, first.Close()) // Simulate an unclean exit in this same process boundary.
	second, err := serverlock.Acquire(lockPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, second.Close())
		// Restore proven state for other package tests without clearing the unsafe marker.
		clean, err := serverlock.Acquire(filepath.Join(t.TempDir(), "clean.db"))
		require.NoError(t, err)
		require.NoError(t, clean.MarkWorkersStopped())
		require.NoError(t, clean.Close())
	})
	require.False(t, serverlock.PriorWorkersStopped())
	_, err = service.RecoverExecutions(context.Background())
	require.ErrorContains(t, err, "previous worker termination")
	var saved models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&saved, "id = ?", legacy.ID).Error)
	require.Equal(t, models.StatusProcessing, saved.Status)
	require.Empty(t, saved.RecoveryState)
	require.Nil(t, saved.CompletedAt)
	var job models.TranscriptionJob
	require.NoError(t, f.db.First(&job, "id = ?", f.recording).Error)
	require.Equal(t, models.StatusCompleted, job.Status)
	require.Equal(t, "published output retained", *job.Transcript)
}
