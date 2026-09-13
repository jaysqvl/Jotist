package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"scriberr/internal/transcription/interfaces"
)

const (
	RecoveryFixed           = "fixed"
	RecoveryStageManagement = "stage_management"
	RecoveryBatchManagement = "batch_management"
	RecoveryCPUFallback     = "cpu_fallback"
	RecoveryShorterWindows  = "shorter_windows"
)

// Broader policy levels require adapter-specific semantics and hardware gates.
// Reject them at admission as well as execution; a hand-written API request
// cannot bypass the UI's disabled choices.
func ValidateRecoveryMode(mode string) error {
	switch mode {
	case "", RecoveryFixed, RecoveryStageManagement, RecoveryBatchManagement, RecoveryCPUFallback, RecoveryShorterWindows:
		return nil
	default:
		return fmt.Errorf("unknown recovery_mode")
	}
}

type gpuInventory struct{ UUID, Name, Driver string }

func visibleGPU(ctx context.Context, index int) gpuInventory {
	if value, ok := os.LookupEnv("CUDA_VISIBLE_DEVICES"); ok && (value == "" || value == "-1") {
		return gpuInventory{}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(queryCtx, "nvidia-smi", "--query-gpu=uuid,name,driver_version", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuInventory{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// CUDA ordinal mapping can be restricted independently of nvidia-smi.
	if visible := os.Getenv("CUDA_VISIBLE_DEVICES"); visible != "" {
		ids := strings.Split(visible, ",")
		if index >= 0 && index < len(ids) {
			selected := strings.TrimSpace(ids[index])
			for i, line := range lines {
				if strings.HasPrefix(line, selected) {
					index = i
					break
				}
				if fmt.Sprint(i) == selected {
					index = i
					break
				}
			}
		}
	}
	if index < 0 || index >= len(lines) {
		return gpuInventory{}
	}
	fields := strings.Split(lines[index], ",")
	if len(fields) != 3 || !strings.HasPrefix(strings.TrimSpace(fields[0]), "GPU-") {
		return gpuInventory{}
	}
	return gpuInventory{strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])}
}

func copyStageParameters(params map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(params))
	for key, value := range params {
		copy[key] = value
	}
	return copy
}

// New plans resolve Auto once. They do not authorize alternate devices or
// numerical conversions. Legacy plans keep their existing fallback wrapper.
func resolveStageParameters(params map[string]interface{}, mode string, gpu gpuInventory) map[string]interface{} {
	resolved := copyStageParameters(params)
	if mode != "" && resolved["device"] == "auto" {
		resolved["device"] = "cpu"
		if gpu.UUID != "" {
			resolved["device"] = "cuda"
		}
	}
	return resolved
}

// Shared across queue, quick jobs, comparison runs and multi-track children.
// The server's exclusive deployment lock makes this a single-server scheduler.
// Conservatively serialize all GPU ordinals until combined peaks are measured.
var gpuStageSlot = make(chan struct{}, 1)

func acquireGPUStage(ctx context.Context, params map[string]interface{}) (func(), error) {
	if params["device"] == "cpu" {
		return func() {}, ctx.Err()
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case gpuStageSlot <- struct{}{}:
		if ctx.Err() != nil {
			<-gpuStageSlot
			return nil, ctx.Err()
		}
		return func() { <-gpuStageSlot }, nil
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("GPU admission wait expired; resume available without changing model settings")
	}
}

// Only structured evidence from this invocation permits adaptive retry. Legacy
// log heuristics remain isolated in runWithAutoDeviceFallback.
func stageFailureCode(ctx context.Context, err error, procCtx interfaces.ProcessingContext, offset int64) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var gpuError *interfaces.GPUExecutionError
	var resourceError *interfaces.ResourceExecutionError
	if errors.As(err,&resourceError)&&resourceError.Kind=="host_out_of_memory"{return resourceError.Kind}
	if errors.As(err, &gpuError) && (gpuError.Kind == "cuda_out_of_memory" || gpuError.Kind == "cuda_runtime_error") {
		return gpuError.Kind
	}
	if kind := structuredGPUFailure(attemptLogTail(procCtx.OutputDirectory, offset)); kind != "" {
		return kind
	}
	for _,line:=range strings.Split(attemptLogTail(procCtx.OutputDirectory,offset),"\n") {if !strings.HasPrefix(line,"SCRIBERR_HOST_FAILURE="){continue};var failure struct{Device string `json:"device"`;Kind string `json:"kind"`};if json.Unmarshal([]byte(strings.TrimPrefix(line,"SCRIBERR_HOST_FAILURE=")),&failure)==nil&&failure.Device=="cpu"&&failure.Kind=="host_out_of_memory"{return failure.Kind}}
	return "adapter_failed"
}

func permitsStageRetry(mode, code string, previousAttempts int) bool {
	return recoveryLevel(mode) >= 1 && previousAttempts < 2 && (code == "cuda_out_of_memory" || code == "cuda_runtime_error")
}
