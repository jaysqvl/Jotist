package queue

import (
	"context"
	"errors"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/repository"
)

var ErrWorkerOverride = errors.New("recording concurrency is controlled by QUEUE_WORKERS")
var ErrQueueSettingsUnavailable = errors.New("queue settings are unavailable")

// SetQueueSettingsRepository is called before Start. Load errors block dispatch
// rather than silently using a different saved concurrency limit.
func (tq *TaskQueue) SetQueueSettingsRepository(repo repository.QueueSettingsRepository) {
	tq.settingsRepo = repo
}

func (tq *TaskQueue) QueueSettings() models.QueueSettingsResponse {
	tq.jobsMutex.RLock()
	defer tq.jobsMutex.RUnlock()
	return models.QueueSettingsResponse{
		Workers: tq.workerCount, BusyWorkers: len(tq.runningJobs),
		MaxWorkers: models.MaxQueueWorkers, EnvironmentOverride: tq.workerOverride,
	}
}

// SetWorkers persists first, then changes the claim limit under the same lock
// as worker ownership. Reductions leave active executions and requests intact.
func (tq *TaskQueue) SetWorkers(ctx context.Context, workers int) error {
	if err := models.ValidateQueueWorkers(workers); err != nil {
		return err
	}
	tq.jobsMutex.Lock()
	defer tq.jobsMutex.Unlock()
	if tq.workerOverride {
		return ErrWorkerOverride
	}
	if tq.settingsRepo == nil {
		return ErrQueueSettingsUnavailable
	}
	if err := tq.ctx.Err(); err != nil {
		return err
	}
	if err := tq.settingsRepo.SaveWorkers(ctx, workers); err != nil {
		return err
	}
	tq.workerCount = workers
	tq.notifyWorkerCapacityLocked()
	if tq.started {
		tq.growWorkersLocked()
	}
	return nil
}

func (tq *TaskQueue) growWorkersLocked() {
	for tq.spawnedWorkers < tq.workerCount {
		id := tq.spawnedWorkers
		tq.spawnedWorkers++
		tq.wg.Add(1)
		go tq.worker(id)
	}
}

func (tq *TaskQueue) notifyWorkerCapacityLocked() {
	if tq.workersChanged != nil {
		close(tq.workersChanged)
	}
	tq.workersChanged = make(chan struct{})
}
