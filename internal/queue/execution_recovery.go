package queue

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"scriberr/internal/models"
	"scriberr/internal/repository"
)

type ExecutionRecoveryProcessor interface {
	RecoverExecutions(context.Context) ([]string, error)
	PrepareExecutionResume(context.Context, string, string) error
	CancelExecution(context.Context, string, string, string) error
}

// ResumeExecution admits only the exact persisted execution. Preparation owns
// plan/deadline/credential validation and its transactional state transition.
func (tq *TaskQueue) ResumeExecution(ctx context.Context, jobID, executionID string) error {
	if tq.startupError != nil {
		return tq.startupError
	}
	if executionID == "" {
		return repository.ErrExecutionOwnership
	}
	recovery, ok := tq.processor.(ExecutionRecoveryProcessor)
	if !ok {
		return errors.New("execution recovery is unavailable")
	}
	tq.jobsMutex.Lock()
	defer tq.jobsMutex.Unlock()
	if _, deleting := tq.deletingJobs[jobID]; deleting {
		return ErrJobStateChanged
	}
	if running := tq.runningJobs[jobID]; running != nil {
		if running.ExecutionID == executionID {
			return nil
		}
		return ErrJobStateChanged
	}
	saved, err := tq.jobRepo.FindExecution(ctx, jobID, executionID)
	if err != nil {
		return err
	}
	if tq.runQueueRepo != nil {
		active, findErr := tq.runQueueRepo.FindActive(ctx, jobID)
		if findErr == nil && (active.ExecutionID == nil || *active.ExecutionID != executionID) {
			return ErrJobStateChanged
		}
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
	}
	if saved.RecoveryState != "pending" || saved.Status != models.StatusPending {
		if err := recovery.PrepareExecutionResume(ctx, jobID, executionID); err != nil {
			return err
		}
	}
	task, err := tq.pendingTask(ctx, jobID)
	if err != nil {
		return err
	}
	if task.ExecutionID != executionID {
		return repository.ErrExecutionOwnership
	}
	delete(tq.protectedJobs, jobID)
	if err := tq.enqueueTask(task, false); err != nil && !errors.Is(err, errQueueFull) {
		return err
	}
	return nil
}

// cancelPersistedExecution fences an interrupted or admitted resume before any
// queue state changes. The exact active item wins; absent one, only a unique
// unresolved recovery execution is eligible. Historical "latest" is not an
// ownership signal. Caller holds jobsMutex.
func (tq *TaskQueue) cancelPersistedExecution(ctx context.Context, jobID, reason string) (bool, error) {
	recovery, ok := tq.processor.(ExecutionRecoveryProcessor)
	if !ok {
		return false, nil
	}
	var candidate *models.TranscriptionJobExecution
	if tq.runQueueRepo != nil {
		active, err := tq.runQueueRepo.FindActive(ctx, jobID)
		if err == nil {
			if active.ExecutionID == nil || *active.ExecutionID == "" {
				return false, nil // A new queued run must not cancel older history.
			}
			candidate, err = tq.jobRepo.FindExecution(ctx, jobID, *active.ExecutionID)
			if err != nil {
				return false, err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
	}
	if candidate == nil {
		records, err := tq.jobRepo.ListExecutionsByJobID(ctx, jobID)
		if err != nil {
			return false, err
		}
		for i := range records {
			e := &records[i]
			if e.RecoveryVersion == 0 || e.CancelledAt != nil || (e.RecoveryState != "pending" && e.RecoveryState != "running" && e.RecoveryState != "interrupted" && e.RecoveryState != "blocked") {
				continue
			}
			if candidate != nil {
				return false, repository.ErrExecutionOwnership
			}
			candidate = e
		}
	}
	if candidate == nil || candidate.RecoveryVersion == 0 {
		return false, nil
	}
	if err := recovery.CancelExecution(ctx, jobID, candidate.ID, reason); err != nil {
		return false, err
	}
	if tq.startupError == nil {
		delete(tq.protectedJobs, jobID)
	}
	return true, nil
}

func (tq *TaskQueue) pendingTask(ctx context.Context, jobID string) (queuedTask, error) {
	task := queuedTask{JobID: jobID}
	if tq.runQueueRepo != nil {
		item, err := tq.runQueueRepo.FindPending(ctx, jobID)
		if err == nil {
			task.QueueItemID = item.ID
			if item.ExecutionID != nil {
				task.ExecutionID = *item.ExecutionID
			}
			return task, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return task, err
		}
	}
	executions, err := tq.jobRepo.ListExecutionsByJobID(ctx, jobID)
	if err != nil {
		return task, err
	}
	for _, saved := range executions {
		if saved.RecoveryVersion > 0 && saved.Status == models.StatusPending && saved.RecoveryState == "pending" {
			if task.ExecutionID != "" {
				return task, repository.ErrExecutionOwnership
			}
			task.ExecutionID = saved.ID
		}
	}
	return task, nil
}
