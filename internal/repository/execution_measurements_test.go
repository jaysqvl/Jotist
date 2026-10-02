package repository

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
)

func TestExecutionMeasurementsSurviveDeadlineWithoutPermittingCheckpointPublication(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	stage, attempt := recoveryAttempt(t, store, recoverySpec(recording, execution))
	expired := time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("deadline_at", expired).Error)
	peak, average := int64(2048), int64(1024)
	measurement := models.StageMeasurements{InvocationID: uuid.NewString(), Samples: 3, ElapsedSeconds: 2, ProcessPeakRSSBytes: &peak, ProcessAverageRSSBytes: &average, RSSSampledSeconds: 1.5}
	require.NoError(t, store.RecordAttemptMeasurements(context.Background(), attempt.ID, 1, measurement))
	require.NoError(t, store.AppendExecutionMeasurements(context.Background(), execution, 1, measurement))
	require.NoError(t, store.AppendExecutionMeasurements(context.Background(), execution, 1, measurement), "acknowledgement retry is idempotent")
	var run models.TranscriptionJobExecution
	require.NoError(t, db.First(&run, "id = ?", execution).Error)
	require.Len(t, run.ResourceMeasurements, 1)
	require.Equal(t, peak, *run.ResourceMeasurements[0].ProcessPeakRSSBytes)
	_, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("synthetic"), nil)
	require.ErrorIs(t, err, ErrRecoveryStaleOwner)
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("owner_generation", 2).Error)
	measurement.InvocationID = uuid.NewString()
	require.ErrorIs(t, store.AppendExecutionMeasurements(context.Background(), execution, 1, measurement), ErrRecoveryStaleOwner)
	require.NoError(t, db.Model(&models.RecoveryStage{}).Where("id = ?", stage.ID).Update("owner_generation", 2).Error)
	require.ErrorIs(t, store.RecordAttemptMeasurements(context.Background(), attempt.ID, 1, measurement), ErrRecoveryStaleOwner)
}

func TestExecutionMeasurementsHonorDeletionAndRejectNonfiniteMetrics(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	_, attempt := recoveryAttempt(t, store, recoverySpec(recording, execution))
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		measurement := models.StageMeasurements{ProcessCPUAveragePercent: &invalid}
		require.ErrorIs(t, store.RecordAttemptMeasurements(context.Background(), attempt.ID, 1, measurement), ErrRecoveryConflict)
	}
	require.NoError(t, db.Create(&models.RecoveryDeletion{RecordingID: recording, RequestedAt: time.Now()}).Error)
	measurement := models.StageMeasurements{InvocationID: uuid.NewString()}
	require.ErrorIs(t, store.AppendExecutionMeasurements(context.Background(), execution, 1, measurement), ErrRecoveryStaleOwner)
	require.ErrorIs(t, store.RecordAttemptMeasurements(context.Background(), attempt.ID, 1, measurement), ErrRecoveryStaleOwner)
}

func TestExecutionMeasurementsSurviveCancellationWithoutPermittingCheckpointPublication(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	_, attempt := recoveryAttempt(t, store, recoverySpec(recording, execution))
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("cancelled_at", time.Now()).Error)
	measurement := models.StageMeasurements{InvocationID: uuid.NewString(), Samples: 1, ElapsedSeconds: 0.5}
	require.NoError(t, store.RecordAttemptMeasurements(context.Background(), attempt.ID, 1, measurement))
	require.NoError(t, store.AppendExecutionMeasurements(context.Background(), execution, 1, measurement))
	_, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("synthetic"), nil)
	require.ErrorIs(t, err, ErrRecoveryStaleOwner)
}
