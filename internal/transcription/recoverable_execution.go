package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	executionctx "scriberr/internal/execution"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/serverlock"
	"scriberr/internal/transcription/interfaces"
	"scriberr/internal/webhook"
	"scriberr/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type recoveryPlan struct {
	Version               int          `json:"version"`
	Mode                  string       `json:"mode"`
	RequestedSettingsHash string       `json:"requested_settings_hash"`
	GPU                   gpuInventory `json:"gpu"`
	MaxStageAttempts      int          `json:"max_stage_attempts"`
	BoundaryVersion       string       `json:"boundary_version"`
	RecordingLayoutHash   string       `json:"recording_layout_hash"`
}

type recoveryTrackKey struct{}

func recordingLayoutHash(job *models.TranscriptionJob) string {
	type track struct {
		ID     uint
		Name   string
		Offset float64
	}
	tracks := make([]track, 0, len(job.MultiTrackFiles))
	for _, file := range job.MultiTrackFiles {
		tracks = append(tracks, track{file.ID, file.FileName, file.Offset})
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].ID < tracks[j].ID })
	return digestJSON(struct {
		MultiTrack bool
		Tracks     []track
	}{job.IsMultiTrack, tracks})
}

func ValidateSavedRecoveryPlan(run models.TranscriptionJobExecution) error {
	var plan recoveryPlan
	if run.RecoveryVersion != 1 || json.Unmarshal([]byte(run.PlanJSON), &plan) != nil || plan.Version != 1 || plan.BoundaryVersion != "adapter-boundary-v1" || plan.Mode != run.ActualParameters.RecoveryMode || plan.RequestedSettingsHash != recoveryRequestHash(run.ActualParameters) || plan.MaxStageAttempts != 7 {
		return errors.New("the saved execution plan is missing or incompatible; start a new run")
	}
	return ValidateRecoveryMode(plan.Mode)
}

func (u *UnifiedTranscriptionService) configureRecovery() {
	provider, ok := u.jobRepo.(interface{ Database() *gorm.DB })
	if !ok {
		return
	}
	u.recoveryDB = provider.Database()
	quota := int64(5 * 1024 * 1024 * 1024)
	if value := os.Getenv("CHECKPOINT_MAX_BYTES"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			u.recoveryInitError = errors.New("CHECKPOINT_MAX_BYTES must be a nonnegative byte quota")
			return
		}
		quota = parsed
	}
	root := os.Getenv("CHECKPOINT_DIR")
	if root == "" {
		root = filepath.Join(filepath.Dir(u.outputDirectory), "checkpoints")
	}
	u.recovery, u.recoveryInitError = repository.NewRecoveryRepository(u.recoveryDB, root, repository.RecoveryOptions{MaxBytes: quota})
	u.lifecycle = repository.NewExecutionLifecycleRepository(u.recoveryDB)
}

func (u *UnifiedTranscriptionService) RecoveryRepository() *repository.RecoveryRepository {
	return u.recovery
}

func (u *UnifiedTranscriptionService) DeleteRecoveryRecording(ctx context.Context, jobID string) error {
	if u.recovery == nil {
		return nil
	}
	return u.recovery.DeleteRecording(ctx, jobID)
}

func (u *UnifiedTranscriptionService) makeRecoveryStageContext(ctx context.Context, job *models.TranscriptionJob, e *models.TranscriptionJobExecution, original, prepared interfaces.AudioInput) (recoveryStageContext, error) {
	result := recoveryStageContext{store: u.recovery, execution: e, mode: job.Parameters.RecoveryMode, reuse: job.Parameters.ReuseCheckpoints == nil || *job.Parameters.ReuseCheckpoints}
	result.nodePrefix, _ = ctx.Value(recoveryTrackKey{}).(string)
	if u.recovery == nil {
		return result, nil
	}
	var err error
	result.originalHash, err = fileDigest(ctx, original.FilePath)
	if err != nil {
		return result, fmt.Errorf("hash source audio: %w", err)
	}
	result.preparedHash = result.originalHash
	if original.FilePath != prepared.FilePath {
		result.preparedHash, err = fileDigest(ctx, prepared.FilePath)
		if err != nil {
			return result, fmt.Errorf("hash prepared audio: %w", err)
		}
	}
	var plan recoveryPlan
	if json.Unmarshal([]byte(e.PlanJSON), &plan) != nil || plan.Version != 1 {
		return result, errors.New("unsupported saved recovery plan")
	}
	result.gpu = plan.GPU
	return result, nil
}

func (u *UnifiedTranscriptionService) processRecoverableJob(ctx context.Context, jobID string) (resultErr error) {
	// A resumed run can exhaust its saved deadline while awaiting a worker.
	// Use a separate cleanup context even if the first work-context read fails,
	// so that an expired pending owner cannot block every subsequent new run.
	defer func() {
		binding, bound := executionctx.ForJob(ctx, jobID)
		if !bound || binding.ExecutionID == "" || (!errors.Is(resultErr, context.DeadlineExceeded) && !errors.Is(resultErr, repository.ErrExecutionDeadline)) {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		current, err := u.jobRepo.FindExecution(cleanup, jobID, binding.ExecutionID)
		if err == nil && current.RecoveryVersion > 0 && current.RecoveryState == "pending" {
			if err := u.lifecycle.ExpirePending(cleanup, jobID, current.ID, current.OwnerGeneration); err != nil {
				logger.Warn("Could not finalize expired pending execution", "execution_id", current.ID)
			}
		}
	}()
	job, err := u.jobRepo.FindWithAssociations(ctx, jobID)
	if err != nil {
		return err
	}
	if err := ValidateAdaptivePolicy(job.Parameters); err != nil {
		return err
	}
	binding, bound := executionctx.ForJob(ctx, jobID)
	var current *models.TranscriptionJobExecution
	if bound && binding.ExecutionID != "" {
		current, err = u.jobRepo.FindExecution(ctx, jobID, binding.ExecutionID)
		if err != nil {
			return err
		}
		// RequestResume restored this exact snapshot and its authorized credentials.
		if recoveryRequestHash(job.Parameters) != recoveryRequestHash(current.ActualParameters) {
			return errors.New("saved execution parameters no longer match the admitted resume")
		}
		if err := ValidateSavedRecoveryPlan(*current); err != nil {
			return err
		}
		var plan recoveryPlan
		_ = json.Unmarshal([]byte(current.PlanJSON), &plan)
		if plan.RecordingLayoutHash != recordingLayoutHash(job) {
			return errors.New("recording track layout changed; start a new execution")
		}
		current, err = u.lifecycle.ClaimResume(ctx, jobID, binding.ExecutionID)
		if err != nil {
			return err
		}
	} else {
		deadline := time.Now().Add(24 * time.Hour)
		if value, ok := ctx.Deadline(); ok {
			deadline = value
		}
		plan := recoveryPlan{Version: 1, Mode: job.Parameters.RecoveryMode, RequestedSettingsHash: recoveryRequestHash(job.Parameters), GPU: visibleGPU(ctx, job.Parameters.DeviceIndex), MaxStageAttempts: 7, BoundaryVersion: "adapter-boundary-v1", RecordingLayoutHash: recordingLayoutHash(job)}
		encoded, _ := json.Marshal(plan)
		id := uuid.NewString()
		logPath := filepath.Join(u.outputDirectory, jobID, "runs", id, "transcription.log")
		current = &models.TranscriptionJobExecution{ID: id, TranscriptionJobID: jobID, StartedAt: time.Now(), ActualParameters: job.Parameters.WithoutSecrets(), RecoveryVersion: 1, PlanJSON: string(encoded), DeadlineAt: &deadline, LogPath: &logPath}
		if bound && binding.QueueItemID != "" {
			current.QueueItemID = &binding.QueueItemID
		}
		if err := u.lifecycle.Begin(ctx, current); err != nil {
			return err
		}
	}
	if err := executionctx.RegisterExecution(ctx, jobID, current.ID); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = u.lifecycle.Cancel(cleanup, jobID, current.ID, "Execution registration was cancelled or ownership changed")
		return err
	}
	if current.DeadlineAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *current.DeadlineAt)
		defer cancel()
	}
	u.broadcastRecovery(jobID)
	if job.IsMultiTrack && job.Parameters.IsMultiTrackEnabled {
		err = u.processMultiTrackJob(ctx, job, current)
	} else {
		err = u.processSingleTrackJob(ctx, job, current)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err != nil {
		// Adapter calls have returned and their supervised process groups have
		// stopped. Clear abandoned active attempts even when cancellation or a
		// deadline correctly fenced their normal completion transaction.
		_ = u.recovery.InterruptExecutionStages(cleanup, current.ID, current.OwnerGeneration)
	}
	if errors.Is(err, context.Canceled) {
		_ = u.lifecycle.Cancel(cleanup, jobID, current.ID, "Cancelled by user")
		u.broadcastRecovery(jobID)
		return err
	}
	status := models.StatusCompleted
	message := ""
	if err != nil {
		status = models.StatusFailed
		message = err.Error()
	}
	if status == models.StatusCompleted && current.Transcript == nil {
		status = models.StatusFailed
		err = errors.New("requested transcript output is missing")
		message = err.Error()
	}
	if finishErr := u.lifecycle.FinishWithDetails(cleanup, current.ID, current.OwnerGeneration, status, current.Transcript, message, current); finishErr != nil {
		return fmt.Errorf("execution publication failed: %w", finishErr)
	}
	u.broadcastRecovery(jobID)
	if err != nil {
		return err
	}
	if job.Parameters.CallbackURL != nil && *job.Parameters.CallbackURL != "" {
		claimed, claimErr := u.lifecycle.TryClaimCompletionCallback(cleanup, current.ID, current.OwnerGeneration)
		if claimErr != nil {
			logger.Warn("Could not claim completed execution callback", "execution_id", current.ID)
			return nil
		}
		if claimed {
			payload := webhook.WebhookPayload{JobID: job.ID, Status: models.StatusCompleted, AudioPath: job.AudioPath, Transcript: current.Transcript, CompletedAt: time.Now(), Metadata: map[string]interface{}{"execution_id": current.ID}}
			go func() {
				callbackCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
				defer done()
				if err := u.webhookService.SendWebhook(callbackCtx, *job.Parameters.CallbackURL, payload); err != nil {
					logger.Warn("Completed execution callback failed", "execution_id", current.ID)
				}
			}()
		}
	}
	return nil
}

func (u *UnifiedTranscriptionService) broadcastRecovery(jobID string) {
	if u.broadcaster != nil {
		u.broadcaster.Broadcast(jobID, "job_update", map[string]interface{}{"job_id": jobID, "recovery_updated": true})
	}
}

type resumeParametersKey struct{}

// Credentials are only carried in the authenticated request context and normal
// authorized queue/job storage, never in manifests or observations.
func WithResumeParameters(ctx context.Context, params models.WhisperXParams) context.Context {
	return context.WithValue(ctx, resumeParametersKey{}, params)
}

func (u *UnifiedTranscriptionService) PrepareExecutionResume(ctx context.Context, jobID, executionID string) error {
	if u.recovery == nil {
		return errors.New("recovery storage is unavailable")
	}
	current, err := u.jobRepo.FindExecution(ctx, jobID, executionID)
	if err != nil {
		return err
	}
	if current.RecoveryVersion != 1 {
		return errors.New("this older execution has no recoverable plan")
	}
	if err := ValidateSavedRecoveryPlan(*current); err != nil {
		return err
	}
	job, err := u.jobRepo.FindWithAssociations(ctx, jobID)
	if err != nil {
		return err
	}
	var plan recoveryPlan
	_ = json.Unmarshal([]byte(current.PlanJSON), &plan)
	if plan.RecordingLayoutHash != recordingLayoutHash(job) {
		return errors.New("recording track layout changed; start a new execution")
	}
	if current.CancelledAt != nil {
		return errors.New("cancelled executions cannot be resumed; start a new run")
	}
	if !serverlock.PriorWorkersStopped() {
		return errors.New("previous worker termination is unverified; restart the container before resuming")
	}
	if current.DeadlineAt != nil && !time.Now().Before(*current.DeadlineAt) {
		return repository.ErrExecutionDeadline
	}
	stages, err := u.recovery.ListStages(ctx, current.ID)
	if err != nil {
		return err
	}
	for _, stage := range stages {
		if stage.State == models.RecoveryRunning || stage.State == models.RecoveryWaiting {
			return errors.New("prior stage process termination has not been confirmed")
		}
		if stage.AttemptNumber >= 7 && stage.CheckpointID == nil {
			return errors.New("saved stage attempt budget exhausted; start a new run")
		}
		if stage.CheckpointID != nil {
			if _, _, err := u.recovery.SelectedCheckpoint(ctx, stage.ID); err != nil {
				return err
			}
		}
	}
	params, ok := ctx.Value(resumeParametersKey{}).(models.WhisperXParams)
	if !ok {
		job, err := u.jobRepo.FindByID(ctx, jobID)
		if err != nil {
			return err
		}
		params = job.Parameters
	}
	if recoveryRequestHash(params) != recoveryRequestHash(current.ActualParameters) {
		return errors.New("resume requires the original settings; use a new run for changed settings")
	}
	if params.EffectiveHFTokenSource() == "custom" && (params.HfToken == nil || *params.HfToken == "") && !(params.ModelFamily == FamilyOpenAI && !params.Diarize) {
		return errors.New("the saved custom model credential is unavailable; provide a credential before resuming")
	}
	_, err = u.lifecycle.RequestResume(ctx, jobID, executionID, current.OwnerGeneration, params)
	return err
}

func (u *UnifiedTranscriptionService) CancelExecution(ctx context.Context, jobID, executionID, reason string) error {
	if u.lifecycle == nil {
		return nil
	}
	current, err := u.jobRepo.FindExecution(ctx, jobID, executionID)
	if err != nil {
		return err
	}
	if current.RecoveryVersion == 0 {
		return nil
	}
	return u.lifecycle.Cancel(ctx, jobID, executionID, reason)
}

// Restart recovery is a barrier, never a latest-execution inference. Interrupted
// plans remain visible and require an explicit same-plan resume.
func (u *UnifiedTranscriptionService) RecoverExecutions(ctx context.Context) ([]string, error) {
	if u.recoveryInitError != nil {
		return nil, u.recoveryInitError
	}
	if u.recovery == nil {
		return nil, nil
	}
	var executions []models.TranscriptionJobExecution
	if err := u.recoveryDB.WithContext(ctx).Where("recovery_version > 0 AND recovery_state IN ?", []string{"running", "pending", "interrupted", "blocked"}).Find(&executions).Error; err != nil {
		return nil, err
	}
	if !serverlock.PriorWorkersStopped() {
		u.recoveryInitError = errors.New("previous worker termination could not be verified; inference is paused until the container is restarted")
		return nil, u.recoveryInitError
	}
	if reconciled, err := u.lifecycle.ReconcileLegacyOrphans(ctx); err != nil {
		return nil, err
	} else if reconciled > 0 {
		logger.Info("Reconciled orphaned legacy execution history", "count", reconciled)
	}
	// Cancellation may fence a write before a stopped worker can finish its
	// attempt bookkeeping. Once startup proves all prior workers gone, clear
	// those active markers too, including cancelled executions, for safe GC.
	var abandoned []models.TranscriptionJobExecution
	if err := u.recoveryDB.WithContext(ctx).Where("recovery_version > 0 AND EXISTS (SELECT 1 FROM recovery_stages WHERE recovery_stages.execution_id = transcription_job_executions.id AND recovery_stages.state IN ?)", []string{models.RecoveryRunning, models.RecoveryWaiting}).Find(&abandoned).Error; err != nil {
		return nil, err
	}
	for _, run := range abandoned {
		stages, err := u.recovery.ListStages(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		for _, stage := range stages {
			if stage.State == models.RecoveryRunning || stage.State == models.RecoveryWaiting {
				if err := u.recovery.InterruptExecutionStages(ctx, run.ID, stage.OwnerGeneration); err != nil {
					return nil, err
				}
			}
		}
	}
	protected := make([]string, 0, len(executions))
	for _, current := range executions {
		protected = append(protected, current.TranscriptionJobID)
		if current.RecoveryState != "running" && current.RecoveryState != "pending" {
			continue
		}
		if err := u.recovery.InterruptExecutionStages(ctx, current.ID, current.OwnerGeneration); err != nil {
			return nil, err
		}
		result := u.recoveryDB.WithContext(ctx).Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND cancelled_at IS NULL", current.ID, current.OwnerGeneration).Updates(map[string]interface{}{"status": models.StatusFailed, "recovery_state": "interrupted", "completed_at": time.Now(), "error_message": "Server restarted; completed checkpoints retained. Resume this saved execution."})
		if result.Error != nil {
			return nil, result.Error
		}
		if err := u.recoveryDB.WithContext(ctx).Model(&models.TranscriptionJob{}).Where("id = ?", current.TranscriptionJobID).Updates(map[string]interface{}{"status": models.StatusFailed, "error_message": "Run interrupted; checkpoints retained"}).Error; err != nil {
			return nil, err
		}
	}
	if err := u.recovery.RetryDeletions(ctx); err != nil {
		return nil, err
	}
	if _, err := u.recovery.Collect(ctx, repository.RetentionPolicy{}); err != nil {
		return nil, err
	}
	return protected, nil
}
