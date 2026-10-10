package transcription

import (
	"context"
	"strings"
	"testing"

	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestSpeechPreparationChangesCheckpointCompatibilityAndPreservesOldOutcome(t *testing.T) {
	f := newStageTestFixture(t)
	adapter := newStageTestAdapter(t, "pinned-test-asr")
	calls := 0
	run := func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		return stageTestTranscript("Retained output."), nil
	}
	first := f.execution(t, RecoveryFixed, true)
	_, _, err := runRecoverableStage(context.Background(), first, "recognition", "asr", adapter, f.input, stageTestParams(), f.proc, run)
	require.NoError(t, err)
	oldStage := f.stage(t, first.execution.ID, "asr")
	oldCheckpoint, oldBytes, err := f.store.SelectedCheckpoint(context.Background(), oldStage.ID)
	require.NoError(t, err)
	input := f.input
	input.SpeechDetection = &interfaces.SpeechDetectionConfig{Implementation: "speech-ranges-v1", PackageVersion: "pinned", ModelSHA256: strings.Repeat("a", 64), ContextPaddingMS: 500}
	second := f.execution(t, RecoveryFixed, true)
	_, metadata, err := runRecoverableStage(context.Background(), second, "recognition", "asr", adapter, input, stageTestParams(), f.proc, run)
	require.NoError(t, err)
	require.NotEqual(t, "true", metadata["checkpoint_reused"], "a completed pre-VAD result cannot satisfy VAD recognition")
	require.NotEqual(t, oldStage.CompatibilityKey, f.stage(t, second.execution.ID, "asr").CompatibilityKey)
	third := f.execution(t, RecoveryFixed, true)
	_, metadata, err = runRecoverableStage(context.Background(), third, "recognition", "asr", adapter, input, stageTestParams(), f.proc, run)
	require.NoError(t, err)
	require.Equal(t, "true", metadata["checkpoint_reused"])
	require.Equal(t, 2, calls)
	input.SpeechDetection.ContextPaddingMS = 750
	fourth := f.execution(t, RecoveryFixed, true)
	_, metadata, err = runRecoverableStage(context.Background(), fourth, "recognition", "asr", adapter, input, stageTestParams(), f.proc, run)
	require.NoError(t, err)
	require.NotEqual(t, "true", metadata["checkpoint_reused"], "changed VAD settings require a new recognition result")
	require.Equal(t, 3, calls)
	retained, retainedBytes, err := f.store.SelectedCheckpoint(context.Background(), oldStage.ID)
	require.NoError(t, err)
	require.Equal(t, oldCheckpoint.ID, retained.ID)
	require.Equal(t, oldBytes, retainedBytes)
	require.Equal(t, digestBytes([]byte("mono-pcm16-16000-v1")), audioPreprocessingHash(f.input), "unrelated preprocessing compatibility must stay stable")
}
