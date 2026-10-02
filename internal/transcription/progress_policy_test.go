package transcription

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
)

func TestPreparedInitializationDoesNotWaitForAnotherGPUStage(t *testing.T) {
	f := newRecoveryServiceFixture(t, false)
	require.NoError(t, f.service.Initialize(context.Background()))
	f.service.tempDirectory = filepath.Join(t.TempDir(), "prepared-temp")
	f.service.outputDirectory = filepath.Join(t.TempDir(), "prepared-output")
	release, err := acquireGPUStage(context.Background(), map[string]interface{}{"device": "cuda"})
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, f.service.Initialize(ctx), "prepared CPU startup must not depend on GPU admission")
	require.DirExists(t, f.service.tempDirectory)
	require.DirExists(t, f.service.outputDirectory)
}

func TestRecoverableExecutionCanResumeAfterMoreThanTwoHours(t *testing.T) {
	f := newRecoveryServiceFixture(t, false)
	require.Error(t, f.service.ProcessJob(f.binding(""), f.recording))
	failed := f.currentExecution(t)
	require.Nil(t, failed.DeadlineAt)
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", failed.ID).UpdateColumn("started_at", time.Now().Add(-3*time.Hour)).Error)
	ctx := f.admitResume(t, f.currentExecution(t))
	require.NoError(t, f.service.ProcessJob(ctx, f.recording))
	completed := f.currentExecution(t)
	require.Equal(t, models.StatusCompleted, completed.Status)
	require.NotNil(t, completed.ProcessingDuration)
	require.Greater(t, *completed.ProcessingDuration, int64(2*time.Hour/time.Millisecond))
	require.Equal(t, 1, f.asr.callCount(f.job(t).AudioPath), "resume must consume the retained recognition checkpoint")
}

func TestQuickExpiryRetainsActiveAudioAndStartsAfterCompletion(t *testing.T) {
	directory := t.TempDir()
	audio := filepath.Join(directory, "audio.wav")
	require.NoError(t, os.WriteFile(audio, []byte("fixture"), 0600))
	job := &QuickTranscriptionJob{ID: "active", AudioPath: audio, Status: models.StatusProcessing, ExpiresAt: time.Now().Add(-time.Hour)}
	service := &QuickTranscriptionService{tempDir: directory, jobs: map[string]*QuickTranscriptionJob{"active": job}}
	service.cleanupExpiredJobs()
	_, err := service.GetQuickJob("active")
	require.NoError(t, err)
	require.FileExists(t, audio)
	job.Status = models.StatusCompleted
	service.cleanupExpiredJobs()
	require.NoFileExists(t, audio)
	require.Empty(t, service.jobs)
}
