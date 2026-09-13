package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func selectedPyannoteCheckpoint(params models.WhisperXParams) string {
	if params.DiarizationCheckpoint != "" {
		return params.DiarizationCheckpoint
	}
	if params.DiarizeModel == ModelDiarization31 || params.DiarizeModel == "pyannote/speaker-diarization-community-1" {
		return params.DiarizeModel
	}
	return "pyannote/speaker-diarization-community-1"
}

func followResolvedASRDevice(params models.WhisperXParams, transcript *interfaces.TranscriptResult, diarizerParams map[string]interface{}) {
	if diarizationFollowsASR(params) && transcript != nil && transcript.Metadata["resolved_device"] == "cpu" {
		diarizerParams["device"] = "cpu"
	}
	// Same + successful Auto CUDA retains Auto so diarization has its own CPU
	// retry. Explicit CUDA remains explicit; independently selected Auto is untouched.
}

func attemptLogOffset(directory string) int64 {
	info, err := os.Stat(filepath.Join(directory, "transcription.log"))
	if err != nil {
		return 0
	}
	return info.Size()
}

func attemptLogTail(directory string, offset int64) string {
	file, err := os.Open(filepath.Join(directory, "transcription.log"))
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= offset {
		return ""
	}
	const limit = 64 * 1024
	if info.Size()-offset > limit {
		offset = info.Size() - limit
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	content, _ := io.ReadAll(io.LimitReader(file, limit))
	return string(content)
}

func cpuRetryParameters(params map[string]interface{}) map[string]interface{} {
	retry := make(map[string]interface{}, len(params))
	for key, value := range params {
		retry[key] = value
	}
	retry["device"] = "cpu"
	for _, key := range []string{"precision", "compute_type"} {
		if _, exists := retry[key]; exists {
			retry[key] = "float32"
		}
	}
	if _, exists := retry["fp16"]; exists {
		retry["fp16"] = false
	}
	return retry
}

func appendFallbackLog(directory, stage, kind, outcome string) {
	file, err := os.OpenFile(filepath.Join(directory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	// Only controlled labels are written. The original error may contain secrets.
	fmt.Fprintf(file, "Auto device %s: CUDA attempt failed (%s); CPU fallback %s\n", stage, kind, outcome)
}

func structuredGPUFailure(log string) string {
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "SCRIBERR_GPU_FAILURE=") {
			continue
		}
		var failure struct{ Device, Kind string }
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "SCRIBERR_GPU_FAILURE=")), &failure) == nil && failure.Device == "cuda" && (failure.Kind == "cuda_out_of_memory" || failure.Kind == "cuda_runtime_error") {
			return failure.Kind
		}
	}
	return ""
}

// Each adapter invocation owns and cleans its temporary output and process tree.
// Retrying here keeps database publication and external side effects outside both
// attempts. Explicit devices and non-CUDA failures are never retried.
func runWithAutoDeviceFallback[T any](ctx context.Context, stage string, params map[string]interface{}, procCtx interfaces.ProcessingContext, run func(map[string]interface{}) (T, error)) (T, map[string]string, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	offset := attemptLogOffset(procCtx.OutputDirectory)
	result, err := run(params)
	if ctx.Err() != nil {
		return zero, nil, ctx.Err()
	}
	if err == nil {
		return result, nil, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return zero, nil, err
	}
	device, _ := params["device"].(string)
	if strings.ToLower(strings.TrimSpace(device)) != "auto" {
		return zero, nil, err
	}
	kind := ""
	var gpuError *interfaces.GPUExecutionError
	if errors.As(err, &gpuError) {
		kind = gpuError.Kind
	}
	if kind == "" {
		freshLog := attemptLogTail(procCtx.OutputDirectory, offset)
		kind = structuredGPUFailure(freshLog)
		if kind == "" {
			kind = interfaces.GPUFailureKind(freshLog)
		}
	}
	if kind == "" {
		// Legacy adapter errors can contain a tail that predates this attempt.
		// Only the freshly read log above is admissible as execution evidence.
		summary := err.Error()
		if index := strings.Index(summary, "\nLogs:"); index >= 0 {
			summary = summary[:index]
		}
		kind = interfaces.GPUFailureKind(summary)
	}
	if kind != "cuda_out_of_memory" && kind != "cuda_runtime_error" {
		return zero, nil, err
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	appendFallbackLog(procCtx.OutputDirectory, stage, kind, "starting")
	cpuResult, cpuErr := run(cpuRetryParameters(params))
	if ctx.Err() != nil {
		return zero, nil, ctx.Err()
	}
	outcome := "completed"
	if cpuErr != nil {
		outcome = "failed"
	}
	attempts, _ := json.Marshal([]map[string]string{
		{"device": "cuda", "status": "failed", "error_kind": kind},
		{"device": "cpu", "status": outcome},
	})
	metadata := map[string]string{stage + "_device_attempts": string(attempts), stage + "_device_fallback": "cuda_to_cpu", stage + "_fallback_reason": kind}
	appendFallbackLog(procCtx.OutputDirectory, stage, kind, outcome)
	if cpuErr != nil {
		return zero, metadata, fmt.Errorf("GPU execution failed (%s); CPU fallback failed: %w", kind, cpuErr)
	}
	return cpuResult, metadata, nil
}
