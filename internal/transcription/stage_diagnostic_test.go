package transcription

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/transcription/interfaces"
)

func TestRecoveryPreservesControlledDiagnosticWithoutPrivateCause(t *testing.T) {
	for _, mode := range []string{"", RecoveryFixed} {
		t.Run(mode, func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, mode, false)
			a := &stageTestAdapter{path: t.TempDir(), capabilities: interfaces.ModelCapabilities{ModelID: "test"}}
			diagnostic := interfaces.NewSafeRuntimeDiagnostic("runtime_model_access", "Save a Hugging Face token in Settings.", errors.New("private hf_secret transcript"))
			_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", a, f.input, map[string]interface{}{"device": "cpu"}, f.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) { return nil, diagnostic })
			require.ErrorContains(t, err, "Save a Hugging Face token")
			require.NotContains(t, err.Error(), "hf_secret")
			log, readErr := os.ReadFile(filepath.Join(f.proc.OutputDirectory, "transcription.log"))
			require.NoError(t, readErr)
			require.Contains(t, string(log), "runtime_model_access")
			require.NotContains(t, string(log), "hf_secret")
		})
	}
}

func TestUnknownStageErrorStaysPrivate(t *testing.T) {
	err := errors.New("private hf_secret transcript")
	dir := t.TempDir()
	appendStageDiagnostic(dir, "asr", "attempt", err)
	data, e := os.ReadFile(filepath.Join(dir, "transcription.log"))
	require.NoError(t, e)
	require.NotContains(t, string(data), "hf_secret")
	require.NotContains(t, safeOrOriginalStageError("asr", "adapter_failed", err).Error(), "hf_secret")
}
