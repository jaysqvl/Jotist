package repository

import (
	"context"
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

func adaptiveHostEvidence(t *testing.T, f *adaptiveFixture, settings models.AdaptiveStageSettings) models.AdaptiveObservation {
	t.Helper()
	o := f.completedObservation(t, 1, settings, "host_out_of_memory")
	o.Scope.MemoryDomain, o.Scope.MemoryCapacityBytes = "host_system", 32<<30
	o.AvailableBeforeBytes, o.PeakMemoryBytes, o.ReserveBytes = adaptiveInt(24<<30), adaptiveInt(4<<30), adaptiveInt(16<<30)
	var attempt models.RecoveryAttempt
	require.NoError(t, f.db.First(&attempt, "id = ?", o.AttemptID).Error)
	attempt.Measurements = &models.StageMeasurements{Scope: "sampled-owned-process-rss-and-host-or-cgroup", HostTotalBytes: adaptiveInt(32 << 30), HostAvailableBeforeBytes: adaptiveInt(24 << 30), HostMinimumAvailableBytes: adaptiveInt(16 << 30), ProcessPeakBytes: adaptiveInt(4 << 30), Samples: 3, ElapsedSeconds: 1}
	require.NoError(t, f.db.Model(&attempt).Select("measurements").Updates(&attempt).Error)
	return o
}

func TestAdaptiveMixedDeviceExhaustionRequiresWholeLadderAndFreshBothCapacities(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	full := f.settings
	f.settings.WindowSeconds, f.settings.OverlapSeconds, f.settings.StitchingVersion = 15, 2, "tested-boundary-v1"
	f.successes(t)
	request := f.promotion()
	for _, batch := range []int{2, 1} {
		settings := full
		settings.BatchSize = batch
		o := f.append(t, f.completedObservation(t, 0, settings, "cuda_out_of_memory"))
		request.FullWindowCandidateHashes = append(request.FullWindowCandidateHashes, o.CandidateHash)
		request.ExhaustionEvidenceIDs = append(request.ExhaustionEvidenceIDs, o.ID)
	}
	cpu := full
	cpu.Device, cpu.BatchSize = "cpu", 1
	request.FullWindowCandidateHashes = append(request.FullWindowCandidateHashes, AdaptiveCandidateHash(cpu))
	_, err := f.store.Promote(ctx, request)
	require.ErrorIs(t, err, ErrAdaptiveEvidence, "a permitted full-window CPU candidate cannot be skipped")
	host := f.append(t, adaptiveHostEvidence(t, f, cpu))
	request.ExhaustionEvidenceIDs = append(request.ExhaustionEvidenceIDs, host.ID)
	plan, err := f.store.Promote(ctx, request)
	require.NoError(t, err)
	require.Len(t, plan.ExhaustionCapacityLimits, 2)
	policy, err := f.store.SnapshotAtAdmission(ctx, f.profile.ID, 1, f.profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	selected, err := f.store.SelectStartingPlan(ctx, policy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Nil(t, selected, "missing fresh CPU capacity cannot justify skipping full CPU")
	for _, reading := range []models.AdaptiveCapacityReading{{MemoryDomain: "host_system", MemoryCapacityBytes: 32 << 30, AvailableBytes: 25 << 30}, {MemoryDomain: "host_system", MemoryCapacityBytes: 64 << 30, AvailableBytes: 24 << 30}, {MemoryDomain: "host_system", MemoryCapacityBytes: 32 << 30, AvailableBytes: 0}} {
		selected, err = f.store.SelectStartingPlan(ctx, policy, f.scope, 7<<30, reading)
		require.NoError(t, err)
		require.Nil(t, selected)
	}
	freshCPU := models.AdaptiveCapacityReading{MemoryDomain: "host_system", MemoryCapacityBytes: 32 << 30, AvailableBytes: 24 << 30}
	selected, err = f.store.SelectStartingPlan(ctx, policy, f.scope, 7<<30, freshCPU)
	require.NoError(t, err)
	require.Equal(t, plan.ID, selected.ID)
	selected, err = f.store.SelectStartingPlan(ctx, policy, f.scope, (7<<30)+1, freshCPU)
	require.NoError(t, err)
	require.Nil(t, selected, "new GPU headroom requires another full-window attempt")
}

func TestAdaptiveMixedExhaustionRejectsUnrelatedOrContendedCPUFailures(t *testing.T) {
	for _, change := range []string{"hardware", "runtime", "model", "fixed settings", "workload", "original window", "contention", "capacity"} {
		t.Run(change, func(t *testing.T) {
			f := newAdaptiveFixture(t)
			full := f.settings
			f.settings.WindowSeconds, f.settings.OverlapSeconds, f.settings.StitchingVersion = 15, 2, "tested-boundary-v1"
			f.successes(t)
			gpu := f.append(t, f.completedObservation(t, 0, full, "cuda_out_of_memory"))
			cpu := full
			cpu.Device = "cpu"
			o := adaptiveHostEvidence(t, f, cpu)
			switch change {
			case "hardware":
				o.Scope.HardwareFingerprint = recoveryHash([]byte("another host"))
			case "runtime":
				o.Scope.RuntimeFingerprint = recoveryHash([]byte("another runtime"))
			case "model":
				o.Scope.ModelFingerprint = recoveryHash([]byte("another checkpoint"))
			case "fixed settings":
				o.Scope.FixedSettingsHash = recoveryHash([]byte("another prompt"))
			case "workload":
				o.Scope.WorkloadClass = "duration-7200-channels-1"
			case "original window":
				o.Scope.OriginalWindowSeconds = 60
			case "contention":
				o.ExternalContention = true
			case "capacity":
				o.Scope.MemoryCapacityBytes = 64 << 30
			}
			if change == "capacity" {
				_, err := f.store.AppendObservation(context.Background(), o)
				require.ErrorIs(t, err, ErrAdaptiveEvidence)
				return
			}
			host := f.append(t, o)
			request := f.promotion()
			request.FullWindowCandidateHashes = []string{gpu.CandidateHash, host.CandidateHash}
			request.ExhaustionEvidenceIDs = []string{gpu.ID, host.ID}
			_, err := f.store.Promote(context.Background(), request)
			require.ErrorIs(t, err, ErrAdaptiveEvidence)
		})
	}
}

func TestAdaptiveMetadataOnlyEditsPreserveImmutableSelectedPlans(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	f.successes(t)
	plan, err := f.store.Promote(ctx, f.promotion())
	require.NoError(t, err)
	repo := NewProfileRepository(f.db)
	profile, err := repo.FindByID(ctx, f.profile.ID)
	require.NoError(t, err)
	description := "new description"
	profile.Name, profile.Description, profile.IsDefault = "renamed", &description, false
	require.NoError(t, repo.Update(ctx, profile))
	require.Equal(t, int64(2), profile.Revision)
	require.Equal(t, int64(3), profile.LearningGeneration)
	policy, err := f.store.SnapshotAtAdmission(ctx, profile.ID, 2, profile.Parameters.AdaptivePolicy)
	require.NoError(t, err)
	require.Len(t, policy.LearnedPlans, 1)
	require.Equal(t, plan.ID, policy.LearnedPlans[0].PlanID)
	selected, err := f.store.SelectStartingPlan(ctx, policy, f.scope, 7<<30)
	require.NoError(t, err)
	require.Equal(t, plan.ID, selected.ID)
	require.Equal(t, int64(1), selected.ProfileRevision, "source plan must stay immutable")
	snapshot, err := f.store.Snapshot(ctx, profile.ID, "")
	require.NoError(t, err)
	require.Equal(t, []string{plan.ID}, snapshot.SelectedPlanIDs)
	require.Equal(t, int64(1), snapshot.Plans[0].ProfileRevision)
	frozen, err := f.store.Freeze(ctx, profile.ID, 2, 3, plan.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", frozen.Name)
	require.Equal(t, &description, frozen.Description)
	require.False(t, frozen.IsDefault)
	require.Equal(t, f.settings, *frozen.Parameters.AdaptivePolicy.Stages["recognition"].Fixed)
}

func TestAdaptiveFixedParameterEditDoesNotCarryPriorSelection(t *testing.T) {
	f := newAdaptiveFixture(t)
	ctx := context.Background()
	f.successes(t)
	_, err := f.store.Promote(ctx, f.promotion())
	require.NoError(t, err)
	profile, err := NewProfileRepository(f.db).FindByID(ctx, f.profile.ID)
	require.NoError(t, err)
	profile.Parameters.Model = "different-checkpoint"
	require.NoError(t, NewProfileRepository(f.db).Update(ctx, profile))
	snapshot, err := f.store.Snapshot(ctx, profile.ID, "")
	require.NoError(t, err)
	require.Empty(t, snapshot.Plans)
	require.Empty(t, snapshot.SelectedPlanIDs)
}
