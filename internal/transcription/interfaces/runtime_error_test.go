package interfaces

import "testing"

func TestGPUFailureKindUsesTerminalFailureAndExcludesOtherCauses(t *testing.T) {
	for _, test := range []struct{ message, kind string }{
		{"CUDA out of memory. Tried to allocate 20 MiB", "cuda_out_of_memory"},
		{"CUDA error: out of memory", "cuda_out_of_memory"},
		{"CUBLAS_STATUS_ALLOC_FAILED", "cuda_out_of_memory"},
		{"CUDNN_STATUS_NOT_SUPPORTED", "cuda_runtime_error"},
		{"RuntimeError: CUDA error: invalid device function", "cuda_runtime_error"},
		{"DefaultCPUAllocator: out of memory", ""},
		{"CUDA error: audio input invalid", ""},
		{"CUDA error: 403 Client Error Forbidden", ""},
		{"CUDA out of memory warning\nTraceback (most recent call last):\nFileNotFoundError: missing audio", ""},
		{"CUDA out of memory warning\nTraceback (most recent call last):\nRuntimeError: configuration invalid", ""},
	} {
		if got := GPUFailureKind(test.message); got != test.kind {
			t.Errorf("%q => %q want %q", test.message, got, test.kind)
		}
	}
}
