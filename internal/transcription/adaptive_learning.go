package transcription

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

func stageLearningScope(ctx context.Context, recovery recoveryStageContext, kind, fingerprint string, artifacts []repository.ModelArtifactIdentity, params map[string]interface{}, input interfaces.AudioInput) (models.AdaptiveLearningScope, int64) {
	hostTotal, hostAvailable := hostCapacity()
	hostname, _ := os.Hostname()
	scope := models.AdaptiveLearningScope{StageKey: recoveryStageKey(kind), FixedSettingsHash: privateSettingsHash(params), RuntimeFingerprint: fingerprint, ModelFingerprint: digestJSON(artifacts), HardwareFingerprint: digestJSON(struct {
		GPU  gpuInventory
		Host string
		Arch string
		CPUs int
		RAM  int64
	}{recovery.gpu, hostname, runtime.GOARCH, runtime.NumCPU(), hostTotal}), WorkloadClass: workloadClass(input.Duration.Seconds(), input.Channels), MemoryDomain: "host_system", MemoryCapacityBytes: hostTotal, OriginalWindowSeconds: stageWindow(params)}
	available := hostAvailable
	if params["device"] == "cuda" {
		scope.MemoryDomain = "gpu_device"
		scope.MemoryCapacityBytes = 0
		available = 0
		if gpu, ok := gpuCapacity(ctx, recovery.gpu, ""); ok {
			scope.MemoryCapacityBytes = gpu.total
			available = policyMax(int64(0), gpu.total-gpu.used)
		}
	}
	return scope, available
}

func waitForStageCapacity(ctx context.Context, settings models.AdaptiveStageSettings, gpu gpuInventory, directory string) error {
	wait, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		if settings.Device != "cuda" {
			total, available := hostCapacity()
			if total == 0 || available >= policyMax(int64(1024*1024*1024), total*15/100) {
				return ctx.Err()
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-wait.Done():
				timer.Stop()
				return wait.Err()
			case <-timer.C:
			}
			continue
		}
		sample, ok := gpuCapacity(wait, gpu, directory)
		if !ok {
			return ctx.Err()
		}
		reserve := policyMax(int64(1024*1024*1024), sample.total*15/100)
		if (!sample.external && !sample.ownershipUnknown) || sample.total-sample.used >= reserve {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-wait.Done():
			timer.Stop()
			return wait.Err()
		case <-timer.C:
		}
	}
}

func recordStageObservation(ctx context.Context, learning *repository.AdaptiveLearningRepository, recovery recoveryStageContext, stage *models.RecoveryStage, attempt *models.RecoveryAttempt, scope models.AdaptiveLearningScope, settings models.AdaptiveStageSettings, measurements models.StageMeasurements, input interfaces.AudioInput, success, qualified bool, outcome string, original models.AdaptiveStageSettings, descriptor interfaces.StageDescriptor) {
	policy := recovery.execution.ActualParameters.AdaptivePolicy
	if policy == nil || !policy.Learn || !policy.SnapshotTaken || policy.ProfileID == "" || recoveryLevel(recovery.mode) == 0 {
		return
	}
	qualified = qualified && !measurements.OwnershipUnknown
	var peak, before, reserve *int64
	if settings.Device == "cuda" && measurements.GPUTotalBytes != nil {
		scope.MemoryDomain = "gpu_device"
		scope.MemoryCapacityBytes = *measurements.GPUTotalBytes
		peak = measurements.DevicePeakUsedBytes
		if measurements.DeviceUsedBeforeBytes != nil {
			v := scope.MemoryCapacityBytes - *measurements.DeviceUsedBeforeBytes
			before = &v
		}
		if peak != nil {
			v := scope.MemoryCapacityBytes - *peak
			reserve = &v
		}
	} else if settings.Device == "cpu" && measurements.HostTotalBytes != nil {
		scope.MemoryDomain = "host_system"
		scope.MemoryCapacityBytes = *measurements.HostTotalBytes
		peak = measurements.ProcessPeakBytes
		before = measurements.HostAvailableBeforeBytes
		reserve = measurements.HostMinimumAvailableBytes
	}
	if scope.MemoryCapacityBytes <= 0 {
		return
	}
	processing := int64(measurements.ElapsedSeconds * 1000)
	observation, err := learning.AppendObservation(ctx, models.AdaptiveObservation{ProfileID: policy.ProfileID, ProfileRevision: policy.ProfileRevision, LearningGeneration: policy.LearningGeneration, Scope: scope, ExecutionID: recovery.execution.ID, StageID: stage.ID, AttemptID: attempt.ID, RecordingID: stage.RecordingID, OwnerGeneration: recovery.execution.OwnerGeneration, Settings: settings, Outcome: outcome, FullStage: success, Cached: false, Qualified: qualified, ExternalContention: measurements.ExternalContention, PeakMemoryBytes: peak, AvailableBeforeBytes: before, ReserveBytes: reserve, ProcessingMilliseconds: &processing, SourceDurationSeconds: input.Duration.Seconds(), ProcessedDurationSeconds: input.Duration.Seconds(), Channels: policyMax(1, input.Channels)})
	if err != nil {
		logger.Debug("Adaptive observation was not eligible", "execution_id", recovery.execution.ID)
		return
	}
	if !success || !qualified {
		return
	}
	request := repository.AdaptivePromotionRequest{ProfileID: policy.ProfileID, ExpectedRevision: policy.ProfileRevision, ExpectedGeneration: policy.LearningGeneration, ScopeKey: observation.ScopeKey, CandidateHash: observation.CandidateHash}
	if settings.WindowSeconds != scope.OriginalWindowSeconds {
		candidates := permittedFullWindowCandidates(recovery.mode, stageRule(policy, stage.Kind), descriptor, original)
		for _, candidate := range candidates {
			request.FullWindowCandidateHashes = append(request.FullWindowCandidateHashes, repository.AdaptiveCandidateHash(candidate))
		}
		var failures []models.AdaptiveObservation
		if err := recovery.store.Database().WithContext(ctx).Where("profile_id = ? AND profile_revision = ? AND learning_generation = ? AND execution_id = ? AND stage_id = ? AND candidate_hash IN ? AND qualified = ? AND external_contention = ? AND cached = ? AND outcome IN ?", policy.ProfileID, policy.ProfileRevision, policy.LearningGeneration, recovery.execution.ID, stage.ID, request.FullWindowCandidateHashes, true, false, false, []string{"cuda_out_of_memory", "host_out_of_memory"}).Find(&failures).Error; err != nil {
			return
		}
		for _, failure := range failures {
			request.ExhaustionEvidenceIDs = append(request.ExhaustionEvidenceIDs, failure.ID)
		}
	}
	_, _ = learning.Promote(ctx, request)
}

// Native workers can supplement polling with process-owned peaks. These lines
// are read only from the current invocation's log range, never earlier retries.
func supplementStageMeasurements(measurements models.StageMeasurements, log string) models.StageMeasurements {
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "SCRIBERR_STAGE_METRICS=") {
			continue
		}
		var report struct {
			Scope          string `json:"scope"`
			Device         string `json:"device"`
			RSS            int64  `json:"process_peak_rss_bytes"`
			TorchAllocated *int64 `json:"torch_peak_allocated_bytes"`
			TorchReserved  *int64 `json:"torch_peak_reserved_bytes"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "SCRIBERR_STAGE_METRICS=")), &report) != nil || report.Scope != "stage_worker_self" {
			continue
		}
		if report.Device == "cpu" && report.RSS > 0 && (measurements.ProcessPeakBytes == nil || report.RSS > *measurements.ProcessPeakBytes) {
			value := report.RSS
			measurements.ProcessPeakBytes = &value
		}
		if report.TorchAllocated != nil && *report.TorchAllocated >= 0 {
			measurements.TorchPeakAllocatedBytes = report.TorchAllocated
		}
		if report.TorchReserved != nil && *report.TorchReserved >= 0 {
			measurements.TorchPeakReservedBytes = report.TorchReserved
		}
	}
	return measurements
}

// Enumerate the complete permitted full-window ladder, independently of which
// attempts happened. A shortcut can never treat untried CPU/batch settings as
// exhausted merely because another device ran out of memory.
func permittedFullWindowCandidates(mode string, rule models.AdaptiveStagePolicy, descriptor interfaces.StageDescriptor, original models.AdaptiveStageSettings) []models.AdaptiveStageSettings {
	current := original
	candidates := []models.AdaptiveStageSettings{original}
	history := []models.RecoveryAttempt{}
	reason := "initial"
	for len(history) < 7 {
		code := "cuda_out_of_memory"
		if current.Device == "cpu" {
			code = "host_out_of_memory"
		}
		history = append(history, models.RecoveryAttempt{Device: current.Device, Precision: current.Precision, BatchSize: current.BatchSize, WindowSeconds: current.WindowSeconds, OverlapSeconds: current.OverlapSeconds, StitchingVersion: current.StitchingVersion, Reason: reason})
		next := nextAdaptiveCandidate(mode, code, rule, descriptor, original, current, history, false)
		if next == nil || next.Settings.WindowSeconds != original.WindowSeconds {
			break
		}
		if next.Settings != current {
			candidates = append(candidates, next.Settings)
		}
		current = next.Settings
		reason = next.Reason
	}
	return candidates
}
