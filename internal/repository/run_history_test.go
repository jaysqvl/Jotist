package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"github.com/jaysqvl/Jotist/internal/models"
)

func TestRunHistoryKeepsRecordingNumbersAcrossFiltersAndProjectsOnlyDirectoryFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.TranscriptionQueueItem{}))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	title, secret := "Planning 100%", "synthetic-private-context"
	job := models.TranscriptionJob{ID: "history-recording", Title: &title, AudioPath: "private-audio-path.wav"}
	require.NoError(t, db.Create(&job).Error)
	for index, id := range []string{"old", "failed", "completed", "active"} {
		start := now.Add(time.Duration(index-5) * time.Hour)
		if id == "old" {
			start = now.AddDate(0, 0, -40)
		}
		state := models.StatusCompleted
		if id == "failed" {
			state = models.StatusFailed
		}
		if id == "active" {
			state = models.StatusProcessing
		}
		queueID := "queue-" + id
		profileName := "GPU demo profile"
		require.NoError(t, db.Create(&models.TranscriptionQueueItem{ID: queueID, TranscriptionJobID: job.ID, ProfileName: &profileName}).Error)
		run := models.TranscriptionJobExecution{ID: id, TranscriptionJobID: job.ID, QueueItemID: &queueID, StartedAt: start, CreatedAt: start, Status: state, Transcript: &secret, ActualParameters: models.WhisperXParams{ModelFamily: "qwen3_asr", Model: "Qwen model", HfToken: &secret, TranscriptionContext: &secret}}
		require.NoError(t, db.Create(&run).Error)
	}
	deleted := models.TranscriptionJob{ID: "deleted-history", AudioPath: "deleted.wav"}
	require.NoError(t, db.Create(&deleted).Error)
	require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: "deleted-run", TranscriptionJobID: deleted.ID, StartedAt: now, Status: models.StatusCompleted}).Error)
	require.NoError(t, db.Delete(&deleted).Error)
	filter := RunHistoryFilter{Days: 30, Page: 1, Limit: 2, Status: "all"}
	result, err := ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.Pagination.Total)
	require.Equal(t, 2, result.Pagination.Pages)
	require.Len(t, result.Runs, 2)
	require.Equal(t, "active", result.Runs[0].ID)
	require.Equal(t, 4, result.Runs[0].RunNumber)
	require.Equal(t, title, result.Runs[0].RecordingTitle)
	filter.Page = 2
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Runs[0].ID)
	require.Equal(t, 2, result.Runs[0].RunNumber)
	filter.Page, filter.Status = 1, "failed"
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Pagination.Total)
	require.Equal(t, 2, result.Runs[0].RunNumber)
	filter.Status, filter.Query = "all", "%"
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.Pagination.Total, "literal percent in the recording title")
	filter.Query = "unmatched%"
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Zero(t, result.Pagination.Total)
	filter.Query, filter.Status = "GPU demo", "active"
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Pagination.Total)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, value := range []string{secret, "private-audio-path", `"transcript":`, "actual_parameters", "hf_token", "transcription_context"} {
		require.NotContains(t, string(encoded), value)
	}
	filter.Days, filter.Query, filter.Status, filter.ExecutionID = 0, "", "all", "old"
	result, err = ReadRunHistory(context.Background(), db, filter, now)
	require.NoError(t, err)
	require.Len(t, result.Runs, 1)
	require.Equal(t, 1, result.Runs[0].RunNumber)
}
