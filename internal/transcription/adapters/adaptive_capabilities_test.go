package adapters

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/transcription/interfaces"
)

func TestAdaptiveCapabilitiesRespectActualStageBoundaries(t *testing.T) {
	for _, test := range []struct {
		family, timestamp string
		kinds             []string
	}{
		{"whisper", "", []string{"recognition", "alignment", "diarization"}},
		{"ibm_granite_speech", "native_word_ends_previous_end_starts", []string{"recognition"}},
		{"qwen3_asr", "forced_alignment", []string{"recognition", "alignment"}},
		{"pyannote", "", []string{"diarization"}},
	} {
		t.Run(test.family, func(t *testing.T) {
			original := interfaces.ModelCapabilities{ModelFamily: test.family, Metadata: map[string]string{"timestamp_source": test.timestamp}}
			cap := withAdaptiveCapabilities(original)
			var stages []interfaces.StageDescriptor
			require.NoError(t, json.Unmarshal([]byte(cap.Metadata["adaptive_stages"]), &stages))
			require.Len(t, stages, 1)
			require.Equal(t, test.kinds, stages[0].CombinedStages)
			require.Empty(t, stages[0].QualifiedBatches)
			require.Nil(t, stages[0].WindowPolicy)
			require.NotContains(t, original.Metadata, "adaptive_stages")
		})
	}
}

func TestAdaptiveCapabilitiesDoNotInventDevicesOrPrecision(t *testing.T) {
	for _, family := range []string{"openai_whisper", "unknown"} {
		require.Empty(t, combinedRecoveryStages(interfaces.ModelCapabilities{ModelFamily: family}))
	}
	parakeet := combinedRecoveryStages(interfaces.ModelCapabilities{ModelFamily: "nvidia_parakeet"})[0]
	require.Equal(t, []string{"float32"}, parakeet.DevicePrecisions["cuda"])
	require.Empty(t, parakeet.PrecisionParameter)
	bitnet := combinedRecoveryStages(interfaces.ModelCapabilities{ModelFamily: "vibevoice-bitnet"})[0]
	require.NotContains(t, bitnet.DevicePrecisions, "cuda")
	require.Equal(t, []string{"i2_s+i8_s"}, bitnet.DevicePrecisions["cpu"])
	require.Empty(t, bitnet.PrecisionParameter)
	suplime := combinedRecoveryStages(interfaces.ModelCapabilities{ModelFamily: "suplime"})[0]
	require.Equal(t, []string{"mixed_backend_fixed"}, suplime.DevicePrecisions["cuda"])
}
