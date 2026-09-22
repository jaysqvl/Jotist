package transcription

import (
	"context"
	"encoding/json"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func TestLegacyCanaryCPUPrecisionMatchesStagePlanAndCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name, device, precision, wantDevice, wantPrecision string
		gpu                                                gpuInventory
	}{
		{name: "saved_cpu_bfloat16", device: "cpu", precision: "bfloat16", wantDevice: "cpu", wantPrecision: "float32"},
		{name: "saved_cpu_float16", device: "cpu", precision: "float16", wantDevice: "cpu", wantPrecision: "float32"},
		{name: "cpu_float32", device: "cpu", precision: "float32", wantDevice: "cpu", wantPrecision: "float32"},
		{name: "auto_without_gpu", device: "auto", precision: "bfloat16", wantDevice: "cpu", wantPrecision: "float32"},
		{name: "auto_with_gpu", device: "auto", precision: "bfloat16", wantDevice: "cuda", wantPrecision: "bfloat16", gpu: gpuInventory{UUID: "GPU-fixture"}},
		{name: "explicit_cuda_bfloat16", device: "cuda", precision: "bfloat16", wantDevice: "cuda", wantPrecision: "bfloat16", gpu: gpuInventory{UUID: "GPU-fixture"}},
		{name: "explicit_cuda_float16", device: "cuda", precision: "float16", wantDevice: "cuda", wantPrecision: "float16", gpu: gpuInventory{UUID: "GPU-fixture"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, "", true)
			r.gpu = test.gpu
			adapter := newStagedOrchestrationAdapter(t)
			adapter.capabilities.ModelFamily = "nvidia_canary"
			adapter.descriptors = adapters.NewCanaryAdapter(t.TempDir()).Stages()
			requested := models.WhisperXParams{Device: test.device, NvidiaPrecision: test.precision, Task: "transcribe", BatchSize: 1}
			params := (&UnifiedTranscriptionService{}).convertToCanaryParams(requested)
			before := copyStageParameters(params)
			var invoked map[string]interface{}
			calls := 0
			adapter.onStage = func(desc interfaces.StageDescriptor, actual map[string]interface{}, _ []byte) ([]byte, error) {
				calls++
				require.Equal(t, test.wantDevice, actual["device"])
				if desc.Kind == "recognition" {
					require.Equal(t, test.wantPrecision, actual["precision"])
					invoked = copyStageParameters(actual)
					return syntheticRecognitionState(t), nil
				}
				require.Equal(t, "float32", actual["alignment_precision"])
				return json.Marshal(stageTestTranscript("Synthetic parseHTTP words."))
			}
			result, _, err := runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Equal(t, before, params, "normalization must not rewrite the saved request")
			require.Equal(t, test.precision, requested.NvidiaPrecision)
			require.Equal(t, test.wantPrecision, result.Metadata["recognition_precision"])
			stage := f.stage(t, r.execution.ID, "recognition")
			require.Len(t, stage.Attempts, 1)
			require.Equal(t, test.wantDevice, stage.Attempts[0].Device)
			require.Equal(t, test.wantPrecision, stage.Attempts[0].Precision)
			var provenance repository.CheckpointProvenance
			require.NoError(t, json.Unmarshal([]byte(stage.ProvenanceJSON), &provenance))
			require.Equal(t, privateSettingsHash(invoked), provenance.SettingsHash, "the initial plan must describe the effective precision/device")
			require.Equal(t, provenance.SettingsHash, stage.Attempts[0].SettingsHash)
			_, payload, err := f.store.SelectedCheckpoint(context.Background(), stage.ID)
			require.NoError(t, err)
			var saved interfaces.TranscriptResult
			require.NoError(t, json.Unmarshal(payload, &saved))
			require.Equal(t, test.wantPrecision, saved.Metadata["precision"])
			f.resume(t, &r)
			_, _, err = runRecoverableTranscription(context.Background(), r, adapter, f.input, params, f.proc)
			require.NoError(t, err)
			require.Equal(t, 2, calls, "same-run resume must reuse both correctly normalized checkpoints")
		})
	}
}

func TestEffectiveStagePrecisionPreservesExplicitPoliciesAndOtherAdapters(t *testing.T) {
	for _, mode := range []string{RecoveryFixed, RecoveryStageManagement, RecoveryBatchManagement, RecoveryCPUFallback, RecoveryShorterWindows} {
		for _, device := range []string{"cpu", "auto", "cuda"} {
			request := map[string]interface{}{"device": device, "precision": "bfloat16", "batch_size": 4, "chunk_duration": 40}
			before := copyStageParameters(request)
			resolved := effectiveStageParameters(request, mode, gpuInventory{}, "nvidia_canary")
			require.Equal(t, "bfloat16", resolved["precision"], "%s must not inherit a legacy numerical conversion", mode)
			require.Equal(t, 4, resolved["batch_size"])
			require.Equal(t, 40, resolved["chunk_duration"])
			require.Equal(t, before, request)
		}
	}
	// Canary-Qwen's existing CPU BF16 branch is distinct from Canary's always-
	// FP32 CPU worker. A shared precision parameter is not conversion authority.
	for _, family := range []string{"nvidia_canary_qwen", "qwen3_asr", "whisper", "test"} {
		request := map[string]interface{}{"device": "cpu", "precision": "bfloat16", "compute_type": "int8"}
		require.Equal(t, request, effectiveStageParameters(request, "", gpuInventory{}, family))
	}
}
