package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/transcription/interfaces"
)

func TestBuiltInTranscriptionResilienceMatrixIsExplicit(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		model  interfaces.TranscriptionAdapter
		stages []string
	}{
		{name: "whisperx", model: NewWhisperXAdapter(root), stages: []string{"recognition", "alignment", "speaker_assignment"}},
		{name: "parakeet", model: NewParakeetAdapter(root)},
		{name: "canary", model: NewCanaryAdapter(root), stages: []string{"recognition", "alignment"}},
		{name: "canary_qwen", model: NewCanaryQwenAdapter(root)},
		{name: "voxtral_legacy_alias", model: NewVoxtralAdapter(root), stages: []string{"recognition", "alignment"}},
		{name: "openai_whisper", model: NewOpenAIAdapter("")},
		{name: "vibevoice_bitnet", model: NewVibeVoiceBitNetAdapter(root), stages: []string{"recognition", "alignment"}},
	}
	for _, spec := range LocalASRModels() {
		adapter, err := NewLocalASRAdapter(root, spec.ID)
		require.NoError(t, err)
		stages := []string{"recognition", "alignment"}
		if spec.Engine == "granite_plus" {
			stages = []string{"recognition"}
		}
		tests = append(tests, struct {
			name   string
			model  interfaces.TranscriptionAdapter
			stages []string
		}{name: spec.ID, model: adapter, stages: stages})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NotEmpty(t, test.model.GetCapabilities().Metadata["resilience_contract"], "every production recognizer needs an explicit resilience contract")
			staged, ok := test.model.(interfaces.StagedTranscriptionAdapter)
			if len(test.stages) == 0 {
				require.False(t, ok, "single-process/native-timestamp adapters must not advertise invented subprocess boundaries")
			} else {
				require.True(t, ok)
				actual := make([]string, 0, len(staged.Stages()))
				for _, stage := range staged.Stages() {
					require.True(t, stage.Recoverable)
					actual = append(actual, stage.Kind)
				}
				require.Equal(t, test.stages, actual)
			}
			for _, parameter := range test.model.GetParameterSchema() {
				if parameter.Name == "max_new_tokens" {
					require.NotEmpty(t, test.model.GetCapabilities().Metadata["generation_completion_policy"], "a generated decoder needs an explicit completion policy")
				}
			}
		})
	}
}

func TestCanaryQwenAutoTokenContract(t *testing.T) {
	adapter := NewCanaryQwenAdapter(t.TempDir())
	for _, parameter := range adapter.GetParameterSchema() {
		if parameter.Name != "max_new_tokens" {
			continue
		}
		require.Equal(t, 0, parameter.Default)
		require.NotNil(t, parameter.Min)
		require.Zero(t, *parameter.Min)
		return
	}
	t.Fatal("Canary-Qwen max_new_tokens schema is missing")
}
