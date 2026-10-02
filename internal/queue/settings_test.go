package queue

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/repository"
)

func TestDefaultOneWorkerSerializesDifferentRecordings(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	p := &burstProcessor{started: make(chan struct{}, 8), release: make(chan struct{}, 8)}
	q, jobs, _, db, _, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return p })
	require.NoError(t, db.AutoMigrate(&models.QueueSetting{}))
	q.SetQueueSettingsRepository(repository.NewQueueSettingsRepository(db))
	require.NoError(t, q.Start())
	defer q.Stop()
	require.Equal(t, 1, q.QueueSettings().Workers)
	for index := 0; index < 5; index++ {
		job := &models.TranscriptionJob{ID: fmt.Sprintf("serial-recording-%d", index), AudioPath: "audio.wav", Status: models.StatusPending}
		require.NoError(t, jobs.Create(context.Background(), job))
		require.NoError(t, q.EnqueueJob(job.ID))
		p.release <- struct{}{}
	}
	require.Eventually(t, func() bool {
		completed, err := jobs.CountByStatus(context.Background(), models.StatusCompleted)
		return err == nil && completed == 6 && q.QueueSettings().BusyWorkers == 0
	}, 3*time.Second, 5*time.Millisecond)
	require.Equal(t, int32(1), p.peak.Load())
}

func TestLiveConcurrencyReductionWaitsForActiveOwnersAndPersists(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	p := &burstProcessor{started: make(chan struct{}, 8), release: make(chan struct{}, 8)}
	q, jobs, _, db, _, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return p })
	require.NoError(t, db.AutoMigrate(&models.QueueSetting{}))
	settings := repository.NewQueueSettingsRepository(db)
	q.SetQueueSettingsRepository(settings)
	require.NoError(t, q.Start())
	defer q.Stop()
	for index := 0; index < 4; index++ {
		job := &models.TranscriptionJob{ID: fmt.Sprintf("resize-recording-%d", index), AudioPath: "audio.wav", Status: models.StatusPending}
		require.NoError(t, jobs.Create(context.Background(), job))
		require.NoError(t, q.EnqueueJob(job.ID))
	}
	waitStarted := func() {
		t.Helper()
		select {
		case <-p.started:
		case <-time.After(3 * time.Second):
			t.Fatal("recording did not start")
		}
	}
	waitStarted()
	require.Equal(t, int32(1), p.active.Load())
	require.NoError(t, q.SetWorkers(context.Background(), 2))
	waitStarted()
	require.Equal(t, int32(2), p.active.Load())
	require.NoError(t, q.SetWorkers(context.Background(), 1))
	require.Equal(t, 2, q.QueueSettings().BusyWorkers, "lowering the limit must preserve both active runs")
	p.release <- struct{}{}
	require.Eventually(t, func() bool { return q.QueueSettings().BusyWorkers == 1 }, time.Second, time.Millisecond)
	select {
	case <-p.started:
		t.Fatal("new work started while an existing owner occupied the reduced limit")
	case <-time.After(80 * time.Millisecond):
	}
	for index := 0; index < 2; index++ {
		p.release <- struct{}{}
		waitStarted()
		require.Equal(t, int32(1), p.active.Load())
	}
	p.release <- struct{}{}
	require.Eventually(t, func() bool {
		completed, err := jobs.CountByStatus(context.Background(), models.StatusCompleted)
		return err == nil && completed == 5 && q.QueueSettings().BusyWorkers == 0
	}, 3*time.Second, 5*time.Millisecond)
	q.Stop()
	// A fresh server queue restores the saved setting, including values above 1.
	require.NoError(t, settings.SaveWorkers(context.Background(), 3))
	restored := NewTaskQueue(models.DefaultQueueWorkers, nil, jobs)
	restored.SetQueueSettingsRepository(settings)
	require.NoError(t, restored.Start())
	defer restored.Stop()
	require.Equal(t, 3, restored.QueueSettings().Workers)
}

type budgetProcessor struct {
	started chan time.Duration
	release chan struct{}
}

func (p *budgetProcessor) ProcessJob(ctx context.Context, jobID string) error {
	return p.ProcessJobWithProcess(ctx, jobID, nil)
}

func (p *budgetProcessor) ProcessJobWithProcess(ctx context.Context, _ string, _ func(*exec.Cmd)) error {
	deadline, _ := ctx.Deadline()
	p.started <- time.Until(deadline)
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLowerLimitCapacityWaitDoesNotConsumeProcessingTimeout(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	p := &budgetProcessor{started: make(chan time.Duration, 2), release: make(chan struct{}, 2)}
	q, jobs, _, db, _, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return p })
	require.NoError(t, db.AutoMigrate(&models.QueueSetting{}))
	settings := repository.NewQueueSettingsRepository(db)
	require.NoError(t, settings.SaveWorkers(context.Background(), 2))
	q.SetQueueSettingsRepository(settings)
	const budget = time.Second
	q.SetJobTimeout(budget)
	require.NoError(t, q.Start())
	defer q.Stop()
	enqueue := func(id string) {
		t.Helper()
		require.NoError(t, jobs.Create(context.Background(), &models.TranscriptionJob{ID: id, AudioPath: "audio.wav", Status: models.StatusPending}))
		require.NoError(t, q.EnqueueJob(id))
	}
	waitStarted := func() time.Duration {
		t.Helper()
		select {
		case remaining := <-p.started:
			return remaining
		case <-time.After(2 * time.Second):
			t.Fatal("recording did not start")
			return 0
		}
	}
	enqueue("budget-owner")
	waitStarted()
	require.NoError(t, q.SetWorkers(context.Background(), 1))
	enqueue("budget-waiter")
	require.Eventually(t, func() bool { return len(q.jobChannel) == 0 }, time.Second, time.Millisecond)
	// The idle worker has received the task, but cannot claim it at the lower
	// limit. Waiting here must not subtract from the processing timeout.
	time.Sleep(200 * time.Millisecond)
	p.release <- struct{}{}
	require.Greater(t, waitStarted(), 900*time.Millisecond)
	p.release <- struct{}{}
	require.Eventually(t, func() bool {
		completed, err := jobs.CountByStatus(context.Background(), models.StatusCompleted)
		return err == nil && completed == 3 && q.QueueSettings().BusyWorkers == 0
	}, time.Second, time.Millisecond)
}

type failingQueueSettings struct{}

func (failingQueueSettings) LoadWorkers(context.Context) (int, error) {
	return 0, errors.New("storage unavailable")
}
func (failingQueueSettings) SaveWorkers(context.Context, int) error {
	return errors.New("storage unavailable")
}

func TestQueueSettingsFailureAndDeploymentOverridePreserveLimit(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	q := NewTaskQueue(1, nil, nil)
	defer q.Stop()
	q.SetQueueSettingsRepository(failingQueueSettings{})
	require.Error(t, q.SetWorkers(context.Background(), 2))
	require.Equal(t, 1, q.QueueSettings().Workers)
	require.Error(t, q.Start(), "unreadable saved limits must block startup dispatch")
	t.Setenv("QUEUE_WORKERS", "2")
	overridden := NewTaskQueue(1, nil, nil)
	defer overridden.Stop()
	overridden.SetQueueSettingsRepository(failingQueueSettings{})
	require.ErrorIs(t, overridden.SetWorkers(context.Background(), 1), ErrWorkerOverride)
	require.True(t, overridden.QueueSettings().EnvironmentOverride)
	require.Equal(t, 2, overridden.QueueSettings().Workers)
}
