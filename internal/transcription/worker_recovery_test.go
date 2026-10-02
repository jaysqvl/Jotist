package transcription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestRecoveryStrengthsUseGPUWindowsBeforeCPULast(t *testing.T) {
	desc, _, original := adaptivePolicyFixture()
	for _, tc := range []struct {
		mode    string
		reasons []string
		device  string
	}{
		{models.RecoveryStandard, []string{"cleanup_retry", "smaller_batch", "smaller_batch"}, "cuda"},
		{models.RecoveryStrong, []string{"cleanup_retry", "smaller_batch", "smaller_batch", "shorter_window", "shorter_window"}, "cuda"},
		{models.RecoveryAggressive, []string{"cleanup_retry", "smaller_batch", "smaller_batch", "shorter_window", "shorter_window", "cpu_fallback"}, "cpu"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			rule := executionStageRule(models.WhisperXParams{RecoveryMode: tc.mode}, "recognize", desc)
			current, reason := original, "initial"
			history := []models.RecoveryAttempt{}
			for _, expected := range tc.reasons {
				history = adaptiveHistory(current, reason, history...)
				next := nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", rule, desc, original, current, history, false)
				require.NotNil(t, next)
				require.Equal(t, expected, next.Reason)
				current, reason = next.Settings, next.Reason
			}
			require.Equal(t, tc.device, current.Device)
			history = adaptiveHistory(current, reason, history...)
			require.Nil(t, nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", rule, desc, original, current, history, false))
			if tc.mode == models.RecoveryStrong {
				require.False(t, candidateAllowed(tc.mode, models.AdaptiveStagePolicy{AllowCPU: true, CPUPrecision: "float32"}, desc, original, models.AdaptiveStageSettings{Device: "cpu", Precision: "float32", BatchSize: 8, Concurrency: 1, WindowSeconds: 60}))
			}
		})
	}
	locked := models.WhisperXParams{RecoveryMode: models.RecoveryAggressive, AdaptivePolicy: &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"recognition": {DeviceLocked: true, MinBatchSize: 4}}}}
	require.True(t, executionStageRule(locked, "recognize", desc).DeviceLocked)
	desc.DevicePrecisions["cpu"] = []string{"int8"}
	require.False(t, executionStageRule(models.WhisperXParams{RecoveryMode: models.RecoveryAggressive}, "recognize", desc).AllowCPU, "never invent a CPU precision")
}

func TestWorkerAndCoordinatorShareOneStageRetryBudget(t *testing.T) {
	for _, max := range []int{2, 3} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, models.RecoveryStrong, false)
			p := models.DefaultExecutionPolicy()
			p.RecoveryStrength, p.MaxRetries, p.BackoffSeconds, p.MaxBackoffSeconds = models.RecoveryStrong, max, 0, 0
			r.execution.ActualParameters.ExecutionPolicy, r.execution.ActualParameters.RecoveryMode = &p, models.RecoveryStrong
			desc, _, _ := adaptivePolicyFixture()
			adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
			params := stageTestParams()
			params["device"], params["precision"], params["batch_size"] = "cuda", "float16", 8
			calls := 0
			_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				policy := actual["worker_recovery_policy"].(map[string]interface{})
				if calls == 1 {
					require.Equal(t, max, policy["remaining_retries"])
					require.NoError(t, os.WriteFile(filepath.Join(f.proc.OutputDirectory, "transcription.log"), []byte("JOTIST_RECOVERY={\"action\":\"decoder_budget_retry\",\"retry\":1}\nJOTIST_RECOVERY={\"action\":\"token_window_split\",\"retry\":2}\n"+strings.Repeat("unrelated diagnostic\n", 10000)), 0600))
				} else {
					require.Equal(t, 0, policy["remaining_retries"])
				}
				return nil, syntheticGPUOOM()
			})
			require.Error(t, err)
			require.Equal(t, max-1, calls)
			stage := f.stage(t, r.execution.ID, "recognition")
			require.Equal(t, 2, stage.Attempts[0].Measurements.WorkerRetryCount)
			require.Equal(t, 1, stage.Attempts[0].Measurements.WorkerRecoveryActions["token_window_split"])
			require.Equal(t, max, savedRetryUsage(stage.Attempts))
		})
	}
}

func TestWorkerPolicyRespectsOffContextLocksAndLegacyPlans(t *testing.T) {
	p := models.DefaultExecutionPolicy()
	params := models.WhisperXParams{RecoveryMode: models.RecoveryStrong, ExecutionPolicy: &p}
	require.True(t, workerRecoveryPolicy(params, 0, "recognition")["allow_output_changes"].(bool))
	params.RecoveryMode = models.RecoveryStandard
	require.False(t, workerRecoveryPolicy(params, 0, "recognition")["allow_output_changes"].(bool))
	params.RecoveryMode = models.RecoveryStrong
	params.AdaptivePolicy = &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"recognition": {DeviceLocked: true, AllowShorterWindows: false}}}
	require.False(t, workerRecoveryPolicy(params, 0, "recognition")["allow_output_changes"].(bool))
	p.AutomaticRecovery = false
	require.Equal(t, 0, workerRecoveryPolicy(params, 0, "recognition")["remaining_retries"])
	require.Nil(t, workerRecoveryPolicy(models.WhisperXParams{}, 0, "recognition"))
	p.RecoveryStrength = ""
	require.Nil(t, workerRecoveryPolicy(params, 0, "recognition"), "old saved plans retain their worker behavior")
}

func TestWorkerEvidenceMustBeSavedBeforeRetryAndMissingHistoryCannotResetBudget(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, models.RecoveryStrong, false)
	p := models.DefaultExecutionPolicy()
	p.RecoveryStrength, p.BackoffSeconds, p.MaxBackoffSeconds = models.RecoveryStrong, 0, 0
	r.execution.ActualParameters.ExecutionPolicy, r.execution.ActualParameters.RecoveryMode = &p, models.RecoveryStrong
	desc, _, _ := adaptivePolicyFixture()
	adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
	params := stageTestParams()
	params["device"], params["precision"], params["batch_size"] = "cuda", "float16", 8
	require.NoError(t, f.db.Exec("CREATE TRIGGER fail_measurement BEFORE UPDATE OF measurements ON recovery_attempts BEGIN SELECT RAISE(ABORT, 'fixture measurement persistence failure'); END").Error)
	calls := 0
	_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		return nil, syntheticGPUOOM()
	})
	require.ErrorContains(t, err, "recovery evidence could not be saved")
	require.Equal(t, 1, calls, "do not retry with an undurable budget")
	stage := f.stage(t, r.execution.ID, "recognition")
	require.Equal(t, models.RecoveryBlocked, stage.State)
	require.Nil(t, stage.Attempts[0].Measurements)
	require.Equal(t, p.MaxRetries, stageRetryUsage(stage.Attempts, &p))
	require.NoError(t, f.db.Exec("DROP TRIGGER fail_measurement").Error)
	f.resume(t, &r)
	_, _, err = runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
		calls++
		require.Equal(t, 0, actual["worker_recovery_policy"].(map[string]interface{})["remaining_retries"])
		return nil, syntheticGPUOOM()
	})
	require.Error(t, err)
	require.Equal(t, 2, calls, "manual resume cannot replenish an unknown inner retry budget")
}
