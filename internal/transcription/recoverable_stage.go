package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"

	"gorm.io/gorm"
)

type recoveryStageContext struct {
	store                      *repository.RecoveryRepository
	execution                  *models.TranscriptionJobExecution
	originalHash, preparedHash string
	mode                       string
	reuse                      bool
	gpu                        gpuInventory
	nodePrefix                 string
	upstream                   []repository.CheckpointInput
	descriptor                 *interfaces.StageDescriptor
}

func runRecoverableStage[T any](ctx context.Context, recovery recoveryStageContext, kind, node string, adapter interfaces.ModelAdapter, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, run func(map[string]interface{}) (T, error)) (T, map[string]string, error) {
	var zero T
	if recovery.nodePrefix != "" {
		node = recovery.nodePrefix + node
	}
	if recovery.store == nil {
		return runWithAutoDeviceFallback(ctx, node, params, procCtx, run)
	}
	prepareRelease, err := acquireGPUStage(ctx, resolveStageParameters(params, RecoveryFixed, recovery.gpu))
	if err != nil {
		return zero, nil, err
	}
	err = adapter.PrepareEnvironment(ctx)
	prepareRelease()
	if err != nil {
		return zero, nil, fmt.Errorf("%s runtime preparation failed; verify model access and installed dependencies", node)
	}
	descriptor, qualified := stageDescriptor(adapter, kind)
	if recovery.descriptor != nil {
		descriptor = *recovery.descriptor
		qualified = true
	}
	fingerprint, revision, reusable := stageRuntimeIdentity(ctx, adapter, recovery.execution.ID)
	artifacts := []repository.ModelArtifactIdentity{{ModelID: adapter.GetCapabilities().ModelID, Revision: revision}}
	if len(descriptor.ModelArtifacts) > 0 {
		artifacts = nil
		keys := make([]string, 0, len(descriptor.ModelArtifacts))
		for key := range descriptor.ModelArtifacts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		exact := true
		for _, key := range keys {
			value := descriptor.ModelArtifacts[key]
			exact = exact && immutableModelIdentity(value)
			artifacts = append(artifacts, repository.ModelArtifactIdentity{ModelID: key, Revision: value})
		}
		reusable = reusable && exact
	}
	fingerprint = digestJSON(struct {
		Runtime    string
		Descriptor interfaces.StageDescriptor
	}{fingerprint, descriptor})
	planned := resolveStageParameters(params, recovery.mode, recovery.gpu)
	policy := recovery.execution.ActualParameters.AdaptivePolicy
	rule := stageRule(policy, kind)
	if rule.Fixed != nil {
		if err := validateFrozenStage(descriptor, candidateSettings(planned, descriptor, adapter.GetCapabilities().ModelID), *rule.Fixed); err != nil {
			return zero, nil, fmt.Errorf("%s: %w", node, err)
		}
		planned = applyCandidate(planned, *rule.Fixed, descriptor)
	}
	original := candidateSettings(planned, descriptor, adapter.GetCapabilities().ModelID)
	if original.Device == "" {
		original.Device = "cpu"
		planned["device"] = "cpu"
	}
	if recovery.mode != "" {
		planned = applyCandidate(planned, original, descriptor)
	}
	learning := repository.NewAdaptiveLearningRepository(recovery.store.Database())
	scope, available := stageLearningScope(ctx, recovery, kind, fingerprint, artifacts, planned, input)
	scope.OriginalWindowSeconds = original.WindowSeconds
	selectedPlanID := ""
	var existingStage *models.RecoveryStage
	existingStages, listErr := recovery.store.ListStages(ctx, recovery.execution.ID)
	if listErr != nil {
		return zero, nil, listErr
	}
	for i := range existingStages {
		if existingStages[i].NodeKey == node {
			existingStage = &existingStages[i]
			break
		}
	}
	if existingStage != nil {
		// Capacity is evaluated for a new stage only. Resuming keeps the stage's
		// immutable initial plan even when free memory or profile learning changed.
		var saved repository.CheckpointProvenance
		if json.Unmarshal([]byte(existingStage.ProvenanceJSON), &saved) != nil {
			return zero, nil, repository.ErrRecoveryCorrupt
		}
		if privateSettingsHash(planned) != saved.SettingsHash && len(existingStage.Attempts) > 0 {
			planned = applyCandidate(planned, settingsFromAttempt(existingStage.Attempts[0]), descriptor)
		}
		if privateSettingsHash(planned) != saved.SettingsHash && policy != nil {
			for _, snapshot := range policy.LearnedPlans {
				proposed := applyCandidate(planned, snapshot.Settings, descriptor)
				if privateSettingsHash(proposed) == saved.SettingsHash {
					planned = proposed
					selectedPlanID = snapshot.PlanID
					break
				}
			}
		}
		if privateSettingsHash(planned) != saved.SettingsHash {
			return zero, nil, repository.ErrRecoveryConflict
		}
	}
	if existingStage == nil && policy != nil && policy.Learn && recoveryLevel(recovery.mode) > 0 && qualified && reusable {
		scopes := []models.AdaptiveLearningScope{scope}
		readings := []models.AdaptiveCapacityReading{{MemoryDomain: scope.MemoryDomain, MemoryCapacityBytes: scope.MemoryCapacityBytes, AvailableBytes: available}}
		if original.Device == "cuda" && recoveryLevel(recovery.mode) >= 3 && rule.AllowCPU && !rule.DeviceLocked && supportsPrecision(descriptor, "cpu", rule.CPUPrecision) {
			total, free := hostCapacity()
			cpuScope := scope
			cpuScope.MemoryDomain = "host_system"
			cpuScope.MemoryCapacityBytes = total
			scopes = append(scopes, cpuScope)
			readings = append(readings, models.AdaptiveCapacityReading{MemoryDomain: "host_system", MemoryCapacityBytes: total, AvailableBytes: free})
		}
		for i, candidateScope := range scopes {
			selected, selectErr := learning.SelectStartingPlan(ctx, policy, candidateScope, readings[i].AvailableBytes, readings...)
			if selectErr == nil && selected != nil && candidateAllowed(recovery.mode, rule, descriptor, original, selected.Settings) {
				planned = applyCandidate(planned, selected.Settings, descriptor)
				selectedPlanID = selected.ID
				break
			}
		}
	}
	// A combined adapter with opaque auxiliary weights is not exact across runs.
	if (planned["align_words"] == true || planned["diarize"] == true) && len(descriptor.ModelArtifacts) == 0 {
		reusable = false
	}
	provenance := repository.CheckpointProvenance{AudioSHA256: recovery.originalHash, PreparedAudioSHA256: recovery.preparedHash, PreprocessingHash: digestBytes([]byte("mono-pcm16-16000-v1")), SettingsHash: privateSettingsHash(planned), RuntimeFingerprint: fingerprint, Implementation: "adapter-boundary-v1", Models: artifacts, Upstream: recovery.upstream}
	schema := "1"
	if descriptor.SchemaVersion != "" {
		schema = descriptor.SchemaVersion
	}
	if descriptor.ImplementationVersion != "" {
		provenance.Implementation = descriptor.ImplementationVersion
	}
	compatibilityFor := func(p repository.CheckpointProvenance) string {
		values := map[string]interface{}{"kind": kind, "schema": schema, "provenance": p, "gpu": recovery.gpu}
		if !reusable || recovery.mode == "" || adapter.GetCapabilities().ModelFamily == "openai" {
			values["execution_scope"] = recovery.execution.ID
		}
		return digestJSON(values)
	}
	stage, err := recovery.store.EnsureStage(ctx, repository.StageSpec{RecordingID: recovery.execution.TranscriptionJobID, ExecutionID: recovery.execution.ID, NodeKey: node, Kind: kind, SchemaVersion: schema, CompatibilityKey: compatibilityFor(provenance), OwnerGeneration: recovery.execution.OwnerGeneration, DurationSeconds: input.Duration.Seconds(), RecoverableBoundary: true, Provenance: provenance})
	if err != nil {
		return zero, nil, fmt.Errorf("cannot establish %s checkpoint identity: %w", node, err)
	}
	decode := func(checkpoint *models.RecoveryCheckpoint, data []byte) (T, map[string]string, error) {
		var result T
		if json.Unmarshal(data, &result) != nil {
			return zero, nil, repository.ErrRecoveryCorrupt
		}
		return result, map[string]string{"checkpoint_reused": "true", "checkpoint_id": checkpoint.ID, "checkpoint_result_sha256": checkpoint.ResultSHA256}, nil
	}
	if stage.CheckpointID != nil {
		checkpoint, data, err := recovery.store.SelectedCheckpoint(ctx, stage.ID)
		if err != nil {
			return zero, nil, err
		}
		return decode(checkpoint, data)
	}
	if recovery.reuse && reusable && recovery.mode != "" && adapter.GetCapabilities().ModelFamily != "openai" {
		checkpoint, err := recovery.store.FindReusable(ctx, stage.RecordingID, stage.CompatibilityKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, repository.ErrRecoveryCorrupt) {
			return zero, nil, err
		}
		if err == nil && checkpoint != nil {
			checkpoint, data, err := recovery.store.ReuseCheckpoint(ctx, stage.ID, recovery.execution.OwnerGeneration, checkpoint.ID)
			if err != nil {
				return zero, nil, err
			}
			return decode(checkpoint, data)
		}
	}
	history := stage.Attempts
	if len(history) == 0 && stage.AttemptNumber > 0 {
		stages, e := recovery.store.ListStages(ctx, recovery.execution.ID)
		if e != nil {
			return zero, nil, e
		}
		for _, s := range stages {
			if s.ID == stage.ID {
				history = s.Attempts
			}
		}
	}
	count := stage.AttemptNumber
	current := candidateSettings(planned, descriptor, adapter.GetCapabilities().ModelID)
	reason := "initial"
	if selectedPlanID != "" {
		reason = "learned_start"
	}
	if len(history) > 0 && recovery.mode != "" {
		current = settingsFromAttempt(history[len(history)-1])
		reason = "resume_same_settings"
	}
	var lastCode string
	var lastMeasurements models.StageMeasurements
	var lastMetadata map[string]string
	invoke := func(attemptParams map[string]interface{}, attemptReason string) (T, error) {
		lastCode = ""
		if count >= 7 {
			return zero, fmt.Errorf("%s exhausted its saved seven-attempt recovery budget; start a new run", node)
		}
		actual := resolveStageParameters(attemptParams, RecoveryFixed, recovery.gpu)
		if actual["device"] == nil || actual["device"] == "" {
			actual["device"] = "cpu"
		}
		if recovery.mode == "" && attemptParams["device"] == "auto" && actual["device"] == "cpu" {
			actual = cpuRetryParameters(actual)
		}
		settings := candidateSettings(actual, descriptor, adapter.GetCapabilities().ModelID)
		if recovery.mode != "" && settings.Device == "cpu" && (settings.Precision == "float16" || settings.Precision == "bfloat16") && !supportsPrecision(descriptor, "cpu", settings.Precision) {
			return zero, fmt.Errorf("%s CPU execution requires an explicitly supported precision; %s will not be converted to FP32", node, settings.Precision)
		}
		if recovery.mode == "" && settings.Device == "cpu" && params["device"] == "auto" && count > 0 {
			attemptReason = "legacy_cpu_fallback"
		}
		effectiveProvenance := provenance
		effectiveProvenance.SettingsHash = privateSettingsHash(actual)
		attempt, err := recovery.store.ClaimStage(ctx, stage.ID, recovery.execution.OwnerGeneration, repository.AttemptSettings{Device: settings.Device, Precision: settings.Precision, BatchSize: settings.BatchSize, WindowSeconds: settings.WindowSeconds, OverlapSeconds: settings.OverlapSeconds, StitchingVersion: settings.StitchingVersion, SettingsHash: effectiveProvenance.SettingsHash, Reason: attemptReason, CompatibilityKey: compatibilityFor(effectiveProvenance), Provenance: &effectiveProvenance})
		if err != nil {
			return zero, err
		}
		count = attempt.AttemptNumber
		_ = recovery.store.SetAttemptWaiting(ctx, attempt.ID, recovery.execution.OwnerGeneration, true)
		release, err := acquireGPUStage(ctx, actual)
		if release != nil {
			rawRelease := release
			var once sync.Once
			release = func() { once.Do(rawRelease) }
			defer release()
		}
		if err == nil {
			err = waitForStageCapacity(ctx, settings, recovery.gpu, procCtx.OutputDirectory)
		}
		if err != nil {
			if release != nil {
				release()
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = recovery.store.FailAttempt(cleanup, attempt.ID, recovery.execution.OwnerGeneration, models.RecoveryBlocked, "resource_wait_expired")
			return zero, err
		}
		if err = recovery.store.SetAttemptWaiting(ctx, attempt.ID, recovery.execution.OwnerGeneration, false); err != nil {
			release()
			return zero, err
		}
		finishMeasurements := startStageMeasurements(ctx, settings.Device, recovery.gpu, procCtx.OutputDirectory)
		defer finishMeasurements()
		offset := attemptLogOffset(procCtx.OutputDirectory)
		result, runErr := run(actual)
		lastMeasurements = supplementStageMeasurements(finishMeasurements(), attemptLogTail(procCtx.OutputDirectory, offset))
		release()
		if ctx.Err() != nil {
			runErr = ctx.Err()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = recovery.store.RecordAttemptMeasurements(cleanup, attempt.ID, recovery.execution.OwnerGeneration, lastMeasurements)
		if runErr != nil {
			lastCode = stageFailureCode(ctx, runErr, procCtx, offset)
			attempt.State = models.RecoveryFailed
			attempt.ErrorCode = lastCode
			attempt.Measurements = &lastMeasurements
			history = append(history, *attempt)
			next := nextAdaptiveCandidate(recovery.mode, lastCode, rule, descriptor, original, settings, history, lastMeasurements.ExternalContention)
			if next != nil {
				attempt.State = models.RecoveryRetryable
			}
			if lastCode == "cancelled" {
				attempt.State = models.RecoveryCancelled
			}
			if err := recovery.store.FailAttempt(cleanup, attempt.ID, recovery.execution.OwnerGeneration, attempt.State, lastCode); err != nil {
				return zero, err
			}
			recordStageObservation(cleanup, learning, recovery, stage, attempt, scope, settings, lastMeasurements, input, false, qualified && reusable, lastCode, original, descriptor)
			if lastCode == "cuda_out_of_memory" || lastCode == "cuda_runtime_error" {
				return zero, &interfaces.GPUExecutionError{Kind: lastCode, Err: runErr}
			}
			return zero, safeStageError(node, lastCode)
		}
		lastMetadata = map[string]string{"resolved_device": settings.Device, "precision": settings.Precision, "actual_batch_size": fmt.Sprint(settings.BatchSize), "actual_window_seconds": fmt.Sprint(settings.WindowSeconds)}
		if selectedPlanID != "" {
			lastMetadata["learned_plan_id"] = selectedPlanID
		}
		if attemptReason == "legacy_cpu_fallback" {
			prefix := "asr"
			if recoveryStageKey(kind) == "diarization" {
				prefix = "diarization"
			}
			lastMetadata[prefix+"_device_fallback"] = "cuda_to_cpu"
			if len(history) > 0 {
				code := history[len(history)-1].ErrorCode
				lastMetadata[prefix+"_fallback_reason"] = code
				attempts, _ := json.Marshal([]map[string]string{{"device": "cuda", "status": "failed", "error_kind": code}, {"device": "cpu", "status": "completed"}})
				lastMetadata[prefix+"_device_attempts"] = string(attempts)
			}
		}
		result = addEffectiveMetadata(result, lastMetadata)
		data, err := json.Marshal(result)
		if err != nil {
			_ = recovery.store.FailAttempt(cleanup, attempt.ID, recovery.execution.OwnerGeneration, models.RecoveryBlocked, "checkpoint_serialization_failed")
			return zero, err
		}
		var checkpoint *models.RecoveryCheckpoint
		for persistence := 0; persistence < 3; persistence++ {
			checkpoint, err = recovery.store.CommitCheckpoint(ctx, attempt.ID, recovery.execution.OwnerGeneration, data, nil)
			if err == nil {
				lastMetadata["checkpoint_id"] = checkpoint.ID
				lastMetadata["checkpoint_result_sha256"] = checkpoint.ResultSHA256
				attempt.CheckpointID = &checkpoint.ID
				recordStageObservation(cleanup, learning, recovery, stage, attempt, scope, settings, lastMeasurements, input, true, qualified && reusable, "succeeded", original, descriptor)
				return result, nil
			}
			if ctx.Err() != nil || errors.Is(err, repository.ErrRecoveryStaleOwner) || errors.Is(err, repository.ErrRecoveryCorrupt) || errors.Is(err, repository.ErrRecoveryQuota) {
				break
			}
		}
		_ = recovery.store.FailAttempt(cleanup, attempt.ID, recovery.execution.OwnerGeneration, models.RecoveryBlocked, "checkpoint_persistence_failed")
		return zero, fmt.Errorf("%s result could not be checkpointed: %w", node, err)
	}
	if recovery.mode == "" {
		result, metadata, err := runWithAutoDeviceFallback(ctx, node, params, procCtx, func(p map[string]interface{}) (T, error) { return invoke(p, "initial") })
		if metadata == nil {
			metadata = map[string]string{}
		}
		for key, value := range lastMetadata {
			metadata[key] = value
		}
		return result, metadata, err
	}
	for {
		actual := applyCandidate(planned, current, descriptor)
		result, err := invoke(actual, reason)
		if err == nil {
			return result, lastMetadata, nil
		}
		if ctx.Err() != nil {
			return zero, nil, ctx.Err()
		}
		next := nextAdaptiveCandidate(recovery.mode, lastCode, rule, descriptor, original, current, history, lastMeasurements.ExternalContention)
		if next == nil {
			return zero, nil, safeOrOriginalStageError(node, lastCode, err)
		}
		current = next.Settings
		reason = next.Reason
	}
}

func safeOrOriginalStageError(node, code string, err error) error {
	if code == "" {
		return err
	}
	return safeStageError(node, code)
}
func addEffectiveMetadata[T any](value T, metadata map[string]string) T {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return value
	}
	existing := map[string]string{}
	_ = json.Unmarshal(object["metadata"], &existing)
	if existing == nil {
		existing = map[string]string{}
	}
	for key, v := range metadata {
		existing[key] = v
	}
	object["metadata"], _ = json.Marshal(existing)
	data, err = json.Marshal(object)
	if err != nil {
		return value
	}
	var result T
	if json.Unmarshal(data, &result) != nil {
		return value
	}
	return result
}
