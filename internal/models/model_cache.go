package models

import "time"

// ModelCacheSetting is a server-wide policy. Defaults are supplied by the
// repository so saving Enabled=false cannot be replaced by a database default.
type ModelCacheSetting struct {
	ID                uint `gorm:"primaryKey;check:model_cache_setting_singleton,id = 1"`
	Enabled           bool `gorm:"not null"`
	RetentionDays     int  `gorm:"not null;check:model_cache_retention_days,retention_days >= 1 AND retention_days <= 365"`
	LastCheckedAt     *time.Time
	LastRemovedModels int
	LastRemovedBytes  int64
	LastSkippedModels int
	LastFailedModels  int
	UpdatedAt         time.Time
}

// ModelCacheRecord survives deletion of a recording's execution history. Cache
// versions describe model metadata, never model contents or user documents.
type ModelCacheRecord struct {
	ID           string `gorm:"primaryKey;size:32"`
	Source       string `gorm:"not null"`
	CacheVersion string `gorm:"not null;size:64"`
	CachedSince  time.Time
	LastUsedAt   *time.Time
}
