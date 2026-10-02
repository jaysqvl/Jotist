package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"scriberr/internal/models"
)

type QueueSettingsRepository interface {
	LoadWorkers(context.Context) (int, error)
	SaveWorkers(context.Context, int) error
}

type queueSettingsRepository struct{ db *gorm.DB }

func NewQueueSettingsRepository(db *gorm.DB) QueueSettingsRepository {
	return &queueSettingsRepository{db: db}
}

func (r *queueSettingsRepository) LoadWorkers(ctx context.Context) (int, error) {
	var settings models.QueueSetting
	err := r.db.WithContext(ctx).First(&settings, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.DefaultQueueWorkers, nil
	}
	if err != nil {
		return 0, err
	}
	if err := models.ValidateQueueWorkers(settings.Workers); err != nil {
		return 0, err
	}
	return settings.Workers, nil
}

func (r *queueSettingsRepository) SaveWorkers(ctx context.Context, workers int) error {
	if err := models.ValidateQueueWorkers(workers); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"workers", "updated_at"}),
	}).Create(&models.QueueSetting{ID: 1, Workers: workers}).Error
}
