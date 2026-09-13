package transcription

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func TestSpeakerMemoryEstimatesCoverExactSelectableCheckpoints(t *testing.T) {
	for _, adapter := range []interfaces.DiarizationAdapter{
		adapters.NewPyAnnoteAdapter(t.TempDir()), adapters.NewSortformerAdapter(t.TempDir()),
		adapters.NewDiariZenAdapter(t.TempDir()), adapters.NewSUPlimeAdapter(t.TempDir()),
	} {
		capability := adapter.GetCapabilities()
		t.Run(capability.ModelID, func(t *testing.T) {
			catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{capability.ModelID: capability})
			metadata := catalog[capability.ModelID].Metadata
			var rows []map[string]string
			require.NoError(t, json.Unmarshal([]byte(metadata["variant_memory_estimates"]), &rows))
			require.NotEmpty(t, rows)
			models := map[string]bool{}
			for _, row := range rows {
				require.False(t, models[row["model"]], "duplicate checkpoint memory row")
				models[row["model"]] = true
				require.Equal(t, "unmeasured", row["estimate_status"])
				require.Contains(t, row["notes"], "Unmeasured")
				require.Contains(t, row["source"], "https://huggingface.co/")
				require.Empty(t, row["gpu_float16_vram_gb"], "none of these adapters casts the complete pipeline to FP16")
				gpu := row["gpu_float32_vram_gb"]
				if capability.ModelID == "suplime" {
					require.Empty(t, gpu, "mixed encoder execution must not claim full-FP32 GPU arithmetic")
					gpu = row["gpu_vram_gb"]
					require.Equal(t, "FP32 weights + FP16 encoder autocast", row["gpu_memory_precision"])
				}
				for _, value := range []string{row["cpu_float32_ram_gb"], gpu} {
					bounds := strings.Split(value, "–")
					require.Len(t, bounds, 2)
					low, err := strconv.ParseFloat(bounds[0], 64)
					require.NoError(t, err)
					high, err := strconv.ParseFloat(bounds[1], 64)
					require.NoError(t, err)
					require.Greater(t, low, float64(0))
					require.Greater(t, high, low)
				}
				if row["model"] == metadata["memory_model"] {
					require.Equal(t, row["cpu_float32_ram_gb"], metadata["cpu_float32_ram_gb"])
					require.Equal(t, row["gpu_memory_precision"], metadata["gpu_memory_precision"])
				}
			}
			require.True(t, models[metadata["memory_model"]], "top-level estimate must identify its checkpoint")
			for _, schema := range adapter.GetParameterSchema() {
				if schema.Name == "model" {
					for _, model := range schema.Options {
						require.True(t, models[model], "selectable checkpoint %s needs its own estimate", model)
					}
				}
			}
		})
	}
}

func TestSUPlimeDefaultMemoryUsesLargeCheckpoint(t *testing.T) {
	metadata := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{"suplime": {}})["suplime"].Metadata
	require.Equal(t, "rewayai/suplime-large", metadata["memory_model"])
	require.Equal(t, "6–12", metadata["gpu_vram_gb"])
	require.Equal(t, "7–13", metadata["cpu_float32_ram_gb"])
}

func TestParakeetMemoryDoesNotAdvertiseUnsupportedHalfPrecision(t *testing.T) {
	metadata := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{ModelParakeet: {}})[ModelParakeet].Metadata
	require.Equal(t, "float32", metadata["fixed_precision"])
	require.Equal(t, "float32", metadata["supported_precisions"])
	require.Empty(t, metadata["gpu_float16_vram_gb"])
	require.NotEmpty(t, metadata["gpu_float32_vram_gb"])
	require.Equal(t, "FP32 (TF32 math enabled)", metadata["gpu_memory_precision"])
}

func TestAlignmentMemoryTracksActualDevicePolicyAndUnknowns(t *testing.T) {
	for _, model := range adapters.LocalASRModels() {
		alignment := alignmentMemoryEstimate(model.ID)
		if model.Engine == "granite_plus" {
			require.Nil(t, alignment, "Granite Plus always returns native word timing")
			continue
		}
		require.Equal(t, "Qwen/Qwen3-ForcedAligner-0.6B-hf", alignment["model"])
		require.Equal(t, "same_as_asr", alignment["device_policy"])
		require.Equal(t, "after_asr", alignment["stage_order"])
		require.Equal(t, "unmeasured", alignment["estimate_status"])
		require.NotEmpty(t, alignment["gpu_float32_vram_gb"])
		require.NotEmpty(t, alignment["gpu_float16_vram_gb"])
	}
	bitnet := alignmentMemoryEstimate("vibevoice-bitnet")
	require.Equal(t, "cpu", bitnet["device_policy"])
	require.Equal(t, "float32", bitnet["fixed_precision"])
	require.NotEmpty(t, bitnet["cpu_float32_ram_gb"])
	require.Empty(t, bitnet["gpu_float32_vram_gb"], "unsupported GPU alignment must not be represented as zero or a hypothetical supported estimate")
	require.Empty(t, bitnet["gpu_float16_vram_gb"])
	whisper := alignmentMemoryEstimate(ModelWhisperX)
	require.Equal(t, "unknown", whisper["estimate_status"])
	require.Empty(t, whisper["cpu_float32_ram_gb"])
	require.Empty(t, whisper["gpu_float32_vram_gb"])
	require.Nil(t, alignmentMemoryEstimate(ModelOpenAI))
	require.Nil(t, alignmentMemoryEstimate("unknown-adapter"))
	require.Nil(t, alignmentMemoryEstimate(ModelParakeet), "Parakeet uses native timings")
	catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{"vibevoice-bitnet": {}})
	var fromJSON map[string]string
	require.NoError(t, json.Unmarshal([]byte(catalog["vibevoice-bitnet"].Metadata["alignment_memory_estimates"]), &fromJSON))
	require.Equal(t, bitnet, fromJSON)
}

func TestGranitePlusCatalogOmitsUnusedAlignmentStage(t *testing.T) {
	baseID, plusID := "ibm-granite/granite-speech-4.1-2b", "ibm-granite/granite-speech-4.1-2b-plus"
	catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{baseID: {}, plusID: {}})
	require.NotEmpty(t, catalog[baseID].Metadata["alignment_memory_estimates"], "base Granite still requires a separate aligner for word timing")
	_, hasAlignment := catalog[plusID].Metadata["alignment_memory_estimates"]
	require.False(t, hasAlignment, "the UI must not advertise an alignment stage that Granite Plus does not run")
}

func TestLegacyVoxtralAliasKeepsItsActualAlignmentStage(t *testing.T) {
	exactID := "mistralai/Voxtral-Mini-3B-2507"
	catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{ModelVoxtral: {}, exactID: {}})
	require.NotEmpty(t, catalog[ModelVoxtral].Metadata["alignment_memory_estimates"])
	require.Equal(t, catalog[exactID].Metadata["alignment_memory_estimates"], catalog[ModelVoxtral].Metadata["alignment_memory_estimates"], "the legacy alias executes the same local ASR and Qwen alignment runtime")
}

func TestCanaryCatalogReportsSeparateFP32AlignmentWithoutInventingMemory(t *testing.T) {
	metadata := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{ModelCanary: {}})[ModelCanary].Metadata
	var alignment map[string]string
	require.NoError(t, json.Unmarshal([]byte(metadata["alignment_memory_estimates"]), &alignment))
	require.Equal(t, "same_as_asr", alignment["device_policy"])
	require.Equal(t, "after_asr", alignment["stage_order"])
	require.Equal(t, "unknown", alignment["estimate_status"])
	require.Equal(t, "float32", alignment["fixed_precision"])
	require.Equal(t, "FP32", alignment["cpu_memory_precision"])
	require.Equal(t, "FP32", alignment["gpu_memory_precision"])
	for _, key := range []string{"cpu_float32_ram_gb", "gpu_float32_vram_gb", "gpu_float16_vram_gb", "gpu_vram_gb"} {
		require.Empty(t, alignment[key], "unmeasured CTC memory must not borrow recognition estimates")
	}
	require.Contains(t, alignment["notes"], "after recognition is released")
}
