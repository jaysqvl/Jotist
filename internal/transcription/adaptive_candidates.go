package transcription

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func recoveryLevel(mode string) int {
	switch mode {
	case RecoveryStageManagement:
		return 1
	case RecoveryBatchManagement:
		return 2
	case RecoveryCPUFallback:
		return 3
	case RecoveryShorterWindows:
		return 4
	}
	return 0
}
func recoveryStageKey(kind string) string {
	switch kind {
	case "asr", "recognize", "recognition", "combined":
		return "recognition"
	case "align", "alignment":
		return "alignment"
	case "diarize", "diarization":
		return "diarization"
	}
	return kind
}
func stageDescriptor(adapter interfaces.ModelAdapter, kind string) (interfaces.StageDescriptor, bool) {
	if qualified, ok := adapter.(interfaces.AdaptiveCapabilities); ok {
		return qualified.RecoveryStage(recoveryStageKey(kind))
	}
	return interfaces.StageDescriptor{}, false
}
func stageRule(policy *models.AdaptiveExecutionPolicy, kind string) models.AdaptiveStagePolicy {
	if policy != nil {
		return policy.Stages[recoveryStageKey(kind)]
	}
	return models.AdaptiveStagePolicy{}
}
func ValidateAdaptivePolicy(params models.WhisperXParams) error {
	if err := ValidateRecoveryMode(params.RecoveryMode); err != nil {
		return err
	}
	if params.AdaptivePolicy == nil {
		return nil
	}
	for key, rule := range params.AdaptivePolicy.Stages {
		if key != "recognition" && key != "alignment" && key != "diarization" {
			return fmt.Errorf("unsupported adaptive stage %q", key)
		}
		if rule.AllowCPU && rule.CPUPrecision == "" {
			return errors.New("CPU fallback requires an explicitly configured CPU precision")
		}
		if rule.MinBatchSize < 0 || rule.MinWindowSeconds < 0 || math.IsNaN(rule.OverlapSeconds) || math.IsInf(rule.OverlapSeconds, 0) || rule.OverlapSeconds < 0 || len(rule.WindowCandidates) > 2 {
			return errors.New("invalid adaptive stage bounds")
		}
		for _, window := range rule.WindowCandidates {
			if window <= 0 || window < rule.MinWindowSeconds || float64(window) <= 2*rule.OverlapSeconds {
				return errors.New("window candidates must preserve configured minimum duration and overlap")
			}
		}
		if rule.Fixed != nil && (rule.Fixed.BatchSize < 1 || rule.Fixed.Concurrency != 1 || rule.Fixed.WindowSeconds < 0 || rule.Fixed.OverlapSeconds < 0 || math.IsNaN(rule.Fixed.WindowSeconds) || math.IsInf(rule.Fixed.WindowSeconds, 0) || math.IsNaN(rule.Fixed.OverlapSeconds) || math.IsInf(rule.Fixed.OverlapSeconds, 0)) {
			return errors.New("invalid frozen stage settings")
		}
	}
	return nil
}
func batchParameter(desc interfaces.StageDescriptor) string {
	if desc.BatchParameter != "" {
		return desc.BatchParameter
	}
	return "batch_size"
}
func windowParameter(desc interfaces.StageDescriptor) string {
	if desc.WindowParameter != "" {
		return desc.WindowParameter
	}
	return "chunk_duration"
}
func candidateSettings(params map[string]interface{}, desc interfaces.StageDescriptor, modelID string) models.AdaptiveStageSettings {
	batch := stageBatch(params)
	if value, ok := params[batchParameter(desc)]; ok {
		batch = stageBatch(map[string]interface{}{"batch_size": value})
	}
	window := stageWindow(params)
	if value, ok := params[windowParameter(desc)]; ok && params["chunking"] != false {
		window = number(value)
	}
	device, _ := params["device"].(string)
	precision := ""
	if desc.PrecisionParameter != "" {
		precision = stringValue(params[desc.PrecisionParameter])
	}
	if precision == "" {
		precision = desc.DefaultPrecision
	}
	if precision == "" {
		precision = stagePrecision(params, modelID)
	}
	return models.AdaptiveStageSettings{Device: device, Precision: precision, BatchSize: batch, Concurrency: 1, WindowSeconds: window, OverlapSeconds: number(params["overlap_seconds"]), StitchingVersion: stringValue(params["stitching_version"])}
}
func applyCandidate(params map[string]interface{}, settings models.AdaptiveStageSettings, desc interfaces.StageDescriptor) map[string]interface{} {
	actual := copyStageParameters(params)
	actual["device"] = settings.Device
	if desc.PrecisionParameter != "" {
		actual[desc.PrecisionParameter] = settings.Precision
	} else {
		if _, exists := actual["precision"]; exists {
			actual["precision"] = settings.Precision
		}
		if _, exists := actual["compute_type"]; exists {
			actual["compute_type"] = settings.Precision
		}
	}
	actual[batchParameter(desc)] = settings.BatchSize
	// Only write a window override when it actually differs; native unbounded
	// context stays native rather than becoming an invented segmentation mode.
	if settings.WindowSeconds != candidateSettings(params, desc, "").WindowSeconds || settings.StitchingVersion != "" {
		actual[windowParameter(desc)] = settings.WindowSeconds
		actual["chunking"] = settings.WindowSeconds > 0
		actual["overlap_seconds"] = settings.OverlapSeconds
		actual["stitching_version"] = settings.StitchingVersion
	}
	return actual
}
func number(value interface{}) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case float32:
		return float64(v)
	}
	return 0
}
func stringValue(value interface{}) string { s, _ := value.(string); return s }
func supportsPrecision(desc interfaces.StageDescriptor, device, precision string) bool {
	for _, p := range desc.DevicePrecisions[device] {
		if p == precision {
			return true
		}
	}
	return false
}
func settingsEqual(a, b models.AdaptiveStageSettings) bool { return a == b }

type adaptiveCandidate struct {
	Settings models.AdaptiveStageSettings
	Reason   string
}

// The ladder is finite and deterministic. Every candidate changes only a
// capability-qualified value explicitly permitted by the saved stage policy.
func nextAdaptiveCandidate(mode, code string, rule models.AdaptiveStagePolicy, desc interfaces.StageDescriptor, original, current models.AdaptiveStageSettings, history []models.RecoveryAttempt, contention bool) *adaptiveCandidate {
	level := recoveryLevel(mode)
	if level == 0 || len(history) >= 7 {
		return nil
	}
	gpuFailure := code == "cuda_out_of_memory" || code == "cuda_runtime_error"
	hostFailure := code == "host_out_of_memory"
	if !gpuFailure && !hostFailure {
		return nil
	}
	tried := func(s models.AdaptiveStageSettings) bool {
		for _, a := range history {
			if settingsEqual(s, settingsFromAttempt(a)) {
				return true
			}
		}
		return false
	}
	cleanupUsed := false
	batchUsed := 0
	cpuUsed := false
	windowsUsed := 0
	for _, a := range history {
		switch a.Reason {
		case "cleanup_retry":
			cleanupUsed = true
		case "smaller_batch":
			batchUsed++
		case "cpu_fallback":
			cpuUsed = true
		case "shorter_window":
			windowsUsed++
		}
	}
	if gpuFailure && current.Device == "cuda" && !cleanupUsed {
		return &adaptiveCandidate{current, "cleanup_retry"}
	}
	// External consumers are not clean evidence that model context must shrink.
	if contention {
		return nil
	}
	if level >= 2 && code == "cuda_out_of_memory" && current.WindowSeconds == original.WindowSeconds && batchUsed < 2 {
		batches := append([]int(nil), desc.QualifiedBatches...)
		sort.Sort(sort.Reverse(sort.IntSlice(batches)))
		for _, batch := range batches {
			if batch < current.BatchSize && batch >= policyMax(1, rule.MinBatchSize) {
				next := current
				next.BatchSize = batch
				if !tried(next) {
					return &adaptiveCandidate{next, "smaller_batch"}
				}
			}
		}
	}
	if level >= 3 && gpuFailure && current.Device == "cuda" && !rule.DeviceLocked && rule.AllowCPU && !cpuUsed && supportsPrecision(desc, "cpu", rule.CPUPrecision) {
		next := current
		next.Device = "cpu"
		next.Precision = rule.CPUPrecision
		if !tried(next) {
			return &adaptiveCandidate{next, "cpu_fallback"}
		}
	}
	if level < 4 || code == "cuda_runtime_error" || !rule.AllowShorterWindows || desc.WindowPolicy == nil || desc.WindowPolicy.StitchingVersion == "" || desc.WindowPolicy.Unit != "seconds" || windowsUsed >= 2 {
		return nil
	}
	if rule.OverlapSeconds < float64(desc.WindowPolicy.MinimumOverlap) {
		return nil
	}
	windows := append([]int(nil), rule.WindowCandidates...)
	sort.Sort(sort.Reverse(sort.IntSlice(windows)))
	for _, window := range windows {
		qualified := false
		for _, allowed := range desc.WindowPolicy.Candidates {
			if window == allowed {
				qualified = true
			}
		}
		if !qualified || window < rule.MinWindowSeconds || float64(window) <= 2*rule.OverlapSeconds || (current.WindowSeconds > 0 && float64(window) >= current.WindowSeconds) {
			continue
		}
		next := current
		next.WindowSeconds = float64(window)
		next.OverlapSeconds = rule.OverlapSeconds
		next.StitchingVersion = desc.WindowPolicy.StitchingVersion
		// After a CPU capacity failure, reduced windows return to the configured
		// GPU when qualified; an originally CPU-only stage never gains a GPU.
		if original.Device == "cuda" && supportsPrecision(desc, "cuda", original.Precision) {
			next.Device = "cuda"
			next.Precision = original.Precision
		}
		if !tried(next) {
			return &adaptiveCandidate{next, "shorter_window"}
		}
	}
	return nil
}
func settingsFromAttempt(a models.RecoveryAttempt) models.AdaptiveStageSettings {
	return models.AdaptiveStageSettings{Device: a.Device, Precision: a.Precision, BatchSize: a.BatchSize, Concurrency: 1, WindowSeconds: a.WindowSeconds, OverlapSeconds: a.OverlapSeconds, StitchingVersion: a.StitchingVersion}
}

func candidateAllowed(mode string, rule models.AdaptiveStagePolicy, desc interfaces.StageDescriptor, original, candidate models.AdaptiveStageSettings) bool {
	if original == candidate {
		return true
	}
	level := recoveryLevel(mode)
	if level == 0 {
		return false
	}
	if candidate.Concurrency != 1 || candidate.BatchSize < policyMax(1, rule.MinBatchSize) || candidate.BatchSize > original.BatchSize {
		return false
	}
	if candidate.BatchSize != original.BatchSize {
		ok := false
		for _, b := range desc.QualifiedBatches {
			if b == candidate.BatchSize {
				ok = true
			}
		}
		if level < 2 || !ok {
			return false
		}
	}
	if candidate.Device != original.Device {
		if level < 3 || rule.DeviceLocked || !rule.AllowCPU || candidate.Device != "cpu" || candidate.Precision != rule.CPUPrecision || !supportsPrecision(desc, "cpu", candidate.Precision) {
			return false
		}
	} else if candidate.Precision != original.Precision {
		return false
	}
	if candidate.WindowSeconds != original.WindowSeconds || candidate.OverlapSeconds != original.OverlapSeconds || candidate.StitchingVersion != original.StitchingVersion {
		if level < 4 || !rule.AllowShorterWindows || desc.WindowPolicy == nil || candidate.StitchingVersion != desc.WindowPolicy.StitchingVersion || candidate.OverlapSeconds != rule.OverlapSeconds || candidate.OverlapSeconds < float64(desc.WindowPolicy.MinimumOverlap) || candidate.WindowSeconds <= 2*candidate.OverlapSeconds || desc.WindowPolicy.Unit != "seconds" || candidate.WindowSeconds < float64(rule.MinWindowSeconds) || (original.WindowSeconds > 0 && candidate.WindowSeconds >= original.WindowSeconds) {
			return false
		}
		ok := false
		for _, w := range rule.WindowCandidates {
			for _, v := range desc.WindowPolicy.Candidates {
				if w == v && float64(w) == candidate.WindowSeconds {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	return !strings.Contains(candidate.Precision, "auto")
}

func policyMax[T ~int | ~int64 | ~float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}
func policyMin[T ~int | ~int64 | ~float64](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func validateFrozenStage(descriptor interfaces.StageDescriptor, original, frozen models.AdaptiveStageSettings) error {
	if !supportsPrecision(descriptor, frozen.Device, frozen.Precision) {
		return errors.New("frozen stage device and precision are not supported by this adapter")
	}
	if frozen.BatchSize != original.BatchSize {
		supported := false
		for _, batch := range descriptor.QualifiedBatches {
			if batch == frozen.BatchSize {
				supported = true
			}
		}
		if !supported {
			return errors.New("frozen batch is not qualified for this stage")
		}
	}
	if frozen.WindowSeconds != original.WindowSeconds || frozen.OverlapSeconds != original.OverlapSeconds || frozen.StitchingVersion != original.StitchingVersion {
		window := descriptor.WindowPolicy
		if window == nil || window.Unit != "seconds" || frozen.StitchingVersion != window.StitchingVersion || frozen.OverlapSeconds < float64(window.MinimumOverlap) || frozen.WindowSeconds <= 2*frozen.OverlapSeconds {
			return errors.New("frozen window and stitching are not qualified for this stage")
		}
		supported := false
		for _, value := range window.Candidates {
			if float64(value) == frozen.WindowSeconds {
				supported = true
			}
		}
		if !supported {
			return errors.New("frozen window is not qualified for this stage")
		}
	}
	return nil
}
