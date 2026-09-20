package adapters

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"scriberr/internal/transcription/interfaces"
)

var localASRDiagnostics = func() map[string]string {
	data, err := localASRScripts.ReadFile("py/local_asr/diagnostics.json")
	if err != nil {
		panic("missing embedded local ASR diagnostic catalog")
	}
	var messages map[string]string
	if json.Unmarshal(data, &messages) != nil {
		panic("invalid embedded local ASR diagnostic catalog")
	}
	return messages
}()

func localASRDiagnostic(code string, cause error) *interfaces.SafeRuntimeDiagnostic {
	message, ok := localASRDiagnostics[code]
	if !ok {
		code, message = "runtime_model_error", localASRDiagnostics["runtime_model_error"]
	}
	return interfaces.NewSafeRuntimeDiagnostic(code, message, cause)
}

// Parse only catalog codes and closed enums. A library-written sidecar is not
// authority to persist arbitrary text, even if it calls that text an error.
func localASRWorkerDiagnostic(data []byte, cause error) (error, map[string]string) {
	var failure struct {
		Error   string `json:"error"`
		Code    string `json:"diagnostic_code"`
		Class   string `json:"exception_class"`
		Phase   string `json:"phase"`
		Device  string `json:"resolved_device"`
		GPUKind string `json:"gpu_failure_kind"`
	}
	if len(data) > 64*1024 || json.Unmarshal(data, &failure) != nil {
		return localASRDiagnostic("worker_failed_without_diagnostic", cause), nil
	}
	code := "runtime_model_error"
	if _, ok := localASRDiagnostics[failure.Code]; ok {
		code = failure.Code
	} else {
		// Older installed workers have no code. Preserve only exact messages
		// from the embedded application catalog, never arbitrary error strings.
		for candidate, message := range localASRDiagnostics {
			if failure.Error == message {
				code = candidate
				break
			}
		}
	}
	diagnostic := localASRDiagnostic(code, cause)
	fields := map[string]string{}
	if oneOf(failure.Class, "RecognitionError", "RuntimeError", "ValueError", "TypeError", "AttributeError", "KeyError", "IndexError", "ImportError", "ModuleNotFoundError", "OSError", "FileNotFoundError", "PermissionError", "MemoryError", "GatedRepoError", "RepositoryNotFoundError", "HfHubHTTPError", "ModelError") {
		fields["exception_class"] = failure.Class
	}
	if oneOf(failure.Phase, "configuration", "runtime_initialization", "audio_decode", "model_loading", "recognition", "alignment", "output_validation") {
		fields["phase"] = failure.Phase
	}
	if oneOf(failure.Device, "cpu", "cuda") {
		fields["device"] = failure.Device
	}
	if failure.Device == "cuda" && oneOf(failure.GPUKind, "cuda_out_of_memory", "cuda_runtime_error") {
		return &interfaces.GPUExecutionError{Kind: failure.GPUKind, Err: diagnostic}, fields
	}
	return diagnostic, fields
}
func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func appendLocalASRDiagnostic(directory, phase string, err error, fields map[string]string) {
	if directory == "" {
		return
	}
	row := map[string]string{"worker": "local_asr", "phase": phase, "status": "started"}
	for key, value := range fields {
		row[key] = value
	}
	if err != nil {
		row["status"] = "failed"
		if diagnostic, ok := interfaces.RuntimeDiagnostic(err); ok {
			row["code"], row["message"] = diagnostic.Code(), diagnostic.Error()
		} else {
			row["code"], row["message"] = "runtime_model_error", localASRDiagnostics["runtime_model_error"]
		}
	}
	if os.MkdirAll(directory, 0700) != nil {
		return
	}
	file, openErr := os.OpenFile(filepath.Join(directory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		return
	}
	defer file.Close()
	encoded, _ := json.Marshal(row)
	_, _ = file.Write(append(append([]byte("JOTIST_RUNTIME_DIAGNOSTIC="), encoded...), '\n'))
}

func localASRWorkerStartError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) || strings.Contains(err.Error(), "executable file not found") {
		return localASRDiagnostic("worker_start_failed", err)
	}
	return localASRDiagnostic("worker_failed_without_diagnostic", err)
}
