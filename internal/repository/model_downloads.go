package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"gorm.io/gorm"
)

type ModelDownloadRepository struct {
	db *gorm.DB
	mu sync.Mutex
}

func NewModelDownloadRepository(db *gorm.DB) *ModelDownloadRepository {
	return &ModelDownloadRepository{db: db}
}

// Durable leases protect files even when a prior download's child cannot be
// proven stopped after an unclean native-server shutdown. Only a proven worker
// boundary allows Service.Start to reconcile these rows for resumption.
func ReadModelDownloadUses(ctx context.Context, db *gorm.DB, catalog []modeldownloads.Model) ([]modelstorage.Use, error) {
	if db == nil {
		return nil, modelstorage.ErrUnverified
	}
	var rows []models.ModelDownload
	if err := db.WithContext(ctx).Where("active_key IS NOT NULL").Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]modeldownloads.Model)
	for _, model := range catalog {
		byID[model.ID] = model
	}
	uses := make([]modelstorage.Use, 0, len(rows))
	for _, row := range rows {
		model, ok := byID[row.ModelID]
		use := modelstorage.Use{Stage: "model download", State: "downloading"}
		if ok && model.Source == row.Source {
			use.Resources = append([]string{model.Source}, model.Dependencies...)
		} else {
			use.All = true
		}
		if row.State == "queued" {
			use.State = "queued"
		}
		uses = append(uses, use)
	}
	return uses, nil
}

func downloadJob(row models.ModelDownload) modeldownloads.Job {
	return modeldownloads.Job{ID: row.ID, OwnerID: row.OwnerID, ModelID: row.ModelID, Source: row.Source, Name: row.Name,
		State: row.State, Phase: row.Phase, KeepDownloaded: row.KeepDownloaded, DownloadedBytes: row.DownloadedBytes,
		TotalBytes: row.TotalBytes, CompletedFiles: row.CompletedFiles, TotalFiles: row.TotalFiles, ErrorCode: row.ErrorCode,
		Message: row.Message, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, FinishedAt: row.FinishedAt}
}

func (r *ModelDownloadRepository) Recover(ctx context.Context) error {
	if r.db == nil {
		return modeldownloads.ErrUnavailable
	}
	// No download subprocess survives a clean or crashed server boundary. Keep
	// completed/canceled jobs intact, resume interrupted downloads from HF's cache,
	// and honor a cancellation that was durable before the interruption.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&models.ModelDownload{}).Where("state = ?", "canceling").Updates(map[string]any{
			"state": "canceled", "phase": "canceled", "active_key": nil, "finished_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&models.ModelDownload{}).Where("state = ?", "downloading").Updates(map[string]any{
			"state": "queued", "phase": "queued", "downloaded_bytes": 0, "total_bytes": 0, "completed_files": 0, "total_files": 0,
		}).Error
	})
}

func (r *ModelDownloadRepository) Enqueue(ctx context.Context, owner uint, model modeldownloads.Model, keep bool) (modeldownloads.Job, bool, error) {
	if r.db == nil {
		return modeldownloads.Job{}, false, modeldownloads.ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var row models.ModelDownload
	created := false
	err := retryDatabaseContention(ctx, func() error {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			created = false
			err := tx.Where("active_key = ?", model.Source).First(&row).Error
			if err == nil {
				// Repeated or uncertain admissions return the existing job. A request
				// that omits Keep downloaded can never undo an earlier keep request.
				if keep && !row.KeepDownloaded {
					if err := tx.Model(&row).Update("keep_downloaded", true).Error; err != nil {
						return err
					}
				}
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var count int64
			if err := tx.Model(&models.ModelDownload{}).Where("active_key IS NOT NULL").Count(&count).Error; err != nil {
				return err
			}
			if count >= 20 {
				return modeldownloads.ErrFull
			}
			source := model.Source
			row = models.ModelDownload{ID: uuid.NewString(), OwnerID: owner, ModelID: model.ID, Source: source, Name: model.Name,
				ActiveKey: &source, State: "queued", Phase: "queued", KeepDownloaded: keep}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			created = true
			// Retain a bounded recent history independently of recording/run rows.
			return tx.Exec("DELETE FROM model_downloads WHERE active_key IS NULL AND id NOT IN (SELECT id FROM model_downloads WHERE active_key IS NULL ORDER BY created_at DESC, id DESC LIMIT 50)").Error
		})
	})
	return downloadJob(row), created, err
}

func (r *ModelDownloadRepository) List(ctx context.Context) ([]modeldownloads.Job, error) {
	if r.db == nil {
		return nil, modeldownloads.ErrUnavailable
	}
	var rows []models.ModelDownload
	if err := r.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(70).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]modeldownloads.Job, 0, len(rows))
	for _, row := range rows {
		result = append(result, downloadJob(row))
	}
	return result, nil
}

func (r *ModelDownloadRepository) Next(ctx context.Context) (*modeldownloads.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var row models.ModelDownload
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("state = ?", "queued").Order("created_at, id").First(&row).Error; err != nil {
			return err
		}
		result := tx.Model(&row).Where("state = ?", "queued").Updates(map[string]any{"state": "downloading", "phase": "preparing"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return modeldownloads.ErrFinished
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job := downloadJob(row)
	return &job, nil
}

func (r *ModelDownloadRepository) Progress(ctx context.Context, id string, p modeldownloads.Progress) (modeldownloads.Job, error) {
	result := r.db.WithContext(ctx).Model(&models.ModelDownload{}).Where("id = ? AND state = ?", id, "downloading").Updates(map[string]any{
		"phase": p.Phase, "downloaded_bytes": p.DownloadedBytes, "total_bytes": p.TotalBytes, "completed_files": p.CompletedFiles, "total_files": p.TotalFiles,
	})
	if result.Error != nil {
		return modeldownloads.Job{}, result.Error
	}
	return r.get(ctx, id)
}

func (r *ModelDownloadRepository) Finish(ctx context.Context, id, state, code, message string) (modeldownloads.Job, error) {
	// A racing cancellation wins over a completed subprocess. It never causes
	// us to delete valid/shared files; a later download may reuse them.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.ModelDownload
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return err
		}
		if row.State == "canceling" {
			state, code, message = "canceled", "", "Download canceled. Completed files can be reused."
		}
		if row.ActiveKey == nil {
			return nil
		}
		now := time.Now().UTC()
		return tx.Model(&row).Updates(map[string]any{"state": state, "phase": state, "error_code": code, "message": message,
			"active_key": nil, "finished_at": now, "updated_at": now}).Error
	})
	if err != nil {
		return modeldownloads.Job{}, err
	}
	return r.get(ctx, id)
}

func (r *ModelDownloadRepository) Cancel(ctx context.Context, id string, owner uint) (modeldownloads.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.ModelDownload
		if err := tx.First(&row, "id = ? AND owner_id = ?", id, owner).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return modeldownloads.ErrNotFound
			}
			return err
		}
		if row.State == "canceling" {
			return nil
		}
		if row.ActiveKey == nil {
			return modeldownloads.ErrFinished
		}
		updates := map[string]any{"state": "canceling", "phase": "canceling"}
		if row.State == "queued" {
			updates = map[string]any{"state": "canceled", "phase": "canceled", "active_key": nil, "finished_at": time.Now().UTC()}
		}
		return tx.Model(&row).Updates(updates).Error
	})
	if err != nil {
		return modeldownloads.Job{}, err
	}
	return r.get(ctx, id)
}

func (r *ModelDownloadRepository) get(ctx context.Context, id string) (modeldownloads.Job, error) {
	var row models.ModelDownload
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	return downloadJob(row), err
}
