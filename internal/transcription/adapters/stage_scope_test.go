package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
)

func TestRecognitionStageSeparatesFinalSpeakerRequirements(t *testing.T) {
	local, err := NewLocalASRAdapter(t.TempDir(), "Qwen/Qwen3-ASR-0.6B-hf")
	require.NoError(t, err)
	for _, adapter := range []interfaces.StageParameterResolver{local, NewVibeVoiceBitNetAdapter(t.TempDir()), NewCanaryAdapter(t.TempDir()), NewWhisperXAdapter(t.TempDir())} {
		request := map[string]interface{}{"device": "cpu", "precision": "float32", "align_words": true, "no_align": false, "diarize": true, "diarize_model": "pyannote", "external_diarization_requested": true}
		resolved, err := adapter.ResolveStageParameters(interfaces.StageDescriptor{Kind: "recognition"}, request, nil)
		require.NoError(t, err)
		require.Equal(t, false, resolved["align_words"])
		require.Equal(t, false, resolved["diarize"])
		require.Equal(t, true, request["align_words"])
		require.Equal(t, true, request["diarize"])
		if bitnet, ok := adapter.(*VibeVoiceBitNetAdapter); ok {
			require.NoError(t, bitnet.ValidateParameters(resolved), "BitNet recognition must not demand downstream word alignment")
		}
	}
	native := recognitionOnlyParameters(map[string]interface{}{"diarize": true, "diarize_model": "native"})
	require.Equal(t, true, native["diarize"], "native speaker labels belong to recognition")
}
