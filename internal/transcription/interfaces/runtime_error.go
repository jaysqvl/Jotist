package interfaces

import "strings"

// ResourceExecutionError is current-invocation structured resource evidence.
// No generic exception text is promoted into a host-memory failure.
type ResourceExecutionError struct { Kind string; Err error }
func(e *ResourceExecutionError) Error()string{return "Resource execution failed ("+e.Kind+")"}
func(e *ResourceExecutionError) Unwrap()error{return e.Err}

// GPUExecutionError carries a safe hardware-failure category. It never includes
// model input, authentication details, or a third-party traceback in its message.
type GPUExecutionError struct {
	Kind string
	Err  error
}

func (e *GPUExecutionError) Error() string { return "GPU execution failed (" + e.Kind + ")" }
func (e *GPUExecutionError) Unwrap() error { return e.Err }

// GPUFailureKind recognizes CUDA-specific execution failures, not generic model
// or host-memory failures. This also supports older workers whose stderr is the
// only failure signal. The caller must separately enforce Auto and cancellation.
func GPUFailureKind(diagnostic string) string {
	message := strings.ToLower(diagnostic)
	if start := strings.LastIndex(message, "traceback (most recent call last):"); start >= 0 {
		message = message[start:]
	}
	for _, excluded := range []string{"gatedrepo", "gated repo", "unauthorized", "forbidden", "401 client error", "403 client error", "invalid token", "invalid api key", "repository not found", "audio input", "audio file not found", "filenotfounderror", "permissionerror", "keyboardinterrupt", "cancelled", "canceled", "deadline exceeded"} {
		if strings.Contains(message, excluded) {
			return ""
		}
	}
	if strings.Contains(message, "cuda out of memory") || strings.Contains(message, "cuda error: out of memory") || strings.Contains(message, "cublas_status_alloc_failed") {
		return "cuda_out_of_memory"
	}
	for _, marker := range []string{"cuda error:", "cuda runtime error", "cublas_status_", "cudnn_status_", "cudnn error:", "no kernel image is available", "invalid device function", "illegal memory access was encountered", "device-side assert triggered", "cuda driver error"} {
		if strings.Contains(message, marker) {
			return "cuda_runtime_error"
		}
	}
	return ""
}
