package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/queue"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestRunPickerMetadataOnlyExposesRuntimeDisplayFields(t *testing.T) {
	raw := `{"text":"private synthetic text","metadata":{"resolved_device":"cpu","precision":"float32","diarization_resolved_device":"cuda","diarization_model":"synthetic-speakers","hf_token":"synthetic-secret","transcription_context":"private context","unreviewed_field":"private value"}}`
	metadata := runRuntimeMetadata(&raw)
	require.Equal(t, map[string]string{"resolved_device": "cpu", "precision": "float32", "diarization_resolved_device": "cuda", "diarization_model": "synthetic-speakers"}, metadata)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.NotContains(t, string(encoded), "synthetic-secret")
	for _, value := range []string{"not-json", `{"text":"legacy"}`, `{"metadata":{"precision":37}}`} {
		require.Empty(t, runRuntimeMetadata(&value))
	}
	require.Empty(t, runRuntimeMetadata(nil))
	mixed := `{"metadata":{"resolved_device":"cpu","private_counts":42,"precision":37}}`
	require.Equal(t, map[string]string{"resolved_device": "cpu"}, runRuntimeMetadata(&mixed))
}

func TestRunPickerUsesSubmittedProfileNameAndKeepsFailedRuns(t *testing.T) {
	f := newRecoveryResponseFixture(t, models.WhisperXParams{Device: "cuda", ComputeType: "float16"})
	profileName := "Original GPU meeting profile"
	item := models.TranscriptionQueueItem{TranscriptionJobID: f.job.ID, ProfileName: &profileName, Status: models.QueueStatusProcessing, Parameters: f.run.ActualParameters, ExecutionID: &f.run.ID}
	require.NoError(t, f.db.Create(&item).Error)
	require.NoError(t, f.db.Model(&f.run).Update("queue_item_id", item.ID).Error)
	require.NoError(t, f.lifecycle.Finish(context.Background(), f.run.ID, f.run.OwnerGeneration, models.StatusFailed, nil, "synthetic failure"))
	f.h.taskQueue = queue.NewTaskQueue(1, nil, f.h.jobRepo)
	f.h.taskQueue.SetTranscriptionQueueRepository(repository.NewTranscriptionQueueRepository(f.db))
	t.Cleanup(f.h.taskQueue.Stop)
	c, w := hfTokenRequest(http.MethodGet, "", f.userID)
	c.Params = gin.Params{{Key: "id", Value: f.job.ID}}
	f.h.ListJobRuns(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Runs []struct {
			ID          string `json:"id"`
			ProfileName string `json:"profile_name"`
			Status      string `json:"status"`
		} `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Runs, 1)
	require.Equal(t, f.run.ID, response.Runs[0].ID)
	require.Equal(t, profileName, response.Runs[0].ProfileName)
	require.Equal(t, "failed", response.Runs[0].Status)
}

func TestRunPickerRecoveryCountsAreBoundedNumericMetadata(t *testing.T) {
	raw := `{"metadata":{"auto_token_split_windows":"2","native_timing_retry_windows":"1","token_retries":"100001"}}`
	require.Equal(t, map[string]string{"auto_token_split_windows": "2", "native_timing_retry_windows": "1"}, runRuntimeMetadata(&raw))
	for _, count := range []string{"-1", "private content", "1.5", "99999999999999999999999999999"} {
		encoded, err := json.Marshal(map[string]interface{}{"metadata": map[string]string{"token_retries": count}})
		require.NoError(t, err)
		value := string(encoded)
		require.Empty(t, runRuntimeMetadata(&value))
	}
}

func TestRunRecoverySummaryKeepsDifferentStageDevicesSeparate(t *testing.T) {
	ordinary := summarizeRunAttempts([]models.RecoveryAttempt{
		{StageID: "recognition", AttemptNumber: 1, Device: "cuda", Reason: "initial"},
		{StageID: "diarization", AttemptNumber: 1, Device: "cpu", Reason: "initial"},
	})
	require.True(t, ordinary.EvidenceAvailable)
	require.False(t, ordinary.CPUFallback)
	require.Zero(t, ordinary.RetryCount)
	recovered := summarizeRunAttempts([]models.RecoveryAttempt{
		{StageID: "recognition", AttemptNumber: 1, Device: "cuda", Reason: "initial"},
		{StageID: "recognition", AttemptNumber: 2, Device: "cuda", Reason: "smaller_batch"},
		{StageID: "recognition", AttemptNumber: 3, Device: "cpu", Reason: "cpu_fallback"},
		{StageID: "diarization", AttemptNumber: 1, Device: "cpu", Reason: "initial"},
	})
	require.True(t, recovered.CPUFallback)
	require.Equal(t, 2, recovered.RetryCount)
	require.Equal(t, []string{"smaller_batch", "cpu_fallback"}, recovered.Reasons)
	unknown := summarizeRunAttempts([]models.RecoveryAttempt{{StageID: "recognition", AttemptNumber: 1, Device: "cpu", Reason: "initial"}})
	require.False(t, unknown.CPUFallback)
	require.Zero(t, unknown.RetryCount)
}

func TestRunRecoverySummarySeparatesWorkerActionsFromStageAttempts(t *testing.T) {
	summary := summarizeRunAttempts([]models.RecoveryAttempt{{StageID: "recognition", AttemptNumber: 1, Device: "cuda", Reason: "initial", Measurements: &models.StageMeasurements{WorkerRetryCount: 2, WorkerRecoveryActions: map[string]int{"token_window_split": 1, "decoder_budget_retry": 1}}}})
	require.Zero(t, summary.RetryCount)
	require.Equal(t, 2, summary.WorkerRetryCount)
	require.Equal(t, 1, summary.WorkerRecoveryActions["token_window_split"])
	require.False(t, summary.CPUFallback)
}
