package transcription

import (
	"encoding/json"
	"math"
	"testing"

	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func TestLanguageComparisonCoversCatalogWithoutBorrowingVariantScores(t *testing.T) {
	var snapshot languageComparisonSnapshot
	require.NoError(t, json.Unmarshal(languageComparisonJSON, &snapshot))
	byModel := make(map[string]modelLanguageComparison)
	for _, row := range snapshot.Models {
		require.NotContains(t, byModel, row.Model, "Exact checkpoints must be unique")
		require.NotEmpty(t, row.PublisherCoverage)
		require.Contains(t, row.PublisherSource, "https://")
		for _, result := range row.WER {
			require.NotEmpty(t, result.Datasets)
			sum := 0.0
			for _, value := range result.Datasets {
				require.GreaterOrEqual(t, value, 0.0)
				sum += value
			}
			require.Less(t, math.Abs(sum/float64(len(result.Datasets))-result.MeanWER), 0.006, "Rounded language means must retain their raw dataset evidence")
		}
		byModel[row.Model] = row
	}
	require.NotContains(t, byModel, "mistralai/Voxtral-Mini-4B-Realtime-2602")
	for _, spec := range adapters.LocalASRModels() {
		require.Contains(t, byModel, spec.ID)
		if languages := byModel[spec.ID].PublisherLanguages; len(languages) > 0 {
			for _, language := range spec.Languages {
				require.Contains(t, languages, language, "Offered language must match the exact publisher checkpoint: %s", spec.ID)
			}
		}
	}
	for _, model := range []string{"ibm-granite/granite-speech-4.1-2b", "ibm-granite/granite-speech-4.1-2b-plus", "microsoft/VibeVoice-ASR-BitNet"} {
		require.Empty(t, byModel[model].WER, "NAR, base, and full-precision checkpoints must not supply another variant's multilingual scores")
	}
	require.Contains(t, byModel["large-v3"].WER, "hi")
	require.Empty(t, byModel["small"].WER)
	require.Equal(t, []string{"en"}, byModel["small.en"].PublisherLanguages)
}

func TestLanguageComparisonAPIKeepsExactCheckpointAndProvenance(t *testing.T) {
	catalog := withModelComparisonMetadata(map[string]interfaces.ModelCapabilities{
		ModelWhisperX: {}, ModelParakeet: {}, ModelOpenAI: {},
		"plus": {Metadata: map[string]string{"model_id": "ibm-granite/granite-speech-4.1-2b-plus"}},
	})
	var whisper []modelLanguageComparison
	require.NoError(t, json.Unmarshal([]byte(catalog[ModelWhisperX].Metadata["language_comparisons"]), &whisper))
	require.Len(t, whisper, len(whisperCheckpointParameters))
	for _, row := range whisper {
		if row.Model != "large-v3" {
			require.Empty(t, row.WER)
		}
	}
	var parakeet []modelLanguageComparison
	require.NoError(t, json.Unmarshal([]byte(catalog[ModelParakeet].Metadata["language_comparisons"]), &parakeet))
	require.Len(t, parakeet, 1)
	require.Equal(t, "nvidia/parakeet-tdt-0.6b-v3", parakeet[0].Model)
	require.Equal(t, "2026-09-28", parakeet[0].Retrieved)
	require.Contains(t, parakeet[0].BenchmarkSource, parakeet[0].BenchmarkRevision)
	require.NotContains(t, parakeet[0].WER, "hi")
	require.Equal(t, "[]", catalog[ModelOpenAI].Metadata["language_comparisons"], "Cloud API must not inherit downloadable model results")
	require.Contains(t, catalog["plus"].Metadata["language_comparisons"], "5 languages")
	require.NotContains(t, catalog["plus"].Metadata["language_comparisons"], "mean_wer")
}
