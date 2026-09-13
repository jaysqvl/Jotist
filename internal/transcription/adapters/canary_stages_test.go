package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"scriberr/internal/transcription/interfaces"
)

func TestCanaryAlignmentResolvesNativeBatchAndOwnFP32Precision(t *testing.T) {
	a := NewCanaryAdapter(t.TempDir())
	stage, _ := a.RecoveryStage("alignment")
	params := map[string]interface{}{"precision": "float16", "batch_size": 1}
	upstream := []byte(`{"text":"preview","recognition_state":{"schema":"canary-native-recognition-v1","native_alignment_batch_size":17}}`)
	resolved, err := a.ResolveStageParameters(stage, params, upstream)
	require.NoError(t, err)
	require.Equal(t, "float32", resolved["alignment_precision"])
	require.Equal(t, 17, resolved["alignment_batch_size"])
	require.NotContains(t, params, "alignment_precision")
	resolved["alignment_batch_size"] = 1
	resolved, err = a.ResolveStageParameters(stage, resolved, upstream)
	require.NoError(t, err)
	require.Equal(t, 1, resolved["alignment_batch_size"])
	_, err = a.ResolveStageParameters(stage, params, []byte(`{"text":"only text cannot restore native merge"}`))
	require.Error(t, err)
	require.Equal(t, []int{1}, stage.QualifiedBatches)
	require.Equal(t, []int{20, 10}, stage.WindowPolicy.Candidates)
	require.Equal(t, "alignment_window_seconds", stage.WindowParameter)
	recognition, _ := a.RecoveryStage("recognition")
	require.Nil(t, recognition.WindowPolicy, "CTC window qualification must not permit shorter recognition")
	require.Equal(t, []string{"float32"}, stage.DevicePrecisions["cuda"])
}

func TestCanaryStageRejectsPreviousRuntimeContract(t *testing.T) {
	a := NewCanaryAdapter(t.TempDir())
	for _, kind := range []string{"recognition", "alignment"} {
		stage, _ := a.RecoveryStage(kind)
		if kind == "recognition" {
			stage.ImplementationVersion = "canary-nemo-2.7.3-stages-v1"
		} else {
			stage.ImplementationVersion = "canary-nemo-2.7.3-ctc-v1"
		}
		_, err := a.RunStage(context.Background(), stage, interfaces.AudioInput{}, nil, interfaces.ProcessingContext{}, nil)
		require.ErrorContains(t, err, "unsupported Canary stage contract")
	}
}

func TestCanaryStageRunsOneProcessWithPrivateUpstreamAndCleanup(t *testing.T) {
	bin := t.TempDir()
	argsPath := filepath.Join(bin, "args")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$CANARY_ARGUMENTS"
printf 'TMPDIR=%s\n' "$TMPDIR" >> "$CANARY_ARGUMENTS"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then shift; output="$1"; fi
  if [ "$1" = "--upstream" ]; then shift; test -s "$1" || exit 3; fi
  shift
done
printf '%s' '{"text":"exact output","segments":[],"word_segments":[],"metadata":{"recognizer_loaded_for_alignment":"false"}}' > "$output"
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "uv"), []byte(script), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CANARY_ARGUMENTS", argsPath)
	a := NewCanaryAdapter(t.TempDir())
	stage, _ := a.RecoveryStage("alignment")
	audio := filepath.Join(t.TempDir(), "source.wav")
	require.NoError(t, os.WriteFile(audio, []byte("fixture"), 0600))
	temp := t.TempDir()
	upstream := []byte(`{"text":"private source words","recognition_state":{"schema":"canary-native-recognition-v1","native_alignment_batch_size":4}}`)
	data, err := a.RunStage(context.Background(), stage, interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 7}, map[string]interface{}{"device": "cpu", "precision": "float16"}, interfaces.ProcessingContext{TempDirectory: temp, OutputDirectory: t.TempDir()}, upstream)
	require.NoError(t, err)
	var output interfaces.TranscriptResult
	require.NoError(t, json.Unmarshal(data, &output))
	require.Equal(t, "exact output", output.Text)
	args, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	require.Contains(t, string(args), "--precision\nfloat32\n")
	require.Contains(t, string(args), "--alignment-batch-size\n4\n")
	require.Contains(t, string(args), "TMPDIR="+temp+string(os.PathSeparator))
	require.NotContains(t, string(args), "private source words")
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCanaryStageCancellationRemovesAttemptFiles(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "uv"), []byte("#!/bin/sh\nprintf 'private converted audio' > \"$TMPDIR/converted.wav\"\nsleep 30\n"), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := NewCanaryAdapter(t.TempDir())
	stage, _ := a.RecoveryStage("recognition")
	audio := filepath.Join(t.TempDir(), "source.wav")
	require.NoError(t, os.WriteFile(audio, []byte("fixture"), 0600))
	temp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := a.RunStage(ctx, stage, interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 7}, map[string]interface{}{"device": "cpu", "precision": "float32"}, interfaces.ProcessingContext{TempDirectory: temp, OutputDirectory: t.TempDir()}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCanaryCapabilitiesOnlyDeclareExactArtifacts(t *testing.T) {
	a := NewCanaryAdapter(t.TempDir())
	var stages []interfaces.StageDescriptor
	require.NoError(t, json.Unmarshal([]byte(a.GetCapabilities().Metadata["adaptive_stages"]), &stages))
	require.Len(t, stages, 2)
	for _, stage := range stages {
		for _, identity := range stage.ModelArtifacts {
			require.True(t, len(identity) == 40 || len(identity) == 64)
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(a.envPath, "canary-1b-v2.nemo"), []byte(strings.Repeat("x", 32)), 0600))
	require.ErrorContains(t, a.verifyCanaryModel(context.Background()), "differs from the pinned")
}
