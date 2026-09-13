package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/transcription/interfaces"
)

func TestLocalASRStructuredGPUErrorAndTemporaryCleanup(t *testing.T) {
	for _, test := range []struct {
		device, kind string
		typed        bool
	}{
		{"cuda", "cuda_runtime_error", true}, {"cuda", "cuda_out_of_memory", true},
		{"cpu", "cuda_runtime_error", false}, {"cuda", "authentication_error", false},
	} {
		t.Run(test.device+test.kind, func(t *testing.T) {
			logPath := fakeLocalASRUV(t)
			uv := filepath.Join(filepath.Dir(logPath), "uv")
			script, err := os.ReadFile(uv)
			if err != nil {
				t.Fatal(err)
			}
			start := strings.Index(string(script), "  printf '%s'")
			end := strings.Index(string(script)[start:], "\nelse") + start
			failure := `  printf '%s' '{"error":"RuntimeError while loading or running the model","resolved_device":"` + test.device + `","gpu_failure_kind":"` + test.kind + `"}' > "${config%/*}/error.json"` + "\n  exit 1"
			if err := os.WriteFile(uv, []byte(string(script[:start])+failure+string(script[end:])), 0755); err != nil {
				t.Fatal(err)
			}
			a, err := NewLocalASRAdapter(filepath.Join(t.TempDir(), "env"), "Qwen/Qwen3-ASR-1.7B-hf")
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(t.TempDir(), "fixture.wav")
			if err := os.WriteFile(input, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			temp := t.TempDir()
			_, err = a.Transcribe(context.Background(), interfaces.AudioInput{FilePath: input, Format: "wav", Size: 7}, map[string]interface{}{"device": "auto", "precision": "float16"}, interfaces.ProcessingContext{TempDirectory: temp})
			if err == nil {
				t.Fatal("worker failure was accepted")
			}
			var gpu *interfaces.GPUExecutionError
			if errors.As(err, &gpu) != test.typed {
				t.Fatalf("classification: %T %v", err, err)
			}
			if test.typed && gpu.Kind != test.kind {
				t.Fatalf("kind %s", gpu.Kind)
			}
			entries, readErr := os.ReadDir(temp)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("attempt did not remove partial outputs: %v %v", entries, readErr)
			}
		})
	}
}

func TestCPUFallbackRequestFencesCUDAAndPreservesAlignmentPrecision(t *testing.T) {
	a, err := NewLocalASRAdapter(t.TempDir(), "Qwen/Qwen3-ASR-1.7B-hf")
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := a.buildRequest(interfaces.AudioInput{FilePath: "fixture.wav"}, map[string]interface{}{"device": "cpu", "precision": "float32", "align_words": true}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fenced := false
	for _, item := range env {
		if item == "CUDA_VISIBLE_DEVICES=" {
			fenced = true
		}
	}
	if !fenced {
		t.Fatal("CPU worker could initialize CUDA before recognition/alignment")
	}
}
