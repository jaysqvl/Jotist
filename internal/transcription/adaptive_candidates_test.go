package transcription

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func adaptivePolicyFixture() (interfaces.StageDescriptor, models.AdaptiveStagePolicy, models.AdaptiveStageSettings) {
	return interfaces.StageDescriptor{
		Kind: "recognition", Recoverable: true,
		DevicePrecisions: map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "float32"}},
		QualifiedBatches: []int{1, 8, 2, 4},
		WindowPolicy:     &interfaces.StageWindowPolicy{Unit: "seconds", Candidates: []int{15, 30}, MinimumOverlap: 2, StitchingVersion: "test-overlap-v1"},
	}, models.AdaptiveStagePolicy{
		AllowCPU: true, CPUPrecision: "float32", MinBatchSize: 1,
		AllowShorterWindows: true, WindowCandidates: []int{15, 30}, MinWindowSeconds: 15, OverlapSeconds: 2,
	}, models.AdaptiveStageSettings{Device: "cuda", Precision: "float16", BatchSize: 8, Concurrency: 1, WindowSeconds: 60}
}

func adaptiveHistory(settings models.AdaptiveStageSettings, reason string, previous ...models.RecoveryAttempt) []models.RecoveryAttempt {
	return append(previous, models.RecoveryAttempt{
		AttemptNumber: len(previous) + 1, Device: settings.Device, Precision: settings.Precision,
		BatchSize: settings.BatchSize, WindowSeconds: settings.WindowSeconds, OverlapSeconds: settings.OverlapSeconds,
		StitchingVersion: settings.StitchingVersion, Reason: reason,
	})
}

func TestAdaptiveCandidateLevelMatrix(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	initial := adaptiveHistory(original, "initial")
	afterCleanup := adaptiveHistory(original, "cleanup_retry", initial...)
	for _, tc := range []struct {
		mode                                                                  string
		level                                                                 int
		initialReason, afterCleanupReason, noBatchesReason, windowsOnlyReason string
	}{
		{"", 0, "", "", "", ""},
		{RecoveryFixed, 0, "", "", "", ""},
		{RecoveryStageManagement, 1, "cleanup_retry", "", "", ""},
		{RecoveryBatchManagement, 2, "cleanup_retry", "smaller_batch", "", ""},
		{RecoveryCPUFallback, 3, "cleanup_retry", "smaller_batch", "cpu_fallback", ""},
		{RecoveryShorterWindows, 4, "cleanup_retry", "smaller_batch", "cpu_fallback", "shorter_window"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			if got := recoveryLevel(tc.mode); got != tc.level {
				t.Fatalf("level = %d, want %d", got, tc.level)
			}
			assertReason := func(got *adaptiveCandidate, want string) {
				t.Helper()
				if want == "" {
					if got != nil {
						t.Fatalf("unexpected candidate %+v", got)
					}
					return
				}
				if got == nil || got.Reason != want {
					t.Fatalf("candidate %+v, want %s", got, want)
				}
			}
			assertReason(nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", rule, desc, original, original, initial, false), tc.initialReason)
			assertReason(nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", rule, desc, original, original, afterCleanup, false), tc.afterCleanupReason)
			noBatches := desc
			noBatches.QualifiedBatches = nil
			assertReason(nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", rule, noBatches, original, original, afterCleanup, false), tc.noBatchesReason)
			windowsOnly := rule
			windowsOnly.AllowCPU = false
			assertReason(nextAdaptiveCandidate(tc.mode, "cuda_out_of_memory", windowsOnly, noBatches, original, original, afterCleanup, false), tc.windowsOnlyReason)
		})
	}
}

func TestAdaptiveCandidateFiniteLadderSurvivesPersistedHistory(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	current := original
	history := adaptiveHistory(original, "initial")
	want := []struct {
		reason, device, precision string
		batch                     int
		window                    float64
	}{
		{"cleanup_retry", "cuda", "float16", 8, 60},
		{"smaller_batch", "cuda", "float16", 4, 60},
		{"smaller_batch", "cuda", "float16", 2, 60},
		{"cpu_fallback", "cpu", "float32", 2, 60},
		{"shorter_window", "cuda", "float16", 2, 30},
		{"shorter_window", "cuda", "float16", 2, 15},
	}
	for _, expected := range want {
		// A new coordinator sees only persisted attempts, not process-local retry counters.
		encoded, err := json.Marshal(history)
		if err != nil {
			t.Fatal(err)
		}
		var restored []models.RecoveryAttempt
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		code := "cuda_out_of_memory"
		if current.Device == "cpu" {
			code = "host_out_of_memory"
		}
		next := nextAdaptiveCandidate(RecoveryShorterWindows, code, rule, desc, original, current, restored, false)
		if next == nil {
			t.Fatalf("ladder stopped before %s at history %#v", expected.reason, restored)
		}
		if next.Reason != expected.reason || next.Settings.Device != expected.device || next.Settings.Precision != expected.precision || next.Settings.BatchSize != expected.batch || next.Settings.WindowSeconds != expected.window {
			t.Fatalf("got %+v; want %+v", next, expected)
		}
		if !candidateAllowed(RecoveryShorterWindows, rule, desc, original, next.Settings) {
			t.Fatalf("ladder generated a candidate rejected by its own policy: %+v", next)
		}
		if next.Reason == "shorter_window" && (next.Settings.OverlapSeconds != 2 || next.Settings.StitchingVersion != desc.WindowPolicy.StitchingVersion) {
			t.Fatal("shortened window lost its overlap/stitching contract")
		}
		current = next.Settings
		history = adaptiveHistory(current, next.Reason, restored...)
	}
	if len(history) != 7 {
		t.Fatal(len(history))
	}
	if next := nextAdaptiveCandidate(RecoveryShorterWindows, "cuda_out_of_memory", rule, desc, original, current, history, false); next != nil {
		t.Fatalf("eighth attempt proposed: %+v", next)
	}
	// Even history with no cleanup marker cannot reset the absolute seven-attempt ceiling.
	for i := range history {
		history[i].Reason = "resume_same_settings"
	}
	if next := nextAdaptiveCandidate(RecoveryStageManagement, "cuda_runtime_error", rule, desc, original, original, history, false); next != nil {
		t.Fatalf("persisted budget bypassed: %+v", next)
	}
	if !reflect.DeepEqual(desc.QualifiedBatches, []int{1, 8, 2, 4}) || !reflect.DeepEqual(rule.WindowCandidates, []int{15, 30}) {
		t.Fatal("candidate selection mutated adapter/profile order")
	}
}

func TestAdaptiveCandidateCPURequiresExplicitUnlockedSupportedPrecision(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	desc.QualifiedBatches = nil
	rule.AllowShorterWindows = false
	history := adaptiveHistory(original, "cleanup_retry", adaptiveHistory(original, "initial")...)
	for _, tc := range []struct {
		name string
		edit func(*models.AdaptiveStagePolicy, *interfaces.StageDescriptor)
	}{
		{"device locked", func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor) { r.DeviceLocked = true }},
		{"permission absent", func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor) { r.AllowCPU = false }},
		{"precision absent", func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor) { r.CPUPrecision = "" }},
		{"unsupported precision", func(r *models.AdaptiveStagePolicy, _ *interfaces.StageDescriptor) { r.CPUPrecision = "float16" }},
		{"CPU not declared", func(_ *models.AdaptiveStagePolicy, d *interfaces.StageDescriptor) {
			d.DevicePrecisions = map[string][]string{"cuda": {"float16"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, d := rule, desc
			tc.edit(&r, &d)
			if next := nextAdaptiveCandidate(RecoveryCPUFallback, "cuda_out_of_memory", r, d, original, original, history, false); next != nil {
				t.Fatalf("unapproved CPU fallback: %+v", next)
			}
		})
	}
	next := nextAdaptiveCandidate(RecoveryCPUFallback, "cuda_out_of_memory", rule, desc, original, original, history, false)
	if next == nil || next.Settings.Device != "cpu" || next.Settings.Precision != "float32" {
		t.Fatalf("explicit fallback missing: %+v", next)
	}
	if next.Settings.BatchSize != original.BatchSize || next.Settings.WindowSeconds != original.WindowSeconds || next.Settings.OverlapSeconds != original.OverlapSeconds {
		t.Fatal("CPU fallback changed batch or audio context")
	}
}

func TestAdaptiveCandidateQualifiedBatchesRespectMinimumWithoutChangingWindows(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	rule.MinBatchSize = 3
	history := adaptiveHistory(original, "cleanup_retry", adaptiveHistory(original, "initial")...)
	next := nextAdaptiveCandidate(RecoveryBatchManagement, "cuda_out_of_memory", rule, desc, original, original, history, false)
	if next == nil || next.Reason != "smaller_batch" || next.Settings.BatchSize != 4 {
		t.Fatalf("expected nearest smaller qualified batch: %+v", next)
	}
	want := original
	want.BatchSize = 4
	if next.Settings != want {
		t.Fatalf("batch reduction changed other settings: %+v", next.Settings)
	}
	history = adaptiveHistory(next.Settings, next.Reason, history...)
	if next := nextAdaptiveCandidate(RecoveryBatchManagement, "cuda_out_of_memory", rule, desc, original, want, history, false); next != nil {
		t.Fatalf("went below batch floor or changed windows: %+v", next)
	}
	desc.QualifiedBatches = nil
	if next := nextAdaptiveCandidate(RecoveryBatchManagement, "cuda_out_of_memory", rule, desc, original, original, history, false); next != nil {
		t.Fatalf("invented batch qualification: %+v", next)
	}
}

func TestAdaptiveCandidateFailureClassificationAndContention(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	initial := adaptiveHistory(original, "initial")
	afterCleanup := adaptiveHistory(original, "cleanup_retry", initial...)
	for _, code := range []string{"adapter_failed", "cancelled", "deadline_exceeded", "import_error", "access_denied", "checkpoint_persistence_failed", "CUDA out of memory"} {
		if next := nextAdaptiveCandidate(RecoveryShorterWindows, code, rule, desc, original, original, initial, false); next != nil {
			t.Fatalf("ordinary error %q proposed quality change %+v", code, next)
		}
	}
	for _, code := range []string{"cuda_out_of_memory", "cuda_runtime_error"} {
		next := nextAdaptiveCandidate(RecoveryShorterWindows, code, rule, desc, original, original, initial, true)
		if next == nil || next.Reason != "cleanup_retry" || next.Settings != original {
			t.Fatalf("contention may only allow identical cleanup: %+v", next)
		}
		if next := nextAdaptiveCandidate(RecoveryShorterWindows, code, rule, desc, original, original, afterCleanup, true); next != nil {
			t.Fatalf("contention caused an adaptive quality change: %+v", next)
		}
	}
	next := nextAdaptiveCandidate(RecoveryShorterWindows, "cuda_runtime_error", rule, desc, original, original, afterCleanup, false)
	if next == nil || next.Reason != "cpu_fallback" || next.Settings.BatchSize != original.BatchSize || next.Settings.WindowSeconds != original.WindowSeconds {
		t.Fatalf("CUDA runtime error changed batch/window: %+v", next)
	}
	rule.AllowCPU = false
	if next := nextAdaptiveCandidate(RecoveryShorterWindows, "cuda_runtime_error", rule, desc, original, original, afterCleanup, false); next != nil {
		t.Fatalf("runtime error tried a smaller window: %+v", next)
	}
}

func TestAdaptiveCandidateHostOOMUsesOptedInWindowsAndOriginalDevice(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	cpu := original
	cpu.Device, cpu.Precision = "cpu", "float32"
	history := adaptiveHistory(cpu, "cpu_fallback", adaptiveHistory(original, "cleanup_retry", adaptiveHistory(original, "initial")...)...)
	next := nextAdaptiveCandidate(RecoveryShorterWindows, "host_out_of_memory", rule, desc, original, cpu, history, false)
	if next == nil || next.Reason != "shorter_window" || next.Settings.Device != "cuda" || next.Settings.Precision != original.Precision || next.Settings.BatchSize != cpu.BatchSize || next.Settings.WindowSeconds != 30 {
		t.Fatalf("expected opted-in smaller window on original GPU: %+v", next)
	}
	if next := nextAdaptiveCandidate(RecoveryCPUFallback, "host_out_of_memory", rule, desc, original, cpu, history, false); next != nil {
		t.Fatalf("Level 3 silently shortened host workload: %+v", next)
	}
	if next := nextAdaptiveCandidate(RecoveryShorterWindows, "host_out_of_memory", rule, desc, original, cpu, history, true); next != nil {
		t.Fatalf("contention shortened host workload: %+v", next)
	}
	rule.DeviceLocked = true
	next = nextAdaptiveCandidate(RecoveryShorterWindows, "host_out_of_memory", rule, desc, cpu, cpu, adaptiveHistory(cpu, "initial"), false)
	if next == nil || next.Settings.Device != "cpu" || next.Settings.Precision != "float32" {
		t.Fatalf("CPU-only request gained a GPU or different precision: %+v", next)
	}
	rule.AllowShorterWindows = false
	if next := nextAdaptiveCandidate(RecoveryShorterWindows, "host_out_of_memory", rule, desc, cpu, cpu, adaptiveHistory(cpu, "initial"), false); next != nil {
		t.Fatalf("host exhaustion bypassed explicit window opt-in: %+v", next)
	}
}

func TestAdaptiveCandidateAllowedRespectsCurrentPolicyAndFrozenMode(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	batch := original
	batch.BatchSize = 4
	cpu := original
	cpu.Device, cpu.Precision = "cpu", "float32"
	window := original
	window.WindowSeconds, window.OverlapSeconds, window.StitchingVersion = 30, 2, "test-overlap-v1"
	for _, tc := range []struct {
		name, mode string
		candidate  models.AdaptiveStageSettings
		edit       func(*models.AdaptiveStagePolicy)
		want       bool
	}{
		{"original fixed", RecoveryFixed, original, nil, true},
		{"batch under L1", RecoveryStageManagement, batch, nil, false},
		{"batch under L2", RecoveryBatchManagement, batch, nil, true},
		{"CPU under L2", RecoveryBatchManagement, cpu, nil, false},
		{"CPU under L3", RecoveryCPUFallback, cpu, nil, true},
		{"CPU hard lock", RecoveryShorterWindows, cpu, func(r *models.AdaptiveStagePolicy) { r.DeviceLocked = true }, false},
		{"CPU withdrawn", RecoveryShorterWindows, cpu, func(r *models.AdaptiveStagePolicy) { r.AllowCPU = false }, false},
		{"window under L3", RecoveryCPUFallback, window, nil, false},
		{"window under L4", RecoveryShorterWindows, window, nil, true},
		{"window consent withdrawn", RecoveryShorterWindows, window, func(r *models.AdaptiveStagePolicy) { r.AllowShorterWindows = false }, false},
		{"bound raised", RecoveryShorterWindows, batch, func(r *models.AdaptiveStagePolicy) { r.MinBatchSize = 5 }, false},
		{"frozen mode rejects learned divergence", RecoveryFixed, batch, func(r *models.AdaptiveStagePolicy) { r.Fixed = &original }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rule
			if tc.edit != nil {
				tc.edit(&r)
			}
			if got := candidateAllowed(tc.mode, r, desc, original, tc.candidate); got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
	for _, change := range []func(*models.AdaptiveStageSettings){
		func(s *models.AdaptiveStageSettings) { s.Concurrency = 2 },
		func(s *models.AdaptiveStageSettings) { s.BatchSize = 16 },
		func(s *models.AdaptiveStageSettings) { s.BatchSize = 3 },
		func(s *models.AdaptiveStageSettings) { s.Precision = "float32" },
		func(s *models.AdaptiveStageSettings) { s.Device = "mps" },
		func(s *models.AdaptiveStageSettings) { s.WindowSeconds = 20 },
		func(s *models.AdaptiveStageSettings) { s.StitchingVersion = "other-stitcher" },
	} {
		candidate := original
		change(&candidate)
		if candidateAllowed(RecoveryShorterWindows, rule, desc, original, candidate) {
			t.Fatalf("accepted unqualified learned settings %+v", candidate)
		}
	}
}

func TestAdaptiveCandidateAllowedRejectsUnapprovedOverlapAndUnits(t *testing.T) {
	desc, rule, original := adaptivePolicyFixture()
	for _, overlap := range []float64{3, 15, 30, math.NaN(), math.Inf(1)} {
		candidate := original
		candidate.WindowSeconds, candidate.OverlapSeconds, candidate.StitchingVersion = 30, overlap, "test-overlap-v1"
		if candidateAllowed(RecoveryShorterWindows, rule, desc, original, candidate) {
			t.Errorf("accepted unapproved or invalid overlap %v", overlap)
		}
	}
	desc.WindowPolicy.Unit = "tokens"
	candidate := original
	candidate.WindowSeconds, candidate.OverlapSeconds, candidate.StitchingVersion = 30, 2, "test-overlap-v1"
	if candidateAllowed(RecoveryShorterWindows, rule, desc, original, candidate) {
		t.Error("seconds interpreted using a token-based descriptor")
	}
	rule.AllowCPU = false
	desc.QualifiedBatches = nil
	history := adaptiveHistory(original, "cleanup_retry", adaptiveHistory(original, "initial")...)
	if next := nextAdaptiveCandidate(RecoveryShorterWindows, "cuda_out_of_memory", rule, desc, original, original, history, false); next != nil {
		t.Errorf("proposed unqualified window unit: %+v", next)
	}
}

func TestAdaptivePolicyValidationRejectsInvalidBoundsAndFrozenSettings(t *testing.T) {
	_, good, original := adaptivePolicyFixture()
	for _, mode := range []string{"", RecoveryFixed, RecoveryStageManagement, RecoveryBatchManagement, RecoveryCPUFallback, RecoveryShorterWindows} {
		if err := ValidateAdaptivePolicy(models.WhisperXParams{RecoveryMode: mode}); err != nil {
			t.Fatalf("valid mode %q: %v", mode, err)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*models.AdaptiveStagePolicy)
	}{
		{"CPU precision missing", func(r *models.AdaptiveStagePolicy) { r.CPUPrecision = "" }},
		{"negative batch", func(r *models.AdaptiveStagePolicy) { r.MinBatchSize = -1 }},
		{"negative minimum window", func(r *models.AdaptiveStagePolicy) { r.MinWindowSeconds = -1 }},
		{"too many windows", func(r *models.AdaptiveStagePolicy) { r.WindowCandidates = []int{15, 30, 45} }},
		{"zero window", func(r *models.AdaptiveStagePolicy) { r.WindowCandidates = []int{0} }},
		{"window below floor", func(r *models.AdaptiveStagePolicy) { r.WindowCandidates = []int{10} }},
		{"no unique middle after overlap", func(r *models.AdaptiveStagePolicy) { r.OverlapSeconds = 7.5 }},
		{"overlap NaN", func(r *models.AdaptiveStagePolicy) { r.OverlapSeconds = math.NaN() }},
		{"overlap infinity", func(r *models.AdaptiveStagePolicy) { r.OverlapSeconds = math.Inf(1) }},
		{"frozen batch zero", func(r *models.AdaptiveStagePolicy) { fixed := original; fixed.BatchSize = 0; r.Fixed = &fixed }},
		{"frozen parallelism", func(r *models.AdaptiveStagePolicy) { fixed := original; fixed.Concurrency = 2; r.Fixed = &fixed }},
		{"frozen window NaN", func(r *models.AdaptiveStagePolicy) {
			fixed := original
			fixed.WindowSeconds = math.NaN()
			r.Fixed = &fixed
		}},
		{"frozen overlap NaN", func(r *models.AdaptiveStagePolicy) {
			fixed := original
			fixed.OverlapSeconds = math.NaN()
			r.Fixed = &fixed
		}},
		{"frozen overlap infinity", func(r *models.AdaptiveStagePolicy) {
			fixed := original
			fixed.OverlapSeconds = math.Inf(1)
			r.Fixed = &fixed
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := good
			tc.edit(&rule)
			params := models.WhisperXParams{RecoveryMode: RecoveryShorterWindows, AdaptivePolicy: &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"recognition": rule}}}
			if err := ValidateAdaptivePolicy(params); err == nil {
				t.Fatal("accepted invalid policy")
			}
		})
	}
	if ValidateAdaptivePolicy(models.WhisperXParams{RecoveryMode: "invented"}) == nil {
		t.Error("accepted unknown recovery mode")
	}
	if ValidateAdaptivePolicy(models.WhisperXParams{RecoveryMode: RecoveryFixed, AdaptivePolicy: &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"unknown": good}}}) == nil {
		t.Error("accepted unknown stage")
	}
}

func TestAdaptiveCandidateApplicationPreservesQualityInputsAndNativeWindow(t *testing.T) {
	desc, _, _ := adaptivePolicyFixture()
	desc.BatchParameter, desc.WindowParameter, desc.PrecisionParameter = "alignment_batch_size", "alignment_window_seconds", "alignment_precision"
	params := map[string]interface{}{
		"device": "cuda", "precision": "float16", "compute_type": "float16", "alignment_precision": "float32",
		"batch_size": 8, "alignment_batch_size": 4, "chunking": false, "chunk_duration": 60,
		"model": "pinned-model", "model_revision": "pinned-revision", "aligner_revision": "pinned-aligner",
		"context": "The release uses parseHTTP and PostgreSQL.", "context_terms": "parseHTTP\nPostgreSQL",
		"language": "en", "task": "transcribe", "beam_size": 5, "temperature": 0.0,
		"hf_token": "synthetic-test-token", "diarize_model": "pyannote", "diarize": true,
	}
	before := copyStageParameters(params)
	settings := candidateSettings(params, desc, "test-model")
	if settings.BatchSize != 4 || settings.WindowSeconds != 0 || settings.Precision != "float32" {
		t.Fatalf("descriptor parameter/native-window mismatch %+v", settings)
	}
	settings.Device, settings.Precision, settings.BatchSize = "cpu", "float32", 2
	actual := applyCandidate(params, settings, desc)
	for key, value := range before {
		if key == "device" || key == "precision" || key == "compute_type" || key == "alignment_precision" || key == "alignment_batch_size" {
			continue
		}
		if !reflect.DeepEqual(actual[key], value) {
			t.Errorf("changed fixed quality input %s", key)
		}
	}
	if !reflect.DeepEqual(params, before) {
		t.Fatal("mutated the submitted parameters")
	}
	if actual["alignment_batch_size"] != 2 || actual["batch_size"] != 8 || actual["alignment_precision"] != "float32" || actual["chunking"] != false {
		t.Fatalf("candidate applied to wrong stage fields: %+v", actual)
	}
	if _, exists := actual["alignment_window_seconds"]; exists {
		t.Fatal("native/full context was converted into a segmentation override")
	}
	settings.WindowSeconds, settings.OverlapSeconds, settings.StitchingVersion = 30, 2, "test-overlap-v1"
	shortened := applyCandidate(params, settings, desc)
	if shortened["alignment_window_seconds"] != float64(30) || shortened["overlap_seconds"] != float64(2) || shortened["stitching_version"] != "test-overlap-v1" || shortened["chunking"] != true {
		t.Fatal("explicit qualified window/stitching not applied")
	}
	if shortened["chunk_duration"] != 60 || shortened["context"] != before["context"] || shortened["context_terms"] != before["context_terms"] {
		t.Fatal("alignment policy changed recognition context or transcript hint")
	}
}

func TestAdaptiveCandidateSettingsUseDeclaredPrecisionParameterAndFixedDefault(t *testing.T) {
	params := map[string]interface{}{"device": "cuda", "precision": "float16", "compute_type": "float16", "alignment_precision": "float32"}
	desc := interfaces.StageDescriptor{PrecisionParameter: "alignment_precision", DefaultPrecision: "float32"}
	if got := candidateSettings(params, desc, "test-alignment"); got.Precision != "float32" {
		t.Errorf("alignment inherited recognition precision: %+v", got)
	}
	delete(params, "alignment_precision")
	if got := candidateSettings(params, desc, "test-alignment"); got.Precision != "float32" {
		t.Errorf("alignment ignored its declared default: %+v", got)
	}
	desc.PrecisionParameter = ""
	if got := candidateSettings(params, desc, "test-fixed-float32"); got.Precision != "float32" {
		t.Errorf("fixed runtime was relabeled by stale generic precision: %+v", got)
	}
	desc.DefaultPrecision = "i2_s+i8_s"
	params["device"] = "cpu"
	if got := candidateSettings(params, desc, "test-fixed-quantized"); got.Precision != "i2_s+i8_s" {
		t.Errorf("fixed quantized runtime lost its declared precision: %+v", got)
	}
}
