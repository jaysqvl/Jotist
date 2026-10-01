package models

import "time"

// RunHistoryEntry is a small directory entry, without transcript content,
// resource JSON, private configuration, paths or credentials.
type RunHistoryEntry struct {
	ID                 string     `json:"id"`
	TranscriptionJobID string     `json:"transcription_job_id"`
	RecordingTitle     string     `json:"recording_title"`
	RunNumber          int        `json:"run_number"`
	ProfileName        string     `json:"profile_name"`
	Status             string     `json:"status"`
	ModelFamily        string     `json:"model_family"`
	Model              string     `json:"model"`
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	ProcessingDuration *int64     `json:"processing_duration"`
}

type RunHistory struct {
	Runs       []RunHistoryEntry `json:"runs"`
	Pagination struct {
		Page  int   `json:"page"`
		Limit int   `json:"limit"`
		Total int64 `json:"total"`
		Pages int   `json:"pages"`
	} `json:"pagination"`
}
