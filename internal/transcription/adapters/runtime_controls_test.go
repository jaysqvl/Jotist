package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/transcription/interfaces"
)

func TestResearchDiarizerInitializesOnFirstUseAndForwardsTokenPrivately(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
if [ "$1" = "sync" ]; then
  touch "$PREPARE_MARKER"
  exit 0
fi
test -f "$PREPARE_MARKER" || exit 3
test "$HF_TOKEN" = "test-only-token" || exit 4
for arg in "$@"; do
  test "$arg" != "test-only-token" || exit 5
done
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    printf '%s' '{"segments":[{"start":0,"end":1,"speaker":"A"}],"speakers":["A"],"speaker_count":1,"resolved_device":"cpu"}' > "$1"
    exit 0
  fi
  shift
done
exit 6
`
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PREPARE_MARKER", filepath.Join(directory, "prepared"))
	audio := filepath.Join(directory, "audio.wav")
	if err := os.WriteFile(audio, []byte("test fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := NewSUPlimeAdapter(filepath.Join(directory, "environment"))
	result, err := adapter.Diarize(context.Background(), interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 12}, map[string]interface{}{"hf_token": "test-only-token", "device": "cpu"}, interfaces.ProcessingContext{JobID: "test", TempDirectory: directory, OutputDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Segments) != 1 || result.Metadata["resolved_device"] != "cpu" {
		t.Fatal("first-use result is incomplete")
	}
	if !strings.Contains(adapter.GetCapabilities().Metadata["model_variants"], "rewayai/suplime\"") {
		t.Fatal("base SUPlime checkpoint missing from model variants")
	}
}

func TestWhisperXContextAndDiarizationArguments(t *testing.T) {
	adapter := NewWhisperXAdapter(t.TempDir())
	params := map[string]interface{}{"device": "cpu", "diarize": true, "initial_prompt": "Legacy terminology.", "context": "Engineering planning", "context_terms": "PostgreSQL\nKubernetes", "condition_on_previous_text": true}
	args, err := adapter.buildWhisperXArgs(interfaces.AudioInput{FilePath: "/tmp/meeting.wav"}, params, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--initial_prompt", "Legacy terminology.\nEngineering planning")
	assertArgValue(t, args, "--hotwords", "PostgreSQL, Kubernetes")
	if strings.Contains(strings.Join(args, " "), "--condition_on_previous_text") {
		t.Fatal("WhisperX's batched decoder does not support previous-chunk conditioning")
	}
	assertArgValue(t, args, "--diarize_model", "pyannote/speaker-diarization-community-1")
	assertArgValue(t, args, "--device", "cpu")
	if !strings.Contains(strings.Join(args, " "), "python -I ") {
		t.Fatal("legacy WhisperX source checkout must not shadow the pinned installed package")
	}
	if err := adapter.ValidateParameters(params); err != nil {
		t.Fatal(err)
	}
}

func TestPyannoteForwardsExplicitCPUWithoutTokenArgument(t *testing.T) {
	adapter := NewPyAnnoteAdapter(t.TempDir())
	args, err := adapter.buildPyAnnoteArgs(interfaces.AudioInput{FilePath: "/tmp/audio.wav"}, map[string]interface{}{"device": "cpu", "hf_token": "secret-token"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--device", "cpu")
	if strings.Contains(strings.Join(args, " "), "secret-token") {
		t.Fatal("token exposed in process arguments")
	}
}

func TestPyannoteAllowsCachedLoginWithoutTokenOverride(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
test -z "$HF_TOKEN" || exit 3
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    printf '%s' '{"segments":[{"start":0,"end":1,"speaker":"SPEAKER_00"}],"speakers":["SPEAKER_00"],"speaker_count":1,"resolved_device":"cpu"}' > "$1"
    exit 0
  fi
  shift
done
exit 4
`
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HF_TOKEN", "")
	audio := filepath.Join(directory, "audio.wav")
	if err := os.WriteFile(audio, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := NewPyAnnoteAdapter(filepath.Join(directory, "environment"))
	result, err := adapter.Diarize(context.Background(), interfaces.AudioInput{FilePath: audio, Format: "wav", Size: 7}, map[string]interface{}{"device": "cpu", "output_format": "json"}, interfaces.ProcessingContext{JobID: "test", TempDirectory: directory, OutputDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if result.SpeakerCount != 1 || result.Speakers[0] != "SPEAKER_00" {
		t.Fatal("cached-auth loader result was not parsed")
	}
}

func TestCanaryQwenContextPreservesLegacyPrompt(t *testing.T) {
	adapter := NewCanaryQwenAdapter(t.TempDir())
	args, err := adapter.buildCanaryQwenArgs(interfaces.AudioInput{FilePath: "/tmp/audio.wav"}, map[string]interface{}{"prompt": "Transcribe exactly:", "context": "Database migration", "context_terms": "SQL, PostgreSQL"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--prompt", "Transcribe exactly:")
	assertArgValue(t, args, "--context", "Database migration\nVocabulary: SQL, PostgreSQL")
}

func TestSortformerRejectsUnsupportedSpeakerCapacity(t *testing.T) {
	adapter := NewSortformerAdapter(t.TempDir())
	if adapter.GetMaxSpeakers() != 4 {
		t.Fatal("Sortformer output capacity must be four")
	}
	if adapter.ValidateParameters(map[string]interface{}{"max_speakers": 5}) == nil {
		t.Fatal("five-speaker capacity must be rejected")
	}
}

func TestParakeetDeviceAppliedToBothRoutes(t *testing.T) {
	adapter := NewParakeetAdapter(t.TempDir())
	input := interfaces.AudioInput{FilePath: "/tmp/audio.wav"}
	params := map[string]interface{}{"device": "cpu"}
	args, err := adapter.buildParakeetArgs(input, params, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--device", "cpu")
	args, err = adapter.buildBufferedArgs(input, params, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--device", "cpu")
}

func TestResearchDiarizationPreservesOverlapAndLicense(t *testing.T) {
	result, err := parseResearchDiarization([]byte(`{"segments":[{"start":0,"end":2,"speaker":"A"},{"start":1,"end":3,"speaker":"B"}],"speakers":["A","B"],"speaker_count":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Segments) != 2 || result.Segments[1].Start != 1 || result.SpeakerCount != 2 {
		t.Fatal("overlapping speaker turns were not preserved")
	}
	for _, adapter := range []*ResearchDiarizationAdapter{NewDiariZenAdapter(t.TempDir()), NewSUPlimeAdapter(t.TempDir())} {
		caps := adapter.GetCapabilities()
		if caps.Metadata["license"] != "CC-BY-NC-4.0" || caps.Metadata["optional_install"] != "true" || caps.RequiresGPU {
			t.Fatal("incorrect research-model capabilities")
		}
	}
}

func TestResolvedDeviceMetadataSurvivesRequestDefaults(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "result.json"), []byte(`{"resolved_device":"cpu"}`), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := mergeRuntimeMetadata(map[string]string{"device": "auto"}, readRuntimeMetadata(directory))
	if metadata["resolved_device"] != "cpu" || metadata["device"] != "auto" {
		t.Fatal("actual device metadata lost")
	}
}

func TestExplicitCPUEnvironmentHidesCUDAWithoutChangingAuto(t *testing.T) {
	original := []string{"PATH=/usr/bin", "CUDA_VISIBLE_DEVICES=0", "HF_TOKEN=test-token", "CUDA_VISIBLE_DEVICES=1"}
	cpu := withRequestedDevice(original, "cpu")
	count := 0
	for _, entry := range cpu {
		if strings.HasPrefix(entry, "CUDA_VISIBLE_DEVICES=") {
			count++
			if entry != "CUDA_VISIBLE_DEVICES=" {
				t.Fatal("CPU subprocess can still see CUDA devices")
			}
		}
	}
	if count != 1 {
		t.Fatal("CPU device fence must replace every inherited CUDA visibility entry")
	}
	for _, device := range []string{"auto", "cuda"} {
		if strings.Join(withRequestedDevice(original, device), "\n") != strings.Join(original, "\n") {
			t.Fatal("non-CPU device choice changed host environment")
		}
	}
}
