package adapters

import (
	"encoding/json"

	"scriberr/internal/transcription/interfaces"
)

// Existing adapters expose only the process boundary they actually own. Device
// support is backed by their explicit runtime paths and public-audio CPU smoke
// tests. No batch/window equivalence is inferred from a parameter name.
func combinedRecoveryStages(cap interfaces.ModelCapabilities) []interfaces.StageDescriptor {
	stage := interfaces.StageDescriptor{Kind: "recognition", SchemaVersion: "transcript-result-v1", ImplementationVersion: "combined-adapter-v1", Recoverable: true, Cancellable: true,
		CombinedStages: []string{"recognition"}, PrecisionParameter: "precision",
		QualificationNotes: []string{"The adapter's complete subprocess output is the recoverable boundary. Optional internal work repeats with recognition.", "Devices and precisions reflect implemented runtime paths, not meeting accuracy or cross-precision equivalence measurements.", "Batch and shorter-window adaptation require separate qualification and are not inferred from existing controls."}}
	switch cap.ModelFamily {
	case "whisper":
		stage.PrecisionParameter = "compute_type"
		stage.DevicePrecisions = map[string][]string{"cpu": {"float32", "int8"}, "cuda": {"float16", "float32", "int8"}}
		stage.CombinedStages = []string{"recognition", "alignment", "diarization"}
	case "nvidia_parakeet":
		stage.DefaultPrecision = "float32"
		stage.PrecisionParameter = ""
		stage.DevicePrecisions = map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}}
	case "nvidia_canary_qwen", "qwen3_asr", "ibm_granite_speech", "cohere_transcribe", "ark_asr", "moss_asr", "mistral_voxtral":
		stage.DevicePrecisions = map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "bfloat16", "float32"}}
		if cap.ModelFamily != "nvidia_canary_qwen" && cap.Metadata["timestamp_source"] != "native_word_ends_previous_end_starts" {
			stage.CombinedStages = []string{"recognition", "alignment"}
		}
		if cap.Features["integrated_diarization"] {
			stage.CombinedStages = append(stage.CombinedStages, "diarization")
		}
	case "vibevoice-bitnet":
		stage.DefaultPrecision = "i2_s+i8_s"
		stage.PrecisionParameter = ""
		stage.DevicePrecisions = map[string][]string{"cpu": {"i2_s+i8_s"}}
		stage.CombinedStages = []string{"recognition", "alignment"}
	case "pyannote", "nvidia_sortformer", "diarizen", "suplime":
		stage.Kind = "diarization"
		stage.SchemaVersion = "diarization-result-v1"
		stage.CombinedStages = []string{"diarization"}
		stage.DefaultPrecision = "float32"
		stage.PrecisionParameter = ""
		stage.DevicePrecisions = map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}}
		if cap.ModelFamily == "suplime" {
			stage.DevicePrecisions["cuda"] = []string{"mixed_backend_fixed"}
			stage.DefaultPrecision = ""
		}
	default:
		return nil
	}
	return []interfaces.StageDescriptor{stage}
}

func (b *BaseAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	for _, stage := range combinedRecoveryStages(b.capabilities) {
		if stage.Kind == kind {
			return stage, true
		}
	}
	return interfaces.StageDescriptor{}, false
}

func withAdaptiveCapabilities(cap interfaces.ModelCapabilities) interfaces.ModelCapabilities {
	if cap.Metadata["adaptive_stages"] != "" {
		return cap
	}
	stages := combinedRecoveryStages(cap)
	if len(stages) == 0 {
		return cap
	}
	metadata := make(map[string]string, len(cap.Metadata)+1)
	for key, value := range cap.Metadata {
		metadata[key] = value
	}
	encoded, _ := json.Marshal(stages)
	metadata["adaptive_stages"] = string(encoded)
	cap.Metadata = metadata
	return cap
}
