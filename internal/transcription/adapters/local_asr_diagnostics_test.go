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

func TestLocalASRDiagnosticsKeepOnlyBoundedTokenWindowDetails(t *testing.T) {
	err, fields := localASRWorkerDiagnostic([]byte(`{"error":"private transcript", "diagnostic_code":"application_8923ea1c9bdf", "exception_class":"ModelError", "phase":"recognition", "window_index":17, "window_count":82, "token_limit":1000}`), nil)
	diagnostic, ok := interfaces.RuntimeDiagnostic(err)
	require.True(t, ok)
	require.Equal(t, "application_8923ea1c9bdf", diagnostic.Code())
	require.Contains(t, diagnostic.Error(), "Recognition window 17/82")
	require.Contains(t, diagnostic.Error(), "Limit: 1000 tokens")
	require.NotContains(t, diagnostic.Error(), "private")
	require.Equal(t, "17", fields["window_index"])

	err, fields = localASRWorkerDiagnostic([]byte(`{"diagnostic_code":"application_8923ea1c9bdf", "phase":"recognition", "window_index":200000, "window_count":200000, "token_limit":999999}`), nil)
	diagnostic, ok = interfaces.RuntimeDiagnostic(err)
	require.True(t, ok)
	require.NotContains(t, diagnostic.Error(), "200000")
	require.NotContains(t, diagnostic.Error(), "999999")
	require.NotContains(t, fields, "window_index")
	require.NotContains(t, fields, "token_limit")
}
