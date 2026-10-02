package transcription

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
)

func TestSharedExecutionPolicyBoundsAutomaticStageRetries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		enabled        bool
		retries, calls int
	}{
		{"off", false, 3, 1}, {"zero", true, 0, 1}, {"one", true, 1, 2}, {"two", true, 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStageTestFixture(t)
			r := f.execution(t, RecoveryBatchManagement, false)
			desc, rule, _ := adaptivePolicyFixture()
			rule.AllowCPU, rule.AllowShorterWindows = false, false
			setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"recognition": rule})
			r.execution.ActualParameters.ExecutionPolicy = &models.ExecutionPolicy{AutomaticRecovery: tc.enabled, MaxRetries: tc.retries}
			adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
			params := stageTestParams()
			params["device"], params["precision"], params["batch_size"], params["chunk_duration"] = "cuda", "float16", 8, 60
			calls := 0
			_, _, err := runRecoverableStage(context.Background(), r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
				calls++
				require.Equal(t, "cuda", actual["device"])
				require.Equal(t, float64(60), stageWindow(actual))
				require.Equal(t, params["context"], actual["context"])
				return nil, syntheticGPUOOM()
			})
			require.Error(t, err)
			require.Equal(t, tc.calls, calls)
			stage := f.stage(t, r.execution.ID, "recognition")
			require.Equal(t, models.RecoveryFailed, stage.State)
			require.Len(t, stage.Attempts, tc.calls)
		})
	}
}

func TestRetryBackoffIsBoundedAndCancellable(t *testing.T) {
	policy := models.ExecutionPolicy{BackoffSeconds: 2, MaxBackoffSeconds: 7}
	for retry, seconds := range []int{0, 2, 4, 7, 7} {
		require.Equal(t, time.Duration(seconds)*time.Second, retryBackoff(&policy, retry))
	}
	require.Zero(t, retryBackoff(nil, 1), "legacy saved plans keep their original timing")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- waitRetryBackoff(ctx, time.Minute) }()
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("backoff did not stop on cancellation")
	}
}

func TestScheduledStageRetryRemainsVisibleAndCancellationStopsInference(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryBatchManagement, false)
	desc, rule, _ := adaptivePolicyFixture()
	rule.AllowCPU, rule.AllowShorterWindows = false, false
	setAdaptiveExecutionPolicy(t, f, &r, map[string]models.AdaptiveStagePolicy{"recognition": rule})
	r.execution.ActualParameters.ExecutionPolicy = &models.ExecutionPolicy{AutomaticRecovery: true, MaxRetries: 3, BackoffSeconds: 60, MaxBackoffSeconds: 60}
	adapter := newAdaptiveExecutionAdapter(t, "recognition", desc)
	params := stageTestParams()
	params["device"], params["precision"], params["batch_size"] = "cuda", "float16", 8
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		_, _, err := runRecoverableStage(ctx, r, "recognize", "recognition", adapter, f.input, params, f.proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
			calls.Add(1)
			return nil, syntheticGPUOOM()
		})
		result <- err
	}()
	require.Eventually(t, func() bool {
		var stage models.RecoveryStage
		if err := f.db.Preload("Attempts").Where("execution_id = ? AND node_key = ?", r.execution.ID, "recognition").First(&stage).Error; err != nil {
			return false
		}
		for _, attempt := range stage.Attempts {
			if attempt.State == models.RecoveryWaiting && attempt.RetryAt != nil && attempt.RetryAt.After(time.Now()) {
				return stage.State == models.RecoveryWaiting
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("stage retry did not stop promptly")
	}
	require.Equal(t, int32(1), calls.Load(), "cancellation during backoff must prevent another inference attempt")
	stage := f.stage(t, r.execution.ID, "recognition")
	require.Equal(t, models.RecoveryCancelled, stage.State)
	require.Len(t, stage.Attempts, 2)
	require.Equal(t, models.RecoveryCancelled, stage.Attempts[1].State)
}
