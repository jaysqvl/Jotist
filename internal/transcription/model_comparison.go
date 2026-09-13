package transcription

import (
	"fmt"
	"math"
	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/interfaces"
)

// Published English WER percentages, not predictions for the user's recordings.
// Source: HF Open ASR Leaderboard, snapshot dated 2026-09-11 America/Vancouver.
// AMI-Cleaned and
// private conversational results are distinct datasets and never averaged here.
var publishedModelWER = map[string][2]string{
	"Qwen/Qwen3-ASR-0.6B-hf":                          {"9.33", "12.74"},
	"ibm-granite/granite-speech-5.0-470m-turboctc-nc": {"7.13", "13.42"},
	"mistralai/Voxtral-Mini-3B-2507":                  {"13.57", "16.00"},
	"mistralai/Voxtral-Mini-4B-Realtime-2602":         {"13.34", "17.45"},
	"microsoft/VibeVoice-ASR-HF":                      {"12.02", "14.62"},
	"Qwen/Qwen3-ASR-1.7B-hf":                          {"8.31", "12.14"},
	"ibm-granite/granite-speech-4.1-2b":               {"7.06", "14.19"},
	"ibm-granite/granite-speech-5.0-470m-turboctc":    {"7.72", "13.97"},
	"CohereLabs/cohere-transcribe-03-2026":            {"7.02", "13.46"},
	"Edge0/ARK-ASR-3B":                                {"7.89", "12.90"},
	"OpenMOSS-Team/MOSS-Transcribe-Diarize":           {"8.33", "13.40"},
	"OpenMOSS-Team/MOSS-Transcribe-preview-2B":        {"7.80", "14.47"},
	"nvidia/canary-1b-v2":                             {"13.03", "15.16"},
	"nvidia/canary-qwen-2.5b":                         {"7.91", "13.41"},
	"nvidia/parakeet-tdt-0.6b-v3":                     {"9.42", "13.44"},
	"large-v3":                                        {"13.63", "15.13"},
}

// Parameter counts include audio components as reported by the benchmark snapshot.
// These are used only for rough weight-size planning, never accuracy comparisons.
var planningParametersBillions = map[string]float64{
	"Qwen/Qwen3-ASR-1.7B-hf": 2.04, "Qwen/Qwen3-ASR-0.6B-hf": 0.78,
	"ibm-granite/granite-speech-4.1-2b": 2, "ibm-granite/granite-speech-4.1-2b-plus": 2,
	"ibm-granite/granite-speech-5.0-470m-turboctc": 0.47, "ibm-granite/granite-speech-5.0-470m-turboctc-nc": 0.47,
	"CohereLabs/cohere-transcribe-03-2026": 2, "Edge0/ARK-ASR-3B": 3.75,
	"OpenMOSS-Team/MOSS-Transcribe-Diarize": 0.91, "OpenMOSS-Team/MOSS-Transcribe-preview-2B": 2.42,
	"mistralai/Voxtral-Mini-3B-2507": 5, "mistralai/Voxtral-Mini-4B-Realtime-2602": 4,
	"nvidia/canary-1b-v2": 1, "nvidia/canary-qwen-2.5b": 2.5, "nvidia/parakeet-tdt-0.6b-v3": 0.6,
	"large-v3": 1.55,
}

func withModelComparisonMetadata(catalog map[string]interfaces.ModelCapabilities) map[string]interfaces.ModelCapabilities {
	for id, capability := range catalog {
		metadata := make(map[string]string, len(capability.Metadata)+8)
		for key, value := range capability.Metadata {
			metadata[key] = value
		}
		if id == ModelOpenAI {
			metadata["execution_location"] = "cloud"
			metadata["audio_destination"] = "OpenAI API"
			metadata["privacy_notes"] = "Uploads audio and any recognition prompt to OpenAI for transcription. Requires an OpenAI API key."
		} else if isLocalSpeechAdapter(id) {
			metadata["execution_location"] = "local"
			metadata["audio_destination"] = "Jotist server"
			metadata["privacy_notes"] = "Audio processing runs on your Jotist server. Model files may be downloaded. Optional summaries and chat use their own provider setting."
		}
		model := metadata["model_id"]
		if model == "" {
			// Legacy adapters execute a fixed checkpoint but predate model_id metadata.
			model = map[string]string{"canary": "nvidia/canary-1b-v2", "parakeet": "nvidia/parakeet-tdt-0.6b-v3"}[id]
		}
		if model == "" {
			model = capability.ModelID
		}
		if id == ModelWhisperX {
			model = "large-v3"
			metadata["benchmark_model"] = model
			metadata["memory_model"] = model
			metadata["variant_memory_estimates"] = whisperVariantMemoryEstimates()
		}
		if values, ok := publishedModelWER[model]; ok {
			metadata["benchmark_ami_wer"] = values[0]
			metadata["benchmark_conversational_wer"] = values[1]
			metadata["benchmark_date"] = "2026-09-11"
			metadata["benchmark_source"] = "https://huggingface.co/spaces/hf-audio/open_asr_leaderboard"
			metadata["benchmark_notes"] = "Published English benchmark WER; lower is better. Different audio, decoding, context and precision can change results. Not measured on your meetings."
		}
		if capability.MemoryRequirement > 0 && id != "openai_whisper" {
			// Catalog memory_mb values historically use MiB. Convert to decimal GB
			// and show headroom, not a precise-looking single unmeasured number.
			baseGB := float64(capability.MemoryRequirement) * 1024 * 1024 / 1e9
			metadata["cpu_float32_ram_gb"] = fmt.Sprintf("%.0f–%.0f", math.Ceil(baseGB), math.Ceil(baseGB*1.5))
			metadata["memory_estimate_notes"] = "Unmeasured planning range based on the adapter memory allowance with 50% headroom, for one FP32 CPU worker and short chunks. Loading, full-recording context, alignment and concurrent workers can exceed it."
		}
		if model == "large-v3" {
			metadata["cpu_float32_ram_gb"] = "10–16"
			metadata["memory_estimate_notes"] = "Unmeasured planning range for Whisper large-v3, batch size 1, CPU FP32 and short chunks. The approximately 1.55B-parameter weights alone are about 6.2 GB; runtime, audio and alignment need additional memory. Quantized CPU execution may use less."
		}
		if model == "Edge0/ARK-ASR-3B" {
			metadata["benchmark_model_alias"] = "AutoArk-AI/ARK-ASR-3B"
		}
		if model == "Qwen/Qwen3-ASR-1.7B-hf" {
			metadata["cpu_float32_ram_gb"] = "13–17"
			metadata["memory_estimate_notes"] = "Planning range. A 15-second CPU float32 smoke test including loading and word alignment peaked at 12.3 GiB (13.2 GB) on a Ryzen 7 5700G. Longer recordings and concurrent workers may need more."
		}
		if id == "sortformer" {
			metadata["benchmark_der"] = "17.80"
			metadata["benchmark_der_dataset"] = "AMI Test SDM"
			metadata["benchmark_source"] = "https://huggingface.co/nvidia/diar_streaming_sortformer_4spk-v2.1"
			metadata["benchmark_date"] = "2026-09-11"
			metadata["benchmark_notes"] = "Sortformer 2.1 model-card result with 30.4 s input-buffer latency, overlapping speech included, zero collar, and forced-alignment reference labels. Maximum four speakers. Other DER protocols are not directly comparable."
		}
		if id == ModelPyannote {
			metadata["benchmark_model"] = "pyannote/speaker-diarization-community-1"
			metadata["benchmark_der"] = "19.9"
			metadata["benchmark_der_dataset"] = "AMI SDM · Community-1"
			metadata["benchmark_source"] = "https://huggingface.co/pyannote/speaker-diarization-community-1"
			metadata["benchmark_date"] = "2026-09-11"
			metadata["benchmark_notes"] = "Community-1 publisher result: fully automatic, no forgiveness collar, overlapping speech included. Applies to Community-1, not legacy 3.1. The reference-label protocol is not asserted equivalent to Sortformer's forced-alignment labels; do not treat their DER values as a controlled comparison."
		}
		if parameters, ok := planningParametersBillions[model]; ok {
			metadata["weights_parameters_b"] = fmt.Sprintf("%g", parameters)
			metadata["gpu_float16_vram_gb"] = fmt.Sprintf("%.0f–%.0f", math.Ceil(parameters*2+2), math.Ceil(parameters*2+6))
			metadata["gpu_float32_vram_gb"] = fmt.Sprintf("%.0f–%.0f", math.Ceil(parameters*4+2), math.Ceil(parameters*4+6))
			metadata["gpu_memory_estimate_notes"] = "Unmeasured batch-1 short-chunk planning range: weight bytes (2 per parameter for FP16/BF16, 4 for FP32) plus 2–6 GB runtime headroom. Other GPU processes, long context and alignment may need more."
		}
		if model == "mistralai/Voxtral-Mini-3B-2507" {
			metadata["publisher_gpu_float16_vram_gb"] = "9.5"
			metadata["memory_source"] = "https://huggingface.co/mistralai/Voxtral-Mini-3B-2507"
			metadata["gpu_memory_estimate_notes"] += " The publisher separately reports about 9.5 GB for FP16/BF16; that is not a measured peak for this runtime."
		}
		if id == "vibevoice-bitnet" {
			metadata["cpu_ram_gb"] = metadata["cpu_float32_ram_gb"]
			delete(metadata, "cpu_float32_ram_gb")
			metadata["cpu_memory_precision"] = "quantized ASR + FP32 alignment"
			metadata["memory_estimate_notes"] = "Unmeasured 9–13 GB planning range for quantized BitNet ASR and a separate FP32 word aligner. The ASR process exits before alignment. Full VibeVoice WER does not apply to this compressed checkpoint."
			metadata["fixed_precision"] = "i2_s+i8_s"
			metadata["fixed_chunk_duration"] = "30"
		}
		addRuntimeMemoryMetadata(metadata, id)
		metadata["additional_benchmarks"] = additionalEvaluations(id, model)
		addMeetingRecommendation(metadata, id, model)
		capability.Metadata = metadata
		catalog[id] = capability
	}
	return catalog
}

// Keep unknown adapters unclassified rather than promising local processing.
func isLocalSpeechAdapter(id string) bool {
	switch id {
	case ModelWhisperX, ModelParakeet, ModelCanary, ModelCanaryQwen, ModelVoxtral,
		ModelPyannote, ModelSortformer, "vibevoice-bitnet", "diarizen", "suplime":
		return true
	}
	for _, spec := range adapters.LocalASRModels() {
		if id == spec.ID {
			return true
		}
	}
	return false
}
