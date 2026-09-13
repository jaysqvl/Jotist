package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"scriberr/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrAdaptiveConflict = errors.New("profile revision or learning generation changed; reload before retrying")
var ErrAdaptiveEvidence = errors.New("this candidate has insufficient comparable qualified measurements")
var ErrAdaptiveOwnership = errors.New("learning observation does not belong to an authorized stage attempt")

type AdaptiveLearningRepository struct{ db *gorm.DB }

func NewAdaptiveLearningRepository(db *gorm.DB) *AdaptiveLearningRepository {
	return &AdaptiveLearningRepository{db: db}
}

func AdaptiveScopeKey(scope models.AdaptiveLearningScope) string {
	data, _ := json.Marshal(scope)
	return recoveryHash(data)
}
func AdaptiveCandidateHash(settings models.AdaptiveStageSettings) string {
	data, _ := json.Marshal(settings)
	return recoveryHash(data)
}

func validateAdaptiveSettings(settings models.AdaptiveStageSettings) bool {
	if settings.Device != "cpu" && settings.Device != "cuda" || settings.BatchSize < 1 || settings.Concurrency < 1 || settings.Concurrency > 8 {
		return false
	}
	switch settings.Precision {
	case "float32", "float16", "bfloat16", "int8", "int8_float32", "int8_float16", "quantized", "mixed_backend_fixed", "i2_s+i8_s":
	default:
		return false
	}
	return !math.IsNaN(settings.WindowSeconds) && !math.IsInf(settings.WindowSeconds, 0) && settings.WindowSeconds >= 0 && !math.IsNaN(settings.OverlapSeconds) && !math.IsInf(settings.OverlapSeconds, 0) && settings.OverlapSeconds >= 0 && (settings.WindowSeconds == 0 || settings.OverlapSeconds < settings.WindowSeconds) && (settings.StitchingVersion == "" || recoveryLabel.MatchString(settings.StitchingVersion))
}

func validateAdaptiveScope(scope models.AdaptiveLearningScope) bool {
	for _, hash := range []string{scope.FixedSettingsHash, scope.RuntimeFingerprint, scope.ModelFingerprint, scope.HardwareFingerprint} {
		if !recoverySHA.MatchString(hash) {
			return false
		}
	}
	return recoveryLabel.MatchString(scope.StageKey) && recoveryLabel.MatchString(scope.WorkloadClass) && !strings.Contains(scope.WorkloadClass, ":") && scope.MemoryCapacityBytes > 0 && (scope.MemoryDomain == "gpu_device" || scope.MemoryDomain == "host_system") && scope.OriginalWindowSeconds >= 0 && !math.IsNaN(scope.OriginalWindowSeconds) && !math.IsInf(scope.OriginalWindowSeconds, 0)
}

// AppendObservation accepts only typed diagnostics from an exact owned attempt.
// A stale profile generation may append historical evidence, but promotion is
// separately fenced against the current profile revision and generation.
func (r *AdaptiveLearningRepository) AppendObservation(ctx context.Context, observation models.AdaptiveObservation) (*models.AdaptiveObservation, error) {
	if observation.ProfileID == "" || observation.ProfileRevision < 1 || observation.LearningGeneration < 1 || observation.OwnerGeneration < 1 || !validateAdaptiveScope(observation.Scope) || !validateAdaptiveSettings(observation.Settings) || observation.Channels < 1 {
		return nil, ErrAdaptiveEvidence
	}
	if observation.Settings.Device == "cuda" && observation.Scope.MemoryDomain != "gpu_device" || observation.Settings.Device == "cpu" && observation.Scope.MemoryDomain != "host_system" {
		return nil, ErrAdaptiveEvidence
	}
	for _, value := range []float64{observation.SourceDurationSeconds, observation.ProcessedDurationSeconds} {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrAdaptiveEvidence
		}
	}
	for _, value := range []*int64{observation.PeakMemoryBytes, observation.AvailableBeforeBytes, observation.ReserveBytes, observation.LoadingMilliseconds, observation.ProcessingMilliseconds} {
		if value != nil && *value < 0 {
			return nil, ErrAdaptiveEvidence
		}
	}
	switch observation.Outcome {
	case "succeeded", "cuda_out_of_memory", "host_out_of_memory", "cuda_runtime_error", "adapter_failed", "deadline_exceeded", "checkpoint_failed":
	default:
		return nil, ErrAdaptiveEvidence
	}
	if observation.FullStage && observation.Outcome != "succeeded" {
		return nil, ErrAdaptiveEvidence
	}
	observation.ID, observation.CreatedAt = uuid.NewString(), time.Now().UTC()
	observation.ScopeKey, observation.CandidateHash = AdaptiveScopeKey(observation.Scope), AdaptiveCandidateHash(observation.Settings)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fence := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND transcription_job_id = ? AND owner_generation = ? AND cancelled_at IS NULL", observation.ExecutionID, observation.RecordingID, observation.OwnerGeneration).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			return ErrAdaptiveOwnership
		}
		var execution models.TranscriptionJobExecution
		if err := tx.First(&execution, "id = ?", observation.ExecutionID).Error; err != nil {
			return err
		}
		policy := execution.ActualParameters.AdaptivePolicy
		if execution.ActualParameters.ModelFamily == "openai" || policy == nil || !policy.Learn || !policy.SnapshotTaken || policy.ProfileID != observation.ProfileID || policy.ProfileRevision != observation.ProfileRevision || policy.LearningGeneration != observation.LearningGeneration {
			return ErrAdaptiveOwnership
		}
		var profileCount int64
		if err := tx.Model(&models.TranscriptionProfile{}).Where("id = ?", observation.ProfileID).Count(&profileCount).Error; err != nil {
			return err
		}
		if profileCount != 1 {
			return ErrAdaptiveOwnership
		}
		var stage models.RecoveryStage
		if err := tx.First(&stage, "id = ? AND execution_id = ? AND recording_id = ?", observation.StageID, observation.ExecutionID, observation.RecordingID).Error; err != nil {
			return ErrAdaptiveOwnership
		}
		if adaptiveStageKey(stage.Kind) != observation.Scope.StageKey {
			return ErrAdaptiveOwnership
		}
		var attempt models.RecoveryAttempt
		if err := tx.First(&attempt, "id = ? AND stage_id = ? AND owner_generation = ?", observation.AttemptID, stage.ID, observation.OwnerGeneration).Error; err != nil {
			return ErrAdaptiveOwnership
		}
		if attempt.CompletedAt == nil || attempt.Device != observation.Settings.Device || attempt.Precision != observation.Settings.Precision || attempt.BatchSize != observation.Settings.BatchSize || attempt.WindowSeconds != observation.Settings.WindowSeconds || attempt.OverlapSeconds != observation.Settings.OverlapSeconds || attempt.StitchingVersion != observation.Settings.StitchingVersion {
			return ErrAdaptiveOwnership
		}
		if observation.Outcome == "succeeded" && (attempt.State != models.RecoverySucceeded || attempt.CheckpointID == nil) {
			return ErrAdaptiveOwnership
		}
		if observation.Outcome != "succeeded" && (attempt.State == models.RecoverySucceeded || attempt.ErrorCode != observation.Outcome) {
			return ErrAdaptiveOwnership
		}
		if !adaptiveMeasurementsMatch(observation, attempt.Measurements) {
			return ErrAdaptiveEvidence
		}
		if execution.DeadlineAt != nil && attempt.CompletedAt.After(*execution.DeadlineAt) {
			return ErrAdaptiveOwnership
		}
		var prior models.AdaptiveObservation
		if err := tx.First(&prior, "attempt_id = ?", observation.AttemptID).Error; err == nil {
			observation.ID, observation.CreatedAt = prior.ID, prior.CreatedAt
			a, _ := json.Marshal(observation)
			b, _ := json.Marshal(prior)
			if string(a) != string(b) {
				return ErrAdaptiveConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&observation).Error
	})
	return &observation, err
}

func adaptiveStageKey(kind string) string {
	switch kind {
	case "combined", "asr", "recognize", "recognition":
		return "recognition"
	case "align", "alignment":
		return "alignment"
	case "diarize", "diarization":
		return "diarization"
	default:
		return ""
	}
}

type AdaptivePromotionRequest struct {
	ProfileID                                        string
	ExpectedRevision, ExpectedGeneration             int64
	ScopeKey, CandidateHash                          string
	FullWindowCandidateHashes, ExhaustionEvidenceIDs []string
}

func adaptiveReserve(capacity int64) int64 {
	reserve := int64(math.Ceil(float64(capacity) * .15))
	if reserve < 1<<30 {
		return 1 << 30
	}
	return reserve
}

func qualifiedMeasurement(observation models.AdaptiveObservation) bool {
	if observation.Outcome != "succeeded" || !observation.FullStage || observation.Cached || !observation.Qualified || observation.ExternalContention || observation.PeakMemoryBytes == nil || observation.ReserveBytes == nil || observation.ProcessingMilliseconds == nil || *observation.ProcessingMilliseconds <= 0 {
		return false
	}
	capacity := observation.Scope.MemoryCapacityBytes
	return *observation.PeakMemoryBytes > 0 && *observation.PeakMemoryBytes <= capacity && *observation.ReserveBytes >= adaptiveReserve(capacity) && *observation.ReserveBytes <= capacity-*observation.PeakMemoryBytes
}

// Observation summaries cannot introduce measurements absent from the durable
// attempt, or clear its contention flag. Nil stays unknown, never zero usage.
func adaptiveMeasurementsMatch(o models.AdaptiveObservation, m *models.StageMeasurements) bool {
	if m == nil {
		return o.PeakMemoryBytes == nil && o.AvailableBeforeBytes == nil && o.ReserveBytes == nil && o.ProcessingMilliseconds == nil
	}
	if m.OwnershipUnknown && o.Qualified {
		return false
	}
	if m.ExternalContention && !o.ExternalContention {
		return false
	}
	var peak, available, reserve, capacity *int64
	if o.Scope.MemoryDomain == "gpu_device" {
		peak, capacity = m.DevicePeakUsedBytes, m.GPUTotalBytes
		if capacity != nil && m.DeviceUsedBeforeBytes != nil {
			value := *capacity - *m.DeviceUsedBeforeBytes
			available = &value
		}
		if capacity != nil && peak != nil {
			value := *capacity - *peak
			reserve = &value
		}
	} else {
		peak, available, reserve, capacity = m.ProcessPeakBytes, m.HostAvailableBeforeBytes, m.HostMinimumAvailableBytes, m.HostTotalBytes
	}
	if capacity != nil && *capacity != o.Scope.MemoryCapacityBytes {
		return false
	}
	for _, pair := range [][2]*int64{{o.PeakMemoryBytes, peak}, {o.AvailableBeforeBytes, available}, {o.ReserveBytes, reserve}} {
		if pair[0] != nil && (pair[1] == nil || *pair[0] != *pair[1]) {
			return false
		}
	}
	return o.ProcessingMilliseconds == nil || *o.ProcessingMilliseconds == int64(m.ElapsedSeconds*1000)
}

// Promote advances the generation, preserving other selected scope plans. An
// old generation can never replace the selection installed by a newer writer.
func (r *AdaptiveLearningRepository) Promote(ctx context.Context, request AdaptivePromotionRequest) (*models.AdaptiveLearnedPlan, error) {
	var plan models.AdaptiveLearnedPlan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := adaptiveProfileFence(tx, request.ProfileID, request.ExpectedRevision, request.ExpectedGeneration)
		if err != nil {
			return err
		}
		var observations []models.AdaptiveObservation
		if err := tx.Where("profile_id = ? AND profile_revision = ? AND learning_generation = ? AND scope_key = ? AND candidate_hash = ?", profile.ID, profile.Revision, profile.LearningGeneration, request.ScopeKey, request.CandidateHash).Order("created_at,id").Find(&observations).Error; err != nil {
			return err
		}
		recordings := map[string]bool{}
		var elapsed int64
		for _, observation := range observations {
			if !observation.Cached && !observation.ExternalContention && observation.Outcome != "succeeded" {
				return ErrAdaptiveEvidence
			}
			if !qualifiedMeasurement(observation) {
				continue
			}
			var authorized int64
			if err := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND cancelled_at IS NULL", observation.ExecutionID, observation.OwnerGeneration).Count(&authorized).Error; err != nil {
				return err
			}
			if authorized != 1 {
				continue
			}
			if len(plan.ObservationIDs) == 0 {
				plan.Scope, plan.Settings, plan.MinimumReserveBytes = observation.Scope, observation.Settings, *observation.ReserveBytes
			}
			plan.ObservationIDs = append(plan.ObservationIDs, observation.ID)
			recordings[observation.RecordingID] = true
			elapsed += *observation.ProcessingMilliseconds
			if *observation.PeakMemoryBytes > plan.PeakMemoryBytes {
				plan.PeakMemoryBytes = *observation.PeakMemoryBytes
			}
			if *observation.ReserveBytes < plan.MinimumReserveBytes {
				plan.MinimumReserveBytes = *observation.ReserveBytes
			}
		}
		if len(plan.ObservationIDs) < 3 || len(recordings) < 2 {
			return ErrAdaptiveEvidence
		}
		plan.ID, plan.ProfileID, plan.ProfileRevision, plan.LearningGeneration = uuid.NewString(), profile.ID, profile.Revision, profile.LearningGeneration
		plan.ScopeKey, plan.CandidateHash, plan.CreatedAt = request.ScopeKey, request.CandidateHash, time.Now().UTC()
		plan.MeanProcessingMilliseconds = elapsed / int64(len(plan.ObservationIDs))
		if plan.Settings.WindowSeconds > 0 && (plan.Scope.OriginalWindowSeconds == 0 || plan.Settings.WindowSeconds < plan.Scope.OriginalWindowSeconds) {
			if err := validateWindowExhaustion(tx, &plan, request); err != nil {
				return err
			}
		}
		var selected models.AdaptivePlanSelection
		if tx.First(&selected, "profile_id = ? AND profile_revision = ? AND learning_generation = ? AND scope_key = ?", profile.ID, profile.Revision, profile.LearningGeneration, request.ScopeKey).Error == nil {
			var prior models.AdaptiveLearnedPlan
			if err := tx.First(&prior, "id = ?", selected.PlanID).Error; err != nil {
				return err
			}
			if prior.CandidateHash == plan.CandidateHash {
				plan = prior
				return nil
			}
			// A later successful proposal must improve completion headroom or
			// measured time; no automatic upward experiment is created here.
			if plan.PeakMemoryBytes >= prior.PeakMemoryBytes && plan.MeanProcessingMilliseconds >= prior.MeanProcessingMilliseconds {
				return ErrAdaptiveEvidence
			}
		}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		var selections []models.AdaptivePlanSelection
		if err := tx.Where("profile_id = ? AND profile_revision = ? AND learning_generation = ?", profile.ID, profile.Revision, profile.LearningGeneration).Find(&selections).Error; err != nil {
			return err
		}
		for _, selection := range selections {
			if selection.ScopeKey == plan.ScopeKey {
				continue
			}
			selection.LearningGeneration++
			if err := tx.Create(&selection).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&models.AdaptivePlanSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, LearningGeneration: profile.LearningGeneration + 1, ScopeKey: plan.ScopeKey, PlanID: plan.ID}).Error; err != nil {
			return err
		}
		return tx.Model(profile).UpdateColumn("learning_generation", profile.LearningGeneration+1).Error
	})
	return &plan, err
}

func validateWindowExhaustion(tx *gorm.DB, plan *models.AdaptiveLearnedPlan, request AdaptivePromotionRequest) error {
	if len(request.FullWindowCandidateHashes) == 0 || len(request.ExhaustionEvidenceIDs) == 0 || plan.Settings.StitchingVersion == "" {
		return ErrAdaptiveEvidence
	}
	required := map[string]bool{}
	for _, hash := range request.FullWindowCandidateHashes {
		if !recoverySHA.MatchString(hash) {
			return ErrAdaptiveEvidence
		}
		required[hash] = false
	}
	var evidence []models.AdaptiveObservation
	if err := tx.Where("id IN ? AND profile_id = ? AND profile_revision = ? AND learning_generation = ?", request.ExhaustionEvidenceIDs, plan.ProfileID, plan.ProfileRevision, plan.LearningGeneration).Find(&evidence).Error; err != nil {
		return err
	}
	if len(evidence) != len(request.ExhaustionEvidenceIDs) {
		return ErrAdaptiveEvidence
	}
	limits := map[string]models.AdaptiveCapacityLimit{}
	for _, observation := range evidence {
		if !sameAdaptiveWorkload(observation.Scope, plan.Scope) {
			return ErrAdaptiveEvidence
		}
		capacityFailure := observation.Outcome == "cuda_out_of_memory" && observation.Settings.Device == "cuda" || observation.Outcome == "host_out_of_memory" && observation.Settings.Device == "cpu"
		if !capacityFailure || !observation.Qualified || observation.ExternalContention || observation.Cached || observation.AvailableBeforeBytes == nil || *observation.AvailableBeforeBytes <= 0 || observation.Settings.WindowSeconds != plan.Scope.OriginalWindowSeconds {
			return ErrAdaptiveEvidence
		}
		if _, exists := required[observation.CandidateHash]; !exists {
			return ErrAdaptiveEvidence
		}
		var authorized int64
		if err := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND owner_generation = ? AND cancelled_at IS NULL", observation.ExecutionID, observation.OwnerGeneration).Count(&authorized).Error; err != nil {
			return err
		}
		if authorized != 1 {
			return ErrAdaptiveEvidence
		}
		required[observation.CandidateHash] = true
		limit, exists := limits[observation.Scope.MemoryDomain]
		if exists && limit.MemoryCapacityBytes != observation.Scope.MemoryCapacityBytes {
			return ErrAdaptiveEvidence
		}
		if !exists || *observation.AvailableBeforeBytes < limit.MaxAvailableBytes {
			limits[observation.Scope.MemoryDomain] = models.AdaptiveCapacityLimit{MemoryDomain: observation.Scope.MemoryDomain, MemoryCapacityBytes: observation.Scope.MemoryCapacityBytes, MaxAvailableBytes: *observation.AvailableBeforeBytes}
		}
	}
	for _, found := range required {
		if !found {
			return ErrAdaptiveEvidence
		}
	}
	plan.FullWindowCandidateHashes = append([]string(nil), request.FullWindowCandidateHashes...)
	plan.ExhaustionEvidenceIDs = append([]string(nil), request.ExhaustionEvidenceIDs...)
	primary, ok := limits[plan.Scope.MemoryDomain]
	if !ok || primary.MemoryCapacityBytes != plan.Scope.MemoryCapacityBytes {
		return ErrAdaptiveEvidence
	}
	plan.ExhaustionAvailableBytes = primary.MaxAvailableBytes
	for _, limit := range limits {
		plan.ExhaustionCapacityLimits = append(plan.ExhaustionCapacityLimits, limit)
	}
	sort.Slice(plan.ExhaustionCapacityLimits, func(i, j int) bool {
		return plan.ExhaustionCapacityLimits[i].MemoryDomain < plan.ExhaustionCapacityLimits[j].MemoryDomain
	})
	return nil
}

// HardwareFingerprint binds the same complete host/GPU environment. CPU and
// CUDA evidence may differ only in the domain and that domain's total capacity.
func sameAdaptiveWorkload(a, b models.AdaptiveLearningScope) bool {
	a.MemoryDomain, b.MemoryDomain = "", ""
	a.MemoryCapacityBytes, b.MemoryCapacityBytes = 0, 0
	return a == b
}

func adaptiveProfileFence(tx *gorm.DB, id string, revision, generation int64) (*models.TranscriptionProfile, error) {
	query := tx.Model(&models.TranscriptionProfile{}).Where("id = ? AND revision = ?", id, revision)
	if generation > 0 {
		query = query.Where("learning_generation = ?", generation)
	}
	result := query.UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrAdaptiveConflict
	}
	var profile models.TranscriptionProfile
	if err := tx.First(&profile, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// SnapshotAtAdmission always captures server-selected IDs. Invoke at request
// admission, not when dequeuing: already queued requests keep their snapshot.
func (r *AdaptiveLearningRepository) SnapshotAtAdmission(ctx context.Context, profileID string, expectedRevision int64, policy *models.AdaptiveExecutionPolicy) (*models.AdaptiveExecutionPolicy, error) {
	copy := models.AdaptiveExecutionPolicy{}
	if policy != nil {
		data, _ := json.Marshal(policy)
		_ = json.Unmarshal(data, &copy)
	}
	copy.LearnedPlans = nil
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := adaptiveProfileFence(tx, profileID, expectedRevision, 0)
		if err != nil {
			return err
		}
		if err := saveProfileRevision(tx, profile, "original", nil, nil); err != nil {
			return err
		}
		copy.ProfileID, copy.ProfileRevision, copy.LearningGeneration, copy.SnapshotTaken = profile.ID, profile.Revision, profile.LearningGeneration, true
		if !copy.Learn {
			return nil
		}
		var selections []models.AdaptivePlanSelection
		if err := tx.Where("profile_id = ? AND profile_revision = ? AND learning_generation = ?", profile.ID, profile.Revision, profile.LearningGeneration).Find(&selections).Error; err != nil {
			return err
		}
		for _, selection := range selections {
			var plan models.AdaptiveLearnedPlan
			if err := tx.First(&plan, "id = ?", selection.PlanID).Error; err != nil {
				return err
			}
			copy.LearnedPlans = append(copy.LearnedPlans, models.AdaptivePlanSnapshot{PlanID: plan.ID, ScopeKey: plan.ScopeKey, ProfileRevision: profile.Revision, LearningGeneration: profile.LearningGeneration, StageKey: plan.Scope.StageKey, CandidateHash: plan.CandidateHash, Settings: plan.Settings})
		}
		sort.Slice(copy.LearnedPlans, func(i, j int) bool { return copy.LearnedPlans[i].ScopeKey < copy.LearnedPlans[j].ScopeKey })
		return nil
	})
	return &copy, err
}

// SelectStartingPlan reads only an immutable admission snapshot. Newer
// promotions are deliberately invisible to a queued or already running job.
func (r *AdaptiveLearningRepository) SelectStartingPlan(ctx context.Context, policy *models.AdaptiveExecutionPolicy, scope models.AdaptiveLearningScope, availableBytes int64, extraCapacity ...models.AdaptiveCapacityReading) (*models.AdaptiveLearnedPlan, error) {
	if policy == nil || !policy.Learn || !policy.SnapshotTaken || !validateAdaptiveScope(scope) {
		return nil, nil
	}
	for _, snapshot := range policy.LearnedPlans {
		if snapshot.ScopeKey != AdaptiveScopeKey(scope) || snapshot.ProfileRevision != policy.ProfileRevision || snapshot.LearningGeneration != policy.LearningGeneration {
			continue
		}
		var plan models.AdaptiveLearnedPlan
		if err := r.db.WithContext(ctx).First(&plan, "id = ? AND profile_id = ? AND scope_key = ?", snapshot.PlanID, policy.ProfileID, snapshot.ScopeKey).Error; err != nil {
			return nil, err
		}
		if plan.CandidateHash != snapshot.CandidateHash || AdaptiveCandidateHash(snapshot.Settings) != plan.CandidateHash {
			return nil, ErrAdaptiveConflict
		}
		if availableBytes < plan.PeakMemoryBytes+adaptiveReserve(scope.MemoryCapacityBytes) {
			return nil, nil
		}
		if len(plan.ExhaustionEvidenceIDs) > 0 && (availableBytes <= 0 || availableBytes > plan.ExhaustionAvailableBytes) {
			return nil, nil
		}
		if len(plan.ExhaustionCapacityLimits) > 0 {
			readings := append([]models.AdaptiveCapacityReading{{MemoryDomain: scope.MemoryDomain, MemoryCapacityBytes: scope.MemoryCapacityBytes, AvailableBytes: availableBytes}}, extraCapacity...)
			for _, limit := range plan.ExhaustionCapacityLimits {
				found := false
				for _, reading := range readings {
					if reading.MemoryDomain != limit.MemoryDomain {
						continue
					}
					if reading.MemoryCapacityBytes != limit.MemoryCapacityBytes || reading.AvailableBytes <= 0 || reading.AvailableBytes > limit.MaxAvailableBytes {
						return nil, nil
					}
					found = true
				}
				if !found {
					return nil, nil
				}
			}
		}
		return &plan, nil
	}
	return nil, nil
}
