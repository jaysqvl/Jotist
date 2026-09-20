package transcription

// Effective stage parameters describe what the worker will actually run. The
// original request remains untouched, including its Auto fallback permission.
func effectiveStageParameters(params map[string]interface{}, mode string, gpu gpuInventory, family string) map[string]interface{} {
	resolved := resolveStageParameters(params, RecoveryFixed, gpu)
	if mode != "" {
		// Fixed and adaptive policies require explicit precision choices; legacy
		// compatibility must not authorize a numerical change for these plans.
		return resolved
	}
	if params["device"] == "auto" && resolved["device"] == "cpu" {
		return cpuRetryParameters(resolved)
	}
	if family == "nvidia_canary" && resolved["device"] == "cpu" {
		// The original Canary worker always uses model.float() on CPU, even for
		// saved profiles requesting FP16/BF16. Resolve that existing behavior
		// before hashing the stage plan and recording the attempt; the staged
		// worker correctly accepts only FP32 on CPU.
		switch resolved["precision"] {
		case "float16", "bfloat16":
			resolved["precision"] = "float32"
		}
	}
	return resolved
}
