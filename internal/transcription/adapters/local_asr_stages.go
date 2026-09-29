package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"scriberr/internal/transcription/interfaces"
)

const localASRAlignerRevision = "c07281df297b9905d24a508279258cccf987a064"

// Local ASR recognition and Qwen word alignment use different checkpoints and
// never need to be resident together. Exposing their existing process boundary
// lets recovery retain a completed recognition result when alignment fails.
func (a *LocalASRAdapter) Stages() []interfaces.StageDescriptor {
	recognitionArtifacts := map[string]string{}
	if a.spec.Revision != "" {
		recognitionArtifacts[a.spec.ID] = a.spec.Revision
	}
	if a.spec.NativeSpeakers {
		// Terminal native timing recovery uses the same pinned aligner contract
		// after unloading ASR, while retaining native speaker identities.
		recognitionArtifacts["Qwen/Qwen3-ForcedAligner-0.6B-hf"] = localASRAlignerRevision
	}
	stages := []interfaces.StageDescriptor{{
		Kind: "recognition", SchemaVersion: "transcript-result-v1", ImplementationVersion: "local-asr-recognition-v2",
		Recoverable: true, Cancellable: true, ModelArtifacts: recognitionArtifacts,
		DevicePrecisions:   map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "bfloat16", "float32"}},
		PrecisionParameter: "precision", MeasurementSupport: []string{"process_peak_rss", "structured_cuda_failure"},
		QualificationNotes: []string{"Recognition emits truthful native or audio-window segment bounds before any optional word aligner is loaded."},
	}}
	if a.spec.Engine != "granite_plus" {
		stages = append(stages, interfaces.StageDescriptor{
			Kind: "alignment", SchemaVersion: "transcript-result-v1", ImplementationVersion: "qwen3-forced-alignment-v3",
			Recoverable: true, Cancellable: true,
			ModelArtifacts:     map[string]string{"Qwen/Qwen3-ForcedAligner-0.6B-hf": localASRAlignerRevision},
			DevicePrecisions:   map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "bfloat16", "float32"}},
			PrecisionParameter: "precision", QualifiedBatches: []int{1},
			MeasurementSupport: []string{"process_peak_rss", "structured_cuda_failure"},
			QualificationNotes: []string{"Alignment consumes the durable recognition transcript and original audio without loading the recognizer."},
		})
	}
	return stages
}

func (a *LocalASRAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	for _, stage := range a.Stages() {
		if stage.Kind == kind {
			return stage, true
		}
	}
	return interfaces.StageDescriptor{}, false
}

// Override BaseAdapter's combined-stage metadata with the actual local ASR
// boundary exposed above.
func (a *LocalASRAdapter) GetCapabilities() interfaces.ModelCapabilities {
	capabilities := a.BaseAdapter.GetCapabilities()
	metadata := make(map[string]string, len(capabilities.Metadata))
	for key, value := range capabilities.Metadata {
		metadata[key] = value
	}
	encoded, _ := json.Marshal(a.Stages())
	metadata["adaptive_stages"] = string(encoded)
	capabilities.Metadata = metadata
	return capabilities
}

func (a *LocalASRAdapter) ResolveStageParameters(stage interfaces.StageDescriptor, params map[string]interface{}, _ []byte) (map[string]interface{}, error) {
	if _, ok := a.RecoveryStage(stage.Kind); !ok {
		return nil, fmt.Errorf("unsupported local ASR stage %q", stage.Kind)
	}
	resolved := make(map[string]interface{}, len(params)+2)
	for key, value := range params {
		resolved[key] = value
	}
	if stage.Kind == "recognition" {
		// Alignment is a later durable stage. External diarization still sees the
		// aligned final result, while recognition itself emits coarse bounds.
		resolved = recognitionOnlyParameters(params)
	}
	return resolved, nil
}

func (a *LocalASRAdapter) RunStage(ctx context.Context, stage interfaces.StageDescriptor, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	known, ok := a.RecoveryStage(stage.Kind)
	if !ok || stage.SchemaVersion != known.SchemaVersion || stage.ImplementationVersion != known.ImplementationVersion {
		return nil, fmt.Errorf("unsupported local ASR stage contract")
	}
	resolved, err := a.ResolveStageParameters(stage, params, upstream)
	if err != nil {
		return nil, err
	}
	if stage.Kind == "recognition" {
		result, err := a.Transcribe(ctx, input, resolved, procCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if stage.Kind != "alignment" {
		return nil, fmt.Errorf("unsupported local ASR stage %q", stage.Kind)
	}
	var result interfaces.TranscriptResult
	if json.Unmarshal(upstream, &result) != nil {
		return nil, fmt.Errorf("invalid local ASR recognition checkpoint")
	}
	if !a.GetBoolParameter(resolved, "align_words") || len(result.WordSegments) > 0 {
		return json.Marshal(&result)
	}
	return a.runAlignmentStage(ctx, input, resolved, procCtx, upstream)
}

func (a *LocalASRAdapter) runAlignmentStage(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	if err := a.ValidateAudioInput(input); err != nil {
		return nil, localASRDiagnostic("worker_input_invalid", err)
	}
	if err := a.ValidateParameters(params); err != nil {
		return nil, localASRDiagnostic("worker_configuration_invalid", err)
	}
	if err := a.PrepareEnvironment(ctx); err != nil {
		return nil, err
	}
	return runQwenAlignmentWorker(ctx, a.envPath, input, params, procCtx, upstream, a.spec.ID, a.CreateDefaultMetadata(params))
}

var _ interfaces.StagedTranscriptionAdapter = (*LocalASRAdapter)(nil)
var _ interfaces.StageParameterResolver = (*LocalASRAdapter)(nil)
