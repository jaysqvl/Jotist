package queue

import (
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
)

// ActivitySnapshot copies identities under the same lock used to claim,
// cancel and release work. A worker waiting for resources still owns its slot.
func (tq *TaskQueue) ActivitySnapshot() models.QueueRuntimeSnapshot {
	tq.jobsMutex.RLock()
	defer tq.jobsMutex.RUnlock()
	snapshot := models.QueueRuntimeSnapshot{
		CapturedAt: time.Now().UTC(), Workers: tq.workerCount,
		Running: make(map[string]models.QueueRunningSnapshot, len(tq.runningJobs)),
	}
	for id, job := range tq.runningJobs {
		snapshot.Running[id] = models.QueueRunningSnapshot{QueueItemID: job.QueueItemID, ExecutionID: job.ExecutionID, Finishing: job.Finishing}
	}
	return snapshot
}
