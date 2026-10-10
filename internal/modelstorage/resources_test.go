package modelstorage

import (
	"testing"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/stretchr/testify/require"
)

func TestEveryStageBoundaryProtectsSharedAuxiliaryModels(t *testing.T) {
	for _, test := range []struct{ kind, resource string }{
		{"combined", "@alignment"}, {"combined", "@vad"}, {"recognition", "@alignment"}, {"alignment", "@alignment"}, {"align", "@alignment"}, {"diarize", "@speakers"}, {"diarization", "@speakers"},
	} {
		t.Run(test.kind+test.resource, func(t *testing.T) {
			use := StageUse("recognizer", test.kind, "recording", map[string]interface{}{})
			require.True(t, overlaps([]string{test.resource}, use))
		})
	}
	use := StageUse("recognizer", "combined", "recording", map[string]interface{}{"diarize": true, "align_words": false})
	require.True(t, overlaps([]string{"@speakers"}, use))
	require.False(t, overlaps([]string{"@alignment"}, use))
}

func TestImmutableRequestsIdentifyCheckpointAndAutomaticDependencies(t *testing.T) {
	use := RequestUse(models.WhisperXParams{ModelFamily: "nvidia_parakeet", Model: "parakeet", Diarize: true, DiarizeModel: "diarizen", DiarizationCheckpoint: "BUT-FIT/diarizen-wavlm-large-s80-md-v2"}, "recording", "Title", "queued")
	for _, resource := range []string{"parakeet", "@alignment", "@speakers", "BUT-FIT/diarizen-wavlm-large-s80-md-v2"} {
		require.True(t, overlaps([]string{resource}, use), resource)
	}
	use = RequestUse(models.WhisperXParams{NoAlign: true, Diarize: false, Model: "CohereLabs/cohere-transcribe-03-2026", ModelFamily: "cohere_transcribe"}, "recording", "Title", "queued")
	require.False(t, overlaps([]string{"@alignment", "@speakers"}, use))
	use = RequestUse(models.WhisperXParams{Model: "model", ModelCacheOnly: true}, "recording", "Title", "queued")
	require.True(t, use.CachedOnly)
}
