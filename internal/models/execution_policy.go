package models

// ExecutionPolicy is shared across models. A copy is resolved at admission so
// changing user defaults cannot change queued work or an execution's resume plan.
// Strength grants recovery permissions; explicit stage constraints may narrow them.
type ExecutionPolicy struct {
	AutomaticRecovery bool   `json:"automatic_recovery" required:"false"`
	RecoveryStrength  string `json:"recovery_strength,omitempty"`
	MaxRetries        int    `json:"max_retries" required:"false"`
	BackoffSeconds    int    `json:"backoff_seconds" required:"false"`
	MaxBackoffSeconds int    `json:"max_backoff_seconds" required:"false"`
	ReduceBatchSize   bool   `json:"reduce_batch_size" required:"false"`
	ReuseCheckpoints  bool   `json:"reuse_checkpoints" required:"false"`
}

const (
	RecoveryStandard   = "standard"
	RecoveryStrong     = "strong"
	RecoveryAggressive = "aggressive"
)

func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{AutomaticRecovery: true, RecoveryStrength: RecoveryStandard, MaxRetries: 3, BackoffSeconds: 2, MaxBackoffSeconds: 30, ReduceBatchSize: true, ReuseCheckpoints: true}
}

func (u User) EffectiveExecutionPolicy() ExecutionPolicy {
	if u.ExecutionPolicy != nil {
		return *u.ExecutionPolicy
	}
	return DefaultExecutionPolicy()
}
