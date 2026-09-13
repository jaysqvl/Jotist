package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

type adaptiveExecutionAdapter struct {
	*stageTestAdapter
	descriptor interfaces.StageDescriptor
}

func (a *adaptiveExecutionAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	return a.descriptor, recoveryStageKey(kind) == a.descriptor.Kind
}

func newAdaptiveExecutionAdapter(t *testing.T, kind string, desc interfaces.StageDescriptor) *adaptiveExecutionAdapter {
	t.Helper()
	desc.Kind, desc.SchemaVersion, desc.ImplementationVersion = kind, "transcript-result-v1", "synthetic-adaptive-v1"
	desc.ModelArtifacts = map[string]string{"synthetic-pinned-model": strings.Repeat("b", 40)}
	return &adaptiveExecutionAdapter{stageTestAdapter: newStageTestAdapter(t, "synthetic-"+kind), descriptor: desc}
}

func setAdaptiveExecutionPolicy(t *testing.T, f *stageTestFixture, r *recoveryStageContext, stages map[string]models.AdaptiveStagePolicy) {
	t.Helper()
	r.execution.ActualParameters.RecoveryMode = r.mode
	r.execution.ActualParameters.AdaptivePolicy = &models.AdaptiveExecutionPolicy{Stages: stages}
	require.NoError(t, f.db.Model(r.execution).Select("actual_parameters").Updates(r.execution).Error)
	r.gpu = gpuInventory{UUID: "GPU-synthetic-policy-test", Name: "Synthetic; no actual GPU inference", Driver: "test"}
}

func syntheticGPUOOM() error {
	return &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("synthetic capacity failure")}
}

func TestAdaptiveExecutionCheckpointUsesActualCandidateAndResumeRetainsIt(t *testing.T) {
	for _, tc := range []struct {
		name, mode, finalDevice, finalPrecision string
		finalBatch                              int
		finalWindow                             float64
	}{
		{"batch", RecoveryBatchManagement, "cuda", "float16", 4, 60},
		{"CPU", RecoveryCPUFallback, "cpu", "float32", 8, 60},
		{"window", RecoveryShorterWindows, "cuda", "float16", 8, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, tc.mode, true)
			desc, rule, _ := adaptivePolicyFixture()
			if tc.name != "batch" {
				desc.QualifiedBatches = nil
			}
			if tc.name == "window" {
				rule.AllowCPU = false
			}
			setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"recognition": rule})
			adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
			params := stageTestParams()
			params["device"], params["precision"], params["batch_size"], params["chunk_duration"] = "cuda", "float16", 8, 60
			original := copyStageParameters(params)
			calls := 0
			var successful map[string]interface{}
			run := func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				for _, key := range []string{"context", "context_terms", "hf_token"} {
					require.Equal(t, original[key], actual[key], "adaptive retry changed %s", key)
				}
				if calls <= 2 {
					return nil, syntheticGPUOOM()
				}
				require.Equal(t, tc.finalDevice, actual["device"])
				require.Equal(t, tc.finalPrecision, actual["precision"])
				require.Equal(t, tc.finalBatch, actual["batch_size"])
				require.Equal(t, tc.finalWindow, stageWindow(actual))
				successful = copyStageParameters(actual)
				return stageTestTranscript("Synthetic parseHTTP transcript; no ML was run."), nil
			}
			result, metadata, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, run)
			require.NoError(t, err)
			require.Equal(t, 3, calls)
			require.Equal(t, original, params, "adaptive choices must not rewrite requested parameters")
			require.Equal(t, tc.finalDevice, metadata["resolved_device"])
			require.Equal(t, tc.finalPrecision, result.Metadata["precision"])
			stage := f.stage(t, r.execution.ID, "recognition")
			require.Len(t, stage.Attempts, 3)
			last := stage.Attempts[2]
			require.Equal(t, privateSettingsHash(successful), last.SettingsHash)
			require.NotEqual(t, stage.Attempts[0].SettingsHash, last.SettingsHash)
			checkpoint, bytesBefore, err := f.store.SelectedCheckpoint(context.Background(), stage.ID)
			require.NoError(t, err)
			require.Equal(t, last.ID, checkpoint.ProducerAttemptID)
			require.Equal(t, last.CompatibilityKey, checkpoint.CompatibilityKey, "checkpoint identity must follow the actual candidate")
			require.NotEqual(t, stage.Attempts[0].CompatibilityKey, checkpoint.CompatibilityKey)
			var provenance repository.CheckpointProvenance
			require.NoError(t, json.Unmarshal([]byte(last.ProvenanceJSON), &provenance))
			require.Equal(t, last.SettingsHash, provenance.SettingsHash)

			f.resume(t, &r)
			resumed, reused, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, run)
			require.NoError(t, err)
			require.Equal(t, result, resumed)
			require.Equal(t, "true", reused["checkpoint_reused"])
			require.Equal(t, checkpoint.ID, reused["checkpoint_id"])
			require.Equal(t, 3, calls, "resuming an adapted checkpoint must not rerun the requested larger candidate")
			_, retained, err := f.store.SelectedCheckpoint(context.Background(), stage.ID)
			require.NoError(t, err)
			require.Equal(t, bytesBefore, retained)

			// Cross-execution reuse needs the selected actual settings, not a larger
			// requested configuration that only happened to recover to this output.
			exact := f.execution(t, RecoveryFixed, true)
			exact.gpu = r.gpu
			_, exactMetadata, err := runRecoverableStage(context.Background(), exact, "recognize", "recognition", adapter, f.input, successful, f.proc, run)
			require.NoError(t, err)
			require.Equal(t, checkpoint.ID, exactMetadata["checkpoint_id"])
			require.Equal(t, 3, calls)
			requested := f.execution(t, RecoveryFixed, true)
			requested.gpu = r.gpu
			_, requestedMetadata, err := runRecoverableStage(context.Background(), requested, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				require.Equal(t, "cuda", actual["device"])
				require.Equal(t, "float16", actual["precision"])
				require.Equal(t, 8, actual["batch_size"])
				require.Equal(t, float64(60), stageWindow(actual))
				return stageTestTranscript("Synthetic larger requested configuration."), nil
			})
			require.NoError(t, err)
			require.NotEqual(t, checkpoint.ID, requestedMetadata["checkpoint_id"])
			require.Equal(t, 4, calls, "lowering policy to Fixed must not borrow a smaller/CPU artifact")
		})
	}
}

func TestAdaptiveExecutionAlignmentReductionPreservesRecognitionCheckpointAndContext(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryBatchManagement, true)
	desc, rule, _ := adaptivePolicyFixture()
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"alignment": rule})
	asr := newAdaptiveExecutionAdapter(t, "recognition", interfaces.StageDescriptor{Recoverable: true, DevicePrecisions: map[string][]string{"cpu": {"float32"}}})
	recognitionParams := stageTestParams()
	asrCalls := 0
	recognize := func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		asrCalls++
		require.Equal(t, recognitionParams["context"], actual["context"])
		return stageTestTranscript("Pinned synthetic words: C++ parseHTTP PostgreSQL."), nil
	}
	_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", asr, f.input, recognitionParams, f.proc, recognize)
	require.NoError(t, err)
	asrStage := f.stage(t, r.execution.ID, "recognition")
	checkpoint, before, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	desc.PrecisionParameter, desc.DefaultPrecision, desc.BatchParameter = "alignment_precision", "float32", "alignment_batch_size"
	desc.QualifiedBatches, desc.WindowPolicy = []int{1, 2, 4}, nil
	aligner := newAdaptiveExecutionAdapter(t, "alignment", desc)
	alignmentParams := copyStageParameters(recognitionParams)
	alignmentParams["device"], alignmentParams["precision"], alignmentParams["alignment_precision"], alignmentParams["alignment_batch_size"] = "cuda", "float16", "float32", 4
	r.upstream = []repository.CheckpointInput{{ID: checkpoint.ID, ResultSHA256: checkpoint.ResultSHA256}}
	alignCalls := 0
	_, _, err = runRecoverableStage(context.Background(), r, "align", "alignment", aligner, f.input, alignmentParams, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		alignCalls++
		require.Equal(t, "float32", actual["alignment_precision"])
		require.Equal(t, recognitionParams["batch_size"], actual["batch_size"], "alignment must not change the requested ASR batch field")
		require.Equal(t, recognitionParams["chunk_duration"], actual["chunk_duration"])
		for _, key := range []string{"context", "context_terms", "hf_token"} {
			require.Equal(t, recognitionParams[key], actual[key])
		}
		if alignCalls <= 2 {
			require.Equal(t, 4, actual["alignment_batch_size"])
			return nil, syntheticGPUOOM()
		}
		require.Equal(t, 2, actual["alignment_batch_size"])
		var words interfaces.TranscriptResult
		require.NoError(t, json.Unmarshal(before, &words))
		words.WordSegments = []interfaces.TranscriptWord{{Start: 0, End: 2, Word: "Pinned synthetic words"}}
		return &words, nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, alignCalls)
	require.Equal(t, 1, asrCalls)
	_, retained, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	require.Equal(t, before, retained)
	aligned := f.stage(t, r.execution.ID, "alignment")
	require.Equal(t, 2, aligned.Attempts[2].BatchSize)
	var actualProvenance repository.CheckpointProvenance
	require.NoError(t, json.Unmarshal([]byte(aligned.Attempts[2].ProvenanceJSON), &actualProvenance))
	require.Equal(t, r.upstream, actualProvenance.Upstream, "alignment output must retain dependency on the exact recognition words")
}

func TestAdaptiveExecutionIndependentDiarizationCPUFallbackDoesNotRepeatASR(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryCPUFallback, true)
	desc, rule, _ := adaptivePolicyFixture()
	desc.QualifiedBatches, desc.WindowPolicy = nil, nil
	rule.AllowShorterWindows = false
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"diarization": rule})
	asr := newAdaptiveExecutionAdapter(t, "recognition", desc)
	params := stageTestParams()
	asrCalls, diarizerCalls := 0, 0
	recognize := func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		asrCalls++
		return stageTestTranscript("Synthetic unchanged recognition."), nil
	}
	_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", asr, f.input, params, f.proc, recognize)
	require.NoError(t, err)
	diarizer := newAdaptiveExecutionAdapter(t, "diarization", desc)
	speakerParams := copyStageParameters(params)
	speakerParams["device"] = "cuda"
	_, _, err = runRecoverableStage(context.Background(), r, "diarize", "diarization", diarizer, f.input, speakerParams, f.proc, func(actual map[string]interface{}) (*interfaces.DiarizationResult, error) {
		diarizerCalls++
		if diarizerCalls <= 2 {
			require.Equal(t, "cuda", actual["device"])
			return nil, syntheticGPUOOM()
		}
		require.Equal(t, "cpu", actual["device"])
		require.Equal(t, "float32", actual["precision"])
		return &interfaces.DiarizationResult{Segments: []interfaces.DiarizationSegment{{Start: 0, End: 2, Speaker: "speaker_0"}}, SpeakerCount: 1, Speakers: []string{"speaker_0"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, asrCalls)
	require.Equal(t, 3, diarizerCalls)
	f.resume(t, &r)
	_, metadata, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", asr, f.input, params, f.proc, recognize)
	require.NoError(t, err)
	require.Equal(t, "true", metadata["checkpoint_reused"])
	require.Equal(t, 1, asrCalls)
	stage := f.stage(t, r.execution.ID, "diarization")
	require.Equal(t, "cpu_fallback", stage.Attempts[2].Reason)
	require.Len(t, f.stage(t, r.execution.ID, "recognition").Attempts, 1)
}

func TestAdaptiveExecutionFailedReducedAttemptResumesItsSavedSettings(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryBatchManagement, true)
	desc, rule, _ := adaptivePolicyFixture()
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"recognition": rule})
	adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
	params := stageTestParams()
	params["device"], params["precision"], params["batch_size"], params["chunk_duration"] = "cuda", "float16", 8, 60
	calls := 0
	_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		if calls <= 2 {
			return nil, syntheticGPUOOM()
		}
		require.Equal(t, 4, actual["batch_size"])
		return nil, errors.New("synthetic non-capacity interruption after reduced candidate started")
	})
	require.Error(t, err)
	stage := f.stage(t, r.execution.ID, "recognition")
	require.Len(t, stage.Attempts, 3)
	require.Equal(t, 4, stage.Attempts[2].BatchSize)
	require.Nil(t, stage.CheckpointID)
	f.resume(t, &r)
	_, _, err = runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		require.Equal(t, "cuda", actual["device"])
		require.Equal(t, "float16", actual["precision"])
		require.Equal(t, 4, actual["batch_size"], "resume must not silently restart the larger originally requested batch")
		require.Equal(t, params["context"], actual["context"])
		return stageTestTranscript("Synthetic reduced attempt completed on resume."), nil
	})
	require.NoError(t, err)
	require.Equal(t, 4, calls)
	stage = f.stage(t, r.execution.ID, "recognition")
	require.Len(t, stage.Attempts, 4)
	require.Equal(t, "resume_same_settings", stage.Attempts[3].Reason)
	require.EqualValues(t, 2, stage.Attempts[3].OwnerGeneration)
	require.Equal(t, stage.Attempts[2].SettingsHash, stage.Attempts[3].SettingsHash)
	checkpoint, _, err := f.store.SelectedCheckpoint(context.Background(), stage.ID)
	require.NoError(t, err)
	require.Equal(t, stage.Attempts[3].CompatibilityKey, checkpoint.CompatibilityKey)
}

func TestAdaptiveExecutionFullSevenAttemptLadderCannotResetOnResume(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryShorterWindows, true)
	desc, rule, _ := adaptivePolicyFixture()
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"recognition": rule})
	adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
	params := stageTestParams()
	params["device"], params["precision"], params["batch_size"], params["chunk_duration"] = "cuda", "float16", 8, 60
	want := []struct {
		reason, device, precision string
		batch                     int
		window                    float64
	}{
		{"initial", "cuda", "float16", 8, 60},
		{"cleanup_retry", "cuda", "float16", 8, 60},
		{"smaller_batch", "cuda", "float16", 4, 60},
		{"smaller_batch", "cuda", "float16", 2, 60},
		{"cpu_fallback", "cpu", "float32", 2, 60},
		{"shorter_window", "cuda", "float16", 2, 30},
		{"shorter_window", "cuda", "float16", 2, 15},
	}
	calls := 0
	run := func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		require.Less(t, calls, len(want), "an eighth model invocation violates the persisted attempt budget")
		expected := want[calls]
		calls++
		require.Equal(t, expected.device, actual["device"])
		require.Equal(t, expected.precision, actual["precision"])
		require.Equal(t, expected.batch, actual["batch_size"])
		require.Equal(t, expected.window, stageWindow(actual))
		for _, key := range []string{"context", "context_terms", "hf_token"} {
			require.Equal(t, params[key], actual[key])
		}
		if actual["device"] == "cpu" {
			return nil, &interfaces.ResourceExecutionError{Kind: "host_out_of_memory", Err: errors.New("synthetic host capacity failure")}
		}
		return nil, syntheticGPUOOM()
	}
	_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, run)
	require.Error(t, err)
	require.Equal(t, 7, calls)
	stage := f.stage(t, r.execution.ID, "recognition")
	require.Len(t, stage.Attempts, 7)
	require.Nil(t, stage.CheckpointID, "failed attempts do not fabricate recoverable output")
	for i, expected := range want {
		attempt := stage.Attempts[i]
		require.Equal(t, i+1, attempt.AttemptNumber)
		require.Equal(t, expected.reason, attempt.Reason)
		require.Equal(t, expected.device, attempt.Device)
		require.Equal(t, expected.precision, attempt.Precision)
		require.Equal(t, expected.batch, attempt.BatchSize)
		require.Equal(t, expected.window, attempt.WindowSeconds)
		if i < 6 {
			require.Equal(t, models.RecoveryRetryable, attempt.State)
		} else {
			require.Equal(t, models.RecoveryFailed, attempt.State)
		}
	}
	require.Equal(t, "host_out_of_memory", stage.Attempts[4].ErrorCode)
	require.Equal(t, float64(2), stage.Attempts[5].OverlapSeconds)
	require.Equal(t, desc.WindowPolicy.StitchingVersion, stage.Attempts[6].StitchingVersion)
	f.resume(t, &r)
	_, _, err = runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, run)
	require.Error(t, err)
	require.Equal(t, 7, calls, "manual resume cannot reset the saved attempt budget")
	require.Len(t, f.stage(t, r.execution.ID, "recognition").Attempts, 7)
}
