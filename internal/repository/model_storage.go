package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"gorm.io/gorm"
)

// Only model-selection fields are read from embedded parameter columns. Prompts,
// credentials and transcripts never enter an inventory projection.
const storageParameterColumns = "model_family, model, model_dir, model_cache_only, no_align, align_model, diarize, diarize_model, diarization_checkpoint"

type storageJob struct {
	ID         string
	Title      string
	Status     string
	Parameters models.WhisperXParams `gorm:"embedded"`
}

// ReadModelStorageUses reads immutable requests as well as legacy immediate and
// quick jobs. Called inside the process-wide removal/admission barrier.
func ReadModelStorageUses(ctx context.Context, db *gorm.DB) ([]modelstorage.Use, error) {
	if db == nil {
		return nil, errors.New("model storage database unavailable")
	}
	var result []modelstorage.Use
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var queued []struct{ ID, RecordingID, Title, Status, ParametersJSON string }
		if err := tx.Table("transcription_queue_items AS q").Select("q.id, q.transcription_job_id AS recording_id, COALESCE(j.title, 'Untitled recording') AS title, q.status, q.parameters_json").
			Joins("LEFT JOIN transcription_jobs AS j ON j.id = q.transcription_job_id").Where("q.status IN ?", []string{"queued", "pending", "processing"}).Scan(&queued).Error; err != nil {
			return err
		}
		for _, row := range queued {
			var params models.WhisperXParams
			if err := json.Unmarshal([]byte(row.ParametersJSON), &params); err != nil {
				return errors.New("queued model identity unavailable")
			}
			state := "queued"
			if row.Status == "processing" {
				state = "in_use"
			}
			result = append(result, modelstorage.RequestUse(params, row.RecordingID, row.Title, state))
		}
		var jobs []storageJob
		if err := tx.Table("transcription_jobs").Select("id, COALESCE(title, 'Untitled recording') AS title, status, "+storageParameterColumns).
			Where("status IN ? AND deleted_at IS NULL", []string{"pending", "processing"}).Scan(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			state := "queued"
			if job.Status == "processing" {
				state = "in_use"
			}
			result = append(result, modelstorage.RequestUse(job.Parameters, job.ID, job.Title, state))
		}
		// Resumed executions use their immutable actual parameters even if the
		// parent recording now displays a newer configuration.
		var executions []struct {
			TranscriptionJobID, Title, Status, RecordingStatus string
			ActualParameters                                   models.WhisperXParams `gorm:"embedded;embeddedPrefix:actual_"`
		}
		if err := tx.Table("transcription_job_executions AS e").Select("e.transcription_job_id, COALESCE(j.title, 'Untitled recording') AS title, e.status, j.status AS recording_status, "+prefixedStorageColumns("e.actual_")).
			Joins("LEFT JOIN transcription_jobs AS j ON j.id = e.transcription_job_id").Where("e.status IN ?", []string{"pending", "processing"}).Scan(&executions).Error; err != nil {
			return err
		}
		for _, execution := range executions {
			state := "queued"
			if execution.Status == "processing" || execution.RecordingStatus == "processing" {
				state = "in_use"
			}
			result = append(result, modelstorage.RequestUse(execution.ActualParameters, execution.TranscriptionJobID, execution.Title, state))
		}
		return nil
	})
	return result, err
}

func prefixedStorageColumns(prefix string) string {
	return prefix + "model_family, " + prefix + "model, " + prefix + "model_dir, " + prefix + "model_cache_only, " + prefix + "no_align, " + prefix + "align_model, " + prefix + "diarize, " + prefix + "diarize_model, " + prefix + "diarization_checkpoint"
}

func ReadModelStorageDetails(ctx context.Context, db *gorm.DB, catalog []modelstorage.Model) (map[string]modelstorage.Details, error) {
	result := make(map[string]modelstorage.Details)
	if db == nil {
		return result, errors.New("model storage database unavailable")
	}
	var profiles []models.TranscriptionProfile
	if err := db.WithContext(ctx).Select(storageParameterColumns).Find(&profiles).Error; err != nil {
		return result, err
	}
	var executions []models.TranscriptionJobExecution
	if err := db.WithContext(ctx).Select("started_at, " + prefixedStorageColumns("actual_")).Order("started_at DESC").Find(&executions).Error; err != nil {
		return result, err
	}
	for _, model := range catalog {
		details := modelstorage.Details{}
		for _, profile := range profiles {
			if modelstorage.ReferencesSelection(model, profile.Parameters) {
				details.Profiles++
				if profile.Parameters.ModelCacheOnly {
					details.CachedOnlyProfiles++
				}
			}
		}
		for _, execution := range executions {
			if modelstorage.ReferencesSelection(model, execution.ActualParameters) {
				stamp := execution.StartedAt
				details.LastUsed = &stamp
				break
			}
		}
		result[model.Source] = details
	}
	return result, nil
}

// Custom model_dir paths are cache roots, not arbitrary removal targets. The
// scanner still accepts only registered Hub repositories or exact aligner files.
func ReadModelStorageDirectories(ctx context.Context, db *gorm.DB) ([]string, error) {
	if db == nil {
		return nil, errors.New("model storage database unavailable")
	}
	var result []string
	for _, spec := range []struct{ table, column string }{
		{"transcription_profiles", "model_dir"}, {"transcription_jobs", "model_dir"}, {"transcription_job_executions", "actual_model_dir"},
	} {
		var dirs []string
		if err := db.WithContext(ctx).Table(spec.table).Distinct(spec.column).Where(spec.column+" IS NOT NULL AND "+spec.column+" != ''").Pluck(spec.column, &dirs).Error; err != nil {
			return nil, err
		}
		result = append(result, dirs...)
	}
	var requests []string
	if err := db.WithContext(ctx).Table("transcription_queue_items").Where("status IN ?", []string{"queued", "pending", "processing"}).Pluck("parameters_json", &requests).Error; err != nil {
		return nil, err
	}
	for _, request := range requests {
		var params struct {
			ModelDir *string `json:"model_dir"`
		}
		if err := json.Unmarshal([]byte(request), &params); err != nil {
			return nil, err
		}
		if params.ModelDir != nil && *params.ModelDir != "" {
			result = append(result, *params.ModelDir)
		}
	}
	return result, nil
}
