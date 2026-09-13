package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func TestAutoGPUFallbackPreservesOptionsAndReportsAttempts(t *testing.T) {
	for _, stage := range []string{"asr", "diarization"} {
		t.Run(stage, func(t *testing.T) {
			params := map[string]interface{}{"device": "auto", "precision": "float16", "compute_type": "int8_float16", "fp16": true, "context": "Scriberr", "model": "checkpoint", "hf_token": "private-token"}
			original := map[string]interface{}{}
			for k, v := range params {
				original[k] = v
			}
			calls := 0
			dir := t.TempDir()
			result, metadata, err := runWithAutoDeviceFallback(context.Background(), stage, params, interfaces.ProcessingContext{OutputDirectory: dir}, func(p map[string]interface{}) (string, error) {
				calls++
				if calls == 1 {
					return "partial", &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("private-token")}
				}
				if p["device"] != "cpu" || p["precision"] != "float32" || p["compute_type"] != "float32" || p["fp16"] != false {
					t.Fatalf("unsafe CPU fallback: %v", p)
				}
				if p["context"] != "Scriberr" || p["model"] != "checkpoint" || p["hf_token"] != "private-token" {
					t.Fatal("fallback lost model inputs")
				}
				return "CPU result", nil
			})
			if err != nil || result != "CPU result" || calls != 2 {
				t.Fatalf("result=%q calls=%d err=%v", result, calls, err)
			}
			if !reflect.DeepEqual(params, original) {
				t.Fatal("saved/requested parameters mutated")
			}
			if metadata[stage+"_device_fallback"] != "cuda_to_cpu" || metadata[stage+"_fallback_reason"] != "cuda_out_of_memory" {
				t.Fatalf("metadata: %v", metadata)
			}
			var attempts []map[string]string
			if err := json.Unmarshal([]byte(metadata[stage+"_device_attempts"]), &attempts); err != nil || len(attempts) != 2 || attempts[1]["status"] != "completed" {
				t.Fatalf("attempts: %v %v", attempts, err)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "transcription.log"))
			if strings.Contains(string(log), "private-token") || strings.Contains(metadata[stage+"_device_attempts"], "private-token") {
				t.Fatal("failure reporting leaked input")
			}
		})
	}
}

func TestAutoFallbackRequiresGPUExecutionFailure(t *testing.T) {
	for _, test := range []struct {
		name, device, message string
		wantCalls             int
	}{
		{"explicit cuda", "cuda", "CUDA out of memory", 1},
		{"explicit cpu", "cpu", "CUDA out of memory", 1},
		{"generic model", "auto", "RuntimeError during model setup", 1},
		{"host memory", "auto", "DefaultCPUAllocator: cannot allocate memory", 1},
		{"auth", "auto", "CUDA error: 403 Client Error Forbidden", 1},
		{"input", "auto", "CUDA error: audio input invalid", 1},
		{"gpu kernel", "auto", "CUDA error: no kernel image is available for execution", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, _, _ = runWithAutoDeviceFallback(context.Background(), "asr", map[string]interface{}{"device": test.device}, interfaces.ProcessingContext{OutputDirectory: t.TempDir()}, func(map[string]interface{}) (string, error) {
				calls++
				if calls == 1 {
					return "", errors.New(test.message)
				}
				return "done", nil
			})
			if calls != test.wantCalls {
				t.Fatalf("calls %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestAutoCPUOnlySuccessRunsOnce(t *testing.T) {
	calls := 0
	result, metadata, err := runWithAutoDeviceFallback(context.Background(), "asr", map[string]interface{}{"device": "auto"}, interfaces.ProcessingContext{OutputDirectory: t.TempDir()}, func(map[string]interface{}) (string, error) { calls++; return "cpu", nil })
	if err != nil || calls != 1 || result != "cpu" || len(metadata) != 0 {
		t.Fatalf("result %q metadata %v calls %d err %v", result, metadata, calls, err)
	}
}

func TestAutoCompletionAfterCancellationIsNotPublished(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, _, err := runWithAutoDeviceFallback(ctx, "asr", map[string]interface{}{"device": "auto"}, interfaces.ProcessingContext{OutputDirectory: t.TempDir()}, func(map[string]interface{}) (string, error) {
		cancel()
		return "late result", nil
	})
	if !errors.Is(err, context.Canceled) || result != "" {
		t.Fatalf("late result escaped cancellation: %q %v", result, err)
	}
}

func TestAutoFallbackUsesFreshWorkerEvidenceOnly(t *testing.T) {
	for _, test := range []struct {
		name, append string
		want         int
	}{
		{"stale cuda", "", 1},
		{"fresh kernel failure", "SCRIBERR_GPU_FAILURE={\"device\":\"cuda\",\"kind\":\"cuda_runtime_error\"}\nRuntimeError: backend does not support this dtype\n", 2},
		{"fresh legacy OOM", "RuntimeError: CUDA out of memory\n", 2},
		{"invalid structured category", "SCRIBERR_GPU_FAILURE={\"device\":\"cuda\",\"kind\":\"input_error\"}\n", 1},
		{"CPU structured failure", "SCRIBERR_GPU_FAILURE={\"device\":\"cpu\",\"kind\":\"cuda_runtime_error\"}\n", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "transcription.log")
			if err := os.WriteFile(path, []byte("old CUDA out of memory\n"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, _, _ = runWithAutoDeviceFallback(context.Background(), "asr", map[string]interface{}{"device": "auto"}, interfaces.ProcessingContext{OutputDirectory: dir}, func(map[string]interface{}) (string, error) {
				calls++
				if calls == 1 {
					f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if e != nil {
						t.Fatal(e)
					}
					_, _ = f.WriteString(test.append)
					_ = f.Close()
					return "", errors.New("worker failed\nLogs:\nold CUDA out of memory")
				}
				return "done", nil
			})
			if calls != test.want {
				t.Fatalf("calls %d want %d", calls, test.want)
			}
		})
	}
}

func TestAutoFallbackCancellationAndBothFailures(t *testing.T) {
	for _, when := range []string{"before", "gpu", "cpu", "returned cancellation", "returned deadline"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if when == "before" {
				cancel()
			}
			calls := 0
			_, _, err := runWithAutoDeviceFallback(ctx, "asr", map[string]interface{}{"device": "auto"}, interfaces.ProcessingContext{OutputDirectory: t.TempDir()}, func(map[string]interface{}) (string, error) {
				calls++
				if when == "gpu" || when == "cpu" && calls == 2 {
					cancel()
				}
				if when == "returned cancellation" {
					return "", context.Canceled
				}
				if when == "returned deadline" {
					return "", context.DeadlineExceeded
				}
				return "", &interfaces.GPUExecutionError{Kind: "cuda_runtime_error"}
			})
			want := 1
			if when == "before" {
				want = 0
			}
			if when == "cpu" {
				want = 2
			}
			if calls != want || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("calls %d want %d err %v", calls, want, err)
			}
		})
	}
	calls := 0
	cpuError := errors.New("CPU allocation failed")
	_, metadata, err := runWithAutoDeviceFallback(context.Background(), "diarization", map[string]interface{}{"device": "auto"}, interfaces.ProcessingContext{OutputDirectory: t.TempDir()}, func(map[string]interface{}) (string, error) {
		calls++
		if calls == 1 {
			return "", &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory"}
		}
		return "", cpuError
	})
	if calls != 2 || !errors.Is(err, cpuError) || !strings.Contains(err.Error(), "CPU fallback failed") || !strings.Contains(metadata["diarization_device_attempts"], `"status":"failed"`) {
		t.Fatalf("calls %d metadata %v err %v", calls, metadata, err)
	}
}

func TestSameDiarizerFollowsCPUFallbackWithoutLosingAutoRetry(t *testing.T) {
	for _, test := range []struct{ requested, actual, diarizer, expected string }{
		{"auto", "cpu", "same", "cpu"}, {"auto", "cuda", "same", "auto"},
		{"cuda", "cuda", "same", "cuda"}, {"auto", "cpu", "auto", "auto"},
		{"cpu", "cpu", "cuda", "cuda"},
	} {
		p := models.WhisperXParams{Device: test.requested, DiarizationDevice: test.diarizer}
		converted := map[string]interface{}{"device": resolvedDiarizationDevice(p)}
		followResolvedASRDevice(p, &interfaces.TranscriptResult{Metadata: map[string]string{"resolved_device": test.actual}}, converted)
		if converted["device"] != test.expected {
			t.Fatalf("%+v got %v", test, converted)
		}
	}
}

func TestPyannoteCheckpointFieldReachesInlineAndSeparateRuns(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	p := models.WhisperXParams{ModelFamily: FamilyWhisper, Device: "cpu", DiarizationDevice: "same", Diarize: true, DiarizeModel: "pyannote", DiarizationCheckpoint: ModelDiarization31}
	if got := s.convertToWhisperXParams(p)["diarize_model"]; got != ModelDiarization31 {
		t.Fatalf("inline checkpoint %v", got)
	}
	if got := s.convertToPyannoteParams(p)["model"]; got != ModelDiarization31 {
		t.Fatalf("separate checkpoint %v", got)
	}
	if got := selectedPyannoteCheckpoint(p); got != ModelDiarization31 {
		t.Fatalf("metadata checkpoint %v", got)
	}
}

func TestEmptyDiarizationDevicePreservesFamilySpecificSemanticsAfterFallback(t *testing.T) {
	for _, test := range []struct{ family, want string }{{"qwen3_asr", "cpu"}, {FamilyWhisper, "cpu"}, {FamilyNvidiaCanary, "auto"}, {FamilyMistralVoxtral, "auto"}} {
		params := models.WhisperXParams{ModelFamily: test.family, Device: "auto"}
		diarizer := map[string]interface{}{"device": resolvedDiarizationDevice(params)}
		followResolvedASRDevice(params, &interfaces.TranscriptResult{Metadata: map[string]string{"resolved_device": "cpu"}}, diarizer)
		if diarizer["device"] != test.want {
			t.Fatalf("%s: device %v, want %s", test.family, diarizer["device"], test.want)
		}
	}
}
