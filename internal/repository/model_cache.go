package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ModelCacheRepository struct {
	db *gorm.DB
	mu sync.Mutex
}

func NewModelCacheRepository(db *gorm.DB) *ModelCacheRepository {
	return &ModelCacheRepository{db: db}
}

func (r *ModelCacheRepository) LoadPolicy(ctx context.Context) (modelstorage.CacheRetentionPolicy, error) {
	if r.db == nil {
		return modelstorage.CacheRetentionPolicy{}, modelstorage.ErrUnverified
	}
	var row models.ModelCacheSetting
	err := r.db.WithContext(ctx).First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return modelstorage.CacheRetentionPolicy{Enabled: true, RetentionDays: modelstorage.DefaultRetentionDays}, nil
	}
	return modelstorage.CacheRetentionPolicy{Enabled: row.Enabled, RetentionDays: row.RetentionDays,
		LastCheckedAt: row.LastCheckedAt, LastRemovedModels: row.LastRemovedModels,
		LastRemovedBytes: row.LastRemovedBytes, LastSkippedModels: row.LastSkippedModels,
		LastFailedModels: row.LastFailedModels}, err
}

func (r *ModelCacheRepository) SavePolicy(ctx context.Context, enabled bool, days int) error {
	if r.db == nil {
		return modelstorage.ErrUnverified
	}
	if err := modelstorage.ValidateRetentionDays(days); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "retention_days", "updated_at"}),
	}).Create(&models.ModelCacheSetting{ID: 1, Enabled: enabled, RetentionDays: days}).Error
}

func (r *ModelCacheRepository) Observe(ctx context.Context, entries []modelstorage.Entry, now time.Time) (map[string]modelstorage.CacheRecord, error) {
	if r.db == nil {
		return nil, modelstorage.ErrUnverified
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]modelstorage.CacheRecord, len(entries))
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []models.ModelCacheRecord
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		byID := make(map[string]models.ModelCacheRecord, len(rows))
		for _, row := range rows {
			byID[row.ID] = row
		}
		for _, entry := range entries {
			row, exists := byID[entry.ID]
			if exists && row.Source != entry.Source {
				return modelstorage.ErrUnverified
			}
			changed := !exists
			if !exists {
				row = models.ModelCacheRecord{ID: entry.ID, Source: entry.Source, CachedSince: now}
			}
			if row.CachedSince.IsZero() {
				row.CachedSince = now
				changed = true
			}
			if row.CacheVersion != entry.Version {
				if entry.Status != "removal_pending" || row.CachedSince.IsZero() {
					row.CachedSince = now
				}
				row.CacheVersion = entry.Version
				changed = true
			}
			if entry.LastUsed != nil && (row.LastUsedAt == nil || entry.LastUsed.After(*row.LastUsedAt)) {
				row.LastUsedAt = entry.LastUsed
				changed = true
			}
			if changed {
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
					return err
				}
			}
			result[entry.ID] = modelstorage.CacheRecord{Source: row.Source, CacheVersion: row.CacheVersion, CachedSince: row.CachedSince, LastUsed: row.LastUsedAt, KeepDownloaded: row.KeepDownloaded}
		}
		return nil
	})
	return result, err
}

func (r *ModelCacheRepository) ReadRecord(ctx context.Context, id string) (modelstorage.CacheRecord, error) {
	if r.db == nil {
		return modelstorage.CacheRecord{}, modelstorage.ErrUnverified
	}
	var row models.ModelCacheRecord
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	return modelstorage.CacheRecord{Source: row.Source, CacheVersion: row.CacheVersion, CachedSince: row.CachedSince, LastUsed: row.LastUsedAt, KeepDownloaded: row.KeepDownloaded}, err
}

func (r *ModelCacheRepository) SaveKeepDownloaded(ctx context.Context, id, source, version string, keep bool) error {
	if r.db == nil {
		return modelstorage.ErrUnverified
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.db.WithContext(ctx).Model(&models.ModelCacheRecord{}).
		Where("id = ? AND source = ? AND cache_version = ?", id, source, version).
		Update("keep_downloaded", keep)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return modelstorage.ErrChanged
	}
	return nil
}

func (r *ModelCacheRepository) SaveCleanup(ctx context.Context, result modelstorage.CleanupResult) error {
	if r.db == nil {
		return modelstorage.ErrUnverified
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.ModelCacheSetting{
			ID: 1, Enabled: true, RetentionDays: modelstorage.DefaultRetentionDays,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&models.ModelCacheSetting{}).Where("id = 1").Updates(map[string]interface{}{
			"last_checked_at": result.CheckedAt, "last_removed_models": result.RemovedModels,
			"last_removed_bytes": result.RemovedBytes, "last_skipped_models": result.SkippedModels,
			"last_failed_models": result.FailedModels,
		}).Error
	})
}
