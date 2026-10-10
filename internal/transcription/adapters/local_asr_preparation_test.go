package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestLocalASRSpeechDetectionContractIsAutomaticAndCohereOnly(t *testing.T) {
	for _, spec := range LocalASRModels() {
		adapter, err := NewLocalASRAdapter(t.TempDir(), spec.ID)
		require.NoError(t, err)
		contract := adapter.GetCapabilities().SpeechDetection
		require.Equal(t, spec.Engine == "cohere", contract != nil, spec.ID)
		requestDir := t.TempDir()
		_, _, err = adapter.buildRequest(interfaces.AudioInput{FilePath: "/fixture/full-audio.wav"}, nil, requestDir)
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(requestDir, "request.json"))
		require.NoError(t, err)
		var config map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &config))
		if spec.Engine != "cohere" {
			require.Nil(t, config["speech_detection"])
			require.Equal(t, "local-asr-recognition-v2", adapter.Stages()[0].ImplementationVersion)
			continue
		}
		var sent interfaces.SpeechDetectionConfig
		require.NoError(t, json.Unmarshal(config["speech_detection"], &sent))
		require.Equal(t, *contract, sent, "worker and checkpoint preparation must agree")
		require.Equal(t, contract.ModelSHA256, adapter.Stages()[0].ModelArtifacts["silero-vad/"+contract.PackageVersion])
		changed := *contract
		changed.Threshold = 0.9
		_, _, err = adapter.buildRequest(interfaces.AudioInput{FilePath: "/fixture/full-audio.wav", SpeechDetection: &changed}, nil, t.TempDir())
		require.Error(t, err, "mismatched preparation settings must fail")
	}
}

func TestLocalASRSpeechDetectionFailureRetainsPreparationPhaseWithoutPrivateText(t *testing.T) {
	err, fields := localASRWorkerDiagnostic([]byte(`{"diagnostic_code":"runtime_audio_preparation","phase":"audio_preparation","exception_class":"AudioPreparationError","error":"private token and transcript","resolved_device":"cuda"}`), nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private")
	require.Equal(t, "audio_preparation", fields["phase"])
	require.Equal(t, "AudioPreparationError", fields["exception_class"])
	require.Equal(t, "cuda", fields["device"])
}
