package transcription

import (
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/interfaces"
	"scriberr/internal/transcription/registry"
)

type combinedProgressAdapter struct {
	interfaces.TranscriptionAdapter
}

func TestPlannedStageKindsMatchEnabledRuntimeBoundaries(t *testing.T) {
	f := newStageTestFixture(t)
	u := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	u.recovery = f.store
	registry.RegisterTranscriptionAdapter(ModelWhisperX, adapters.NewWhisperXAdapter(t.TempDir()))
	registry.RegisterTranscriptionAdapter(ModelCanary, adapters.NewCanaryAdapter(t.TempDir()))
	combined := combinedProgressAdapter{newStagedOrchestrationAdapter(t)}
	registry.RegisterTranscriptionAdapter("queue-progress-combined", combined)
	native := newStagedOrchestrationAdapter(t)
	registry.RegisterTranscriptionAdapter("queue-progress-native", native)
	off := false
	for _, tc := range []struct {
		name, model, diarizer string
		params                models.WhisperXParams
		want                  []string
	}{
		{"Whisper inline", ModelWhisperX, ModelPyannote, models.WhisperXParams{Diarize: true, DiarizeModel: ModelPyannote, Device: "cuda", DiarizationDevice: "same"}, []string{"recognition", "alignment", "speaker_assignment"}},
		{"Whisper external", ModelWhisperX, ModelSortformer, models.WhisperXParams{Diarize: true, DiarizeModel: DiarizeSortformer}, []string{"recognition", "alignment", "diarize"}},
		{"no alignment", ModelWhisperX, ModelPyannote, models.WhisperXParams{NoAlign: true, Diarize: true, DiarizeModel: ModelPyannote, Device: "cuda", DiarizationDevice: "same"}, []string{"recognition", "speaker_assignment"}},
		{"no speakers", ModelWhisperX, "", models.WhisperXParams{}, []string{"recognition", "alignment"}},
		{"recognition only", ModelWhisperX, "", models.WhisperXParams{NoAlign: true}, []string{"recognition"}},
		{"Canary timestamps off", ModelCanary, ModelSortformer, models.WhisperXParams{NvidiaTimestamps: &off, Diarize: true, DiarizeModel: DiarizeSortformer}, []string{"recognition", "diarize"}},
		{"combined external", "queue-progress-combined", ModelPyannote, models.WhisperXParams{Diarize: true}, []string{"combined", "diarize"}},
		{"native speakers have no extra boundary", "queue-progress-native", "", models.WhisperXParams{Diarize: true, DiarizeModel: "native"}, []string{"recognition", "alignment"}},
		{"individual track has no speakers", ModelWhisperX, "", models.WhisperXParams{Diarize: false, ReturnCharAlignments: true}, []string{"recognition", "alignment"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, u.plannedStageKinds(tc.params, tc.model, tc.diarizer))
		})
	}
}
