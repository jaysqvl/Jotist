package transcription

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This adapter only prepares three small identity files. Inference is supplied
// by each test, so these integration tests need neither a model nor a GPU.
type stageTestAdapter struct {
	path         string
	capabilities interfaces.ModelCapabilities
	prepareCalls int
	installs     int
}

func (a *stageTestAdapter) GetCapabilities() interfaces.ModelCapabilities    { return a.capabilities }
func (a *stageTestAdapter) GetParameterSchema() []interfaces.ParameterSchema { return nil }
func (a *stageTestAdapter) ValidateParameters(map[string]interface{}) error  { return nil }
func (a *stageTestAdapter) GetModelPath() string                             { return a.path }
func (a *stageTestAdapter) GetEstimatedProcessingTime(interfaces.AudioInput) time.Duration {
	return time.Second
}
func (a *stageTestAdapter) IsReady(context.Context) bool {
	_, err := os.Stat(filepath.Join(a.path, "uv.lock"))
	return err == nil
}
func (a *stageTestAdapter) PrepareEnvironment(ctx context.Context) error {
	a.prepareCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.IsReady(ctx) {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(a.path, ".venv"), 0700); err != nil {
		return err
	}
	for _, name := range []string{"uv.lock", "pyproject.toml", ".venv/pyvenv.cfg"} {
		if err := os.WriteFile(filepath.Join(a.path, name), []byte("fixture-runtime-v1\n"), 0600); err != nil {
			return err
		}
	}
	a.installs++
	return nil
}

type stageTestFixture struct {
	db        *gorm.DB
	store     *repository.RecoveryRepository
	recording string
	input     interfaces.AudioInput
	proc      interfaces.ProcessingContext
}

func newStageTestFixture(t *testing.T) *stageTestFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}, &models.RecoveryCheckpoint{}, &models.RecoveryCheckpointDependency{}, &models.RecoveryDeletion{}))
	store, err := repository.NewRecoveryRepository(db, t.TempDir(), repository.RecoveryOptions{})
	require.NoError(t, err)
	recording := uuid.NewString()
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: recording, AudioPath: "fixture.wav", Status: models.StatusProcessing}).Error)
	return &stageTestFixture{
		db: db, store: store, recording: recording,
		input: interfaces.AudioInput{FilePath: "fixture.wav", Format: "wav", SampleRate: 16000, Channels: 1, Duration: 10 * time.Second},
		proc:  interfaces.ProcessingContext{JobID: recording, OutputDirectory: t.TempDir()},
	}
}

func (f *stageTestFixture) execution(t *testing.T, mode string, reuse bool) recoveryStageContext {
	t.Helper()
	deadline := time.Now().Add(time.Hour)
	execution := &models.TranscriptionJobExecution{
		ID: uuid.NewString(), TranscriptionJobID: f.recording, RecoveryVersion: 1,
		Status: models.StatusProcessing, RecoveryState: models.RecoveryRunning, OwnerGeneration: 1,
		StartedAt: time.Now(), DeadlineAt: &deadline,
	}
	require.NoError(t, f.db.Create(execution).Error)
	return recoveryStageContext{store: f.store, execution: execution, originalHash: digestBytes([]byte("original-audio")), preparedHash: digestBytes([]byte("prepared-audio")), mode: mode, reuse: reuse}
}

func (f *stageTestFixture) resume(t *testing.T, recovery *recoveryStageContext) {
	t.Helper()
	// The lifecycle suite tests admission itself. Here we simulate its completed
	// ownership handoff, then exercise the orchestrator against the retained rows.
	recovery.execution.OwnerGeneration++
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", recovery.execution.ID).Updates(map[string]interface{}{
		"owner_generation": recovery.execution.OwnerGeneration, "status": models.StatusProcessing, "recovery_state": models.RecoveryRunning,
	}).Error)
}

func (f *stageTestFixture) stage(t *testing.T, executionID, node string) models.RecoveryStage {
	t.Helper()
	var stage models.RecoveryStage
	require.NoError(t, f.db.Preload("Attempts", func(db *gorm.DB) *gorm.DB { return db.Order("attempt_number") }).Where("execution_id = ? AND node_key = ?", executionID, node).First(&stage).Error)
	return stage
}

func newStageTestAdapter(t *testing.T, id string) *stageTestAdapter {
	t.Helper()
	return &stageTestAdapter{path: t.TempDir(), capabilities: interfaces.ModelCapabilities{
		ModelID: id, ModelFamily: "test", Version: "1", Metadata: map[string]string{"revision": strings.Repeat("a", 40)},
	}}
}

func stageTestParams() map[string]interface{} {
	return map[string]interface{}{"device": "cpu", "precision": "float32", "batch_size": 1, "chunk_duration": 30, "context": "C++ service: parseHTTP uses PostgreSQL.", "context_terms": "parseHTTP\nPostgreSQL", "hf_token": "fixture-only-credential"}
}

func stageTestTranscript(text string) *interfaces.TranscriptResult {
	return &interfaces.TranscriptResult{Text: text, Language: "en", ModelUsed: "test-asr", Segments: []interfaces.TranscriptSegment{{Start: 0, End: 2, Text: text}}, WordSegments: []interfaces.TranscriptWord{{Start: 0, End: 1, Word: text}}}
}

func TestRecoverableStageRetainsASRAcrossDiarizationFailureAndResume(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryFixed, true)
	asr := newStageTestAdapter(t, "test-asr")
	diarizer := newStageTestAdapter(t, "test-diarizer")
	params := stageTestParams()
	asrCalls, diarizerCalls := 0, 0
	recognize := func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		asrCalls++
		return stageTestTranscript("C++ parseHTTP, unchanged."), nil
	}
	result, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", asr, f.input, params, f.proc, recognize)
	require.NoError(t, err)
	require.Equal(t, 1, asr.installs, "the runtime must be prepared before its first identity is captured")
	asrStage := f.stage(t, r.execution.ID, "asr")
	checkpoint, before, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	_, _, err = runRecoverableStage(context.Background(), r, "diarize", "diarization", diarizer, f.input, params, f.proc, func(map[string]interface{}) (*interfaces.DiarizationResult, error) {
		diarizerCalls++
		return nil, errors.New("fixture diarizer failed")
	})
	require.Error(t, err)
	require.Equal(t, models.RecoveryFailed, f.stage(t, r.execution.ID, "diarization").State)
	_, retained, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	require.Equal(t, before, retained)

	f.resume(t, &r)
	resumed, metadata, err := runRecoverableStage(context.Background(), r, "combined", "asr", asr, f.input, params, f.proc, recognize)
	require.NoError(t, err)
	require.Equal(t, result, resumed)
	require.Equal(t, "true", metadata["checkpoint_reused"])
	require.Equal(t, checkpoint.ID, metadata["checkpoint_id"])
	require.Equal(t, 1, asrCalls, "a failed downstream speaker stage must not cause another ASR invocation")
	require.Equal(t, 1, asr.installs)
	speakers, _, err := runRecoverableStage(context.Background(), r, "diarize", "diarization", diarizer, f.input, params, f.proc, func(map[string]interface{}) (*interfaces.DiarizationResult, error) {
		diarizerCalls++
		return &interfaces.DiarizationResult{Segments: []interfaces.DiarizationSegment{{Start: 0, End: 2, Speaker: "speaker_0"}}, SpeakerCount: 1, Speakers: []string{"speaker_0"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, speakers.SpeakerCount)
	require.Equal(t, 2, diarizerCalls)
	diarizerStage := f.stage(t, r.execution.ID, "diarization")
	require.Equal(t, models.RecoverySucceeded, diarizerStage.State)
	require.Len(t, diarizerStage.Attempts, 2)
	require.EqualValues(t, 1, diarizerStage.Attempts[0].OwnerGeneration)
	require.EqualValues(t, 2, diarizerStage.Attempts[1].OwnerGeneration)
	require.Equal(t, "resume_same_settings", diarizerStage.Attempts[1].Reason)
	require.Len(t, f.stage(t, r.execution.ID, "asr").Attempts, 1)
}

func TestRecoverableStageFreshArtifactsAndQualifiedReuse(t *testing.T) {
	f := newStageTestFixture(t)
	adapter := newStageTestAdapter(t, "pinned-test-asr")
	params := stageTestParams()
	calls := 0
	run := func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		return stageTestTranscript("Same model output."), nil
	}
	first := f.execution(t, RecoveryFixed, true)
	_, _, err := runRecoverableStage(context.Background(), first, "combined", "asr", adapter, f.input, params, f.proc, run)
	require.NoError(t, err)
	firstStage := f.stage(t, first.execution.ID, "asr")
	firstCheckpoint, firstBytes, err := f.store.SelectedCheckpoint(context.Background(), firstStage.ID)
	require.NoError(t, err)
	fresh := f.execution(t, RecoveryFixed, false)
	_, metadata, err := runRecoverableStage(context.Background(), fresh, "combined", "asr", adapter, f.input, params, f.proc, run)
	require.NoError(t, err)
	require.NotEqual(t, "true", metadata["checkpoint_reused"])
	freshStage := f.stage(t, fresh.execution.ID, "asr")
	freshCheckpoint, _, err := f.store.SelectedCheckpoint(context.Background(), freshStage.ID)
	require.NoError(t, err)
	require.Equal(t, firstStage.CompatibilityKey, freshStage.CompatibilityKey)
	require.NotEqual(t, firstCheckpoint.ID, freshCheckpoint.ID)
	require.NotEqual(t, firstCheckpoint.RelativePath, freshCheckpoint.RelativePath)
	require.Equal(t, 2, calls)

	third := f.execution(t, RecoveryFixed, true)
	_, metadata, err = runRecoverableStage(context.Background(), third, "combined", "asr", adapter, f.input, params, f.proc, run)
	require.NoError(t, err)
	require.Equal(t, freshCheckpoint.ID, metadata["checkpoint_id"])
	require.Equal(t, 2, calls)
	thirdStage := f.stage(t, third.execution.ID, "asr")
	require.Empty(t, thirdStage.Attempts, "reuse is selection of a saved artifact, not an inference attempt")
	require.NotNil(t, thirdStage.ReusedFromExecutionID)
	require.Equal(t, fresh.execution.ID, *thirdStage.ReusedFromExecutionID)
	selected, retained, err := f.store.SelectedCheckpoint(context.Background(), firstStage.ID)
	require.NoError(t, err)
	require.Equal(t, firstCheckpoint.ID, selected.ID)
	require.Equal(t, firstBytes, retained, "fresh runs must not replace an older run's selected output")
}

func TestRecoverableStageLevelOneRetryPreservesExactSettings(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryStageManagement, true)
	r.gpu = gpuInventory{UUID: "GPU-fixture", Name: "fixture GPU", Driver: "fixture"}
	adapter := newStageTestAdapter(t, "test-asr")
	params := stageTestParams()
	params["device"], params["precision"] = "auto", "float16"
	expected := copyStageParameters(params)
	expected["device"] = "cuda"
	calls := 0
	_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		require.Equal(t, expected, actual, "cleanup retry must not change precision, context, credentials, batch or window")
		if calls == 1 {
			return nil, &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("fixture OOM")}
		}
		return stageTestTranscript("Recovered on the same GPU."), nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "auto", params["device"], "resolving the plan must not modify the caller's request")
	stage := f.stage(t, r.execution.ID, "asr")
	require.Len(t, stage.Attempts, 2)
	require.Equal(t, models.RecoveryRetryable, stage.Attempts[0].State)
	require.Equal(t, "cuda_out_of_memory", stage.Attempts[0].ErrorCode)
	require.Equal(t, "cleanup_retry", stage.Attempts[1].Reason)
	for _, attempt := range stage.Attempts {
		require.Equal(t, "cuda", attempt.Device)
		require.Equal(t, "float16", attempt.Precision)
		require.Equal(t, 1, attempt.BatchSize)
		require.Equal(t, float64(30), attempt.WindowSeconds)
		require.Equal(t, privateSettingsHash(expected), attempt.SettingsHash)
	}
	require.Equal(t, models.RecoverySucceeded, stage.Attempts[1].State)
}

func TestRecoverableStageRetryBudgetsAndFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		failure    error
		wantCalls  int
	}{
		{"level_one_gpu", RecoveryStageManagement, &interfaces.GPUExecutionError{Kind: "cuda_runtime_error", Err: errors.New("fixture CUDA failure")}, 2},
		{"fixed_gpu", RecoveryFixed, &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("fixture OOM")}, 1},
		{"level_one_ordinary", RecoveryStageManagement, errors.New("CUDA out of memory in unstructured adapter prose"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, test.mode, true)
			r.gpu = gpuInventory{UUID: "GPU-fixture"}
			params := stageTestParams()
			params["device"], params["precision"] = "cuda", "float16"
			adapter := newStageTestAdapter(t, "test-asr")
			calls := 0
			fail := func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				require.Equal(t, params, actual)
				return nil, test.failure
			}
			_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, fail)
			require.Error(t, err)
			require.Equal(t, test.wantCalls, calls)
			stage := f.stage(t, r.execution.ID, "asr")
			require.Equal(t, models.RecoveryFailed, stage.State)
			require.Nil(t, stage.CheckpointID)
			require.Len(t, stage.Attempts, test.wantCalls)
			if test.name == "level_one_gpu" {
				f.resume(t, &r)
				_, _, err = runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, fail)
				require.Error(t, err)
				require.Equal(t, 3, calls, "manual resume must not reset the one-cleanup-retry budget")
				resumed := f.stage(t, r.execution.ID, "asr")
				require.Len(t, resumed.Attempts, 3)
				require.Equal(t, "resume_same_settings", resumed.Attempts[2].Reason)
				require.EqualValues(t, 2, resumed.Attempts[2].OwnerGeneration)
				for wantCalls := 4; wantCalls <= 7; wantCalls++ {
					f.resume(t, &r)
					_, _, err = runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, fail)
					require.Error(t, err)
					require.Equal(t, wantCalls, calls)
				}
				f.resume(t, &r)
				_, _, err = runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, fail)
				require.ErrorContains(t, err, "seven-attempt recovery budget")
				require.Equal(t, 7, calls, "the saved ceiling survives every ownership handoff")
				require.Len(t, f.stage(t, r.execution.ID, "asr").Attempts, 7)
			}
		})
	}
}

func TestRecoverableStageLegacyAutoUsesCPUFloat32WhenNoGPUExists(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, "", true)
	params := stageTestParams()
	params["device"], params["precision"], params["compute_type"], params["fp16"] = "auto", "float16", "float16", true
	adapter := newStageTestAdapter(t, "test-asr")
	calls := 0
	_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		require.Equal(t, cpuRetryParameters(params), actual)
		return stageTestTranscript("CPU legacy Auto."), nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "float16", params["precision"])
	require.Equal(t, true, params["fp16"])
	stage := f.stage(t, r.execution.ID, "asr")
	require.Len(t, stage.Attempts, 1)
	require.Equal(t, "cpu", stage.Attempts[0].Device)
	require.Equal(t, "float32", stage.Attempts[0].Precision)
}

func TestRecoverableStageLegacyAutoRetainsExplicitCPUFallback(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, "", true)
	r.gpu = gpuInventory{UUID: "GPU-fixture"}
	params := stageTestParams()
	params["device"], params["precision"], params["fp16"] = "auto", "float16", true
	adapter := newStageTestAdapter(t, "test-asr")
	calls := 0
	_, metadata, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		if calls == 1 {
			expected := copyStageParameters(params)
			expected["device"] = "cuda"
			require.Equal(t, expected, actual)
			return nil, &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("fixture OOM")}
		}
		require.Equal(t, cpuRetryParameters(params), actual)
		return stageTestTranscript("Legacy CPU retry."), nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "cuda_to_cpu", metadata["asr_device_fallback"])
	stage := f.stage(t, r.execution.ID, "asr")
	require.Len(t, stage.Attempts, 2)
	require.Equal(t, "cuda", stage.Attempts[0].Device)
	require.Equal(t, "float16", stage.Attempts[0].Precision)
	require.Equal(t, "cpu", stage.Attempts[1].Device)
	require.Equal(t, "float32", stage.Attempts[1].Precision)
	require.Equal(t, "legacy_cpu_fallback", stage.Attempts[1].Reason)
}

func TestRecoverableStageFixedCPURejectsHalfPrecisionWithoutConversion(t *testing.T) {
	for _, mode := range []string{RecoveryFixed, RecoveryStageManagement} {
		for _, device := range []string{"cpu", "auto"} {
			t.Run(mode+"_"+device, func(t *testing.T) {
				f := newStageTestFixture(t)
				r := f.execution(t, mode, true)
				params := stageTestParams()
				params["device"], params["precision"] = device, "float16"
				calls := 0
				_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", newStageTestAdapter(t, "test-asr"), f.input, params, f.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
					calls++
					return stageTestTranscript("must not run"), nil
				})
				require.ErrorContains(t, err, "will not be converted to FP32")
				require.Zero(t, calls)
				require.Empty(t, f.stage(t, r.execution.ID, "asr").Attempts)
			})
		}
	}
}

func TestRecoverableStageUnpinnedAuxiliaryPreventsCrossExecutionReuse(t *testing.T) {
	for _, auxiliary := range []string{"align_words", "diarize"} {
		t.Run(auxiliary, func(t *testing.T) {
			f := newStageTestFixture(t)
			adapter := newStageTestAdapter(t, "pinned-asr-with-mutable-auxiliary")
			params := stageTestParams()
			params[auxiliary] = true
			params["aligner_revision"] = "main"
			calls := 0
			run := func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				return stageTestTranscript("Combined stage."), nil
			}
			first := f.execution(t, RecoveryFixed, true)
			_, _, err := runRecoverableStage(context.Background(), first, "combined", "asr", adapter, f.input, params, f.proc, run)
			require.NoError(t, err)
			second := f.execution(t, RecoveryFixed, true)
			_, metadata, err := runRecoverableStage(context.Background(), second, "combined", "asr", adapter, f.input, params, f.proc, run)
			require.NoError(t, err)
			require.NotEqual(t, "true", metadata["checkpoint_reused"])
			require.Equal(t, 2, calls, "a pinned primary model does not pin its auxiliary alignment or speaker model")
			firstStage, secondStage := f.stage(t, first.execution.ID, "asr"), f.stage(t, second.execution.ID, "asr")
			require.NotEqual(t, *firstStage.CheckpointID, *secondStage.CheckpointID)
			f.resume(t, &second)
			_, metadata, err = runRecoverableStage(context.Background(), second, "combined", "asr", adapter, f.input, params, f.proc, run)
			require.NoError(t, err)
			require.Equal(t, "true", metadata["checkpoint_reused"])
			require.Equal(t, 2, calls, "an unqualified auxiliary must still permit exact same-execution resume")
		})
	}
}
