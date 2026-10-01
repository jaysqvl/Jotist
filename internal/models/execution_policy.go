package models

// ExecutionPolicy is shared across models. A copy is resolved at admission so
// changing user defaults cannot change queued work or an execution's resume plan.
// Device fallback and shorter audio windows require separate stage permissions.
type ExecutionPolicy struct {
	AutomaticRecovery bool `json:"automatic_recovery"`
	MaxRetries        int  `json:"max_retries"`
	BackoffSeconds    int  `json:"backoff_seconds"`
	MaxBackoffSeconds int  `json:"max_backoff_seconds"`
	ReduceBatchSize   bool `json:"reduce_batch_size"`
	ReuseCheckpoints  bool `json:"reuse_checkpoints"`
}

func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{AutomaticRecovery: true, MaxRetries: 3, BackoffSeconds: 2, MaxBackoffSeconds: 30, ReduceBatchSize: true, ReuseCheckpoints: true}
}

func (u User) EffectiveExecutionPolicy() ExecutionPolicy {
	if u.ExecutionPolicy != nil {
		return *u.ExecutionPolicy
	}
	return DefaultExecutionPolicy()
}
