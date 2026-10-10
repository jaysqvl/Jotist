package modelstorage

import (
	"os"
	"path/filepath"
)

// DefaultOptions follows the cache locations used by the child runtimes. No
// location is created while auditing, and runtime/package folders are read-only.
func DefaultOptions(environmentRoot string, catalog []Model) Options {
	home, _ := os.UserHomeDir()
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	hfHome := envOr("HF_HOME", filepath.Join(cache, "huggingface"))
	hub := envOr("HF_HUB_CACHE", envOr("HUGGINGFACE_HUB_CACHE", filepath.Join(hfHome, "hub")))
	torch := envOr("TORCH_HOME", filepath.Join(cache, "torch"))
	options := Options{
		EnvironmentRoot: environmentRoot, HubRoots: []string{hub, filepath.Join(cache, "whisper"), envOr("PYANNOTE_CACHE", filepath.Join(torch, "pyannote"))},
		PackageRoot:        envOr("UV_CACHE_DIR", filepath.Join(cache, "uv")),
		OtherRuntimeRoots:  []string{filepath.Join(hfHome, "modules"), filepath.Join(torch, "hub")},
		OtherDownloadRoots: []string{envOr("HF_XET_CACHE", filepath.Join(hfHome, "xet")), envOr("HF_ASSETS_CACHE", filepath.Join(hfHome, "assets"))},
		Models:             catalog,
	}
	model := func(source, name, purpose string, resources ...string) Model {
		return Model{Source: source, Name: name, Purpose: purpose, Resources: resources}
	}
	nemo := filepath.Join(environmentRoot, "parakeet")
	for _, spec := range []struct{ source, name, file, resource, purpose string }{
		{"nvidia/parakeet-tdt-0.6b-v3", "Parakeet TDT 0.6B v3", "parakeet-tdt-0.6b-v3.nemo", "parakeet", "Recognition"},
		{"nvidia/canary-1b-v2", "Canary 1B v2", "canary-1b-v2.nemo", "canary", "Recognition and word timing"},
		{"nvidia/diar_streaming_sortformer_4spk-v2.1", "Sortformer v2.1", "diar_streaming_sortformer_4spk-v2.1.nemo", "sortformer", "Speakers"},
	} {
		paths := []string{spec.file, spec.file + ".tmp"}
		if spec.resource == "canary" {
			paths = append(paths, "canary-stage-assets")
		}
		options.Direct = append(options.Direct, DirectFiles{Model: model(spec.source, spec.name, spec.purpose, spec.resource), Root: nemo, Paths: paths})
	}
	options.Direct = append(options.Direct,
		DirectFiles{Model: model("microsoft/VibeVoice-ASR-BitNet", "VibeVoice ASR BitNet", "Recognition", "vibevoice-bitnet"), Root: filepath.Join(environmentRoot, "vibevoice-bitnet", "models"), Paths: []string{"vibeasr-vae-encoder-i8_s.gguf", "vibeasr-lm-i2_s-embed-q6_k.gguf", "vibeasr-vae-encoder-i8_s.gguf.tmp", "vibeasr-lm-i2_s-embed-q6_k.gguf.tmp"}},
		DirectFiles{Model: model("jotist/whisperx-vad", "WhisperX voice activity detection", "Speech detection", "@vad"), Root: torch, Paths: []string{"jotist-whisperx-vad"}},
		DirectFiles{Model: model("snakers4/silero-vad", "Silero voice activity detection", "Speech detection", "@vad"), Root: filepath.Join(torch, "hub"), Paths: []string{"snakers4_silero-vad_master", "snakers4_silero-vad_main"}},
	)
	// NeMo 3.0.0 uses its own copy when a Hub model has no single .nemo file.
	// The explicit override is used directly; its default includes the version.
	nemoCache := envOr("NEMO_CACHE_DIR", filepath.Join(home, ".cache", "torch", "NeMo", "NeMo_3.0.0"))
	options.OtherDownloadRoots = append(options.OtherDownloadRoots, nemoCache)
	options.Direct = append(options.Direct, DirectFiles{Model: model("nvidia/canary-qwen-2.5b", "Canary-Qwen 2.5B", "Recognition", "canary_qwen"), Root: nemoCache, Paths: []string{"hf_hub_cache/nvidia/canary-qwen-2.5b"}})
	options.Direct = append(options.Direct, AlignmentFiles(filepath.Join(torch, "hub", "checkpoints"))...)
	return options
}

// Filenames from the torchaudio 2.11.0 bundles used by vendored WhisperX.
// A custom WhisperX model_dir stores these same files directly in that root.
func AlignmentFiles(root string) []DirectFiles {
	var result []DirectFiles
	for _, spec := range []struct{ source, name, file string }{
		{"torchaudio/WAV2VEC2_ASR_BASE_960H", "English word aligner", "wav2vec2_fairseq_base_ls960_asr_ls960.pth"},
		{"torchaudio/VOXPOPULI_ASR_BASE_10K_FR", "French word aligner", "wav2vec2_voxpopuli_base_10k_asr_fr.pt"},
		{"torchaudio/VOXPOPULI_ASR_BASE_10K_DE", "German word aligner", "wav2vec2_voxpopuli_base_10k_asr_de.pt"},
		{"torchaudio/VOXPOPULI_ASR_BASE_10K_ES", "Spanish word aligner", "wav2vec2_voxpopuli_base_10k_asr_es.pt"},
		{"torchaudio/VOXPOPULI_ASR_BASE_10K_IT", "Italian word aligner", "wav2vec2_voxpopuli_base_10k_asr_it.pt"},
	} {
		result = append(result, DirectFiles{Model: Model{Source: spec.source, Name: spec.name, Purpose: "Word timing", Resources: []string{"@alignment"}}, Root: root, Paths: []string{spec.file, spec.file + ".partial"}})
	}
	return result
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func Referenced(model Model, parametersUse Use) bool { return overlaps(model.Resources, parametersUse) }
