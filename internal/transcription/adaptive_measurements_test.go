package transcription

import (
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func TestGPUProcessOwnershipDistinguishesNamespaceUnknownFromExternal(t *testing.T) {
	before := classifyGPUProcesses(map[int]int64{900: 100}, nil, nil, true, true)
	require.True(t, before.external, "an existing consumer predates this stage worker")
	require.False(t, before.ownershipUnknown)
	foreign := classifyGPUProcesses(map[int]int64{900: 200}, nil, before.pids, true, false)
	require.True(t, foreign.external)
	require.False(t, foreign.processKnown)

	// NVML's host PID is not the new worker's namespace PID. It could also be
	// a newly arriving foreign process: neither ownership claim is justified.
	for _, localPIDs := range []map[int]int64{nil, {37: 100}} {
		unknown := classifyGPUProcesses(map[int]int64{3498351: 1200}, localPIDs, map[int]bool{}, true, false)
		require.True(t, unknown.ownershipUnknown)
		require.False(t, unknown.external)
		require.False(t, unknown.processKnown)
		require.Zero(t, unknown.process)
	}
	owned := classifyGPUProcesses(map[int]int64{37: 1200}, map[int]int64{37: 1}, nil, true, false)
	require.True(t, owned.processKnown)
	require.EqualValues(t, 1200, owned.process)
	require.False(t, owned.external)
	require.False(t, owned.ownershipUnknown)
}

func TestMissingGPUProcessReadingsAreUnknownNotConfirmedContention(t *testing.T) {
	for _, preflight := range []bool{true, false} {
		sample := classifyGPUProcesses(nil, nil, nil, false, preflight)
		require.True(t, sample.ownershipUnknown)
		require.False(t, sample.external)
		require.False(t, sample.processKnown)
	}
}

func TestNamespaceUnknownDoesNotBlockQualifiedOOMLadder(t *testing.T) {
	sample := classifyGPUProcesses(map[int]int64{3498351: 1200}, nil, map[int]bool{}, true, false)
	settings := models.AdaptiveStageSettings{Device: "cuda", Precision: "float32", BatchSize: 2, Concurrency: 1}
	history := []models.RecoveryAttempt{
		{Device: "cuda", Precision: "float32", BatchSize: 2, Reason: "initial"},
		{Device: "cuda", Precision: "float32", BatchSize: 2, Reason: "cleanup_retry"},
	}
	next := nextAdaptiveCandidate(RecoveryBatchManagement, "cuda_out_of_memory", models.AdaptiveStagePolicy{}, interfaces.StageDescriptor{QualifiedBatches: []int{1}}, settings, settings, history, sample.external)
	require.NotNil(t, next)
	require.Equal(t, "smaller_batch", next.Reason)
	require.Equal(t, 1, next.Settings.BatchSize)
	confirmed := classifyGPUProcesses(map[int]int64{900: 1200}, nil, map[int]bool{900: true}, true, false)
	require.Nil(t, nextAdaptiveCandidate(RecoveryBatchManagement, "cuda_out_of_memory", models.AdaptiveStagePolicy{}, interfaces.StageDescriptor{QualifiedBatches: []int{1}}, settings, settings, history, confirmed.external))
}
