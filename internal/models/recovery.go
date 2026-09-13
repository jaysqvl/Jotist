package models

import "time"

const (
	RecoveryPending     = "pending"
	RecoveryWaiting     = "waiting_for_resource"
	RecoveryRunning     = "running"
	RecoverySucceeded   = "succeeded"
	RecoveryRetryable   = "retryable"
	RecoveryBlocked     = "blocked"
	RecoveryFailed      = "failed"
	RecoveryCancelled   = "cancelled"
	RecoveryInterrupted = "interrupted"
)

// RecoveryStage is one immutable node identity beneath an exact execution.
// State and ownership change; a successful checkpoint selection never changes.
type RecoveryStage struct {
	ID                    string            `json:"id" gorm:"primaryKey;type:varchar(36)"`
	RecordingID           string            `json:"recording_id" gorm:"not null;index"`
	ExecutionID           string            `json:"execution_id" gorm:"not null;uniqueIndex:idx_recovery_execution_node"`
	NodeKey               string            `json:"node_key" gorm:"not null;uniqueIndex:idx_recovery_execution_node"`
	Kind                  string            `json:"kind" gorm:"not null"`
	SchemaVersion         string            `json:"schema_version" gorm:"not null"`
	CompatibilityKey      string            `json:"compatibility_key" gorm:"not null;index"`
	ProvenanceJSON        string            `json:"-" gorm:"type:text;not null"`
	DurationSeconds       float64           `json:"duration_seconds"`
	RecoverableBoundary   bool              `json:"recoverable_boundary"`
	State                 string            `json:"status" gorm:"not null;index"`
	OwnerGeneration       int64             `json:"owner_generation"`
	AttemptNumber         int               `json:"attempt_number"`
	CheckpointID          *string           `json:"checkpoint_id,omitempty" gorm:"index"`
	ReusedFromExecutionID *string           `json:"reused_from_execution_id,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	Attempts              []RecoveryAttempt `json:"attempts" gorm:"foreignKey:StageID;constraint:OnDelete:CASCADE"`
}

type RecoveryAttempt struct {
	ID               string             `json:"id" gorm:"primaryKey;type:varchar(36)"`
	StageID          string             `json:"stage_id" gorm:"not null;uniqueIndex:idx_recovery_stage_attempt"`
	ExecutionID      string             `json:"execution_id" gorm:"not null;index"`
	OwnerGeneration  int64              `json:"owner_generation" gorm:"not null"`
	AttemptNumber    int                `json:"attempt_number" gorm:"not null;uniqueIndex:idx_recovery_stage_attempt"`
	State            string             `json:"status" gorm:"not null;index"`
	Device           string             `json:"device,omitempty"`
	Precision        string             `json:"precision,omitempty"`
	BatchSize        int                `json:"batch_size,omitempty"`
	WindowSeconds    float64            `json:"window_seconds,omitempty"`
	SettingsHash     string             `json:"settings_hash,omitempty"`
	PlanVersion      int                `json:"plan_version"`
	OverlapSeconds   float64            `json:"overlap_seconds,omitempty"`
	StitchingVersion string             `json:"stitching_version,omitempty"`
	CompatibilityKey string             `json:"-"`
	ProvenanceJSON   string             `json:"-" gorm:"type:text"`
	Measurements     *StageMeasurements `json:"measurements,omitempty" gorm:"serializer:json;type:text"`
	Reason           string             `json:"reason,omitempty"`
	ErrorCode        string             `json:"error_code,omitempty"`
	CheckpointID     *string            `json:"checkpoint_id,omitempty"`
	StartedAt        time.Time          `json:"started_at"`
	CompletedAt      *time.Time         `json:"completed_at,omitempty"`
}

// Device samples include all allocations, while process peaks include only
// verified descendants of this coordinator. Missing readings stay unknown.
type StageMeasurements struct {
	TorchPeakAllocatedBytes   *int64  `json:"torch_peak_allocated_bytes,omitempty"`
	TorchPeakReservedBytes    *int64  `json:"torch_peak_reserved_bytes,omitempty"`
	HostTotalBytes            *int64  `json:"host_total_bytes,omitempty"`
	HostAvailableBeforeBytes  *int64  `json:"host_available_before_bytes,omitempty"`
	HostMinimumAvailableBytes *int64  `json:"host_minimum_available_bytes,omitempty"`
	GPUTotalBytes             *int64  `json:"gpu_total_bytes,omitempty"`
	DeviceUsedBeforeBytes     *int64  `json:"device_used_before_bytes,omitempty"`
	DevicePeakUsedBytes       *int64  `json:"device_peak_used_bytes,omitempty"`
	ProcessPeakBytes          *int64  `json:"process_peak_bytes,omitempty"`
	AvailableAfterBytes       *int64  `json:"available_after_bytes,omitempty"`
	ExternalContention        bool    `json:"external_contention"`
	OwnershipUnknown          bool    `json:"ownership_unknown,omitempty"`
	Samples                   int     `json:"samples"`
	ElapsedSeconds            float64 `json:"elapsed_seconds"`
	Scope                     string  `json:"scope"`
}

// Compatibility is deliberately not unique: force-fresh runs keep independent
// immutable artifacts, even when their configuration is exactly the same.
type RecoveryCheckpoint struct {
	ID                  string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	RecordingID         string    `json:"recording_id" gorm:"not null;index:idx_recovery_checkpoint_lookup"`
	CompatibilityKey    string    `json:"compatibility_key" gorm:"not null;index:idx_recovery_checkpoint_lookup"`
	Kind                string    `json:"kind" gorm:"not null"`
	SchemaVersion       string    `json:"schema_version" gorm:"not null"`
	ProducerExecutionID string    `json:"producer_execution_id" gorm:"not null;index"`
	ProducerAttemptID   string    `json:"producer_attempt_id" gorm:"not null;uniqueIndex"`
	ResultSHA256        string    `json:"result_sha256" gorm:"not null"`
	ManifestSHA256      string    `json:"manifest_sha256" gorm:"not null"`
	RelativePath        string    `json:"-" gorm:"not null;uniqueIndex"`
	SizeBytes           int64     `json:"size_bytes"`
	CreatedAt           time.Time `json:"created_at" gorm:"index"`
}

// Dependencies protect upstream artifacts even after their original execution
// is removed, while a retained downstream checkpoint still refers to them.
type RecoveryCheckpointDependency struct {
	CheckpointID         string `gorm:"primaryKey"`
	UpstreamCheckpointID string `gorm:"primaryKey;index"`
}

// A tombstone fences late writes before filesystem deletion. Failed cleanup is
// durable and retryable even after the recording row itself has disappeared.
type RecoveryDeletion struct {
	RecordingID string     `json:"recording_id" gorm:"primaryKey"`
	RequestedAt time.Time  `json:"requested_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
}
