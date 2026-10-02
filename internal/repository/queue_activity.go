package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"github.com/jaysqvl/Jotist/internal/models"
)

type activityJob struct {
	ID, Title, Status, ModelFamily, Model string
}

type activityQueueItem struct {
	ID, TranscriptionJobID, Status, ExecutionID, ProfileName, ModelFamily, Model string
	QueuedAt                                                                     time.Time
}

type activityExecution struct {
	ID, TranscriptionJobID, ModelFamily, Model string
	RunNumber                                  int
	StartedAt                                  time.Time
}

type activityStage struct {
	ID, ExecutionID, NodeKey, Kind, State string
	StageNumber, StageTotal               int
	RetryAt                               *time.Time
}

// ReadQueueActivity combines a sampled scheduler view with a read transaction.
// Pending work has no claimed worker; stage waits retain their claimed worker.
// The list is not a global FIFO position or a promise about start times.
func ReadQueueActivity(ctx context.Context, db *gorm.DB, snapshot models.QueueRuntimeSnapshot) (models.QueueActivity, error) {
	result := models.QueueActivity{GeneratedAt: snapshot.CapturedAt, Workers: snapshot.Workers, BusyWorkers: len(snapshot.Running), Recordings: []models.QueueActivityEntry{}}
	ownedIDs := make([]string, 0, len(snapshot.Running))
	for id := range snapshot.Running {
		ownedIDs = append(ownedIDs, id)
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		jobs := []activityJob{}
		if err := tx.Table("transcription_jobs AS j").Select("j.id, COALESCE(NULLIF(j.title, ''), 'Untitled recording') AS title, j.status, j.model_family, j.model").
			Where("j.deleted_at IS NULL").Where("j.status IN ? OR j.id IN ? OR EXISTS (SELECT 1 FROM transcription_queue_items AS qi WHERE qi.transcription_job_id = j.id AND qi.status IN ?)", []string{"pending", "processing"}, ownedIDs, []string{"queued", "pending", "processing"}).Order("j.created_at, j.id").Scan(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		jobIDs := make([]string, 0, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.ID)
		}
		items := []activityQueueItem{}
		projection := `id, transcription_job_id, status, queued_at, COALESCE(execution_id, '') AS execution_id, COALESCE(profile_name, '') AS profile_name,
			CASE WHEN json_valid(parameters_json) THEN COALESCE(json_extract(parameters_json, '$.model_family'), '') ELSE '' END AS model_family,
			CASE WHEN json_valid(parameters_json) THEN COALESCE(json_extract(parameters_json, '$.model'), '') ELSE '' END AS model`
		if err := tx.Table("transcription_queue_items").Select(projection).Where("transcription_job_id IN ? AND status IN ?", jobIDs, []string{"queued", "pending", "processing"}).
			Order("CASE status WHEN 'processing' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, position, queued_at, id").Scan(&items).Error; err != nil {
			return err
		}
		byJob := map[string][]activityQueueItem{}
		executionIDs := []string{}
		for _, item := range items {
			byJob[item.TranscriptionJobID] = append(byJob[item.TranscriptionJobID], item)
		}
		for _, job := range jobs {
			owner, owned := snapshot.Running[job.ID]
			entry := models.QueueActivityEntry{RecordingID: job.ID, RecordingTitle: job.Title, ModelFamily: job.ModelFamily, Model: job.Model, HasWorker: owned, State: "checking", QueuedJobs: []models.QueueActivityQueuedJob{}}
			var head *activityQueueItem
			for index := range byJob[job.ID] {
				item := &byJob[job.ID][index]
				if (!owned && head == nil && (item.Status != "queued" || (job.Status != "pending" && job.Status != "processing"))) || (owned && owner.QueueItemID != "" && item.ID == owner.QueueItemID) {
					head = item
				}
				if item.Status == "queued" {
					entry.QueuedRuns++
					result.QueuedRuns++
					entry.QueuedJobs = append(entry.QueuedJobs, models.QueueActivityQueuedJob{QueueItemID: item.ID, QueuedAt: item.QueuedAt, ModelFamily: item.ModelFamily, Model: item.Model, ProfileName: item.ProfileName})
				}
			}
			if head != nil {
				entry.QueueItemID = head.ID
				if !head.QueuedAt.IsZero() {
					queuedAt := head.QueuedAt
					entry.QueuedAt = &queuedAt
				}
				entry.ProfileName = head.ProfileName
				entry.ExecutionID = head.ExecutionID
				if head.ModelFamily != "" {
					entry.ModelFamily, entry.Model = head.ModelFamily, head.Model
				}
			}
			if owned {
				// An exact scheduler binding wins over a stale queue binding.
				entry.QueueItemID = owner.QueueItemID
				entry.ExecutionID = owner.ExecutionID
				entry.State = "running"
				if owner.Finishing {
					entry.State = "finishing"
				}
			} else if job.Status == "pending" || (head != nil && head.Status == "pending") {
				entry.State = "waiting_worker"
				result.WaitingRecordings++
			} else if job.Status != "processing" && head != nil && head.Status == "queued" {
				entry.State = "queued"
				result.WaitingRecordings++
			}
			if entry.ExecutionID != "" {
				executionIDs = append(executionIDs, entry.ExecutionID)
			}
			result.Recordings = append(result.Recordings, entry)
		}
		if len(executionIDs) == 0 {
			return nil
		}
		executions := []activityExecution{}
		executionProjection := `e.id, e.transcription_job_id, e.actual_model_family AS model_family, e.actual_model AS model, e.started_at,
			1 + (SELECT COUNT(*) FROM transcription_job_executions AS older WHERE older.transcription_job_id = e.transcription_job_id AND (older.started_at < e.started_at OR (older.started_at = e.started_at AND older.created_at < e.created_at) OR (older.started_at = e.started_at AND older.created_at = e.created_at AND older.id < e.id))) AS run_number`
		if err := tx.Table("transcription_job_executions AS e").Select(executionProjection).Where("e.id IN ?", executionIDs).Scan(&executions).Error; err != nil {
			return err
		}
		stages := []activityStage{}
		if err := tx.Table("recovery_stages AS s").Select(`s.id, s.execution_id, s.node_key, s.kind, s.state, s.stage_number, s.stage_total,
			(SELECT a.retry_at FROM recovery_attempts AS a WHERE a.stage_id = s.id ORDER BY a.attempt_number DESC LIMIT 1) AS retry_at`).
			Where("s.execution_id IN ? AND s.state IN ?", executionIDs, []string{models.RecoveryRunning, models.RecoveryWaiting}).Order("s.updated_at DESC, s.id").Scan(&stages).Error; err != nil {
			return err
		}
		for index := range result.Recordings {
			entry := &result.Recordings[index]
			for _, execution := range executions {
				if execution.ID == entry.ExecutionID && execution.TranscriptionJobID == entry.RecordingID {
					entry.RunNumber = execution.RunNumber
					entry.ModelFamily, entry.Model = execution.ModelFamily, execution.Model
					start := execution.StartedAt
					entry.StartedAt = &start
					break
				}
			}
			if !entry.HasWorker || entry.State == "finishing" {
				continue
			}
			for _, stage := range stages {
				if stage.ExecutionID != entry.ExecutionID {
					continue
				}
				entry.Stage, entry.StageState, entry.RetryAt = stage.Kind, stage.State, stage.RetryAt
				entry.StageNumber, entry.StageTotal = stage.StageNumber, stage.StageTotal
				if strings.HasPrefix(stage.NodeKey, "track-") {
					entry.StageScope = "track"
				}
				if stage.State == models.RecoveryWaiting {
					entry.State = "resource_wait"
					if stage.RetryAt != nil {
						entry.State = "retry_wait"
					}
				}
				break
			}
		}
		return nil
	})
	sort.SliceStable(result.Recordings, func(i, j int) bool { return result.Recordings[i].HasWorker && !result.Recordings[j].HasWorker })
	return result, err
}
