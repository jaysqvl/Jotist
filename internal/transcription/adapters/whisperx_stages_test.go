package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
)

func TestWhisperXDeclaresDurablePostRecognitionStages(t *testing.T) {
	adapter := NewWhisperXAdapter(t.TempDir())
	stages := adapter.Stages()
	require.Len(t, stages, 3)
	require.Equal(t, []string{"recognition", "alignment", "speaker_assignment"}, []string{stages[0].Kind, stages[1].Kind, stages[2].Kind})
	params := map[string]interface{}{"no_align": false, "align_words": true, "diarize": true}
	resolved, err := adapter.ResolveStageParameters(stages[0], params, nil)
	require.NoError(t, err)
	require.Equal(t, true, resolved["no_align"])
	require.Equal(t, false, resolved["diarize"])
	require.Equal(t, false, params["no_align"], "stage resolution mutated the saved request")
	var advertised []interfaces.StageDescriptor
	require.NoError(t, json.Unmarshal([]byte(adapter.GetCapabilities().Metadata["adaptive_stages"]), &advertised))
	require.Len(t, advertised, 3)
	diarization, ok := adapter.RecoveryStage("diarization")
	require.True(t, ok)
	require.Equal(t, "speaker_assignment", diarization.Kind)
}

func TestWhisperXPostRecognitionStageConsumesPrivateCheckpoint(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	arguments := filepath.Join(root, "arguments")
	stub := `#!/bin/sh
printf '%s\n' "$@" > "$WHISPERX_ARGUMENTS"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --transcript) shift; transcript="$1" ;;
    --output) shift; output="$1" ;;
  esac
  shift
done
cp "$transcript" "$output"
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WHISPERX_ARGUMENTS", arguments)
	adapter := NewWhisperXAdapter(filepath.Join(root, "runtime"))
	stage, ok := adapter.RecoveryStage("alignment")
	require.True(t, ok)
	audio := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audio, []byte("fixture"), 0600))
	upstream := []byte(`{"text":"private meeting words","language":"en","segments":[{"start":0,"end":1,"text":"private meeting words"}],"metadata":{"resolved_device":"cpu"}}`)
	data, err := adapter.RunStage(context.Background(), stage, interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 7}, map[string]interface{}{"device": "cpu", "interpolate_method": "nearest", "model_cache_only": true}, interfaces.ProcessingContext{TempDirectory: filepath.Join(root, "tmp"), OutputDirectory: filepath.Join(root, "output")}, upstream)
	require.NoError(t, err)
	require.JSONEq(t, string(upstream), string(data))
	args, err := os.ReadFile(arguments)
	require.NoError(t, err)
	require.NotContains(t, string(args), "private meeting words")
	require.Contains(t, string(args), "--stage\nalignment\n")
	require.Contains(t, string(args), "--model-cache-only\n")
}

func TestWhisperXAlignmentFailurePreservesStructuredDiagnostic(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	stub := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) shift; output="$1" ;;
  esac
  shift
done
cat > "${output%/*}/error.json" <<'JSON'
{"diagnostic_code":"runtime_alignment_error","error":"controlled","exception_class":"ValueError","phase":"alignment","resolved_device":"cpu","gpu_failure_kind":null}
JSON
exit 7
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	adapter := NewWhisperXAdapter(filepath.Join(root, "runtime"))
	stage, ok := adapter.RecoveryStage("alignment")
	require.True(t, ok)
	audio := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audio, []byte("fixture"), 0600))
	private := "private meeting words"
	upstream := []byte(`{"text":"` + private + `","language":"en","segments":[{"start":0,"end":1,"text":"` + private + `"}],"metadata":{"resolved_device":"cpu"}}`)
	_, err := adapter.RunStage(context.Background(), stage, interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 7}, map[string]interface{}{"device": "cpu", "interpolate_method": "nearest"}, interfaces.ProcessingContext{TempDirectory: filepath.Join(root, "tmp"), OutputDirectory: filepath.Join(root, "output")}, upstream)
	require.Error(t, err)
	diagnostic, ok := interfaces.RuntimeDiagnostic(err)
	require.True(t, ok)
	require.Equal(t, "runtime_alignment_error", diagnostic.Code())
	log, readErr := os.ReadFile(filepath.Join(root, "output", "transcription.log"))
	require.NoError(t, readErr)
	require.Contains(t, string(log), `"exception_class":"ValueError"`)
	require.NotContains(t, string(log), private)
}
