package models

import "time"

// AdaptiveStageSettings is a concrete execution choice, never a permission to
// invent a model, prompt, precision or window. Only qualified settings may learn.
type AdaptiveStageSettings struct {
	Device           string  `json:"device"`
	Precision        string  `json:"precision"`
	BatchSize        int     `json:"batch_size"`
	Concurrency      int     `json:"concurrency"`
	WindowSeconds    float64 `json:"window_seconds"`
	OverlapSeconds   float64 `json:"overlap_seconds"`
	StitchingVersion string  `json:"stitching_version,omitempty"`
}

// Scope contains fingerprints and workload classes, not audio, transcripts,
// prompts, credentials, paths, model download URLs or exception strings.
type AdaptiveLearningScope struct {
	StageKey              string  `json:"stage_key"`
	FixedSettingsHash     string  `json:"fixed_settings_hash"`
	RuntimeFingerprint    string  `json:"runtime_fingerprint"`
	ModelFingerprint      string  `json:"model_fingerprint"`
	HardwareFingerprint   string  `json:"hardware_fingerprint"`
	WorkloadClass         string  `json:"workload_class"`
	MemoryDomain          string  `json:"memory_domain"`
	MemoryCapacityBytes   int64   `json:"memory_capacity_bytes"`
	OriginalWindowSeconds float64 `json:"original_window_seconds"`
}

type AdaptiveObservation struct {
	ID                       string                `json:"id" gorm:"primaryKey"`
	ProfileID                string                `json:"profile_id" gorm:"not null;index:idx_adaptive_observation_scope"`
	ProfileRevision          int64                 `json:"profile_revision" gorm:"not null;index:idx_adaptive_observation_scope"`
	LearningGeneration       int64                 `json:"learning_generation" gorm:"not null;index:idx_adaptive_observation_scope"`
	ScopeKey                 string                `json:"scope_key" gorm:"not null;index:idx_adaptive_observation_scope"`
	Scope                    AdaptiveLearningScope `json:"scope" gorm:"serializer:json;type:text;not null"`
	ExecutionID              string                `json:"execution_id" gorm:"not null;index"`
	StageID                  string                `json:"stage_id" gorm:"not null"`
	AttemptID                string                `json:"attempt_id" gorm:"not null;uniqueIndex"`
	RecordingID              string                `json:"recording_id" gorm:"not null"`
	OwnerGeneration          int64                 `json:"owner_generation" gorm:"not null"`
	CandidateHash            string                `json:"candidate_hash" gorm:"not null;index"`
	Settings                 AdaptiveStageSettings `json:"settings" gorm:"serializer:json;type:text;not null"`
	Outcome                  string                `json:"outcome" gorm:"not null"`
	FullStage                bool                  `json:"full_stage"`
	Cached                   bool                  `json:"cached"`
	Qualified                bool                  `json:"qualified"`
	ExternalContention       bool                  `json:"external_contention"`
	PeakMemoryBytes          *int64                `json:"peak_memory_bytes,omitempty"`
	AvailableBeforeBytes     *int64                `json:"available_before_bytes,omitempty"`
	ReserveBytes             *int64                `json:"reserve_bytes,omitempty"`
	LoadingMilliseconds      *int64                `json:"loading_milliseconds,omitempty"`
	ProcessingMilliseconds   *int64                `json:"processing_milliseconds,omitempty"`
	SourceDurationSeconds    float64               `json:"source_duration_seconds"`
	ProcessedDurationSeconds float64               `json:"processed_duration_seconds"`
	Channels                 int                   `json:"channels"`
	CreatedAt                time.Time             `json:"created_at"`
}

// Plans and their supporting observation IDs are immutable. Selection is kept
// separately so resetting learning never rewrites evidence used by an old run.
type AdaptiveLearnedPlan struct {
	ID                         string                  `json:"id" gorm:"primaryKey"`
	ProfileID                  string                  `json:"profile_id" gorm:"not null;index"`
	ProfileRevision            int64                   `json:"profile_revision" gorm:"not null;index"`
	LearningGeneration         int64                   `json:"learning_generation" gorm:"not null;index"`
	ScopeKey                   string                  `json:"scope_key" gorm:"not null;index"`
	Scope                      AdaptiveLearningScope   `json:"scope" gorm:"serializer:json;type:text;not null"`
	CandidateHash              string                  `json:"candidate_hash" gorm:"not null"`
	Settings                   AdaptiveStageSettings   `json:"settings" gorm:"serializer:json;type:text;not null"`
	ObservationIDs             []string                `json:"observation_ids" gorm:"serializer:json;type:text;not null"`
	ExhaustionEvidenceIDs      []string                `json:"exhaustion_evidence_ids,omitempty" gorm:"serializer:json;type:text"`
	FullWindowCandidateHashes  []string                `json:"full_window_candidate_hashes,omitempty" gorm:"serializer:json;type:text"`
	ExhaustionAvailableBytes   int64                   `json:"exhaustion_available_bytes,omitempty"`
	ExhaustionCapacityLimits   []AdaptiveCapacityLimit `json:"exhaustion_capacity_limits,omitempty" gorm:"serializer:json;type:text"`
	PeakMemoryBytes            int64                   `json:"peak_memory_bytes"`
	MinimumReserveBytes        int64                   `json:"minimum_reserve_bytes"`
	MeanProcessingMilliseconds int64                   `json:"mean_processing_milliseconds"`
	CreatedAt                  time.Time               `json:"created_at"`
}

// A shorter-window shortcut must still fit while every exhausted device has
// no more available memory than in its comparable full-window failure.
type AdaptiveCapacityLimit struct {
	MemoryDomain        string `json:"memory_domain"`
	MemoryCapacityBytes int64  `json:"memory_capacity_bytes"`
	MaxAvailableBytes   int64  `json:"max_available_bytes"`
}

type AdaptiveCapacityReading struct {
	MemoryDomain        string `json:"memory_domain"`
	MemoryCapacityBytes int64  `json:"memory_capacity_bytes"`
	AvailableBytes      int64  `json:"available_bytes"`
}

type AdaptivePlanSelection struct {
	ProfileID          string    `json:"profile_id" gorm:"primaryKey"`
	ProfileRevision    int64     `json:"profile_revision" gorm:"primaryKey"`
	LearningGeneration int64     `json:"learning_generation" gorm:"primaryKey"`
	ScopeKey           string    `json:"scope_key" gorm:"primaryKey"`
	PlanID             string    `json:"plan_id"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AdaptivePlanSnapshot struct {
	PlanID             string                `json:"plan_id"`
	ScopeKey           string                `json:"scope_key"`
	ProfileRevision    int64                 `json:"profile_revision"`
	LearningGeneration int64                 `json:"learning_generation"`
	StageKey           string                `json:"stage_key"`
	CandidateHash      string                `json:"candidate_hash"`
	Settings           AdaptiveStageSettings `json:"settings"`
}

// Profile revision parameters are private profile data, with credentials
// removed before persistence. They are not copied into learning observations.
type AdaptiveProfileRevision struct {
	ID                 string         `json:"id" gorm:"primaryKey"`
	ProfileID          string         `json:"profile_id" gorm:"not null;uniqueIndex:idx_adaptive_profile_revision"`
	Revision           int64          `json:"revision" gorm:"not null;uniqueIndex:idx_adaptive_profile_revision"`
	LearningGeneration int64          `json:"learning_generation"`
	Name               string         `json:"name"`
	Description        *string        `json:"description,omitempty"`
	IsDefault          bool           `json:"is_default"`
	Parameters         WhisperXParams `json:"parameters" gorm:"serializer:json;type:text;not null"`
	Reason             string         `json:"reason"`
	SourceRevision     *int64         `json:"source_revision,omitempty"`
	SourcePlanID       *string        `json:"source_plan_id,omitempty"`
	SavedAt            time.Time      `json:"saved_at"`
}
