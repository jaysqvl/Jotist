package queue

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivitySnapshotCopiesSchedulerOwnership(t *testing.T) {
	q := &TaskQueue{workerCount: 2, runningJobs: map[string]*RunningJob{
		"recording": {ExecutionID: "exact-run", QueueItemID: "exact-item", Finishing: true},
	}}
	snapshot := q.ActivitySnapshot()
	require.Equal(t, 2, snapshot.Workers)
	require.True(t, snapshot.Running["recording"].Finishing)
	q.runningJobs["recording"].ExecutionID = "next-run"
	delete(q.runningJobs, "recording")
	require.Equal(t, "exact-run", snapshot.Running["recording"].ExecutionID)
}
