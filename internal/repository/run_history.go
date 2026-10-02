package repository

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"github.com/jaysqvl/Jotist/internal/models"
)

type RunHistoryFilter struct {
	Days, Page, Limit          int
	Query, Status, ExecutionID string
}

func ReadRunHistory(ctx context.Context, db *gorm.DB, filter RunHistoryFilter, now time.Time) (models.RunHistory, error) {
	result := models.RunHistory{Runs: []models.RunHistoryEntry{}}
	result.Pagination.Page, result.Pagination.Limit = filter.Page, filter.Limit
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		base := func() *gorm.DB {
			q := tx.Table("transcription_job_executions AS e").Joins("JOIN transcription_jobs AS j ON j.id = e.transcription_job_id AND j.deleted_at IS NULL").Joins("LEFT JOIN transcription_queue_items AS qi ON qi.id = e.queue_item_id")
			if filter.Days > 0 {
				q = q.Where("e.started_at >= ?", now.UTC().AddDate(0, 0, -filter.Days))
			}
			if filter.ExecutionID != "" {
				q = q.Where("e.id = ?", filter.ExecutionID)
			}
			if filter.Status == "active" {
				q = q.Where("e.status IN ?", []string{"pending", "processing", "running", "waiting", "waiting_for_resource"})
			} else if filter.Status != "" && filter.Status != "all" {
				q = q.Where("e.status = ?", filter.Status)
			}
			if term := strings.TrimSpace(filter.Query); term != "" {
				term = "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.ToLower(term)) + "%"
				q = q.Where(`(LOWER(COALESCE(j.title, '')) LIKE ? ESCAPE '\' OR LOWER(e.actual_model) LIKE ? ESCAPE '\' OR LOWER(e.actual_model_family) LIKE ? ESCAPE '\' OR LOWER(COALESCE(qi.profile_name, '')) LIKE ? ESCAPE '\' OR e.id LIKE ? ESCAPE '\')`, term, term, term, term, term)
			}
			return q
		}
		if err := base().Count(&result.Pagination.Total).Error; err != nil {
			return err
		}
		result.Pagination.Pages = int((result.Pagination.Total + int64(filter.Limit) - 1) / int64(filter.Limit))
		// Number against the recording's complete history, before date, status
		// or search filters. The ordering matches the recording's run picker.
		projection := `e.id, e.transcription_job_id, COALESCE(NULLIF(j.title, ''), 'Untitled recording') AS recording_title, COALESCE(qi.profile_name, '') AS profile_name, e.status, e.actual_model_family AS model_family, e.actual_model AS model, e.started_at, e.completed_at, e.processing_duration,
			1 + (SELECT COUNT(*) FROM transcription_job_executions AS older WHERE older.transcription_job_id = e.transcription_job_id AND (older.started_at < e.started_at OR (older.started_at = e.started_at AND older.created_at < e.created_at) OR (older.started_at = e.started_at AND older.created_at = e.created_at AND older.id < e.id))) AS run_number`
		return base().Select(projection).Order("e.started_at DESC, e.created_at DESC, e.id DESC").Offset((filter.Page - 1) * filter.Limit).Limit(filter.Limit).Scan(&result.Runs).Error
	})
	return result, err
}
