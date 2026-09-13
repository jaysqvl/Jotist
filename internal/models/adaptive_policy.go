package models

// AdaptiveStagePolicy is an explicit permission ceiling. Missing fields never
// authorize an alternate device or a context/window change.
type AdaptiveStagePolicy struct {
	Fixed               *AdaptiveStageSettings `json:"fixed,omitempty"`
	DeviceLocked        bool                   `json:"device_locked"`
	AllowCPU            bool                   `json:"allow_cpu"`
	CPUPrecision        string                 `json:"cpu_precision,omitempty"`
	MinBatchSize        int                    `json:"min_batch_size,omitempty"`
	AllowShorterWindows bool                   `json:"allow_shorter_windows"`
	WindowCandidates    []int                  `json:"window_candidates,omitempty"`
	MinWindowSeconds    int                    `json:"min_window_seconds,omitempty"`
	OverlapSeconds      float64                `json:"overlap_seconds,omitempty"`
}

type AdaptiveExecutionPolicy struct {
	Stages             map[string]AdaptiveStagePolicy `json:"stages,omitempty"`
	Learn              bool                           `json:"learn"`
	ProfileID          string                         `json:"profile_id,omitempty"`
	ProfileRevision    int64                          `json:"profile_revision,omitempty"`
	LearningGeneration int64                          `json:"learning_generation,omitempty"`
	SnapshotTaken      bool                           `json:"snapshot_taken,omitempty"`
	LearnedPlans       []AdaptivePlanSnapshot         `json:"learned_plans,omitempty"`
}
