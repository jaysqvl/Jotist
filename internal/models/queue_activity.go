package models

import "time"

// QueueRuntimeSnapshot contains scheduler ownership, not process or GPU load.
type QueueRuntimeSnapshot struct {
	CapturedAt time.Time
	Workers    int
	Running    map[string]QueueRunningSnapshot
}

type QueueRunningSnapshot struct {
	QueueItemID, ExecutionID string
	Finishing                bool
}

// QueueActivity exposes current work without request bodies, transcripts,
// resource measurements, error text, paths or credentials.
type QueueActivity struct {
	GeneratedAt       time.Time            `json:"generated_at"`
	Workers           int                  `json:"workers"`
	BusyWorkers       int                  `json:"busy_workers"`
	WaitingRecordings int                  `json:"waiting_recordings"`
	QueuedRuns        int                  `json:"queued_runs"`
	Recordings        []QueueActivityEntry `json:"recordings"`
}

type QueueActivityEntry struct {
	RecordingID    string                   `json:"recording_id"`
	RecordingTitle string                   `json:"recording_title"`
	State          string                   `json:"state"`
	HasWorker      bool                     `json:"has_worker"`
	QueueItemID    string                   `json:"queue_item_id,omitempty"`
	QueuedAt       *time.Time               `json:"queued_at,omitempty"`
	ExecutionID    string                   `json:"execution_id,omitempty"`
	RunNumber      int                      `json:"run_number,omitempty"`
	ModelFamily    string                   `json:"model_family"`
	Model          string                   `json:"model"`
	ProfileName    string                   `json:"profile_name,omitempty"`
	Stage          string                   `json:"stage,omitempty"`
	StageNumber    int                      `json:"stage_number,omitempty"`
	StageTotal     int                      `json:"stage_total,omitempty"`
	StageScope     string                   `json:"stage_scope,omitempty"`
	StageState     string                   `json:"stage_state,omitempty"`
	RetryAt        *time.Time               `json:"retry_at,omitempty"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	QueuedRuns     int                      `json:"queued_runs"`
	QueuedJobs     []QueueActivityQueuedJob `json:"queued_jobs"`
}

type QueueActivityQueuedJob struct {
	QueueItemID string    `json:"queue_item_id"`
	QueuedAt    time.Time `json:"queued_at"`
	ModelFamily string    `json:"model_family"`
	Model       string    `json:"model"`
	ProfileName string    `json:"profile_name,omitempty"`
}
