package adapters

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerRecoveryEnvironmentNeverInheritsAnAmbientGrant(t *testing.T) {
	env := withWorkerRecoveryPolicy([]string{"JOTIST_RECOVERY_POLICY=unlimited", "HF_TOKEN=synthetic-private"}, nil)
	require.Contains(t, env, "JOTIST_RECOVERY_POLICY=")
	require.Contains(t, env, "HF_TOKEN=synthetic-private")
	policy := map[string]interface{}{"version": 1, "remaining_retries": 2}
	env = withWorkerRecoveryPolicy(env, map[string]interface{}{"worker_recovery_policy": policy})
	for _, entry := range env {
		if strings.HasPrefix(entry, "JOTIST_RECOVERY_POLICY=") {
			var decoded map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(entry, "JOTIST_RECOVERY_POLICY=")), &decoded))
			require.Equal(t, float64(2), decoded["remaining_retries"])
		}
	}
}

func TestWorkerRecoveryWriterPersistsOnlyBoundedWhitelistedEvidence(t *testing.T) {
	var destination bytes.Buffer
	w := &recoveryEvidenceWriter{destination: &destination}
	_, err := w.Write([]byte("private model output\nJOTIST_RECOV"))
	require.NoError(t, err)
	_, err = w.Write([]byte("ERY={\"action\":\"token_window_split\",\"retry\":1,\"private\":\"secret\"}\nJOTIST_RECOVERY={\"action\":\"unknown\",\"retry\":2}\n" + strings.Repeat("x", 4096) + "\n"))
	require.NoError(t, err)
	require.Equal(t, "JOTIST_RECOVERY={\"action\":\"token_window_split\",\"retry\":1}\n", destination.String())
}
