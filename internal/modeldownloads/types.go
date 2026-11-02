// Package modeldownloads owns manual weight downloads separately from runs.
package modeldownloads

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnknown     = errors.New("model is not available for manual download")
	ErrUnavailable = errors.New("model downloads are unavailable")
	ErrFull        = errors.New("the model download queue is full")
	ErrNotFound    = errors.New("model download was not found")
	ErrFinished    = errors.New("model download is already finished")
)

// Model is a server-owned catalog entry. The API accepts only ID, never a URL,
// repository, revision, destination, shell command, or model-code argument.
type Model struct {
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Name         string   `json:"name"`
	Purpose      string   `json:"purpose"`
	Dependencies []string `json:"dependencies"`
	Gated        bool     `json:"gated"`
}

type Job struct {
	ID              string     `json:"id"`
	OwnerID         uint       `json:"-"`
	CanCancel       bool       `json:"can_cancel"`
	ModelID         string     `json:"model_id"`
	Source          string     `json:"source"`
	Name            string     `json:"name"`
	State           string     `json:"state"`
	Phase           string     `json:"phase"`
	KeepDownloaded  bool       `json:"keep_downloaded"`
	DownloadedBytes int64      `json:"downloaded_bytes"`
	TotalBytes      int64      `json:"total_bytes"`
	CompletedFiles  int        `json:"completed_files"`
	TotalFiles      int        `json:"total_files"`
	ErrorCode       string     `json:"error_code,omitempty"`
	Message         string     `json:"message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

func (j Job) Active() bool {
	return j.State == "queued" || j.State == "downloading" || j.State == "canceling"
}

type Progress struct {
	Phase           string `json:"phase"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	CompletedFiles  int    `json:"completed_files"`
	TotalFiles      int    `json:"total_files"`
}

// DownloadError carries only a fixed public classification. Child output and
// credentials must never become a persisted error or an API response.
type DownloadError struct{ Code string }

func (e DownloadError) Error() string { return e.Code }

type Fetcher interface {
	Catalog() []Model
	Download(context.Context, Model, string, func(Progress)) error
}

type Store interface {
	Recover(context.Context) error
	Enqueue(context.Context, uint, Model, bool) (Job, bool, error)
	List(context.Context) ([]Job, error)
	Next(context.Context) (*Job, error)
	Progress(context.Context, string, Progress) (Job, error)
	Finish(context.Context, string, string, string, string) (Job, error)
	Cancel(context.Context, string, uint) (Job, error)
}

type Event struct {
	Type string `json:"type"`
	Job  *Job   `json:"job,omitempty"`
}
