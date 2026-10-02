package transcription

import (
	"context"
	"fmt"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
)

func ValidateExecutionPolicy(policy models.ExecutionPolicy) error {
	switch policy.RecoveryStrength {
	case "", models.RecoveryStandard, models.RecoveryStrong, models.RecoveryAggressive:
	default:
		return fmt.Errorf("recovery_strength must be standard, strong or aggressive")
	}
	if policy.MaxRetries < 0 || policy.MaxRetries > 6 {
		return fmt.Errorf("max_retries must be between 0 and 6")
	}
	if policy.BackoffSeconds < 0 || policy.BackoffSeconds > 120 || policy.MaxBackoffSeconds < policy.BackoffSeconds || policy.MaxBackoffSeconds > 300 {
		return fmt.Errorf("retry backoff must be between 0 and 120 seconds, with a maximum between the initial delay and 300 seconds")
	}
	return nil
}

// Canonical strengths are permissions, independent of normal stage separation.
// Old modes remain readable for immutable historical execution plans.
func SharedRecoveryMode(policy models.ExecutionPolicy) string {
	if !policy.AutomaticRecovery {
		return RecoveryFixed
	}
	switch policy.RecoveryStrength {
	case models.RecoveryStrong, models.RecoveryAggressive:
		return policy.RecoveryStrength
	default:
		return models.RecoveryStandard
	}
}

func permitsCPUFallback(mode string) bool {
	return mode == RecoveryCPUFallback || mode == RecoveryShorterWindows || mode == models.RecoveryAggressive
}

func permitsShorterWindows(mode string) bool {
	return mode == RecoveryShorterWindows || mode == models.RecoveryStrong || mode == models.RecoveryAggressive
}

func ValidateExecutionPolicyOptions(params models.WhisperXParams) error {
	switch params.ExecutionPolicySource {
	case "", "global", "override":
	default:
		return fmt.Errorf("execution_policy_source must be global or override")
	}
	if params.ExecutionPolicySource == "override" && params.ExecutionPolicy == nil {
		return fmt.Errorf("an execution policy override must include retry settings")
	}
	if params.ExecutionPolicy != nil {
		return ValidateExecutionPolicy(*params.ExecutionPolicy)
	}
	return nil
}

// The finite capability-qualified ladder remains the upper bound, even when a
// larger retry budget is selected. Manual resume also retains its seven-attempt
// lifetime fence. Legacy runs have no new policy and keep their original timing.
func automaticRetryAllowed(policy *models.ExecutionPolicy, retries int) bool {
	return policy == nil || (policy.AutomaticRecovery && retries < policy.MaxRetries)
}

func retryBackoff(policy *models.ExecutionPolicy, retry int) time.Duration {
	if policy == nil || retry < 1 {
		return 0
	}
	delay := policy.BackoffSeconds
	for i := 1; i < retry && delay < policy.MaxBackoffSeconds; i++ {
		delay *= 2
	}
	if delay > policy.MaxBackoffSeconds {
		delay = policy.MaxBackoffSeconds
	}
	return time.Duration(delay) * time.Second
}

func waitRetryBackoff(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
