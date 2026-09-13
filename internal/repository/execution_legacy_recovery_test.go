package repository

import (
	"context"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

func TestReconcileLegacyOrphansPreservesParentsAndUnrelatedOwnership(t *testing.T) {
	db, repo, _ := lifecycleDB(t)
	cases := []struct {
		name          string
		parent        models.JobStatus
		status        models.JobStatus
		version       int
		nullVersion   bool
		activeQueue   bool
		cancelled     bool
		deletedParent bool
		changed       bool
	}{
		{name: "completed_parent_null_legacy", parent: models.StatusCompleted, status: models.StatusProcessing, nullVersion: true, changed: true},
		{name: "failed_parent_pending_legacy", parent: models.StatusFailed, status: models.StatusPending, changed: true},
		{name: "completed_history", parent: models.StatusCompleted, status: models.StatusCompleted},
		{name: "failed_history", parent: models.StatusFailed, status: models.StatusFailed},
		{name: "processing_parent", parent: models.StatusProcessing, status: models.StatusProcessing},
		{name: "pending_parent", parent: models.StatusPending, status: models.StatusPending},
		{name: "uploaded_parent", parent: models.StatusUploaded, status: models.StatusPending},
		{name: "versioned_owner", parent: models.StatusCompleted, status: models.StatusProcessing, version: 1},
		{name: "admitted_queue", parent: models.StatusCompleted, status: models.StatusPending, activeQueue: true},
		{name: "cancelled_history", parent: models.StatusCompleted, status: models.StatusProcessing, cancelled: true},
		{name: "deleted_parent", parent: models.StatusCompleted, status: models.StatusProcessing, deletedParent: true},
	}
	parents := map[string]models.TranscriptionJob{}
	executions := map[string]models.TranscriptionJobExecution{}
	for _, tc := range cases {
		text, summary, individual := "retained output", "retained summary", "retained individual output"
		parent := models.TranscriptionJob{ID: tc.name, AudioPath: "never-read.wav", Status: tc.parent, Transcript: &text, Summary: &summary, IndividualTranscripts: &individual}
		require.NoError(t, db.Create(&parent).Error)
		oldTime := time.Now().Add(-30 * 24 * time.Hour)
		run := models.TranscriptionJobExecution{ID: tc.name, TranscriptionJobID: tc.name, Status: tc.status, RecoveryVersion: tc.version, StartedAt: oldTime, Transcript: &text, IndividualTranscripts: &individual}
		if tc.cancelled {
			run.CancelledAt = &oldTime
		}
		if tc.status == models.StatusCompleted || tc.status == models.StatusFailed {
			run.CompletedAt = &oldTime
		}
		require.NoError(t, db.Create(&run).Error)
		if tc.nullVersion {
			require.NoError(t, db.Model(&run).UpdateColumn("recovery_version", nil).Error)
		}
		if tc.activeQueue {
			require.NoError(t, db.Create(&models.TranscriptionQueueItem{ID: tc.name, TranscriptionJobID: tc.name, Status: models.QueueStatusPending}).Error)
		}
		if tc.deletedParent {
			require.NoError(t, db.Delete(&parent).Error)
		}
		require.NoError(t, db.Unscoped().First(&parent, "id = ?", tc.name).Error)
		require.NoError(t, db.First(&run, "id = ?", tc.name).Error)
		parents[tc.name], executions[tc.name] = parent, run
	}
	changed, err := repo.ReconcileLegacyOrphans(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), changed)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var parent models.TranscriptionJob
			require.NoError(t, db.Unscoped().First(&parent, "id = ?", tc.name).Error)
			require.Equal(t, parents[tc.name], parent)
			var run models.TranscriptionJobExecution
			require.NoError(t, db.First(&run, "id = ?", tc.name).Error)
			before := executions[tc.name]
			if !tc.changed {
				require.Equal(t, before, run)
				return
			}
			require.Equal(t, models.StatusFailed, run.Status)
			require.Equal(t, "interrupted", run.RecoveryState)
			require.NotNil(t, run.CompletedAt)
			require.NotNil(t, run.ErrorMessage)
			require.Equal(t, before.ActualParameters, run.ActualParameters)
			require.Equal(t, before.Transcript, run.Transcript)
			require.Equal(t, before.IndividualTranscripts, run.IndividualTranscripts)
			require.Equal(t, before.StartedAt, run.StartedAt)
			require.Equal(t, before.ProcessingDuration, run.ProcessingDuration)
			require.Zero(t, run.RecoveryVersion)
			require.Zero(t, run.OwnerGeneration)
		})
	}
	changed, err = repo.ReconcileLegacyOrphans(context.Background())
	require.NoError(t, err)
	require.Zero(t, changed)
}
