package adapters

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPyTorchPlatformDefaultPreservesExistingBackends(t *testing.T) {
	for _, tc := range []struct{ os, arch, backend string }{
		{"linux", "arm64", "cpu"},
		{"linux", "amd64", "cu126"},
		{"darwin", "arm64", "cu126"},
		{"darwin", "amd64", "cu126"},
		{"windows", "amd64", "cu126"},
	} {
		require.Equal(t, tc.backend, platformPyTorchBackend(tc.os, tc.arch))
	}
	t.Setenv("PYTORCH_CUDA_VERSION", "")
	require.Equal(t, platformPyTorchBackend(runtime.GOOS, runtime.GOARCH), GetPyTorchCUDAVersion())
	for _, requested := range []string{"cpu", "cu126", "cu130"} {
		t.Setenv("PYTORCH_CUDA_VERSION", requested)
		require.Equal(t, requested, GetPyTorchCUDAVersion(), "explicit operator configuration must be preserved")
	}
}
