package models

import (
	"fmt"
	"time"
)

const DefaultQueueWorkers = 1
const MaxQueueWorkers = 16

// QueueSetting is a server-wide singleton, independent of users and profiles.
type QueueSetting struct {
	ID        uint      `gorm:"primaryKey;check:queue_setting_singleton,id = 1"`
	Workers   int       `gorm:"not null;default:1;check:queue_setting_workers,workers >= 1 AND workers <= 16"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

type QueueSettingsResponse struct {
	Workers             int  `json:"workers"`
	BusyWorkers         int  `json:"busy_workers"`
	MaxWorkers          int  `json:"max_workers"`
	EnvironmentOverride bool `json:"environment_override"`
}

func ValidateQueueWorkers(workers int) error {
	if workers < 1 || workers > MaxQueueWorkers {
		return fmt.Errorf("workers must be between 1 and %d", MaxQueueWorkers)
	}
	return nil
}
