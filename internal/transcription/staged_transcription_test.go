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

type stagedOrchestrationAdapter struct {
	*stageTestAdapter
	descriptors   []interfaces.StageDescriptor
	onStage       func(interfaces.StageDescriptor, map[string]interface{}, []byte) ([]byte, error)
	combinedCalls int
}

func (a *stagedOrchestrationAdapter) GetSupportedModels() []string {
	return []string{"synthetic-staged"}
}
func (a *stagedOrchestrationAdapter) Transcribe(context.Context, interfaces.AudioInput, map[string]interface{}, interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	a.combinedCalls++
	return stageTestTranscript("Synthetic combined fallback."), nil
}
func (a *stagedOrchestrationAdapter) Stages() []interfaces.StageDescriptor { return a.descriptors }
func (a *stagedOrchestrationAdapter) RunStage(_ context.Context, desc interfaces.StageDescriptor, _ interfaces.AudioInput, params map[string]interface{}, _ interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	return a.onStage(desc, params, upstream)
}
func (a *stagedOrchestrationAdapter) ResolveStageParameters(desc interfaces.StageDescriptor, params map[string]interface{}, upstream []byte) (map[string]interface{}, error) {
	result := copyStageParameters(params)
	if desc.Kind == "alignment" {
		var saved struct {
			State struct {
				Schema string `json:"schema"`
				Batch  int    `json:"native_alignment_batch_size"`
			} `json:"recognition_state"`
		}
		if json.Unmarshal(upstream, &saved) != nil || saved.State.Schema != "synthetic-recognition-v1" {
			return nil, errors.New("synthetic missing recognition state")
		}
		result["alignment_precision"], result["alignment_batch_size"] = "float32", saved.State.Batch
	}
	return result, nil
}

func newStagedOrchestrationAdapter(t *testing.T) *stagedOrchestrationAdapter {
	t.Helper()
	return &stagedOrchestrationAdapter{stageTestAdapter: newStageTestAdapter(t, "synthetic-staged"), descriptors: []interfaces.StageDescriptor{
		{Kind: "recognition", SchemaVersion: "1", ImplementationVersion: "synthetic-recognition-v1", Recoverable: true, PrecisionParameter: "precision", DevicePrecisions: map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "float32"}}, ModelArtifacts: map[string]string{"synthetic-recognizer": strings.Repeat("a", 40)}},
		{Kind: "alignment", SchemaVersion: "1", ImplementationVersion: "synthetic-alignment-v1", Recoverable: true, PrecisionParameter: "alignment_precision", DefaultPrecision: "float32", BatchParameter: "alignment_batch_size", DevicePrecisions: map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}}, ModelArtifacts: map[string]string{"synthetic-aligner": strings.Repeat("b", 40)}},
	}}
}

func syntheticRecognitionState(t *testing.T) []byte {
	t.Helper()
	result := map[string]interface{}{"text": "Synthetic parseHTTP words.", "language": "en", "segments": []interfaces.TranscriptSegment{{Start: 0, End: 2, Text: "Synthetic parseHTTP words."}}, "recognition_state": map[string]interface{}{"schema": "synthetic-recognition-v1", "native_alignment_batch_size": 4, "source_chunks": []int{0, 1}, "word_tokens": []string{"Synthetic", "parseHTTP", "words."}}}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	return data
}

func TestStagedTranscriptionFailedAlignmentResumesRecognitionAndPreservesASRDevice(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryCPUFallback, true)
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"alignment": {AllowCPU: true, CPUPrecision: "float32", MinBatchSize: 1}})
	adapter := newStagedOrchestrationAdapter(t)
	params := stageTestParams()
	params["device"], params["precision"], params["timestamps"] = "cuda", "float16", true
	beforeParams := copyStageParameters(params)
	recognitionCalls, alignmentCalls := 0, 0
	var upstreamFirst []byte
	adapter.onStage = func(desc interfaces.StageDescriptor, actual map[string]interface{}, upstream []byte) ([]byte, error) {
		require.Equal(t, params["context"], actual["context"])
		require.Equal(t, params["context_terms"], actual["context_terms"])
		if desc.Kind == "recognition" {
			recognitionCalls++
			require.Empty(t, upstream)
			require.Equal(t, "cuda", actual["device"])
			require.Equal(t, "float16", actual["precision"])
			return syntheticRecognitionState(t), nil
		}
		alignmentCalls++
		require.Equal(t, "float32", actual["alignment_precision"])
		require.Equal(t, 4, actual["alignment_batch_size"], "resolver must use the native saved recognition batch")
		if upstreamFirst == nil {
			upstreamFirst = append([]byte(nil), upstream...)
		} else {
			require.Equal(t, upstreamFirst, upstream, "resumption and retry must use exactly the saved recognition artifact")
		}
		var preserved map[string]interface{}
		require.NoError(t, json.Unmarshal(upstream, &preserved))
		require.Equal(t, "synthetic-recognition-v1", preserved["recognition_state"].(map[string]interface{})["schema"])
		if alignmentCalls == 1 {
			return nil, errors.New("synthetic interrupted alignment")
		}
		if alignmentCalls <= 3 {
			require.Equal(t, "cuda", actual["device"])
			return nil, syntheticGPUOOM()
		}
		require.Equal(t, "cpu", actual["device"])
		aligned := stageTestTranscript(preserved["text"].(string))
		aligned.Metadata = map[string]string{"timestamp_source": "synthetic_forced_alignment"}
		return json.Marshal(aligned)
	}
	_, _, err := runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
	require.Error(t, err)
	require.Equal(t, 1, recognitionCalls)
	require.Equal(t, 1, alignmentCalls)
	recognition := f.stage(t, r.execution.ID, "recognition")
	checkpoint, originalBytes, err := f.store.SelectedCheckpoint(context.Background(), recognition.ID)
	require.NoError(t, err)
	f.resume(t, &r)
	result, metadata, err := runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
	require.NoError(t, err)
	require.Equal(t, 1, recognitionCalls, "failed alignment must not rerun recognition")
	require.Equal(t, 4, alignmentCalls)
	require.Zero(t, adapter.combinedCalls)
	require.Equal(t, "Synthetic parseHTTP words.", result.Text)
	require.Equal(t, "cuda", result.Metadata["resolved_device"], "CPU alignment must not relabel ASR execution")
	require.Equal(t, "float16", result.Metadata["precision"])
	require.Equal(t, "cuda", result.Metadata["recognition_device"])
	require.Equal(t, "cpu", result.Metadata["alignment_device"])
	require.Equal(t, "float32", result.Metadata["alignment_precision"])
	require.Equal(t, "cuda", metadata["resolved_device"])
	require.Equal(t, beforeParams, params)
	_, retained, err := f.store.SelectedCheckpoint(context.Background(), recognition.ID)
	require.NoError(t, err)
	require.Equal(t, originalBytes, retained)
	alignment := f.stage(t, r.execution.ID, "alignment")
	require.Len(t, alignment.Attempts, 4)
	require.Equal(t, "cpu_fallback", alignment.Attempts[3].Reason)
	var provenance repository.CheckpointProvenance
	require.NoError(t, json.Unmarshal([]byte(alignment.Attempts[3].ProvenanceJSON), &provenance))
	require.Equal(t, []repository.CheckpointInput{{ID: checkpoint.ID, ResultSHA256: checkpoint.ResultSHA256}}, provenance.Upstream)
	speakers := map[string]interface{}{"device": "cuda"}
	followResolvedASRDevice(models.WhisperXParams{Device: "cuda", Diarize: true, DiarizationDevice: "same"}, result, speakers)
	require.Equal(t, "cuda", speakers["device"], "speaker device following ASR must not follow the temporary CPU alignment fallback")
	f.resume(t, &r)
	reused, reusedMetadata, err := runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
	require.NoError(t, err)
	require.Equal(t, 1, recognitionCalls)
	require.Equal(t, 4, alignmentCalls)
	require.Equal(t, "true", reusedMetadata["checkpoint_reused"])
	require.Equal(t, "cuda", reused.Metadata["resolved_device"], "reading both saved stages must preserve the recognizer's device")
	require.Equal(t, "cpu", reused.Metadata["alignment_device"])
}

func TestStagedTranscriptionDisablingTimestampsSkipsAlignmentBoundary(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryFixed, true)
	adapter := newStagedOrchestrationAdapter(t)
	params := stageTestParams()
	params["timestamps"] = false
	calls := 0
	adapter.onStage = func(desc interfaces.StageDescriptor, _ map[string]interface{}, upstream []byte) ([]byte, error) {
		calls++
		require.Equal(t, "recognition", desc.Kind)
		require.Empty(t, upstream)
		return syntheticRecognitionState(t), nil
	}
	result, _, err := runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
	require.NoError(t, err)
	require.Equal(t, "Synthetic parseHTTP words.", result.Text)
	require.Equal(t, 1, calls)
	require.Zero(t, adapter.combinedCalls)
	stages, err := f.store.ListStages(context.Background(), r.execution.ID)
	require.NoError(t, err)
	require.Len(t, stages, 1)
	require.Equal(t, "recognition", stages[0].Kind)
	require.Empty(t, result.WordSegments, "disabled timestamp alignment does not invent word timings")
}

func TestStagedTranscriptionWithoutCheckpointStoreKeepsCombinedAdapterContract(t *testing.T) {
	adapter := newStagedOrchestrationAdapter(t)
	adapter.onStage = func(interfaces.StageDescriptor, map[string]interface{}, []byte) ([]byte, error) {
		t.Fatal("unstored execution must retain the adapter's combined path")
		return nil, nil
	}
	result, _, err := runRecoverableTranscription(context.Background(), recoveryStageContext{}, adapter, interfaces.AudioInput{}, stageTestParams(), interfaces.ProcessingContext{})
	require.NoError(t, err)
	require.Equal(t, "Synthetic combined fallback.", result.Text)
	require.Equal(t, 1, adapter.combinedCalls)
}
