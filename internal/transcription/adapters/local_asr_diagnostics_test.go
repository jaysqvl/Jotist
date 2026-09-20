package adapters

import (
	"errors"
	"github.com/stretchr/testify/require"
	"scriberr/internal/transcription/interfaces"
	"testing"
)

func TestLocalASRDiagnosticsRejectUntrustedSidecarText(t *testing.T) {
	for _, raw := range []string{
		`{"error":"hf_secret transcript", "diagnostic_code":"runtime_model_access", "exception_class":"OSError", "phase":"model_loading"}`,
		`{"error":"hf_secret transcript", "diagnostic_code":"hf_secret", "exception_class":"hf_secret", "phase":"hf_secret"}`,
		`malformed hf_secret`,
	} {
		err, fields := localASRWorkerDiagnostic([]byte(raw), errors.New("private process cause"))
		diagnostic, ok := interfaces.RuntimeDiagnostic(err)
		require.True(t, ok)
		require.NotContains(t, diagnostic.Error(), "hf_secret")
		for _, value := range fields {
			require.NotContains(t, value, "hf_secret")
		}
	}
}

func TestLocalASRDiagnosticsRetainGPURecoverySignal(t *testing.T) {
	err, _ := localASRWorkerDiagnostic([]byte(`{"diagnostic_code":"runtime_model_error","resolved_device":"cuda","gpu_failure_kind":"cuda_out_of_memory"}`), nil)
	var gpu *interfaces.GPUExecutionError
	require.ErrorAs(t, err, &gpu)
	require.Equal(t, "cuda_out_of_memory", gpu.Kind)
}
