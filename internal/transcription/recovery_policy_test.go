package transcription

import (
	"context"
	"errors"
	"testing"
	"time"

	"scriberr/internal/transcription/interfaces"
)

func TestRecoveryPolicyModesAndCleanupRetry(t *testing.T) {
	for _, mode := range []string{"", RecoveryFixed, RecoveryStageManagement, RecoveryBatchManagement, RecoveryCPUFallback, RecoveryShorterWindows} {
		if err := ValidateRecoveryMode(mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"batch", "invented_policy", "2", "3", "4"} {
		if ValidateRecoveryMode(mode) == nil {
			t.Errorf("accepted unknown policy %q", mode)
		}
	}
	for _, mode := range []string{"", RecoveryFixed, RecoveryStageManagement} {
		for _, code := range []string{"cuda_out_of_memory", "cuda_runtime_error", "adapter_failed", "cancelled", "deadline_exceeded", "import_error", "access_denied"} {
			for attempt := 1; attempt <= 3; attempt++ {
				want := mode == RecoveryStageManagement && attempt == 1 && (code == "cuda_out_of_memory" || code == "cuda_runtime_error")
				if permitsStageRetry(mode, code, attempt) != want {
					t.Fatalf("wrong retry %s %s %d", mode, code, attempt)
				}
			}
		}
	}
}

func TestFixedAutoResolvesWithoutChangingNumericsOrWindows(t *testing.T) {
	original := map[string]interface{}{"device": "auto", "precision": "bfloat16", "compute_type": "float16", "batch_size": 4, "chunk_duration": 300}
	for _, gpu := range []gpuInventory{{}, {UUID: "GPU-test"}} {
		resolved := resolveStageParameters(original, RecoveryFixed, gpu)
		if resolved["device"] == "auto" {
			t.Fatal("auto was not resolved")
		}
		for _, key := range []string{"precision", "compute_type", "batch_size", "chunk_duration"} {
			if resolved[key] != original[key] {
				t.Fatalf("changed %s", key)
			}
		}
	}
	if original["device"] != "auto" {
		t.Fatal("mutated request")
	}
	if resolveStageParameters(original, "", gpuInventory{})["device"] != "auto" {
		t.Fatal("changed legacy Auto")
	}
}

func TestGPUAdmissionSharedAndCancellationBounded(t *testing.T) {
	release, err := acquireGPUStage(context.Background(), map[string]interface{}{"device": "cuda"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := acquireGPUStage(ctx, map[string]interface{}{"device": "auto"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second consumer admitted: %v", err)
	}
	cpuRelease, err := acquireGPUStage(context.Background(), map[string]interface{}{"device": "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	cpuRelease()
}

func TestStageManagementIgnoresUnstructuredGPUClaims(t *testing.T) {
	p := interfaces.ProcessingContext{OutputDirectory: t.TempDir()}
	if code := stageFailureCode(context.Background(), errors.New("CUDA out of memory"), p, 0); code != "adapter_failed" {
		t.Fatal(code)
	}
	if code := stageFailureCode(context.Background(), &interfaces.GPUExecutionError{Kind: "cuda_out_of_memory", Err: errors.New("private")}, p, 0); code != "cuda_out_of_memory" {
		t.Fatal(code)
	}
}
