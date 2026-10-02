package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/jaysqvl/Jotist/internal/transcription/registry"
)

// Keep this check at the production registration boundary. A new model cannot
// enter the server without declaring how it completes and what work is durable.
func TestRegisteredModelsDeclareResilienceContracts(t *testing.T) {
	registry.ClearRegistry()
	t.Cleanup(registry.ClearRegistry)
	registerAdapters(&config.Config{WhisperXEnv: t.TempDir()})

	transcription := registry.GetTranscriptionAdapters()
	expectedTranscription := []string{"whisperx", "parakeet", "canary", "canary_qwen", "voxtral", "openai_whisper", "vibevoice-bitnet"}
	for _, model := range adapters.LocalASRModels() {
		expectedTranscription = append(expectedTranscription, model.ID)
	}
	require.ElementsMatch(t, expectedTranscription, mapKeys(transcription))
	for id, adapter := range transcription {
		t.Run("transcription/"+id, func(t *testing.T) {
			capabilities := adapter.GetCapabilities()
			require.NotEmpty(t, capabilities.Metadata["resilience_contract"])
			if staged, ok := adapter.(interfaces.StagedTranscriptionAdapter); ok {
				require.NotEmpty(t, staged.Stages())
				for _, stage := range staged.Stages() {
					require.True(t, stage.Recoverable, "%s stage %s has no durable boundary", id, stage.Kind)
					require.NotEmpty(t, stage.ImplementationVersion)
				}
			}
			for _, parameter := range adapter.GetParameterSchema() {
				if parameter.Name == "max_new_tokens" {
					require.NotEmpty(t, capabilities.Metadata["generation_completion_policy"])
				}
			}
		})
	}

	diarization := registry.GetDiarizationAdapters()
	require.ElementsMatch(t, []string{"pyannote", "sortformer", "diarizen", "suplime"}, mapKeys(diarization))
	for id, adapter := range diarization {
		t.Run("diarization/"+id, func(t *testing.T) {
			require.NotEmpty(t, adapter.GetCapabilities().Metadata["resilience_contract"])
		})
	}
}

func mapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
