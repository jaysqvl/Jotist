package repository

import (
	"context"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"scriberr/internal/models"
	"testing"
	"time"
)

func lifecycleDB(t *testing.T) (*gorm.DB, ExecutionLifecycleRepository, *models.TranscriptionJobExecution) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.TranscriptionQueueItem{}))
	old := "previous completed transcript"
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "recording", AudioPath: "fixture.wav", Status: models.StatusProcessing, Transcript: &old}).Error)
	deadline := time.Now().Add(time.Hour)
	e := &models.TranscriptionJobExecution{TranscriptionJobID: "recording", RecoveryVersion: 1, StartedAt: time.Now(), DeadlineAt: &deadline, PlanJSON: `{"mode":"fixed"}`, ActualParameters: models.WhisperXParams{Model: "tiny", Fp16: false, BatchSize: 0, BeamSize: 0, Language: nil}}
	return db, NewExecutionLifecycleRepository(db), e
}

func TestExecutionBeginPreservesSnapshotAndAtomicallyBindsQueue(t *testing.T) {
	db, repo, e := lifecycleDB(t)
	ctx := context.Background()
	item := models.TranscriptionQueueItem{ID: "run", TranscriptionJobID: "recording", Status: models.QueueStatusProcessing, Parameters: models.WhisperXParams{Model: "tiny"}}
	require.NoError(t, db.Create(&item).Error)
	e.QueueItemID = &item.ID
	require.NoError(t, repo.Begin(ctx, e))
	var saved models.TranscriptionJobExecution
	require.NoError(t, db.First(&saved, "id = ?", e.ID).Error)
	require.False(t, saved.ActualParameters.Fp16)
	require.Zero(t, saved.ActualParameters.BatchSize)
	require.Zero(t, saved.ActualParameters.BeamSize)
	require.Nil(t, saved.ActualParameters.Language)
	require.Equal(t, int64(1), saved.OwnerGeneration)
	require.Equal(t, "running", saved.RecoveryState)
	require.NoError(t, db.First(&item, "id = ?", item.ID).Error)
	require.Equal(t, &e.ID, item.ExecutionID)
	other := *e
	other.ID = "different"
	require.ErrorIs(t, repo.Begin(ctx, &other), ErrExecutionOwnership)
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestExecutionCancellationFencesLatePublicationAndCallback(t *testing.T) {
	db, repo, e := lifecycleDB(t)
	ctx := context.Background()
	require.NoError(t, repo.Begin(ctx, e))
	require.NoError(t, repo.Cancel(ctx, "recording", e.ID, "cancelled"))
	require.NoError(t, repo.Cancel(ctx, "recording", e.ID, "cancelled"))
	text := "late transcript"
	require.ErrorIs(t, repo.Finish(ctx, e.ID, 1, models.StatusCompleted, &text, ""), ErrExecutionOwnership)
	claimed, err := repo.TryClaimCompletionCallback(ctx, e.ID, 1)
	require.NoError(t, err)
	require.False(t, claimed)
	_, err = repo.RequestResume(ctx, "recording", e.ID, 2, e.ActualParameters)
	require.ErrorIs(t, err, ErrExecutionOwnership)
	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "recording").Error)
	require.Equal(t, "previous completed transcript", *job.Transcript)
}

func TestExecutionResumeRetainsIdentityPlanDeadlineAndFencesOldOwner(t *testing.T) {
	db, repo, e := lifecycleDB(t)
	ctx := context.Background()
	require.NoError(t, repo.Begin(ctx, e))
	require.NoError(t, repo.Finish(ctx, e.ID, 1, models.StatusFailed, nil, "speaker stage failed"))
	resumed, err := repo.RequestResume(ctx, "recording", e.ID, 1, e.ActualParameters)
	require.NoError(t, err)
	require.Equal(t, e.ID, resumed.ID)
	require.Equal(t, int64(2), resumed.OwnerGeneration)
	require.Equal(t, e.PlanJSON, resumed.PlanJSON)
	require.WithinDuration(t, *e.DeadlineAt, *resumed.DeadlineAt, time.Millisecond)
	_, err = repo.RequestResume(ctx, "recording", e.ID, 1, e.ActualParameters)
	require.ErrorIs(t, err, ErrExecutionOwnership)
	_, err = repo.ClaimResume(ctx, "recording", e.ID)
	require.NoError(t, err)
	_, err = repo.ClaimResume(ctx, "recording", e.ID)
	require.ErrorIs(t, err, ErrExecutionOwnership)
	text := "complete result"
	require.ErrorIs(t, repo.Finish(ctx, e.ID, 1, models.StatusCompleted, &text, ""), ErrExecutionOwnership)
	require.NoError(t, repo.Finish(ctx, e.ID, 2, models.StatusCompleted, &text, ""))
	require.NoError(t, repo.Finish(ctx, e.ID, 2, models.StatusCompleted, &text, ""))
	claimed, err := repo.TryClaimCompletionCallback(ctx, e.ID, 2)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repo.TryClaimCompletionCallback(ctx, e.ID, 2)
	require.NoError(t, err)
	require.False(t, claimed)
	var saved models.TranscriptionJobExecution
	require.NoError(t, db.First(&saved, "id = ?", e.ID).Error)
	require.Equal(t, "completed", saved.RecoveryState)
}

func TestExecutionFinishQueueOutcomeIsAtomicAndExpiredResumeRejected(t *testing.T) {
	db, repo, e := lifecycleDB(t)
	ctx := context.Background()
	item := models.TranscriptionQueueItem{ID: "run", TranscriptionJobID: "recording", Status: models.QueueStatusProcessing, Parameters: models.WhisperXParams{Model: "tiny"}}
	require.NoError(t, db.Create(&item).Error)
	e.QueueItemID = &item.ID
	require.NoError(t, repo.Begin(ctx, e))
	text := "new result"
	require.NoError(t, repo.Finish(ctx, e.ID, 1, models.StatusCompleted, &text, ""))
	require.NoError(t, db.First(&item, "id = ?", item.ID).Error)
	require.Equal(t, models.QueueStatusCompleted, item.Status)
	require.Equal(t, &e.ID, item.ExecutionID)
	deadline := time.Now().Add(-time.Second)
	require.NoError(t, db.Model(e).Updates(map[string]any{"status": models.StatusFailed, "recovery_state": "failed", "deadline_at": deadline}).Error)
	_, err := repo.RequestResume(ctx, "recording", e.ID, 1, e.ActualParameters)
	require.ErrorIs(t, err, ErrExecutionDeadline)
}

func TestMultiTrackPublicationIsAtomicAndCancellationPreservesPreviousOutputs(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancelled"}[cancel], func(t *testing.T) {
			db, repo, e := lifecycleDB(t)
			ctx := context.Background()
			previousIndividual := `{"track-a":"previous"}`
			require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", "recording").Update("individual_transcripts", previousIndividual).Error)
			item := models.TranscriptionQueueItem{ID: "multi-run", TranscriptionJobID: "recording", Status: models.QueueStatusProcessing}
			require.NoError(t, db.Create(&item).Error)
			e.QueueItemID = &item.ID
			require.NoError(t, repo.Begin(ctx, e))
			merged, individual, timings := "merged result", `{"track-a":"new"}`, `[{"track_name":"track-a","duration":50}]`
			start, end, duration := time.Now().Add(-time.Second), time.Now(), int64(1000)
			details := &models.TranscriptionJobExecution{IndividualTranscripts: &individual, MultiTrackTimings: &timings, MergeStartTime: &start, MergeEndTime: &end, MergeDuration: &duration}
			if cancel {
				require.NoError(t, repo.Cancel(ctx, "recording", e.ID, "user cancelled"))
				require.ErrorIs(t, repo.FinishWithDetails(ctx, e.ID, 1, models.StatusCompleted, &merged, "", details), ErrExecutionOwnership)
			} else {
				// Force a failure at the final queue update: execution and both job
				// outputs must roll back as one transaction.
				require.NoError(t, db.Model(&item).Update("execution_id", "different-owner").Error)
				require.ErrorIs(t, repo.FinishWithDetails(ctx, e.ID, 1, models.StatusCompleted, &merged, "", details), ErrExecutionOwnership)
				var unchanged models.TranscriptionJob
				require.NoError(t, db.First(&unchanged, "id = ?", "recording").Error)
				require.Equal(t, "previous completed transcript", *unchanged.Transcript)
				require.Equal(t, previousIndividual, *unchanged.IndividualTranscripts)
				require.NoError(t, db.Model(&item).Update("execution_id", e.ID).Error)
				require.NoError(t, repo.FinishWithDetails(ctx, e.ID, 1, models.StatusCompleted, &merged, "", details))
			}
			var job models.TranscriptionJob
			var saved models.TranscriptionJobExecution
			require.NoError(t, db.First(&job, "id = ?", "recording").Error)
			require.NoError(t, db.First(&saved, "id = ?", e.ID).Error)
			if cancel {
				require.Equal(t, "previous completed transcript", *job.Transcript)
				require.Equal(t, previousIndividual, *job.IndividualTranscripts)
				require.Nil(t, saved.IndividualTranscripts)
			} else {
				require.Equal(t, merged, *job.Transcript)
				require.Equal(t, individual, *job.IndividualTranscripts)
				require.Equal(t, individual, *saved.IndividualTranscripts)
				require.Equal(t, timings, *saved.MultiTrackTimings)
				require.Equal(t, duration, *saved.MergeDuration)
				require.WithinDuration(t, start, *saved.MergeStartTime, time.Millisecond)
				require.WithinDuration(t, end, *saved.MergeEndTime, time.Millisecond)
			}
		})
	}
}

func TestExpiredPendingResumeIsTerminalWithoutResettingDeadlineOrOutputs(t *testing.T) {
	db, repo, e := lifecycleDB(t)
	ctx := context.Background()
	item := models.TranscriptionQueueItem{ID: "expiring-run", TranscriptionJobID: "recording", Status: models.QueueStatusProcessing}
	require.NoError(t, db.Create(&item).Error)
	e.QueueItemID = &item.ID
	require.NoError(t, repo.Begin(ctx, e))
	require.NoError(t, repo.Finish(ctx, e.ID, 1, models.StatusFailed, nil, "retryable failure"))
	resumed, err := repo.RequestResume(ctx, "recording", e.ID, 1, e.ActualParameters)
	require.NoError(t, err)
	require.ErrorIs(t, repo.ExpirePending(ctx, "recording", e.ID, resumed.OwnerGeneration), ErrExecutionOwnership, "a live deadline cannot expire")
	deadline := time.Now().Add(-time.Second)
	require.NoError(t, db.Model(e).Update("deadline_at", deadline).Error)
	require.ErrorIs(t, repo.ExpirePending(ctx, "recording", e.ID, 1), ErrExecutionOwnership)
	require.NoError(t, repo.ExpirePending(ctx, "recording", e.ID, resumed.OwnerGeneration))
	require.NoError(t, repo.ExpirePending(ctx, "recording", e.ID, resumed.OwnerGeneration))
	var saved models.TranscriptionJobExecution
	var job models.TranscriptionJob
	require.NoError(t, db.First(&saved, "id = ?", e.ID).Error)
	require.NoError(t, db.First(&job, "id = ?", "recording").Error)
	require.NoError(t, db.First(&item, "id = ?", item.ID).Error)
	require.Equal(t, "failed", saved.RecoveryState)
	require.Equal(t, ErrExecutionDeadline.Error(), *saved.ErrorMessage)
	require.Equal(t, models.QueueStatusFailed, item.Status)
	require.Equal(t, "previous completed transcript", *job.Transcript)
	require.Equal(t, e.PlanJSON, saved.PlanJSON)
	require.WithinDuration(t, deadline, *saved.DeadlineAt, time.Millisecond)
	require.NoError(t, db.Model(&job).Update("status", models.StatusProcessing).Error)
	require.NoError(t, repo.Begin(ctx, &models.TranscriptionJobExecution{TranscriptionJobID: "recording", RecoveryVersion: 1, StartedAt: time.Now()}), "expired pending history must not block a new execution")
}

func TestQueuePromotionPreservesPublicationUntilSuccessfulReplacement(t *testing.T) {
	db, lifecycle, e := lifecycleDB(t)
	ctx := context.Background()
	oldSummary := "summary of previous transcript"
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", "recording").Updates(map[string]any{"status": models.StatusCompleted, "summary": oldSummary}).Error)
	queue := NewTranscriptionQueueRepository(db)
	require.NoError(t, queue.Append(ctx, "recording", []models.TranscriptionQueueItem{{Parameters: models.WhisperXParams{Model: "tiny"}}}))
	item, err := queue.PromoteNext(ctx, "recording")
	require.NoError(t, err)
	require.NotNil(t, item)
	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "recording").Error)
	require.Equal(t, "previous completed transcript", *job.Transcript)
	require.Equal(t, oldSummary, *job.Summary)
	_, claimed, err := queue.ClaimPending(ctx, "recording", item.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	e.QueueItemID = &item.ID
	require.NoError(t, lifecycle.Begin(ctx, e))
	require.NoError(t, lifecycle.Finish(ctx, e.ID, 1, models.StatusFailed, nil, "failure"))
	job = models.TranscriptionJob{}
	require.NoError(t, db.First(&job, "id = ?", "recording").Error)
	require.Equal(t, "previous completed transcript", *job.Transcript)
	require.Equal(t, oldSummary, *job.Summary)
	resumed, err := lifecycle.RequestResume(ctx, "recording", e.ID, 1, e.ActualParameters)
	require.NoError(t, err)
	_, claimed, err = queue.ClaimPending(ctx, "recording", item.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = lifecycle.ClaimResume(ctx, "recording", e.ID)
	require.NoError(t, err)
	newTranscript := "replacement transcript"
	require.NoError(t, lifecycle.Finish(ctx, e.ID, resumed.OwnerGeneration, models.StatusCompleted, &newTranscript, ""))
	job = models.TranscriptionJob{}
	require.NoError(t, db.First(&job, "id = ?", "recording").Error)
	require.Equal(t, newTranscript, *job.Transcript)
	require.Nil(t, job.Summary, "old summary cannot accompany a new transcript")
}
