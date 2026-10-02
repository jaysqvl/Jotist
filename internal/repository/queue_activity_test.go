package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"github.com/jaysqvl/Jotist/internal/models"
)

func TestQueueActivityShowsSharedWorkersAndExactStagesWithoutPrivateData(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.TranscriptionQueueItem{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	secret := "synthetic-secret-context-and-token"
	params := models.WhisperXParams{ModelFamily: "qwen3_asr", Model: "Qwen model", HfToken: &secret, TranscriptionContext: &secret}
	for _, id := range []string{"running", "retrying", "pending", "queued-only", "stale", "idle", "deleted"} {
		status := models.StatusProcessing
		if id == "pending" {
			status = models.StatusPending
		} else if id == "queued-only" || id == "idle" {
			status = models.StatusCompleted
		}
		title := id + " recording"
		require.NoError(t, db.Create(&models.TranscriptionJob{ID: id, Title: &title, AudioPath: "private-audio-path", Status: status, Parameters: params}).Error)
	}
	profile := "Queued preset"
	for index, row := range []struct {
		id, recording, status string
		position              int
	}{
		{"running-item", "running", "processing", 0}, {"next-2", "running", "queued", 2}, {"next-1", "running", "queued", 1},
		{"pending-item", "pending", "pending", 0}, {"queued-only-item", "queued-only", "queued", 1},
	} {
		require.NoError(t, db.Create(&models.TranscriptionQueueItem{ID: row.id, TranscriptionJobID: row.recording, Status: models.TranscriptionQueueStatus(row.status), Position: row.position, Parameters: params, ProfileName: &profile, QueuedAt: now.Add(time.Duration(index) * time.Minute)}).Error)
	}
	for _, row := range []struct{ id, recording, kind, state string }{
		{"run-a", "running", "recognition", models.RecoveryRunning}, {"run-b", "retrying", "alignment", models.RecoveryWaiting},
	} {
		require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: row.id, TranscriptionJobID: row.recording, Status: models.StatusProcessing, ActualParameters: params, Transcript: &secret, StartedAt: now.Add(-time.Minute)}).Error)
		number := 1
		if row.kind == "alignment" {
			number = 2
		}
		require.NoError(t, db.Create(&models.RecoveryStage{ID: "stage-" + row.id, RecordingID: row.recording, ExecutionID: row.id, NodeKey: row.kind, Kind: row.kind, StageNumber: number, StageTotal: 3, State: row.state, ProvenanceJSON: secret, UpdatedAt: now}).Error)
	}
	retryAt := now.Add(time.Minute)
	require.NoError(t, db.Create(&models.RecoveryAttempt{ID: "attempt-b", StageID: "stage-run-b", ExecutionID: "run-b", State: models.RecoveryWaiting, AttemptNumber: 1, RetryAt: &retryAt, Reason: secret, ErrorCode: secret}).Error)
	// A legacy owner must not inherit the profile or execution of its future run.
	require.NoError(t, db.Create(&models.TranscriptionQueueItem{ID: "retry-future", TranscriptionJobID: "retrying", Status: models.QueueStatusQueued, Parameters: params, ProfileName: &profile}).Error)
	require.NoError(t, db.Delete(&models.TranscriptionJob{ID: "deleted"}).Error)
	snapshot := models.QueueRuntimeSnapshot{CapturedAt: now, Workers: 2, Running: map[string]models.QueueRunningSnapshot{
		"running": {QueueItemID: "running-item", ExecutionID: "run-a"}, "retrying": {ExecutionID: "run-b"},
	}}
	result, err := ReadQueueActivity(context.Background(), db, snapshot)
	require.NoError(t, err)
	require.Equal(t, 2, result.BusyWorkers)
	require.Equal(t, 2, result.WaitingRecordings)
	require.Equal(t, 4, result.QueuedRuns)
	require.Len(t, result.Recordings, 5)
	require.True(t, result.Recordings[0].HasWorker)
	require.True(t, result.Recordings[1].HasWorker)
	entries := map[string]models.QueueActivityEntry{}
	for _, entry := range result.Recordings {
		entries[entry.RecordingID] = entry
	}
	require.Equal(t, "running", entries["running"].State)
	require.Equal(t, "recognition", entries["running"].Stage)
	require.Equal(t, 1, entries["running"].StageNumber)
	require.Equal(t, 3, entries["running"].StageTotal, "not just the one stage already created")
	require.Equal(t, 2, entries["running"].QueuedRuns)
	require.Len(t, entries["running"].QueuedJobs, 2)
	require.Equal(t, "next-1", entries["running"].QueuedJobs[0].QueueItemID)
	require.Equal(t, "next-2", entries["running"].QueuedJobs[1].QueueItemID)
	require.Equal(t, now.Add(2*time.Minute), entries["running"].QueuedJobs[0].QueuedAt)
	require.Equal(t, "pending-item", entries["pending"].QueueItemID)
	require.Equal(t, now.Add(3*time.Minute), *entries["pending"].QueuedAt)
	require.Equal(t, "qwen3_asr", entries["running"].QueuedJobs[0].ModelFamily)
	require.Equal(t, profile, entries["running"].QueuedJobs[0].ProfileName)
	require.Empty(t, entries["pending"].QueuedJobs)
	require.Len(t, entries["queued-only"].QueuedJobs, 1)
	require.Equal(t, "retry_wait", entries["retrying"].State)
	require.Equal(t, 2, entries["retrying"].StageNumber)
	require.Equal(t, 3, entries["retrying"].StageTotal)
	require.Equal(t, retryAt, *entries["retrying"].RetryAt)
	require.Empty(t, entries["retrying"].ProfileName)
	require.Equal(t, "waiting_worker", entries["pending"].State)
	require.Equal(t, "queued", entries["queued-only"].State)
	require.Equal(t, "checking", entries["stale"].State)
	require.False(t, entries["stale"].HasWorker)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{secret, "private-audio-path", "parameters", "transcript", "error_code", "reason", "measurements"} {
		require.NotContains(t, string(encoded), private)
	}
	// A resource wait clears backoff only when the latest attempt has no retry date.
	require.NoError(t, db.Create(&models.RecoveryAttempt{ID: "attempt-b2", StageID: "stage-run-b", ExecutionID: "run-b", State: models.RecoveryWaiting, AttemptNumber: 2}).Error)
	result, err = ReadQueueActivity(context.Background(), db, snapshot)
	require.NoError(t, err)
	for _, entry := range result.Recordings {
		if entry.RecordingID == "retrying" {
			require.Equal(t, "resource_wait", entry.State)
			require.Nil(t, entry.RetryAt)
		}
	}
	// Completion may persist before cleanup releases the actual scheduler owner.
	require.NoError(t, db.Model(&models.RecoveryStage{}).Where("id = ?", "stage-run-b").Update("node_key", "track-synthetic.alignment").Error)
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", "running").Update("status", models.StatusCompleted).Error)
	snapshot.Running["running"] = models.QueueRunningSnapshot{QueueItemID: "running-item", ExecutionID: "run-a", Finishing: true}
	result, err = ReadQueueActivity(context.Background(), db, snapshot)
	require.NoError(t, err)
	for _, entry := range result.Recordings {
		if entry.RecordingID == "running" {
			require.Equal(t, "finishing", entry.State)
			require.True(t, entry.HasWorker)
			require.Empty(t, entry.Stage)
			require.Zero(t, entry.StageNumber)
		} else if entry.RecordingID == "retrying" {
			require.Equal(t, "track", entry.StageScope)
		}
	}
}

func TestQueueActivityDoesNotAttachAFutureRequestToALegacyPendingRun(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.TranscriptionQueueItem{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}))
	params := models.WhisperXParams{ModelFamily: "qwen3_asr", Model: "current"}
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "legacy", Status: models.StatusPending, Parameters: params}).Error)
	profile := "Future preset"
	require.NoError(t, db.Create(&models.TranscriptionQueueItem{ID: "future", TranscriptionJobID: "legacy", Status: models.QueueStatusQueued, Parameters: models.WhisperXParams{ModelFamily: "nvidia", Model: "parakeet"}, ProfileName: &profile}).Error)
	result, err := ReadQueueActivity(context.Background(), db, models.QueueRuntimeSnapshot{Workers: 1, Running: map[string]models.QueueRunningSnapshot{}})
	require.NoError(t, err)
	require.Len(t, result.Recordings, 1)
	entry := result.Recordings[0]
	require.Equal(t, "waiting_worker", entry.State)
	require.Empty(t, entry.QueueItemID)
	require.Empty(t, entry.ProfileName)
	require.Nil(t, entry.QueuedAt)
	require.Len(t, entry.QueuedJobs, 1)
	require.Equal(t, "future", entry.QueuedJobs[0].QueueItemID)
}
