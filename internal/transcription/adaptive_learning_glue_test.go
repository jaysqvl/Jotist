package transcription

import (
	"context"
	"errors"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func learningGlueDescriptor() interfaces.StageDescriptor {
	return interfaces.StageDescriptor{Kind: "recognition", DevicePrecisions: map[string][]string{"cuda": {"float32"}, "cpu": {"float32"}}, QualifiedBatches: []int{1, 8, 2, 4}, WindowPolicy: &interfaces.StageWindowPolicy{Unit: "seconds", Candidates: []int{20, 10}, MinimumOverlap: 2, StitchingVersion: "qualified-v1"}}
}

func TestPermittedFullWindowCandidatesEnumeratesBoundedLadderIndependently(t *testing.T) {
	original := models.AdaptiveStageSettings{Device: "cuda", Precision: "float32", BatchSize: 8, Concurrency: 1, WindowSeconds: 30}
	baseRule := models.AdaptiveStagePolicy{AllowCPU: true, CPUPrecision: "float32", MinBatchSize: 1, AllowShorterWindows: true, WindowCandidates: []int{20, 10}, MinWindowSeconds: 10, OverlapSeconds: 2}
	for _, test := range []struct {
		name, mode string
		mutate     func(*models.AdaptiveStagePolicy, *interfaces.StageDescriptor, *models.AdaptiveStageSettings)
		devices    []string
		batches    []int
	}{
		{name: "full allowed ladder", mode: RecoveryShorterWindows, devices: []string{"cuda", "cuda", "cuda", "cpu"}, batches: []int{8, 4, 2, 2}},
		{name: "two reductions maximum", mode: RecoveryCPUFallback, devices: []string{"cuda", "cuda", "cuda", "cpu"}, batches: []int{8, 4, 2, 2}},
		{name: "batch floor", mode: RecoveryShorterWindows, mutate: func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor, _ *models.AdaptiveStageSettings) {
			r.MinBatchSize = 4
		}, devices: []string{"cuda", "cuda", "cpu"}, batches: []int{8, 4, 4}},
		{name: "device lock", mode: RecoveryShorterWindows, mutate: func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor, _ *models.AdaptiveStageSettings) {
			r.DeviceLocked = true
		}, devices: []string{"cuda", "cuda", "cuda"}, batches: []int{8, 4, 2}},
		{name: "CPU not permitted", mode: RecoveryShorterWindows, mutate: func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor, _ *models.AdaptiveStageSettings) {
			r.AllowCPU = false
		}, devices: []string{"cuda", "cuda", "cuda"}, batches: []int{8, 4, 2}},
		{name: "CPU precision unsupported", mode: RecoveryShorterWindows, mutate: func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor, _ *models.AdaptiveStageSettings) {
			r.CPUPrecision = "float16"
		}, devices: []string{"cuda", "cuda", "cuda"}, batches: []int{8, 4, 2}},
		{name: "batch semantics unqualified", mode: RecoveryShorterWindows, mutate: func(_ *models.AdaptiveStagePolicy, d *interfaces.StageDescriptor, _ *models.AdaptiveStageSettings) {
			d.QualifiedBatches = nil
		}, devices: []string{"cuda", "cpu"}, batches: []int{8, 8}},
		{name: "level two", mode: RecoveryBatchManagement, devices: []string{"cuda", "cuda", "cuda"}, batches: []int{8, 4, 2}},
		{name: "level one", mode: RecoveryStageManagement, devices: []string{"cuda"}, batches: []int{8}},
		{name: "fixed", mode: RecoveryFixed, devices: []string{"cuda"}, batches: []int{8}},
		{name: "CPU never invents GPU", mode: RecoveryShorterWindows, mutate: func(_ *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor, o *models.AdaptiveStageSettings) {
			o.Device = "cpu"
		}, devices: []string{"cpu"}, batches: []int{8}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule, desc, start := baseRule, learningGlueDescriptor(), original
			if test.mutate != nil {
				test.mutate(&rule, &desc, &start)
			}
			candidates := permittedFullWindowCandidates(test.mode, rule, desc, start)
			require.Len(t, candidates, len(test.devices))
			seen := map[models.AdaptiveStageSettings]bool{}
			for i, candidate := range candidates {
				require.Equal(t, test.devices[i], candidate.Device)
				require.Equal(t, test.batches[i], candidate.BatchSize)
				require.Equal(t, start.WindowSeconds, candidate.WindowSeconds)
				require.Equal(t, start.OverlapSeconds, candidate.OverlapSeconds)
				require.Equal(t, start.StitchingVersion, candidate.StitchingVersion)
				require.False(t, seen[candidate], "cleanup retries must not count as another candidate")
				seen[candidate] = true
			}
		})
	}
}

func TestFrozenStageRequiresQualifiedDeviceBatchAndWindow(t *testing.T) {
	descriptor := learningGlueDescriptor()
	original := models.AdaptiveStageSettings{Device: "cuda", Precision: "float32", BatchSize: 8, Concurrency: 1, WindowSeconds: 30}
	for _, test := range []struct {
		name   string
		mutate func(*models.AdaptiveStageSettings)
		valid  bool
	}{
		{name: "unchanged", valid: true},
		{name: "qualified CPU", mutate: func(s *models.AdaptiveStageSettings) { s.Device = "cpu" }, valid: true},
		{name: "qualified smaller batch", mutate: func(s *models.AdaptiveStageSettings) { s.BatchSize = 2 }, valid: true},
		{name: "qualified window", mutate: func(s *models.AdaptiveStageSettings) {
			s.WindowSeconds = 20
			s.OverlapSeconds = 2
			s.StitchingVersion = "qualified-v1"
		}, valid: true},
		{name: "implicit device", mutate: func(s *models.AdaptiveStageSettings) { s.Device = "auto" }},
		{name: "unsupported CPU half", mutate: func(s *models.AdaptiveStageSettings) { s.Device = "cpu"; s.Precision = "float16" }},
		{name: "unqualified batch", mutate: func(s *models.AdaptiveStageSettings) { s.BatchSize = 3 }},
		{name: "unqualified duration", mutate: func(s *models.AdaptiveStageSettings) {
			s.WindowSeconds = 15
			s.OverlapSeconds = 2
			s.StitchingVersion = "qualified-v1"
		}},
		{name: "missing stitching", mutate: func(s *models.AdaptiveStageSettings) { s.WindowSeconds = 20; s.OverlapSeconds = 2 }},
		{name: "insufficient overlap", mutate: func(s *models.AdaptiveStageSettings) {
			s.WindowSeconds = 20
			s.OverlapSeconds = 1
			s.StitchingVersion = "qualified-v1"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixed := original
			if test.mutate != nil {
				test.mutate(&fixed)
			}
			err := validateFrozenStage(descriptor, original, fixed)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	quantized := original
	quantized.Device, quantized.Precision = "cpu", "i2_s+i8_s"
	descriptor.DevicePrecisions["cpu"] = []string{"i2_s+i8_s"}
	require.NoError(t, validateFrozenStage(descriptor, original, quantized))
}

func TestResumedLearnedStageKeepsImmutableInitialSettings(t *testing.T) {
	f := newStageTestFixture(t)
	r := f.execution(t, RecoveryBatchManagement, true)
	adapter := newStageTestAdapter(t, "learned-initial-choice-fixture")
	chosen := stageTestParams()
	chosen["batch_size"] = 1
	_, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, chosen, f.proc, func(params map[string]interface{}) (*interfaces.TranscriptResult, error) {
		require.Equal(t, 1, params["batch_size"])
		return nil, errors.New("fixture failure after admitted starting choice")
	})
	require.Error(t, err)
	before := f.stage(t, r.execution.ID, "asr")
	// A previous admission selected batch 1 from a submitted upper bound of 4.
	// Preserve that initial attempt as the stage's immutable plan on resumption.
	initial := settingsFromAttempt(before.Attempts[0])
	require.Equal(t, 1, initial.BatchSize)
	r.execution.ActualParameters.AdaptivePolicy = &models.AdaptiveExecutionPolicy{Learn: true, ProfileID: "saved-profile", ProfileRevision: 1, LearningGeneration: 1, SnapshotTaken: true, LearnedPlans: []models.AdaptivePlanSnapshot{{PlanID: "original-learned-choice", Settings: initial}}}
	f.resume(t, &r)
	submitted := stageTestParams()
	submitted["batch_size"] = 4
	result, _, err := runRecoverableStage(context.Background(), r, "combined", "asr", adapter, f.input, submitted, f.proc, func(params map[string]interface{}) (*interfaces.TranscriptResult, error) {
		require.Equal(t, 1, params["batch_size"], "new capacity must not replace the saved starting plan with submitted batch 4")
		return stageTestTranscript("unchanged initial plan"), nil
	})
	require.NoError(t, err)
	require.Equal(t, "unchanged initial plan", result.Text)
	after := f.stage(t, r.execution.ID, "asr")
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.CompatibilityKey, after.CompatibilityKey)
	require.Equal(t, before.ProvenanceJSON, after.ProvenanceJSON)
	require.Len(t, after.Attempts, 2)
	require.Equal(t, "resume_same_settings", after.Attempts[1].Reason)
	require.Equal(t, 1, after.Attempts[1].BatchSize)
}
