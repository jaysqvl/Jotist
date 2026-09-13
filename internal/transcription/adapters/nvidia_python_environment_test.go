package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNvidiaPythonProjectMatchesCUDARuntimeToTorch(t *testing.T) {
	for _, path := range []string{"py/nvidia/pyproject.toml", "py/nvidia/canary_qwen_pyproject.toml"} {
		project, err := nvidiaScripts.ReadFile(path)
		require.NoError(t, err)
		for _, index := range []string{"cpu", "cu126", "cu130"} {
			url := "https://download.pytorch.org/whl/" + index
			resolved := string(nvidiaPythonProject(project, url))
			require.Contains(t, resolved, "url = \""+url+"\"")
			if index == "cu130" {
				require.Contains(t, resolved, "numba-cuda[cu13]==0.30.4")
				require.Contains(t, resolved, "cuda-python>=13,<14")
				require.NotContains(t, resolved, "numba-cuda[cu12]")
				require.NotContains(t, resolved, "cuda-python>=12,<13")
			} else {
				require.Contains(t, resolved, "numba-cuda[cu12]==0.30.4")
				require.Contains(t, resolved, "cuda-python>=12,<13")
			}
		}
	}
}

// The nested _compat package is required at import time and Go's recursive
// embed defaults omit underscore directories unless explicitly selected.
func TestNvidiaPythonVendorCopiesCompatibilityModule(t *testing.T) {
	envPath := t.TempDir()
	require.NoError(t, copyNvidiaPythonVendors(envPath))
	relative := "vendor/nv-one-logger-pytorch-lightning-integration/src/nv_one_logger/training_telemetry/integration/_compat/lightning.py"
	actual, err := os.ReadFile(filepath.Join(envPath, filepath.FromSlash(relative)))
	require.NoError(t, err)
	expected, err := nvidiaScripts.ReadFile("py/nvidia/" + relative)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.FileExists(t, filepath.Join(envPath, "vendor/nv-one-logger-pytorch-lightning-integration/LICENSE.Apache-2.0"))
}

func TestNvidiaPythonVendorRejectsUnsupportedBackendBeforeWriting(t *testing.T) {
	t.Setenv("PYTORCH_CUDA_VERSION", "cu128")
	envPath := filepath.Join(t.TempDir(), "unprepared")
	require.Error(t, copyNvidiaPythonVendors(envPath))
	require.NoDirExists(t, envPath)
}
