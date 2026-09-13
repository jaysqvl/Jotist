package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type adaptiveFixture struct {
	db          *gorm.DB
	store       *AdaptiveLearningRepository
	checkpoints *RecoveryRepository
	profile     models.TranscriptionProfile
	policy      *models.AdaptiveExecutionPolicy
	scope       models.AdaptiveLearningScope
	settings    models.AdaptiveStageSettings
	recordings  []string
}

func newAdaptiveFixture(t *testing.T) *adaptiveFixture {
	t.Helper()
	checkpoints, db, recording := recoveryFixture(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.AdaptiveProfileRevision{}, &models.AdaptiveObservation{}, &models.AdaptiveLearnedPlan{}, &models.AdaptivePlanSelection{}))
	contextText, token := "C++ foo.bar engineering context", "test-only-current-secret"
	profile := models.TranscriptionProfile{Name: "measured profile", IsDefault: true, Parameters: models.WhisperXParams{ModelFamily: "whisper", Model: "large-v3", Device: "cuda", ComputeType: "float32", BatchSize: 8, RecoveryMode: "cpu_fallback", HFTokenSource: "custom", HfToken: &token, TranscriptionContext: &contextText, AdaptivePolicy: &models.AdaptiveExecutionPolicy{Learn: true, Stages: map[string]models.AdaptiveStagePolicy{"recognition": {AllowCPU: true, CPUPrecision: "float32"}}}}}
	require.NoError(t, NewProfileRepository(db).Create(context.Background(), &profile))
	second := uuid.NewString()
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: second, AudioPath: "second.wav", Status: models.StatusProcessing}).Error)
	store := NewAdaptiveLearningRepository(db)
	policy, err := store.SnapshotAtAdmission(context.Background(), profile.ID, 1, profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	hash := recoveryHash([]byte("immutable-runtime-model-hardware-settings"))
	return &adaptiveFixture{db: db, store: store, checkpoints: checkpoints, profile: profile, policy: policy, recordings: []string{recording, second}, scope: models.AdaptiveLearningScope{StageKey: "recognition", FixedSettingsHash: hash, RuntimeFingerprint: hash, ModelFingerprint: hash, HardwareFingerprint: hash, WorkloadClass: "minutes_0_10_mono", MemoryDomain: "gpu_device", MemoryCapacityBytes: 8 << 30, OriginalWindowSeconds: 30}, settings: models.AdaptiveStageSettings{Device: "cuda", Precision: "float32", BatchSize: 2, Concurrency: 1, WindowSeconds: 30}}
}

func adaptiveInt(value int64) *int64 { return &value }

// Every observation comes from a real, completed stage and immutable artifact;
// no fake measurement API bypass or database-only success is used in tests.
func (f *adaptiveFixture) completedObservation(t *testing.T, recordingIndex int, settings models.AdaptiveStageSettings, outcome string) models.AdaptiveObservation {
	t.Helper()
	ctx := context.Background()
	recording := f.recordings[recordingIndex]
	execution := recoveryExecution(t, f.db, recording)
	params := f.profile.Parameters
	params.AdaptivePolicy = f.policy
	var savedExecution models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&savedExecution, "id = ?", execution).Error)
	savedExecution.ActualParameters = params.WithoutSecrets()
	require.NoError(t, f.db.Model(&savedExecution).Select("*").Updates(&savedExecution).Error)
	spec := recoverySpec(recording, execution)
	spec.NodeKey = "asr" // Real combined-ASR node; semantic learning key is recognition.
	stage, err := f.checkpoints.EnsureStage(ctx, spec)
	require.NoError(t, err)
	attempt, err := f.checkpoints.ClaimStage(ctx, stage.ID, 1, AttemptSettings{Device: settings.Device, Precision: settings.Precision, BatchSize: settings.BatchSize, WindowSeconds: settings.WindowSeconds, SettingsHash: spec.Provenance.SettingsHash, Reason: "initial", OverlapSeconds: settings.OverlapSeconds, StitchingVersion: settings.StitchingVersion})
	require.NoError(t, err)
	require.NoError(t, f.checkpoints.RecordAttemptMeasurements(ctx, attempt.ID, 1, models.StageMeasurements{Scope: "sampled-device-and-owned-process-vram", GPUTotalBytes: adaptiveInt(8 << 30), DeviceUsedBeforeBytes: adaptiveInt(1 << 30), DevicePeakUsedBytes: adaptiveInt(4 << 30), Samples: 3, ElapsedSeconds: 1}))
	if outcome == "succeeded" {
		_, err = f.checkpoints.CommitCheckpoint(ctx, attempt.ID, 1, recoveryText("C++ foo.bar, intact."), nil)
	} else {
		err = f.checkpoints.FailAttempt(ctx, attempt.ID, 1, models.RecoveryRetryable, outcome)
	}
	require.NoError(t, err)
	return models.AdaptiveObservation{ProfileID: f.profile.ID, ProfileRevision: f.policy.ProfileRevision, LearningGeneration: f.policy.LearningGeneration, Scope: f.scope, ExecutionID: execution, StageID: stage.ID, AttemptID: attempt.ID, RecordingID: recording, OwnerGeneration: 1, Settings: settings, Outcome: outcome, FullStage: outcome == "succeeded", Qualified: true, PeakMemoryBytes: adaptiveInt(4 << 30), AvailableBeforeBytes: adaptiveInt(7 << 30), ReserveBytes: adaptiveInt(4 << 30), LoadingMilliseconds: adaptiveInt(50), ProcessingMilliseconds: adaptiveInt(1000), SourceDurationSeconds: 10, ProcessedDurationSeconds: 10, Channels: 1}
}

func (f *adaptiveFixture) append(t *testing.T, o models.AdaptiveObservation) *models.AdaptiveObservation {
	t.Helper()
	stored, err := f.store.AppendObservation(context.Background(), o)
	require.NoError(t, err)
	return stored
}

func (f *adaptiveFixture) promotion() AdaptivePromotionRequest {
	return AdaptivePromotionRequest{ProfileID: f.profile.ID, ExpectedRevision: f.policy.ProfileRevision, ExpectedGeneration: f.policy.LearningGeneration, ScopeKey: AdaptiveScopeKey(f.scope), CandidateHash: AdaptiveCandidateHash(f.settings)}
}

func (f *adaptiveFixture) successes(t *testing.T) {
	t.Helper()
	for _, recording := range []int{0, 1, 0} {
		f.append(t, f.completedObservation(t, recording, f.settings, "succeeded"))
	}
}

func TestAdaptivePromotionRequiresComparableMeasuredFullSuccesses(t *testing.T) {
	for _, test := range []struct {
		name          string
		mutate        func(*models.AdaptiveObservation)
		sameRecording bool
	}{
		{name: "missing peak", mutate: func(o *models.AdaptiveObservation) { o.PeakMemoryBytes = nil }},
		{name: "missing duration", mutate: func(o *models.AdaptiveObservation) { o.ProcessingMilliseconds = nil }},
		{name: "cached", mutate: func(o *models.AdaptiveObservation) { o.Cached = true }},
		{name: "partial", mutate: func(o *models.AdaptiveObservation) { o.FullStage = false }},
		{name: "unqualified", mutate: func(o *models.AdaptiveObservation) { o.Qualified = false }},
		{name: "contention", mutate: func(o *models.AdaptiveObservation) { o.ExternalContention = true }},

		{name: "same recording", sameRecording: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAdaptiveFixture(t)
			for i := 0; i < 3; i++ {
				index := i % 2
				if test.sameRecording {
					index = 0
				}
				o := f.completedObservation(t, index, f.settings, "succeeded")
				if test.mutate != nil {
					test.mutate(&o)
				}
				f.append(t, o)
			}
			_, err := f.store.Promote(context.Background(), f.promotion())
			require.ErrorIs(t, err, ErrAdaptiveEvidence)
			snapshot, err := f.store.Snapshot(context.Background(), f.profile.ID, "")
			require.NoError(t, err)
			require.Empty(t, snapshot.Plans)
			require.Equal(t, int64(1), snapshot.LearningGeneration)
		})
	}
}

func TestAdaptivePromotionSnapshotsAndExactScope(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	empty, err := f.store.Snapshot(ctx, f.profile.ID, "")
	require.NoError(t, err)
	require.Equal(t, "no_measurements", empty.Status)
	require.Empty(t, empty.Plans)
	_, err = f.store.Freeze(ctx, f.profile.ID, 1, 1, "invented")
	require.ErrorIs(t, err, ErrAdaptiveEvidence)
	f.successes(t)
	plan, err := f.store.Promote(ctx, f.promotion())
	require.NoError(t, err)
	require.Len(t, plan.ObservationIDs, 3)
	require.Equal(t, int64(1), plan.LearningGeneration)
	selected, err := f.store.SelectStartingPlan(ctx, f.policy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Nil(t, selected, "already admitted requests must not pull a promotion")
	newPolicy, err := f.store.SnapshotAtAdmission(ctx, f.profile.ID, 1, f.profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	require.Equal(t, int64(2), newPolicy.LearningGeneration)
	require.Len(t, newPolicy.LearnedPlans, 1)
	selected, err = f.store.SelectStartingPlan(ctx, newPolicy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Equal(t, plan.ID, selected.ID)
	for _, mutate := range []func(*models.AdaptiveLearningScope){func(s *models.AdaptiveLearningScope) { s.HardwareFingerprint = recoveryHash([]byte("new GPU")) }, func(s *models.AdaptiveLearningScope) { s.RuntimeFingerprint = recoveryHash([]byte("new runtime")) }, func(s *models.AdaptiveLearningScope) { s.WorkloadClass = "minutes_30_60_stereo" }, func(s *models.AdaptiveLearningScope) { s.MemoryCapacityBytes = 12 << 30 }} {
		changed := f.scope
		mutate(&changed)
		selected, err = f.store.SelectStartingPlan(ctx, newPolicy, changed, 7<<30)
		require.NoError(t, err)
		require.Nil(t, selected)
	}
	selected, err = f.store.SelectStartingPlan(ctx, newPolicy, f.scope, 4<<30)
	require.NoError(t, err)
	require.Nil(t, selected, "fresh capacity must include measured peak and reserve")
	_, err = f.store.Reset(ctx, f.profile.ID, 1, 2)
	require.NoError(t, err)
	selected, err = f.store.SelectStartingPlan(ctx, newPolicy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Equal(t, plan.ID, selected.ID, "reset must not alter already admitted choices")
	fresh, err := f.store.SnapshotAtAdmission(ctx, f.profile.ID, 1, f.profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	require.Empty(t, fresh.LearnedPlans)
	_, err = f.store.Promote(ctx, f.promotion())
	require.ErrorIs(t, err, ErrAdaptiveConflict)
}

func TestAdaptiveObservationOwnershipIdempotencyAndHistoricalFence(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	o := f.completedObservation(t, 0, f.settings, "succeeded")
	first := f.append(t, o)
	again := f.append(t, o)
	require.Equal(t, first.ID, again.ID)
	o.LoadingMilliseconds = adaptiveInt(25)
	_, err := f.store.AppendObservation(ctx, o)
	require.ErrorIs(t, err, ErrAdaptiveConflict)
	o = f.completedObservation(t, 0, f.settings, "succeeded")
	o.Settings.Precision = "float16"
	_, err = f.store.AppendObservation(ctx, o)
	require.ErrorIs(t, err, ErrAdaptiveOwnership)
	o = f.completedObservation(t, 0, f.settings, "succeeded")
	o.Outcome = "cuda_out_of_memory"
	o.FullStage = false
	_, err = f.store.AppendObservation(ctx, o)
	require.ErrorIs(t, err, ErrAdaptiveOwnership)
	o = f.completedObservation(t, 0, f.settings, "succeeded")
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", o.ExecutionID).UpdateColumn("cancelled_at", time.Now()).Error)
	_, err = f.store.AppendObservation(ctx, o)
	require.ErrorIs(t, err, ErrAdaptiveOwnership)
	o = f.completedObservation(t, 0, f.settings, "succeeded")
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", o.ExecutionID).UpdateColumn("owner_generation", 2).Error)
	_, err = f.store.AppendObservation(ctx, o)
	require.ErrorIs(t, err, ErrAdaptiveOwnership)
	o = f.completedObservation(t, 1, f.settings, "succeeded")
	_, err = f.store.Reset(ctx, f.profile.ID, 1, 1)
	require.NoError(t, err)
	f.append(t, o)
	_, err = f.store.Promote(ctx, f.promotion())
	require.ErrorIs(t, err, ErrAdaptiveConflict, "old admitted run may append history but cannot replace reset")
}

func TestAdaptiveConcurrentPromotionAndEditCAS(t *testing.T) {
	f := newAdaptiveFixture(t)
	f.successes(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.store.Promote(ctx, f.promotion()); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrAdaptiveConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	repo := NewProfileRepository(f.db)
	first, err := repo.FindByID(ctx, f.profile.ID)
	require.NoError(t, err)
	stale := *first
	first.Name = "User edit"
	require.NoError(t, repo.Update(ctx, first))
	stale.Name = "Late overwrite"
	require.ErrorIs(t, repo.Update(ctx, &stale), ErrAdaptiveConflict)
	_, err = f.store.Promote(ctx, AdaptivePromotionRequest{ProfileID: f.profile.ID, ExpectedRevision: 1, ExpectedGeneration: 2})
	require.ErrorIs(t, err, ErrAdaptiveConflict)
	var history []models.AdaptiveProfileRevision
	require.NoError(t, f.db.Where("profile_id = ?", f.profile.ID).Order("revision").Find(&history).Error)
	require.Len(t, history, 2)
	require.Equal(t, "measured profile", history[0].Name)
	require.Equal(t, "User edit", history[1].Name)
}

func TestAdaptiveFreezeRestorePreserveUserSettingsAndNeverHistoricalSecrets(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	f.successes(t)
	plan, err := f.store.Promote(ctx, f.promotion())
	require.NoError(t, err)
	frozen, err := f.store.Freeze(ctx, f.profile.ID, 1, 2, plan.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), frozen.Revision)
	require.Equal(t, int64(3), frozen.LearningGeneration)
	require.Equal(t, "fixed", frozen.Parameters.RecoveryMode)
	require.False(t, frozen.Parameters.AdaptivePolicy.Learn)
	require.Equal(t, f.settings, *frozen.Parameters.AdaptivePolicy.Stages["recognition"].Fixed)
	require.False(t, frozen.Parameters.Fp16)
	require.Equal(t, f.profile.Parameters.TranscriptionContext, frozen.Parameters.TranscriptionContext)
	newToken := "test-only-new-secret"
	frozen.Parameters.HfToken = &newToken
	frozen.IsDefault = false
	frozen.Name = "edited after freeze"
	require.NoError(t, NewProfileRepository(f.db).Update(ctx, frozen))
	restored, err := f.store.Restore(ctx, f.profile.ID, 3, 4, 1)
	require.NoError(t, err)
	require.Equal(t, int64(4), restored.Revision)
	require.Equal(t, "cpu_fallback", restored.Parameters.RecoveryMode)
	require.True(t, restored.Parameters.AdaptivePolicy.Learn)
	require.Nil(t, restored.Parameters.AdaptivePolicy.Stages["recognition"].Fixed)
	require.Equal(t, &newToken, restored.Parameters.HfToken)
	require.False(t, restored.IsDefault, "restoring settings must not change default preference")
	snapshot, err := f.store.Snapshot(ctx, f.profile.ID, "")
	require.NoError(t, err)
	require.Len(t, snapshot.Revisions, 4)
	require.Empty(t, snapshot.Plans)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), newToken)
	require.NotContains(t, string(encoded), "test-only-current-secret")
	require.Equal(t, "restore", snapshot.Revisions[0].Reason)
	require.Equal(t, int64(1), *snapshot.Revisions[0].SourceRevision)
	var raw []string
	require.NoError(t, f.db.Model(&models.AdaptiveProfileRevision{}).Pluck("parameters", &raw).Error)
	for _, value := range raw {
		require.NotContains(t, value, "test-only-")
	}
}

func TestAdaptiveShortWindowRequiresFullExhaustionAndFreshCapacity(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	full := f.settings
	f.settings.WindowSeconds = 15
	f.settings.OverlapSeconds = 2
	f.settings.StitchingVersion = "tested-boundary-v1"
	f.successes(t)
	request := f.promotion()
	_, err := f.store.Promote(ctx, request)
	require.ErrorIs(t, err, ErrAdaptiveEvidence)
	var evidence []*models.AdaptiveObservation
	for index, batch := range []int{2, 1} {
		settings := full
		settings.BatchSize = batch
		o := f.completedObservation(t, index, settings, "cuda_out_of_memory")
		o.AvailableBeforeBytes = adaptiveInt(int64(7-index) << 30)
		var attempt models.RecoveryAttempt
		require.NoError(t, f.db.First(&attempt, "id = ?", o.AttemptID).Error)
		attempt.Measurements.DeviceUsedBeforeBytes = adaptiveInt(int64(index+1) << 30)
		require.NoError(t, f.db.Model(&attempt).Select("measurements").Updates(&attempt).Error)
		evidence = append(evidence, f.append(t, o))
		request.FullWindowCandidateHashes = append(request.FullWindowCandidateHashes, AdaptiveCandidateHash(settings))
		request.ExhaustionEvidenceIDs = append(request.ExhaustionEvidenceIDs, evidence[index].ID)
	}
	plan, err := f.store.Promote(ctx, request)
	require.NoError(t, err)
	require.Equal(t, int64(6<<30), plan.ExhaustionAvailableBytes, "must match every full-window failure, not the most permissive one")
	policy, err := f.store.SnapshotAtAdmission(ctx, f.profile.ID, 1, f.profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	selected, err := f.store.SelectStartingPlan(ctx, policy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Nil(t, selected, "more fresh memory requires trying full windows again")
	selected, err = f.store.SelectStartingPlan(ctx, policy, f.scope, 6<<30)
	require.NoError(t, err)
	require.Equal(t, plan.ID, selected.ID)
}

func TestAdaptiveUnexplainedFailurePreventsPromotion(t *testing.T) {
	f := newAdaptiveFixture(t)
	f.successes(t)
	f.append(t, f.completedObservation(t, 0, f.settings, "adapter_failed"))
	_, err := f.store.Promote(context.Background(), f.promotion())
	require.ErrorIs(t, err, ErrAdaptiveEvidence)
}

func TestAdaptiveObservationsCannotInventMeasurementsOrCloudInference(t *testing.T) {
	for _, change := range []string{"missing", "wrong_peak", "wrong_reserve", "cleared_contention", "unknown_ownership", "cloud"} {
		t.Run(change, func(t *testing.T) {
			f := newAdaptiveFixture(t)
			o := f.completedObservation(t, 0, f.settings, "succeeded")
			var attempt models.RecoveryAttempt
			require.NoError(t, f.db.First(&attempt, "id = ?", o.AttemptID).Error)
			switch change {
			case "missing":
				attempt.Measurements = nil
			case "wrong_peak":
				o.PeakMemoryBytes = adaptiveInt(1 << 30)
			case "wrong_reserve":
				o.ReserveBytes = adaptiveInt(5 << 30)
			case "cleared_contention":
				attempt.Measurements.ExternalContention = true
			case "unknown_ownership":
				attempt.Measurements.OwnershipUnknown = true
			case "cloud":
				require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", o.ExecutionID).UpdateColumn("actual_model_family", "openai").Error)
			}
			require.NoError(t, f.db.Model(&attempt).Select("measurements").Updates(&attempt).Error)
			_, err := f.store.AppendObservation(context.Background(), o)
			if change == "cloud" {
				require.ErrorIs(t, err, ErrAdaptiveOwnership)
			} else {
				require.ErrorIs(t, err, ErrAdaptiveEvidence)
			}
			if change == "unknown_ownership" {
				o.Qualified = false
				stored := f.append(t, o)
				require.False(t, stored.Qualified, "unknown ownership may remain visible as diagnostics only")
			}
		})
	}
}

func TestAdaptiveMeasuredReserveThresholdAndCPUObservations(t *testing.T) {
	for _, precision := range []string{"gpu_insufficient", "float32", "i2_s+i8_s"} {
		cpu := precision != "gpu_insufficient"
		t.Run(precision, func(t *testing.T) {
			f := newAdaptiveFixture(t)
			if cpu {
				f.scope.MemoryDomain = "host_system"
				f.settings.Device = "cpu"
				f.settings.Precision = precision
			}
			for _, index := range []int{0, 1, 0} {
				o := f.completedObservation(t, index, f.settings, "succeeded")
				var attempt models.RecoveryAttempt
				require.NoError(t, f.db.First(&attempt, "id = ?", o.AttemptID).Error)
				if cpu {
					attempt.Measurements = &models.StageMeasurements{Scope: "sampled-owned-process-rss-and-host-or-cgroup", HostTotalBytes: adaptiveInt(8 << 30), HostAvailableBeforeBytes: adaptiveInt(7 << 30), HostMinimumAvailableBytes: adaptiveInt(4 << 30), ProcessPeakBytes: adaptiveInt(4 << 30), Samples: 3, ElapsedSeconds: 1}
				} else {
					attempt.Measurements.DevicePeakUsedBytes = adaptiveInt(7 << 30)
					o.PeakMemoryBytes = adaptiveInt(7 << 30)
					o.ReserveBytes = adaptiveInt(1 << 30)
				}
				require.NoError(t, f.db.Model(&attempt).Select("measurements").Updates(&attempt).Error)
				f.append(t, o)
			}
			_, err := f.store.Promote(context.Background(), f.promotion())
			if cpu {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrAdaptiveEvidence)
			}
		})
	}
}

func TestAdaptiveProfileRevisionWriteFailureRollsBackDefaultAndSettings(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	repo := NewProfileRepository(f.db)
	other := models.TranscriptionProfile{Name: "other", IsDefault: true}
	require.NoError(t, repo.Create(ctx, &other))
	profile, err := repo.FindByID(ctx, f.profile.ID)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_adaptive_revision BEFORE INSERT ON adaptive_profile_revisions WHEN NEW.revision > 1 BEGIN SELECT RAISE(ABORT, 'snapshot write rejected'); END`).Error)
	profile.Name = "must roll back"
	profile.IsDefault = true
	require.ErrorContains(t, repo.Update(ctx, profile), "snapshot write rejected")
	stored, err := repo.FindByID(ctx, f.profile.ID)
	require.NoError(t, err)
	require.Equal(t, "measured profile", stored.Name)
	require.Equal(t, int64(1), stored.Revision)
	require.False(t, stored.IsDefault)
	current, err := repo.FindDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, other.ID, current.ID)
}

func TestAdaptiveFreezeMaterializesEffectiveInheritedContext(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	repo := NewProfileRepository(f.db)
	f.profile.Parameters.TranscriptionContext = nil
	require.NoError(t, repo.Update(ctx, &f.profile))
	policy, err := f.store.SnapshotAtAdmission(ctx, f.profile.ID, f.profile.Revision, f.profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	f.policy = policy
	resolved := "User default actually used for the measured executions"
	f.profile.Parameters.TranscriptionContext = &resolved // Admission resolves this only in execution snapshots.
	fresh := false
	f.profile.Parameters.ReuseCheckpoints = &fresh
	f.successes(t)
	plan, err := f.store.Promote(ctx, f.promotion())
	require.NoError(t, err)
	frozen, err := f.store.Freeze(ctx, f.profile.ID, 2, 3, plan.ID)
	require.NoError(t, err)
	require.Equal(t, &resolved, frozen.Parameters.TranscriptionContext)
	require.Nil(t, frozen.Parameters.ReuseCheckpoints, "a run's force-fresh override must not replace the profile preference")
	restored, err := f.store.Restore(ctx, f.profile.ID, 3, 4, 2)
	require.NoError(t, err)
	require.Nil(t, restored.Parameters.TranscriptionContext, "the prior saved revision must keep inheritance distinct from the measured execution")
}
