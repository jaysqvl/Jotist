package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestSpeechDetectionKeepsFullAudioAndDoesNotDoubleProcessInternalVAD(t *testing.T) {
	input := interfaces.AudioInput{FilePath: "source.wav", Format: "wav", SampleRate: 16000, Channels: 1, Duration: time.Hour, Size: 500, Metadata: map[string]string{"fixture": "preserved"}}
	contract := &interfaces.SpeechDetectionConfig{Implementation: "test-vad", SampleRate: 16000, Threshold: 0.35}
	prepared, err := NewProcessingPipeline().ProcessAudio(context.Background(), input, interfaces.ModelCapabilities{SpeechDetection: contract})
	require.NoError(t, err)
	require.Equal(t, input.FilePath, prepared.FilePath)
	require.Equal(t, input.Duration, prepared.Duration)
	require.Equal(t, input.Size, prepared.Size)
	require.Equal(t, input.Metadata, prepared.Metadata)
	require.Equal(t, contract, prepared.SpeechDetection)
	prepared.SpeechDetection.Threshold = 0.8
	require.Equal(t, 0.35, contract.Threshold, "a job cannot modify the adapter contract")
	internal, err := NewProcessingPipeline().ProcessAudio(context.Background(), input, interfaces.ModelCapabilities{Features: map[string]bool{"vad": true}})
	require.NoError(t, err)
	require.Nil(t, internal.SpeechDetection, "WhisperX already performs internal VAD")
}

type failingPreparation struct{ required bool }

func (f failingPreparation) AppliesTo(interfaces.ModelCapabilities) bool { return true }
func (f failingPreparation) GetRequiredFormats() []string                { return []string{"wav"} }
func (f failingPreparation) Required() bool                              { return f.required }
func (f failingPreparation) Process(_ context.Context, input interfaces.AudioInput, _ interfaces.ModelCapabilities) (interfaces.AudioInput, error) {
	return input, errors.New("fixture")
}

func TestRequiredPreparationCannotSilentlyContinue(t *testing.T) {
	for _, required := range []bool{true, false} {
		p := &ProcessingPipeline{preprocessors: []interfaces.Preprocessor{failingPreparation{required}}}
		_, err := p.ProcessAudio(context.Background(), interfaces.AudioInput{}, interfaces.ModelCapabilities{})
		require.Equal(t, required, err != nil)
	}
}
