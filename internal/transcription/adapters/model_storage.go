package adapters

import (
	"regexp"
	"strings"

	"github.com/jaysqvl/Jotist/internal/modelstorage"
)

// ModelStorageCatalog describes downloadable artifacts, not Python environments.
// Unregistered repositories remain visible but cannot be removed through Jotist.
func ModelStorageCatalog() []modelstorage.Model {
	var result []modelstorage.Model
	add := func(source, name, purpose string, resources ...string) {
		result = append(result, modelstorage.Model{Source: source, Name: name, Purpose: purpose, Resources: resources})
	}
	for _, model := range LocalASRModels() {
		add(model.ID, model.Name, "Recognition", model.ID)
	}
	for _, size := range NewWhisperXAdapter("").GetSupportedModels() {
		if size == "large" {
			size = "large-v3"
		}
		add("Systran/faster-whisper-"+size, "Whisper "+size, "Recognition", "whisperx")
	}
	add("Qwen/Qwen3-ForcedAligner-0.6B-hf", "Qwen3 word aligner", "Word timing", "@alignment")
	add("nvidia/canary-qwen-2.5b", "Canary-Qwen 2.5B", "Recognition", "canary_qwen")
	add("nvidia/parakeet-tdt-0.6b-v3", "Parakeet TDT 0.6B v3", "Recognition", "parakeet")
	add("nvidia/canary-1b-v2", "Canary 1B v2", "Recognition and word timing", "canary")
	add("nvidia/diar_streaming_sortformer_4spk-v2.1", "Sortformer v2.1", "Speakers", "sortformer", "@speakers")
	add(vibeBitNetModel, "VibeVoice ASR BitNet", "Recognition", "vibevoice-bitnet")
	for _, spec := range []struct{ source, name string }{
		{"pyannote/speaker-diarization-3.1", "Pyannote 3.1"},
		{"pyannote/speaker-diarization-community-1", "Pyannote Community-1"},
		{"BUT-FIT/diarizen-wavlm-large-s80-md-v2", "DiariZen Large s80 v2"},
		{"rewayai/suplime", "SUPlime"},
		{"rewayai/suplime-large", "SUPlime Large"},
	} {
		add(spec.source, spec.name, "Speakers", "@speakers", spec.source)
	}
	for _, source := range []string{"pyannote/segmentation", "pyannote/segmentation-3.0", "pyannote/embedding", "pyannote/wespeaker-voxceleb-resnet34-LM", "speechbrain/spkrec-ecapa-voxceleb"} {
		add(source, strings.SplitN(source, "/", 2)[1], "Speaker dependency", "@speakers", "@vad")
	}
	// Read the checked-in WhisperX defaults so adding a supported alignment
	// language also updates its storage ownership without another copied list.
	data, err := whisperxScripts.ReadFile("py/whisperx/vendor/whisperx/whisperx/alignment.py")
	if err != nil {
		panic(err)
	}
	block := strings.SplitN(string(data), "DEFAULT_ALIGN_MODELS_HF = {", 2)
	if len(block) == 2 {
		for _, match := range regexp.MustCompile(`["']\w+["']:\s*["']([^"']+/[^"']+)["']`).FindAllStringSubmatch(strings.SplitN(block[1], "}", 2)[0], -1) {
			add(match[1], strings.SplitN(match[1], "/", 2)[1], "Word timing", "@alignment", match[1])
		}
	}
	return result
}
