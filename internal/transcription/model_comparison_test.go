package transcription

import (
	"encoding/json"
	"testing"

	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func TestModelComparisonDoesNotLeakBetweenVariants(t *testing.T) {
	original := map[string]string{"model_id": "ibm-granite/granite-speech-4.1-2b-plus"}
	catalog := map[string]interfaces.ModelCapabilities{
		ModelWhisperX: {Metadata: map[string]string{}},
		"plus":        {Metadata: original},
		"vibevoice-bitnet": {MemoryRequirement: 8192, Metadata: map[string]string{
			"model_id": "microsoft/VibeVoice-ASR-BitNet",
		}},
	}
	result := withModelComparisonMetadata(catalog)
	require.Equal(t, "large-v3", result[ModelWhisperX].Metadata["benchmark_model"])
	require.Equal(t, "large-v3", result[ModelWhisperX].Metadata["memory_model"])
	require.Empty(t, result["plus"].Metadata["benchmark_ami_wer"], "Plus must not inherit the base checkpoint benchmark")
	require.Empty(t, result["vibevoice-bitnet"].Metadata["benchmark_ami_wer"], "BitNet must not inherit full VibeVoice WER")
	require.Empty(t, result["vibevoice-bitnet"].Metadata["cpu_float32_ram_gb"], "Quantized ASR must not be labeled FP32")
	require.NotEmpty(t, result["vibevoice-bitnet"].Metadata["cpu_ram_gb"])
	require.Len(t, original, 1, "Enrichment must not mutate adapter metadata")
}

func TestSupplementalEvaluationsRemainSeparateFromSharedLeaderboard(t *testing.T) {
	var rows []publishedEvaluation
	require.NoError(t, json.Unmarshal(additionalBenchmarksJSON, &rows))
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.NotEmpty(t, row.Model)
		require.NotEmpty(t, row.Dataset)
		require.Contains(t, []string{"WER", "DER"}, row.Metric)
		require.Contains(t, []string{"publisher", "community"}, row.Provenance)
		require.Contains(t, row.Source, "https://")
		require.GreaterOrEqual(t, row.Value, float64(0))
	}
	result := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{
		"vibevoice-bitnet": {Metadata: map[string]string{"model_id": "microsoft/VibeVoice-ASR-BitNet"}},
		ModelOpenAI:        {},
		ModelWhisperX:      {},
		ModelPyannote:      {Metadata: map[string]string{"model_id": "pyannote/speaker-diarization-community-1"}},
	})
	require.Contains(t, result["vibevoice-bitnet"].Metadata["additional_benchmarks"], "21.36")
	require.Empty(t, result["vibevoice-bitnet"].Metadata["benchmark_ami_wer"], "AMI-IHM is not the shared AMI-Cleaned result")
	require.Equal(t, "[]", result[ModelOpenAI].Metadata["additional_benchmarks"])
	require.Empty(t, result[ModelOpenAI].Metadata["meeting_recommendation_rank"])
	require.Contains(t, result[ModelWhisperX].Metadata["additional_benchmarks"], `"model":"small"`)
	require.Contains(t, result[ModelWhisperX].Metadata["variant_memory_estimates"], `"model":"small"`)
	require.Equal(t, "pyannote/speaker-diarization-community-1", result[ModelPyannote].Metadata["benchmark_model"])
	require.Contains(t, result[ModelPyannote].Metadata["additional_benchmarks"], "pyannote/speaker-diarization-3.1")
}

func TestModelComparisonDistinguishesLocalWhisperFromOpenAIAPI(t *testing.T) {
	catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{
		ModelWhisperX:      {},
		ModelOpenAI:        {},
		"unknown-provider": {},
	})
	require.Equal(t, "local", catalog[ModelWhisperX].Metadata["execution_location"])
	require.Equal(t, "Scriberr server", catalog[ModelWhisperX].Metadata["audio_destination"])
	require.Equal(t, "cloud", catalog[ModelOpenAI].Metadata["execution_location"])
	require.Equal(t, "OpenAI API", catalog[ModelOpenAI].Metadata["audio_destination"])
	require.Empty(t, catalog[ModelOpenAI].Metadata["cpu_float32_ram_gb"])
	require.Empty(t, catalog[ModelOpenAI].Metadata["benchmark_ami_wer"], "Hosted Whisper must not inherit local large-v3 scores")
	require.Empty(t, catalog["unknown-provider"].Metadata["execution_location"])
}
