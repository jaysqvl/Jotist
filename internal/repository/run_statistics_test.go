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

func TestRunStatisticsProjectEvidenceAndExcludeReusedResumedAndDeleted(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	job := models.TranscriptionJob{ID: "stats-recording", AudioPath: "unused.wav"}
	require.NoError(t, db.Create(&job).Error)
	secret := "synthetic-secret-context"
	params := models.WhisperXParams{ModelFamily: "qwen3_asr", Model: "Qwen/Qwen3-ASR-1.7B-hf", Device: "cuda", Diarize: true, DiarizeModel: "pyannote", DiarizationCheckpoint: "pyannote/speaker-diarization-3.1", HfToken: &secret, TranscriptionContext: &secret}
	meta := `{"text":"synthetic-private-transcript","metadata":{"resolved_device":"cuda","precision":"float16","diarization_device":"cuda","diarization_model":"pyannote/speaker-diarization-3.1","hf_token":"synthetic-private-token"}}`
	i64 := func(v int64) *int64 { return &v }
	measure := func(average, seconds int64) models.StageMeasurements {
		return models.StageMeasurements{ProcessPeakRSSBytes: i64(average + 1), ProcessAverageRSSBytes: i64(average), DevicePeakUsedBytes: i64(average + 2), DeviceAverageUsedBytes: i64(average), RSSSampledSeconds: float64(seconds), DeviceSampledSeconds: float64(seconds), Samples: 4, ElapsedSeconds: float64(seconds)}
	}
	for index, id := range []string{"fresh-a", "fresh-b", "reused", "resumed", "legacy", "failed", "old", "multitrack"} {
		started := now.Add(-time.Duration(index+1) * time.Hour)
		if id == "old" {
			started = now.AddDate(0, 0, -40)
		}
		completed := started.Add(time.Minute)
		run := models.TranscriptionJobExecution{ID: id, TranscriptionJobID: job.ID, StartedAt: started, CompletedAt: &completed, ProcessingDuration: i64(60000), Status: models.StatusCompleted, ActualParameters: params, Transcript: &meta}
		if id == "fresh-a" {
			run.ResourceMeasurements = []models.StageMeasurements{measure(4, 10)}
		}
		if id == "fresh-b" {
			run.ResourceMeasurements = []models.StageMeasurements{measure(8, 30)}
			run.ProcessingDuration = i64(120000)
		}
		if id == "resumed" {
			run.ResourceMeasurements = []models.StageMeasurements{measure(4, 10), measure(4, 10)}
		}
		if id == "legacy" {
			run.Transcript = nil
		}
		if id == "failed" {
			run.Status = models.StatusFailed
			run.Transcript = nil
		}
		if id == "multitrack" {
			multi := models.TranscriptionJob{ID: "multi-recording", AudioPath: "unused-multi.wav", IsMultiTrack: true}
			require.NoError(t, db.Create(&multi).Error)
			run.TranscriptionJobID = multi.ID
		}
		require.NoError(t, db.Create(&run).Error)
		if id == "legacy" {
			continue
		}
		stage := models.RecoveryStage{ID: "stage-" + id, ExecutionID: id, RecordingID: run.TranscriptionJobID, NodeKey: "recognition", Kind: "recognition", SchemaVersion: "1", CompatibilityKey: id, ProvenanceJSON: "{}", DurationSeconds: 3600, State: models.RecoverySucceeded}
		if id == "reused" {
			prior := "fresh-a"
			stage.ReusedFromExecutionID = &prior
		}
		require.NoError(t, db.Create(&stage).Error)
		if id == "fresh-a" {
			stageMeasurement, _ := json.Marshal(measure(100, 1000))
			require.NoError(t, db.Create(&models.RecoveryAttempt{ID: "stage-measured", StageID: stage.ID, ExecutionID: id, AttemptNumber: 1, State: models.RecoverySucceeded, Device: "cuda", Measurements: &models.StageMeasurements{}, StartedAt: started, CompletedAt: &completed}).Error)
			require.NoError(t, db.Model(&models.RecoveryAttempt{}).Where("id = ?", "stage-measured").Update("measurements", string(stageMeasurement)).Error)
		}
		if id == "failed" {
			require.NoError(t, db.Create(&models.RecoveryAttempt{ID: "failed-attempt", StageID: stage.ID, ExecutionID: id, AttemptNumber: 1, State: models.RecoveryFailed, Device: "cuda", ErrorCode: "adapter_failed", StartedAt: started, CompletedAt: &completed}).Error)
		}
	}
	deleted := models.TranscriptionJob{ID: "deleted", AudioPath: "unused-deleted.wav"}
	require.NoError(t, db.Create(&deleted).Error)
	require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: "deleted-run", TranscriptionJobID: deleted.ID, Status: models.StatusCompleted, StartedAt: now}).Error)
	require.NoError(t, db.Delete(&deleted).Error)
	result, err := ReadRunStatistics(context.Background(), db, 30, now)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.LibraryRecordings)
	require.Equal(t, 7, result.Summary.Runs)
	require.Equal(t, 6, result.Summary.Completed)
	require.Equal(t, 1, result.Summary.Failed)
	require.Equal(t, 1, result.Summary.Reused)
	require.Equal(t, 1, result.Summary.Resumed)
	require.Equal(t, 2, result.Summary.TimingMeasuredRuns)
	require.Equal(t, 90.0, *result.Summary.MedianHourSeconds)
	// Full invocations: (4*10 + 8*30 + 4*20) / 60. Stage averages
	// cannot be added a second time; their numeric peaks can supplement.
	require.InDelta(t, 6.0, *result.Summary.Memory.AverageGPUBytes, 0.001)
	require.Equal(t, int64(102), *result.Summary.Memory.PeakGPUBytes)
	require.Equal(t, 60.0, result.Summary.Memory.GPUSampledSeconds)
	require.Equal(t, 3, result.Summary.ResourceMeasuredRuns)
	unknown := 0
	for _, row := range result.Models {
		if row.RecognitionDevice == "unknown" {
			unknown += row.Runs
		}
		require.Equal(t, "pyannote/speaker-diarization-3.1", row.Diarizer)
	}
	require.Equal(t, 2, unknown, "CUDA requests without runtime evidence must remain unknown")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{secret, "synthetic-private-transcript", "synthetic-private-token", "unused.wav", "actual_parameters", "hf_token", "transcription_context"} {
		require.NotContains(t, string(encoded), private)
	}
	all, err := ReadRunStatistics(context.Background(), db, 0, now)
	require.NoError(t, err)
	require.Equal(t, 8, all.Summary.Runs)
}

func TestRunStatisticsRecoveryAndMissingMeasurementsRemainDistinct(t *testing.T) {
	result := models.RunStatistics{Models: []models.RunModelStatistics{}, Activity: []models.RunActivity{}, Stages: []models.RunStageStatistics{}, Failures: []models.RunFailureStatistics{}}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	aggregateRunStatistics(&result, []statisticsRun{{ID: "recovered", Status: "completed", Model: "same-model", RequestedDevice: "cuda", StartedAt: start, RuntimeJSON: `{"auto_token_split_windows":"1"}`}, {ID: "missing", Status: "completed", Model: "same-model", RequestedDevice: "cuda", StartedAt: start}}, []statisticsStage{{ID: "asr", ExecutionID: "recovered", Kind: "recognition", DurationSeconds: 3600}}, []statisticsAttempt{{StageID: "asr", ExecutionID: "recovered", AttemptNumber: 1, Device: "cuda", State: models.RecoveryFailed, StartedAt: start, CompletedAt: &end}, {StageID: "asr", ExecutionID: "recovered", AttemptNumber: 2, Device: "cpu", State: models.RecoverySucceeded, StartedAt: start, CompletedAt: &end}})
	require.Equal(t, 1, result.Summary.Recovered)
	require.Equal(t, 1, result.Summary.CPUFallback)
	require.Len(t, result.Models, 2, "observed CPU and unrecorded runtime cannot merge")
	require.Nil(t, result.Summary.Memory.PeakGPUBytes)
	require.Nil(t, result.Summary.Memory.AverageRAMBytes)
	require.Nil(t, result.Summary.MedianHourSeconds)
	require.Equal(t, 1, result.Stages[0].Retries)
}

func TestRunStatisticsAcceptsRuntimeStageKindsAndLastSuccessfulAttempt(t *testing.T) {
	result := models.RunStatistics{Models: []models.RunModelStatistics{}, Activity: []models.RunActivity{}, Stages: []models.RunStageStatistics{}, Failures: []models.RunFailureStatistics{}}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	later := start.Add(time.Minute)
	duration := int64(120000)
	aggregateRunStatistics(&result, []statisticsRun{{ID: "combined-run", Status: "completed", Model: "combined-model", Diarize: true, Diarizer: "checkpoint", RequestedDevice: "cuda", RequestedSpeakerDevice: "same", StartedAt: start, ProcessingDuration: &duration}}, []statisticsStage{{ID: "asr", ExecutionID: "combined-run", Kind: "combined", DurationSeconds: 3600}, {ID: "speakers", ExecutionID: "combined-run", Kind: "diarize", DurationSeconds: 3600}}, []statisticsAttempt{{StageID: "asr", ExecutionID: "combined-run", AttemptNumber: 1, Device: "cuda", Precision: "float16", State: models.RecoverySucceeded, StartedAt: start}, {StageID: "asr", ExecutionID: "combined-run", AttemptNumber: 2, Device: "cpu", Precision: "float32", State: models.RecoverySucceeded, StartedAt: later}, {StageID: "speakers", ExecutionID: "combined-run", AttemptNumber: 1, Device: "cuda", State: models.RecoverySucceeded, StartedAt: later}})
	require.Equal(t, "cpu", result.Models[0].RecognitionDevice)
	require.Equal(t, "cuda", result.Models[0].SpeakerDevice)
	require.Equal(t, "float32", result.Models[0].Precision)
	require.Equal(t, 120.0, *result.Summary.MedianHourSeconds)
	require.Equal(t, 1, result.Summary.CPUFallback)
	require.Equal(t, "combined", result.Stages[0].Kind, "combined timing is not recognition-only timing")
}
