package models

import "time"

// ModelDownload contains public weight-download metadata only. In particular,
// Hugging Face tokens, paths and subprocess output are never stored here.
type ModelDownload struct {
	ID              string  `gorm:"primaryKey;size:36"`
	OwnerID         uint    `gorm:"not null;index"`
	ModelID         string  `gorm:"not null;size:32"`
	Source          string  `gorm:"not null"`
	Name            string  `gorm:"not null"`
	ActiveKey       *string `gorm:"uniqueIndex"`
	State           string  `gorm:"not null;index"`
	Phase           string  `gorm:"not null"`
	KeepDownloaded  bool    `gorm:"not null"`
	DownloadedBytes int64
	TotalBytes      int64
	CompletedFiles  int
	TotalFiles      int
	ErrorCode       string
	Message         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      *time.Time
}
