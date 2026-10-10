package queue

import (
	"testing"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestReadinessFollowsStartupAndShutdown(t *testing.T) {
	q, _, _, _, _, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return nil })
	require.False(t, q.Ready(), "constructing a queue does not finish recovery")
	require.NoError(t, q.Start())
	require.True(t, q.Ready())
	q.Stop()
	require.False(t, q.Ready(), "a stopped dispatcher must not report ready")
}

func TestReadinessRejectsFailedStartup(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	q := NewTaskQueue(1, nil, nil)
	defer q.Stop()
	q.SetQueueSettingsRepository(failingQueueSettings{})
	require.Error(t, q.Start())
	require.False(t, q.Ready())
	require.ErrorContains(t, q.EnqueueJob("unadmitted"), "dispatch is blocked")
}
