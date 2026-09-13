package queue

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"os/exec"
	executioncontext "scriberr/internal/execution"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryTestProcessor struct {
	process func(context.Context, string) error
	recover func(context.Context) ([]string, error)
	prepare func(context.Context, string, string) error
	cancel  func(context.Context, string, string, string) error
}

func (p *recoveryTestProcessor) ProcessJob(ctx context.Context, id string) error {
	if p.process != nil {
		return p.process(ctx, id)
	}
	return nil
}
func (p *recoveryTestProcessor) ProcessJobWithProcess(ctx context.Context, id string, _ func(*exec.Cmd)) error {
	return p.ProcessJob(ctx, id)
}
func (p *recoveryTestProcessor) RecoverExecutions(ctx context.Context) ([]string, error) {
	if p.recover != nil {
		return p.recover(ctx)
	}
	return nil, nil
}
func (p *recoveryTestProcessor) PrepareExecutionResume(ctx context.Context, j, e string) error {
	return p.prepare(ctx, j, e)
}
func (p *recoveryTestProcessor) CancelExecution(ctx context.Context, j, e, r string) error {
	if p.cancel != nil {
		return p.cancel(ctx, j, e, r)
	}
	return nil
}

func TestRecoveryBarrierPrecedesLegacyCleanupAndPromotion(t *testing.T) {
	p := &recoveryTestProcessor{}
	tq, jobs, runs, db, jobID, _ := newSequentialQueueTest(t, models.StatusProcessing, func(repository.JobRepository) JobProcessor { return p })
	token := "retained-access"
	active := models.TranscriptionQueueItem{ID: "interrupted", TranscriptionJobID: jobID, Status: models.QueueStatusProcessing, Parameters: models.WhisperXParams{HfToken: &token}}
	require.NoError(t, db.Create(&active).Error)
	require.NoError(t, runs.Append(context.Background(), jobID, []models.TranscriptionQueueItem{{Parameters: models.WhisperXParams{Model: "successor"}}}))
	p.recover = func(context.Context) ([]string, error) {
		saved, err := jobs.FindByID(context.Background(), jobID)
		require.NoError(t, err)
		require.Equal(t, models.StatusProcessing, saved.Status)
		return []string{jobID}, nil
	}
	require.NoError(t, tq.Start())
	defer tq.Stop()
	tq.reconcileSequentialRuns()
	saved, err := jobs.FindByID(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, models.StatusProcessing, saved.Status)
	item, err := runs.FindByID(context.Background(), jobID, active.ID)
	require.NoError(t, err)
	require.Equal(t, models.QueueStatusProcessing, item.Status)
	require.Equal(t, &token, item.Parameters.HfToken)
	items, err := runs.List(context.Background(), jobID, false)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, models.QueueStatusQueued, items[1].Status)
	require.ErrorIs(t, tq.StartImmediateRun(context.Background(), saved), ErrJobStateChanged)
}

func TestFailedRecoveryBarrierBlocksDispatchWithoutLegacyWrites(t *testing.T) {
	p := &recoveryTestProcessor{recover: func(context.Context) ([]string, error) { return nil, errors.New("old worker termination unproven") }}
	tq, jobs, _, _, jobID, _ := newSequentialQueueTest(t, models.StatusProcessing, func(repository.JobRepository) JobProcessor { return p })
	require.ErrorContains(t, tq.Start(), "recovery barrier")
	defer tq.Stop()
	require.ErrorContains(t, tq.EnqueueJob(jobID), "dispatch is blocked")
	saved, err := jobs.FindByID(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, models.StatusProcessing, saved.Status)
}

func TestSequentialRunBindsExplicitExecutionInsteadOfLatest(t *testing.T) {
	p := &recoveryTestProcessor{}
	tq, jobs, runs, _, jobID, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return p })
	created := make(chan string, 1)
	p.process = func(ctx context.Context, id string) error {
		e := &models.TranscriptionJobExecution{TranscriptionJobID: id, Status: models.StatusCompleted, StartedAt: time.Now()}
		if err := jobs.CreateExecution(ctx, e); err != nil {
			return err
		}
		if err := executioncontext.RegisterExecution(ctx, id, e.ID); err != nil {
			return err
		}
		unrelated := &models.TranscriptionJobExecution{TranscriptionJobID: id, Status: models.StatusCompleted, StartedAt: time.Now().Add(time.Second)}
		if err := jobs.CreateExecution(ctx, unrelated); err != nil {
			return err
		}
		created <- e.ID
		return nil
	}
	require.NoError(t, tq.Start())
	defer tq.Stop()
	items, err := tq.AddSequentialRuns(context.Background(), jobID, []models.TranscriptionQueueItem{{Parameters: models.WhisperXParams{Model: "fixture"}}})
	require.NoError(t, err)
	id := <-created
	require.Eventually(t, func() bool {
		item, e := runs.FindByID(context.Background(), jobID, items[0].ID)
		return e == nil && item.Status == models.QueueStatusCompleted && item.ExecutionID != nil && *item.ExecutionID == id
	}, time.Second, 5*time.Millisecond)
}

func TestResumeUsesExactIdentityAndIsIdempotentWhileRunning(t *testing.T) {
	p := &recoveryTestProcessor{}
	tq, jobs, runs, db, jobID, _ := newSequentialQueueTest(t, models.StatusProcessing, func(repository.JobRepository) JobProcessor { return p })
	ctx := context.Background()
	lifecycle := repository.NewExecutionLifecycleRepository(db)
	item := models.TranscriptionQueueItem{ID: "resume-run", TranscriptionJobID: jobID, Status: models.QueueStatusProcessing, Parameters: models.WhisperXParams{Model: "tiny"}}
	require.NoError(t, db.Create(&item).Error)
	deadline := time.Now().Add(time.Hour)
	e := &models.TranscriptionJobExecution{TranscriptionJobID: jobID, QueueItemID: &item.ID, RecoveryVersion: 1, StartedAt: time.Now(), DeadlineAt: &deadline, ActualParameters: item.Parameters}
	require.NoError(t, lifecycle.Begin(ctx, e))
	require.NoError(t, lifecycle.Finish(ctx, e.ID, 1, models.StatusFailed, nil, "speaker stage failed"))
	started := make(chan executioncontext.Binding, 1)
	release := make(chan struct{})
	var preparations atomic.Int32
	p.prepare = func(ctx context.Context, j, id string) error {
		preparations.Add(1)
		_, err := lifecycle.RequestResume(ctx, j, id, 1, e.ActualParameters)
		return err
	}
	p.process = func(ctx context.Context, j string) error {
		binding, ok := executioncontext.ForJob(ctx, j)
		if !ok {
			return errors.New("missing binding")
		}
		resumed, err := lifecycle.ClaimResume(ctx, j, binding.ExecutionID)
		if err != nil {
			return err
		}
		if err := executioncontext.RegisterExecution(ctx, j, resumed.ID); err != nil {
			return err
		}
		started <- binding
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		text := "resumed result"
		return lifecycle.Finish(ctx, resumed.ID, resumed.OwnerGeneration, models.StatusCompleted, &text, "")
	}
	require.NoError(t, tq.Start())
	defer tq.Stop()
	require.NoError(t, tq.ResumeExecution(ctx, jobID, e.ID))
	binding := <-started
	require.Equal(t, e.ID, binding.ExecutionID)
	require.Equal(t, item.ID, binding.QueueItemID)
	require.WithinDuration(t, deadline, binding.Deadline, time.Millisecond)
	require.NoError(t, tq.ResumeExecution(ctx, jobID, e.ID))
	require.Equal(t, int32(1), preparations.Load())
	close(release)
	require.Eventually(t, func() bool {
		item, err := runs.FindByID(ctx, jobID, item.ID)
		return err == nil && item.Status == models.QueueStatusCompleted
	}, time.Second, 5*time.Millisecond)
	all, err := jobs.ListExecutionsByJobID(ctx, jobID)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, int64(2), all[0].OwnerGeneration)
}

func TestPersistedCancellationFencesExactExecutionBeforeSuccessorAdmission(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "interrupted", true: "unproven-worker"}[unsafe], func(t *testing.T) {
			p := &recoveryTestProcessor{}
			tq, jobs, runs, db, jobID, _ := newSequentialQueueTest(t, models.StatusProcessing, func(repository.JobRepository) JobProcessor { return p })
			ctx := context.Background()
			lifecycle := repository.NewExecutionLifecycleRepository(db)
			item := models.TranscriptionQueueItem{ID: "exact-active", TranscriptionJobID: jobID, Status: models.QueueStatusProcessing}
			require.NoError(t, db.Create(&item).Error)
			e := &models.TranscriptionJobExecution{TranscriptionJobID: jobID, QueueItemID: &item.ID, RecoveryVersion: 1, StartedAt: time.Now()}
			require.NoError(t, lifecycle.Begin(ctx, e))
			require.NoError(t, jobs.CreateExecution(ctx, &models.TranscriptionJobExecution{ID: "newer-history", TranscriptionJobID: jobID, Status: models.StatusCompleted, StartedAt: time.Now().Add(time.Second)}))
			require.NoError(t, runs.Append(ctx, jobID, []models.TranscriptionQueueItem{{ID: "successor", Parameters: models.WhisperXParams{Model: "tiny"}}}))
			p.cancel = func(ctx context.Context, job, execution, reason string) error {
				require.Equal(t, e.ID, execution)
				return lifecycle.Cancel(ctx, job, execution, reason)
			}
			p.recover = func(context.Context) ([]string, error) {
				if unsafe {
					return nil, errors.New("worker termination unproven")
				}
				require.NoError(t, db.Model(e).Updates(map[string]any{"status": models.StatusFailed, "recovery_state": "interrupted"}).Error)
				require.NoError(t, jobs.UpdateStatus(ctx, jobID, models.StatusFailed))
				return []string{jobID}, nil
			}
			err := tq.Start()
			if unsafe {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			defer tq.Stop()
			require.NoError(t, tq.KillJobIfCurrent(jobID, item.ID))
			saved, err := jobs.FindExecution(ctx, jobID, e.ID)
			require.NoError(t, err)
			require.Equal(t, "cancelled", saved.RecoveryState)
			require.NotNil(t, saved.CancelledAt)
			require.ErrorIs(t, lifecycle.Finish(ctx, e.ID, 1, models.StatusCompleted, nil, ""), repository.ErrExecutionOwnership)
			cancelled, err := runs.FindByID(ctx, jobID, item.ID)
			require.NoError(t, err)
			require.Equal(t, models.QueueStatusCancelled, cancelled.Status)
			if unsafe {
				next, err := runs.FindByID(ctx, jobID, "successor")
				require.NoError(t, err)
				require.Equal(t, models.QueueStatusQueued, next.Status, "a cancellation fence does not prove an orphan exited")
			} else {
				require.Eventually(t, func() bool {
					next, err := runs.FindByID(ctx, jobID, "successor")
					return err == nil && next.Status == models.QueueStatusCompleted
				}, time.Second, 5*time.Millisecond)
			}
		})
	}
}

func TestCommittedCompletionWinsOverCancellationDuringCleanup(t *testing.T) {
	p := &recoveryTestProcessor{}
	tq, jobs, runs, db, jobID, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repository.JobRepository) JobProcessor { return p })
	lifecycle := repository.NewExecutionLifecycleRepository(db)
	require.NoError(t, db.Exec("CREATE TABLE publication_regressions (job_id TEXT)").Error)
	require.NoError(t, db.Exec("CREATE TRIGGER reject_completed_regression AFTER UPDATE OF status ON transcription_jobs WHEN NEW.status = 'failed' BEGIN INSERT INTO publication_regressions(job_id) VALUES (NEW.id); END").Error)
	published, release := make(chan string, 1), make(chan struct{})
	p.process = func(ctx context.Context, jobID string) error {
		job, err := jobs.FindByID(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Parameters.Model == "successor" {
			return nil
		}
		binding, ok := executioncontext.ForJob(ctx, jobID)
		if !ok {
			return errors.New("missing exact execution binding")
		}
		e := &models.TranscriptionJobExecution{TranscriptionJobID: jobID, QueueItemID: &binding.QueueItemID, RecoveryVersion: 1, StartedAt: time.Now()}
		if err := lifecycle.Begin(ctx, e); err != nil {
			return err
		}
		if err := executioncontext.RegisterExecution(ctx, jobID, e.ID); err != nil {
			return err
		}
		result := "committed transcript"
		if err := lifecycle.Finish(ctx, e.ID, 1, models.StatusCompleted, &result, ""); err != nil {
			return err
		}
		published <- e.ID
		<-release
		return ctx.Err()
	}
	require.NoError(t, tq.Start())
	defer tq.Stop()
	items, err := tq.AddSequentialRuns(context.Background(), jobID, []models.TranscriptionQueueItem{{Parameters: models.WhisperXParams{Model: "publishes"}}, {Parameters: models.WhisperXParams{Model: "successor"}}})
	require.NoError(t, err)
	executionID := <-published
	tq.jobsMutex.Lock()
	tq.runningJobs[jobID].Cancel() // deadline/shutdown observation after committed publication
	tq.jobsMutex.Unlock()
	close(release)
	require.Eventually(t, func() bool {
		first, e1 := runs.FindByID(context.Background(), jobID, items[0].ID)
		next, e2 := runs.FindByID(context.Background(), jobID, items[1].ID)
		return e1 == nil && e2 == nil && first.Status == models.QueueStatusCompleted && next.Status == models.QueueStatusCompleted
	}, time.Second, 5*time.Millisecond)
	saved, err := jobs.FindExecution(context.Background(), jobID, executionID)
	require.NoError(t, err)
	require.Equal(t, "completed", saved.RecoveryState)
	require.NotNil(t, saved.Transcript)
	require.Equal(t, "committed transcript", *saved.Transcript)
	job, err := jobs.FindByID(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, models.StatusCompleted, job.Status)
	var regressions int64
	require.NoError(t, db.Table("publication_regressions").Count(&regressions).Error)
	require.Zero(t, regressions, "completion must not be transiently overwritten as failed before successor promotion")
}
