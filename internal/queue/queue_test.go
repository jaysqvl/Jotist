package queue

import (
	"context"
	"fmt"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/stretchr/testify/require"
)

func TestConfiguredWorkerCount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      string
		fallback int
		want     int
	}{
		{"default", "", 2, 2},
		{"environment override", "4", 2, 4},
		{"invalid override", "invalid", 2, 2},
		{"zero override", "0", 3, 3},
		{"negative override", "-1", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("QUEUE_WORKERS", tc.env)
			tq := NewTaskQueue(tc.fallback, nil, nil)
			defer tq.cancel()
			require.Equal(t, tc.want, tq.workerCount)
		})
	}
	t.Setenv("QUEUE_WORKERS", "")
	require.Positive(t, configuredWorkerCount(0))
}

type burstProcessor struct {
	active  atomic.Int32
	peak    atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (p *burstProcessor) ProcessJob(ctx context.Context, jobID string) error {
	return p.ProcessJobWithProcess(ctx, jobID, nil)
}

func (p *burstProcessor) ProcessJobWithProcess(ctx context.Context, _ string, _ func(*exec.Cmd)) error {
	current := p.active.Add(1)
	defer p.active.Add(-1)
	for peak := p.peak.Load(); current > peak; peak = p.peak.Load() {
		if p.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	p.started <- struct{}{}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestFixedWorkersRemainBoundedAcrossBursts(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "2")
	processor := &burstProcessor{started: make(chan struct{}, 32), release: make(chan struct{}, 32)}
	tq, repo, _, _, _, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return processor })
	require.NoError(t, tq.Start())
	defer tq.Stop()

	const jobsPerBurst = 14
	for burst := 0; burst < 2; burst++ {
		for index := 0; index < jobsPerBurst; index++ {
			job := &models.TranscriptionJob{ID: fmt.Sprintf("burst-%d-%d", burst, index), AudioPath: "audio.wav", Status: models.StatusPending}
			require.NoError(t, repo.Create(context.Background(), job))
			require.NoError(t, tq.EnqueueJob(job.ID))
		}
		for index := 0; index < 2; index++ {
			select {
			case <-processor.started:
			case <-time.After(3 * time.Second):
				t.Fatal("configured workers did not start")
			}
		}
		require.Equal(t, int32(2), processor.active.Load())
		for index := 0; index < jobsPerBurst; index++ {
			processor.release <- struct{}{}
		}
		require.Eventually(t, func() bool {
			completed, err := repo.CountByStatus(context.Background(), models.StatusCompleted)
			return err == nil && completed == int64(1+(burst+1)*jobsPerBurst) && !tq.IsJobRunning(fmt.Sprintf("burst-%d-%d", burst, jobsPerBurst-1))
		}, 3*time.Second, 10*time.Millisecond)
		// Wait for complete worker cleanup before the next idle-to-busy cycle.
		require.Eventually(t, func() bool { return processor.active.Load() == 0 && tq.GetQueueStats()["running_jobs"] == 0 }, time.Second, time.Millisecond)
		for len(processor.started) > 0 {
			<-processor.started
		}
		require.Equal(t, int32(2), processor.peak.Load())
		stats := tq.GetQueueStats()
		require.Equal(t, 2, stats["current_workers"])
		require.Equal(t, 2, stats["min_workers"])
		require.Equal(t, 2, stats["max_workers"])
		require.Equal(t, false, stats["auto_scale"])
	}
}
