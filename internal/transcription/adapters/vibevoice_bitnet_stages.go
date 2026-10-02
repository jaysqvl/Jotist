package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
)

func (v *VibeVoiceBitNetAdapter) Stages() []interfaces.StageDescriptor {
	return []interfaces.StageDescriptor{
		{
			Kind: "recognition", SchemaVersion: "transcript-result-v1", ImplementationVersion: "vibevoice-bitnet-recognition-v1",
			Recoverable: true, Cancellable: true,
			ModelArtifacts:   map[string]string{vibeBitNetModel: vibeBitNetModelRevision},
			DevicePrecisions: map[string][]string{"cpu": {"i2_s+i8_s"}}, DefaultPrecision: "i2_s+i8_s",
			QualificationNotes: []string{"The CPU recognizer exits and its transcript commits before Qwen word alignment starts."},
		},
		{
			Kind: "alignment", SchemaVersion: "transcript-result-v1", ImplementationVersion: "qwen3-forced-alignment-v3",
			Recoverable: true, Cancellable: true,
			ModelArtifacts:   map[string]string{"Qwen/Qwen3-ForcedAligner-0.6B-hf": localASRAlignerRevision},
			DevicePrecisions: map[string][]string{"cpu": {"float32"}}, DefaultPrecision: "float32",
			QualificationNotes: []string{"Alignment consumes the durable BitNet recognition transcript and original audio."},
		},
	}
}

func (v *VibeVoiceBitNetAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	for _, stage := range v.Stages() {
		if stage.Kind == kind {
			return stage, true
		}
	}
	return interfaces.StageDescriptor{}, false
}

func (v *VibeVoiceBitNetAdapter) GetCapabilities() interfaces.ModelCapabilities {
	capabilities := v.BaseAdapter.GetCapabilities()
	metadata := make(map[string]string, len(capabilities.Metadata)+2)
	for key, value := range capabilities.Metadata {
		metadata[key] = value
	}
	encoded, _ := json.Marshal(v.Stages())
	metadata["adaptive_stages"] = string(encoded)
	metadata["resilience_contract"] = "durable_recognition_alignment"
	metadata["generation_completion_policy"] = "end_marker_required"
	capabilities.Metadata = metadata
	return capabilities
}

func (v *VibeVoiceBitNetAdapter) ResolveStageParameters(stage interfaces.StageDescriptor, params map[string]interface{}, _ []byte) (map[string]interface{}, error) {
	if _, ok := v.RecoveryStage(stage.Kind); !ok {
		return nil, fmt.Errorf("unsupported VibeVoice BitNet stage %q", stage.Kind)
	}
	resolved := copyAdapterParameters(params)
	if stage.Kind == "recognition" {
		resolved = recognitionOnlyParameters(params)
	} else {
		resolved["device"] = "cpu"
		resolved["precision"] = "float32"
	}
	return resolved, nil
}

func (v *VibeVoiceBitNetAdapter) RunStage(ctx context.Context, stage interfaces.StageDescriptor, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	known, ok := v.RecoveryStage(stage.Kind)
	if !ok || stage.SchemaVersion != known.SchemaVersion || stage.ImplementationVersion != known.ImplementationVersion {
		return nil, fmt.Errorf("unsupported VibeVoice BitNet stage contract")
	}
	resolved, err := v.ResolveStageParameters(stage, params, upstream)
	if err != nil {
		return nil, err
	}
	if stage.Kind == "recognition" {
		result, err := v.Transcribe(ctx, input, resolved, procCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	var recognition interfaces.TranscriptResult
	if json.Unmarshal(upstream, &recognition) != nil {
		return nil, fmt.Errorf("invalid VibeVoice BitNet recognition checkpoint")
	}
	if len(recognition.Segments) == 0 {
		return upstream, nil
	}
	alignmentEnvironment := filepath.Join(v.envPath, "alignment")
	if err := PrepareLocalASREnvironment(ctx, alignmentEnvironment); err != nil {
		return nil, fmt.Errorf("prepare BitNet word alignment: %w", err)
	}
	data, err := runQwenAlignmentWorker(ctx, alignmentEnvironment, input, resolved, procCtx, upstream, vibeBitNetModel, v.CreateDefaultMetadata(resolved))
	if err != nil {
		return nil, err
	}
	if _, err := validateAlignedTranscript(data); err != nil {
		return nil, err
	}
	return data, nil
}

var _ interfaces.StagedTranscriptionAdapter = (*VibeVoiceBitNetAdapter)(nil)
var _ interfaces.StageParameterResolver = (*VibeVoiceBitNetAdapter)(nil)
